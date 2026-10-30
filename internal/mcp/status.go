package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// Agent states as agent_status reports them.
const (
	stateWorking = "working"
	stateIdle    = "idle"  // finished its turn, waiting for a prompt
	stateStuck   = "stuck" // working, but no model call for stuckAfter
	stateExited  = "exited"
	stateWaiting = "waiting" // stopped on a permission prompt or a question
)

const defaultStuckAfter = 20 * time.Minute

type problem struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type agentReport struct {
	Key         string `json:"key"`
	Title       string `json:"title,omitempty"`
	Project     string `json:"project,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	Model       string `json:"model,omitempty"`
	State       string `json:"state"`
	StateFor    string `json:"stateFor,omitempty"`    // how long it has been in State
	LastCallAgo string `json:"lastCallAgo,omitempty"` // since its last model call
	LastHuman   string `json:"lastPromptAgo,omitempty"`
	Summary     string `json:"summary,omitempty"`
	// Background: idle, with shell commands it started still running; it
	// may wake by itself when they finish.
	Background bool       `json:"backgroundCommands,omitempty"`
	Context    int64      `json:"contextTokens,omitempty"`
	CacheIn    string     `json:"cacheExpiresIn,omitempty"`
	Plan       *planBrief `json:"plan,omitempty"`
	Problems   []problem  `json:"problems,omitempty"`
}

func stuckAfter(min *float64) (time.Duration, error) {
	if min == nil {
		return defaultStuckAfter, nil
	}
	if *min <= 0 {
		return 0, errors.New("stuckAfterMinutes must be positive")
	}
	return time.Duration(*min * float64(time.Minute)), nil
}

// assess turns a session into a report: its state and what is wrong.
func assess(v model.SessionView, now time.Time, stuck time.Duration) agentReport {
	r := agentReport{
		Key: v.Key, Title: v.Title, Project: v.Project, Cwd: v.Cwd, Model: v.Model,
		LastCallAgo: since(now, v.LastCall), LastHuman: since(now, v.LastHuman),
		Summary: v.Summary, Context: v.ContextTokens, Plan: brief(v.Plan, now),
	}
	r.StateFor = since(now, v.StatusSince)
	quiet := now.Sub(latestOf(v.LastCall, v.StatusSince))
	switch {
	case v.HarnessStatus == "waiting":
		r.State = stateWaiting
		r.Problems = append(r.Problems, problem{"needs_input", fmt.Sprintf(
			"stopped %s ago on a permission prompt or a question; it goes on once someone answers in its terminal", ago(now.Sub(v.StatusSince)))})
	case v.Status == model.StatusWorking && quiet >= stuck:
		r.State = stateStuck
		r.Problems = append(r.Problems, problem{"no_progress", fmt.Sprintf(
			"working, but no model call for %s: it may be waiting on a permission prompt or a question, or running a long or hung command", ago(quiet))})
	case v.Status == model.StatusWorking:
		r.State = stateWorking
	default:
		r.State = stateIdle
		if v.HarnessStatus == "shell" {
			r.Background = true
		}
	}
	if !v.CacheExpiresAt.IsZero() {
		if d := v.CacheExpiresAt.Sub(now); d > 0 {
			r.CacheIn = ago(d)
		} else {
			r.CacheIn = "expired"
		}
	}
	if r.State == stateIdle && !live(v.Plan) && v.ContextTokens > 0 && v.Costs.Known {
		d := v.CacheExpiresAt.Sub(now)
		switch {
		case v.CacheExpiresAt.IsZero():
		case d <= 0:
			r.Problems = append(r.Problems, problem{"cache_cold", fmt.Sprintf(
				"its cache has expired: the next prompt re-writes %d tokens (about $%.2f)", v.ContextTokens, v.Costs.Recache)})
		case d < 10*time.Minute:
			r.Problems = append(r.Problems, problem{"cache_expiring", fmt.Sprintf(
				"its cache expires in %s and no plan keeps it warm (a re-write costs about $%.2f); keep_alive keeps it", ago(d), v.Costs.Recache)})
		}
	}
	if v.Reach == model.ReachNone {
		why := v.ReachWhy
		if why == "" {
			why = "no way to deliver a prompt to it"
		}
		r.Problems = append(r.Problems, problem{"unreachable", "keep-alive cannot reach it: " + why})
	}
	if p := v.Plan; p != nil {
		switch {
		case p.State == model.PlanFailed:
			r.Problems = append(r.Problems, problem{"plan_failed", nonEmpty(p.Reason, p.LastError, "its keep-alive plan failed")})
		case p.State == model.PlanPaused:
			r.Problems = append(r.Problems, problem{"plan_paused", nonEmpty(p.Reason, "its keep-alive plan is paused") + " (keep_alive resumes it)"})
		case p.State == model.PlanActive && p.Blocked != "":
			r.Problems = append(r.Problems, problem{"plan_blocked", p.Blocked})
		case p.State == model.PlanActive && p.LastError != "":
			r.Problems = append(r.Problems, problem{"ping_error", p.LastError})
		}
	}
	return r
}

func latestOf(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func nonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

type statusReport struct {
	Summary string         `json:"summary"`
	Counts  map[string]int `json:"counts"`
	Agents  []agentReport  `json:"agents"`
	// Attention lists the keys with problems, worst first.
	Attention []string `json:"needsAttention,omitempty"`
}

func (s *Server) report(w *world, t Target, stuck time.Duration) (statusReport, error) {
	picked, missing, err := w.pick(t)
	if err != nil {
		return statusReport{}, err
	}
	now := s.now()
	rep := statusReport{Counts: map[string]int{}, Agents: []agentReport{}}
	for _, v := range picked {
		rep.Agents = append(rep.Agents, assess(v, now, stuck))
	}
	for _, k := range missing {
		rep.Agents = append(rep.Agents, agentReport{Key: k, State: stateExited, Problems: []problem{{"exited", "no live session has this key or id: it exited, or switched to another conversation"}}})
	}
	rank := map[string]int{stateExited: 0, stateWaiting: 1, stateStuck: 2, stateIdle: 3, stateWorking: 4}
	sort.SliceStable(rep.Agents, func(i, j int) bool {
		a, b := rep.Agents[i], rep.Agents[j]
		if (len(a.Problems) > 0) != (len(b.Problems) > 0) {
			return len(a.Problems) > 0
		}
		return rank[a.State] < rank[b.State]
	})
	for _, a := range rep.Agents {
		rep.Counts[a.State]++
		if len(a.Problems) > 0 {
			rep.Attention = append(rep.Attention, a.Key)
		}
	}
	var parts []string
	for _, st := range []string{stateWorking, stateIdle, stateWaiting, stateStuck, stateExited} {
		if n := rep.Counts[st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, st))
		}
	}
	if len(parts) == 0 {
		rep.Summary = "no sessions match"
	} else {
		rep.Summary = strings.Join(parts, ", ")
		if n := len(rep.Attention); n > 0 {
			rep.Summary += fmt.Sprintf("; %d need attention", n)
		}
	}
	return rep, nil
}

func (s *Server) agentStatus(ctx context.Context, args json.RawMessage) (any, error) {
	var a struct {
		Target            *Target  `json:"target"`
		StuckAfterMinutes *float64 `json:"stuckAfterMinutes"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	stuck, err := stuckAfter(a.StuckAfterMinutes)
	if err != nil {
		return nil, err
	}
	w, err := s.look(ctx)
	if err != nil {
		return nil, err
	}
	return s.report(w, deref(a.Target), stuck)
}

type change struct {
	Key   string `json:"key"`
	Title string `json:"title,omitempty"`
	From  string `json:"from"`
	To    string `json:"to"`
}

func (s *Server) waitForAgents(ctx context.Context, args json.RawMessage) (any, error) {
	var a struct {
		Target            *Target  `json:"target"`
		Until             string   `json:"until"`
		TimeoutSeconds    *float64 `json:"timeoutSeconds"`
		StuckAfterMinutes *float64 `json:"stuckAfterMinutes"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	allIdle := false
	switch a.Until {
	case "", "any_change":
	case "all_idle":
		allIdle = true
	default:
		return nil, fmt.Errorf("until must be any_change or all_idle, not %q", a.Until)
	}
	timeout := 300 * time.Second
	if a.TimeoutSeconds != nil {
		if *a.TimeoutSeconds < 1 || *a.TimeoutSeconds > 3600 {
			return nil, errors.New("timeoutSeconds must be between 1 and 3600")
		}
		timeout = time.Duration(*a.TimeoutSeconds * float64(time.Second))
	}
	stuck, err := stuckAfter(a.StuckAfterMinutes)
	if err != nil {
		return nil, err
	}
	poll := s.Poll
	if poll <= 0 {
		poll = 3 * time.Second
	}
	t := deref(a.Target)
	w, err := s.look(ctx)
	if err != nil {
		return nil, err
	}
	first, err := s.report(w, t, stuck)
	if err != nil {
		return nil, err
	}
	if len(first.Agents) == 0 {
		return nil, errors.New("no live session matches the target")
	}
	// Watch the sessions picked now, by key, so one that exits shows up as
	// exited instead of dropping out of the target.
	keys := make([]string, 0, len(first.Agents))
	start := map[string]agentReport{}
	for _, ag := range first.Agents {
		keys = append(keys, ag.Key)
		start[ag.Key] = ag
	}
	watch := Target{Sessions: keys}
	done := func(rep statusReport) (bool, []change, bool) {
		var ch []change
		idle, trouble := true, false
		for _, ag := range rep.Agents {
			if ag.State != stateIdle && ag.State != stateExited {
				idle = false
			}
			if was := start[ag.Key]; was.State != ag.State {
				ch = append(ch, change{Key: ag.Key, Title: nonEmpty(ag.Title, was.Title), From: was.State, To: ag.State})
				// Waiting for all to finish would wait forever on these.
				trouble = trouble || ag.State == stateWaiting || ag.State == stateStuck || ag.State == stateExited
			}
		}
		if allIdle {
			return idle || trouble, ch, trouble
		}
		return len(ch) > 0, ch, trouble
	}
	if allIdle && first.Counts[stateWorking]+first.Counts[stateStuck]+first.Counts[stateWaiting] == 0 {
		_, ch, _ := done(first)
		return map[string]any{"reason": "all idle", "changes": nonNilSlice(ch), "status": first}, nil
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	last := first
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			_, ch, _ := done(last)
			return map[string]any{"reason": "timeout: nothing to report yet", "waited": ago(timeout), "changes": nonNilSlice(ch), "status": last}, nil
		case <-tick.C:
		}
		w, err := s.look(ctx)
		if err != nil {
			continue // the server restarting (an update): keep waiting
		}
		rep, err := s.report(w, watch, stuck)
		if err != nil {
			continue
		}
		last = rep
		if ok, ch, trouble := done(rep); ok {
			reason := "state changed"
			switch {
			case trouble:
				reason = "an agent got stuck, is waiting for an answer, or exited"
			case allIdle:
				reason = "all idle"
			}
			return map[string]any{"reason": reason, "changes": nonNilSlice(ch), "status": rep}, nil
		}
	}
}

func nonNilSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return slices.Clip(s)
}
