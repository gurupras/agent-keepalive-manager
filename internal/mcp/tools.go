package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// Backend is the running server's API.
type Backend interface {
	Sessions(ctx context.Context) ([]model.SessionView, error)
	StartPlan(ctx context.Context, key string, hours float64) (model.Plan, error)
	KeepIndefinitely(ctx context.Context, key string) (model.Plan, error)
	ExtendPlan(ctx context.Context, key string, addHours float64) (model.Plan, error)
	ResumePlan(ctx context.Context, key string) (model.Plan, error)
	CancelPlan(ctx context.Context, key string) error
	Totals(ctx context.Context) (model.Totals, error)
}

// Server is the MCP server. Backend is required; the rest have defaults
// for tests to replace.
type Server struct {
	Backend Backend
	Version string
	// PID is this process; the session whose process is one of its
	// ancestors is the caller ("self").
	PID int
	// Cwd is this process's working directory, the fallback for finding the
	// caller where process ancestry is not visible.
	Cwd string
	// Parent returns a pid's parent pid, 0 if unknown (proc.Parent).
	Parent func(pid int) int
	Now    func() time.Time
	// Poll is how often wait_for_agents looks at the sessions when the
	// Backend is not a Notifier.
	Poll time.Duration

	mu       sync.Mutex
	selfAnc  []int            // this process's ancestors, read once
	selfRead bool             //
	anc      map[string][]int // session process ancestors, read once per process
}

// Notifier is a Backend that knows when the sessions change.
type Notifier interface {
	// Changed returns a channel that is closed at the next change.
	Changed() <-chan struct{}
}

// ancestorsOf walks pid's parents with s.Parent.
func (s *Server) ancestorsOf(pid int) []int {
	if s.Parent == nil {
		return nil
	}
	var out []int
	seen := map[int]bool{pid: true}
	for p := s.Parent(pid); p > 0 && !seen[p]; p = s.Parent(p) {
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// sessionAncestors returns the ancestry of each session's process. A
// process's parents are read when it is first seen, so a look costs
// nothing for sessions already known; entries for gone ones are dropped.
func (s *Server) sessionAncestors(ss []model.SessionView) map[string][]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.anc == nil {
		s.anc = map[string][]int{}
	}
	out := make(map[string][]int, len(ss))
	for _, v := range ss {
		id := fmt.Sprintf("%d@%d", v.PID, v.StartedAt.UnixMilli())
		a, ok := s.anc[id]
		if !ok {
			a = s.ancestorsOf(v.PID)
		}
		out[id] = a
	}
	s.anc = out
	return out
}

func (s *Server) selfAncestors() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.selfRead {
		s.selfAnc, s.selfRead = s.ancestorsOf(s.PID), true
	}
	return s.selfAnc
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

type tool struct {
	name, title, description string
	schema                   map[string]any
	readOnly                 bool
	run                      func(s *Server, ctx context.Context, args json.RawMessage) (any, error)
}

var tools = map[string]tool{}

var toolOrder []string

func register(t tool) {
	tools[t.name] = t
	toolOrder = append(toolOrder, t.name)
}

func toolList() []map[string]any {
	out := make([]map[string]any, 0, len(toolOrder))
	for _, n := range toolOrder {
		t := tools[n]
		out = append(out, map[string]any{
			"name":        t.name,
			"title":       t.title,
			"description": t.description,
			"inputSchema": t.schema,
			"annotations": map[string]any{
				"title":          t.title,
				"readOnlyHint":   t.readOnly,
				"idempotentHint": true,
				"openWorldHint":  false,
			},
		})
	}
	return out
}

// Target picks sessions. self, sessions, spawnedByMe and all each add
// sessions; project, cwdPrefix and titleContains then narrow them (alone,
// they narrow all sessions). An empty target is self.
type Target struct {
	Self          bool     `json:"self"`
	Sessions      []string `json:"sessions"`
	SpawnedByMe   bool     `json:"spawnedByMe"`
	All           bool     `json:"all"`
	Project       string   `json:"project"`
	CwdPrefix     string   `json:"cwdPrefix"`
	TitleContains string   `json:"titleContains"`
}

var targetSchema = map[string]any{
	"type": "object",
	"description": "Which sessions to act on. self, sessions, spawnedByMe and all each add sessions; " +
		"project, cwdPrefix and titleContains narrow the result (given alone, they narrow all sessions). " +
		"Leave it out to mean the session running this tool.",
	"properties": map[string]any{
		"self":          map[string]any{"type": "boolean", "description": "the session that runs this tool"},
		"sessions":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "session keys (\"claude:<id>\", \"codex:<id>\") or bare ids, as list_sessions shows them"},
		"spawnedByMe":   map[string]any{"type": "boolean", "description": "sessions whose agent process was started from this session's process (agents it launched in a shell, a terminal multiplexer started from it, and so on)"},
		"all":           map[string]any{"type": "boolean", "description": "every live session"},
		"project":       map[string]any{"type": "string", "description": "only sessions in this project (its repository root) or below it"},
		"cwdPrefix":     map[string]any{"type": "string", "description": "only sessions whose working directory is this directory or below it"},
		"titleContains": map[string]any{"type": "string", "description": "only sessions whose title contains this text (any case)"},
	},
	"additionalProperties": false,
}

func objSchema(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func decodeArgs(args json.RawMessage, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("bad arguments: %v", err)
	}
	return nil
}

// world is one look at the sessions, with the caller worked out.
type world struct {
	sessions []model.SessionView
	self     *model.SessionView
	selfWhy  string // how self was found, or why it was not
	anc      map[string][]int
}

func (s *Server) look(ctx context.Context) (*world, error) {
	ss, err := s.Backend.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	w := &world{sessions: ss, anc: s.sessionAncestors(ss)}
	anc := s.selfAncestors()
	for i := range ss {
		if ss[i].PID > 0 && slices.Contains(anc, ss[i].PID) {
			w.self, w.selfWhy = &ss[i], "this tool runs under its process"
			return w, nil
		}
	}
	if s.Cwd != "" {
		var match []int
		for i := range ss {
			if samePath(ss[i].Cwd, s.Cwd) {
				match = append(match, i)
			}
		}
		if len(match) == 1 {
			w.self, w.selfWhy = &ss[match[0]], "the only session in this tool's working directory"
			return w, nil
		}
		if len(match) > 1 {
			w.selfWhy = fmt.Sprintf("%d sessions run in %s and none is this tool's parent process", len(match), s.Cwd)
			return w, nil
		}
	}
	w.selfWhy = "no live session runs this tool (is the agent's session listed by list_sessions?)"
	return w, nil
}

func samePath(a, b string) bool {
	return a != "" && b != "" && filepath.Clean(a) == filepath.Clean(b)
}

// under reports whether path is dir or below it.
func under(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// spawnedBy reports whether sess's process descends from the process pid.
func (w *world) spawnedBy(sess model.SessionView, pid int) bool {
	id := fmt.Sprintf("%d@%d", sess.PID, sess.StartedAt.UnixMilli())
	return pid > 0 && sess.PID != pid && slices.Contains(w.anc[id], pid)
}

// pick resolves a target. missing lists requested sessions that are not
// live (exited, or never were).
func (w *world) pick(t Target) (picked []model.SessionView, missing []string, err error) {
	base := t.Self || len(t.Sessions) > 0 || t.SpawnedByMe || t.All
	filtered := t.Project != "" || t.CwdPrefix != "" || t.TitleContains != ""
	if !base && !filtered {
		t.Self, base = true, true
	}
	if (t.Self || t.SpawnedByMe) && w.self == nil {
		return nil, nil, fmt.Errorf("cannot tell which session runs this tool: %s; name the sessions instead", w.selfWhy)
	}
	in := map[string]bool{}
	if t.All || !base {
		for _, v := range w.sessions {
			in[v.Key] = true
		}
	}
	if t.Self {
		in[w.self.Key] = true
	}
	if t.SpawnedByMe {
		for _, v := range w.sessions {
			if w.spawnedBy(v, w.self.PID) {
				in[v.Key] = true
			}
		}
	}
	for _, want := range t.Sessions {
		found := false
		for _, v := range w.sessions {
			if v.Key == want || v.ID == want {
				in[v.Key], found = true, true
			}
		}
		if !found {
			missing = append(missing, want)
		}
	}
	for _, v := range w.sessions {
		if !in[v.Key] {
			continue
		}
		if t.Project != "" && !under(v.Project, t.Project) && !under(v.Cwd, t.Project) {
			continue
		}
		if t.CwdPrefix != "" && !under(v.Cwd, t.CwdPrefix) {
			continue
		}
		if t.TitleContains != "" && !strings.Contains(strings.ToLower(v.Title), strings.ToLower(t.TitleContains)) {
			continue
		}
		picked = append(picked, v)
	}
	return picked, missing, nil
}

// ago is a short duration for people and models: "45s", "12m", "3h05m".
func ago(d time.Duration) string {
	switch {
	case d < 0:
		d = 0
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func since(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return ago(now.Sub(t))
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// planBrief is a plan as the tools show it.
type planBrief struct {
	State        model.PlanState `json:"state"`
	UntilStopped bool            `json:"untilStopped,omitempty"`
	Until        time.Time       `json:"until,omitzero"`
	EndsIn       string          `json:"endsIn,omitempty"`
	Pings        int             `json:"pings"`
	PingCostUSD  float64         `json:"pingCostUsd"`
	Reason       string          `json:"reason,omitempty"`
	Blocked      string          `json:"blocked,omitempty"`
	LastError    string          `json:"lastError,omitempty"`
}

func brief(p *model.Plan, now time.Time) *planBrief {
	if p == nil {
		return nil
	}
	b := &planBrief{
		State: p.State, UntilStopped: p.Indefinite, Until: p.Until, Pings: p.Pings,
		PingCostUSD: round2(p.PingCost), Reason: p.Reason, Blocked: p.Blocked, LastError: p.LastError,
	}
	if live(p) && !p.Until.IsZero() {
		b.EndsIn = ago(p.Until.Sub(now))
	}
	return b
}

// live reports whether a plan still runs (or can be resumed).
func live(p *model.Plan) bool {
	return p != nil && (p.State == model.PlanActive || p.State == model.PlanPaused)
}

func init() {
	register(tool{
		name:  "list_sessions",
		title: "List agent sessions",
		description: "List the live coding-agent sessions on this machine (Claude Code, Codex) with their project, " +
			"status, cache expiry and keep-alive plan. The session running this tool is marked self.",
		schema:   objSchema(map[string]any{"target": targetSchema}),
		readOnly: true,
		run:      (*Server).listSessions,
	})
	register(tool{
		name:  "keep_alive",
		title: "Keep sessions warm",
		description: "Keep sessions' prompt caches warm while they are idle, for a number of hours or until stopped, " +
			"by sending a tiny prompt just before each cache would expire. With no target it acts on the session " +
			"running this tool (\"keep this alive for 24 hours\"). A session that already has a plan is extended, never shortened. " +
			"A fixed-hours plan ends early at the session's break-even point, where pinging would cost more than re-writing the cache; " +
			"untilStopped instead rolls forward with each human prompt.",
		schema: objSchema(map[string]any{
			"target":       targetSchema,
			"hours":        map[string]any{"type": "number", "exclusiveMinimum": 0, "description": "how many hours from now to keep the caches warm"},
			"untilStopped": map[string]any{"type": "boolean", "description": "keep them warm until stop_keep_alive (or a long absence)"},
		}),
		run: (*Server).keepAlive,
	})
	register(tool{
		name:        "stop_keep_alive",
		title:       "Stop keeping sessions warm",
		description: "End the keep-alive plans of the target sessions (default: the session running this tool).",
		schema:      objSchema(map[string]any{"target": targetSchema}),
		run:         (*Server).stopKeepAlive,
	})
	register(tool{
		name:  "agent_status",
		title: "Check on agents",
		description: "Report how the target sessions are doing, for a coordinator watching its agents: each one's state " +
			"(working; idle = finished its turn and waiting for a prompt; waiting = stopped on a permission prompt or question; " +
			"stuck = working with no model call for a while; exited), its latest summary, how long since it last made progress, its keep-alive plan, and a list of problems " +
			"worth acting on. Use target.spawnedByMe or target.sessions to pick a swarm.",
		schema: objSchema(map[string]any{
			"target":            targetSchema,
			"stuckAfterMinutes": map[string]any{"type": "number", "exclusiveMinimum": 0, "description": "a working session with no model call for this long is reported stuck (default 20)"},
		}),
		readOnly: true,
		run:      (*Server).agentStatus,
	})
	register(tool{
		name:  "wait_for_agents",
		title: "Wait for agents",
		description: "Block until one of the target sessions changes state (finishes its turn, starts working, gets stuck, exits) " +
			"or, with until=\"all_idle\", until all of them are idle (returning early if one gets stuck, waits for an answer or exits); then return what changed and agent_status for them. " +
			"Use this instead of polling. Returns at timeoutSeconds even if nothing changed.",
		schema: objSchema(map[string]any{
			"target":            targetSchema,
			"until":             map[string]any{"type": "string", "enum": []string{"any_change", "all_idle"}, "description": "default any_change"},
			"timeoutSeconds":    map[string]any{"type": "number", "minimum": 1, "maximum": 3600, "description": "default 300; keep it below your MCP client's tool timeout"},
			"stuckAfterMinutes": map[string]any{"type": "number", "exclusiveMinimum": 0, "description": "as for agent_status (default 20)"},
		}),
		readOnly: true,
		run:      (*Server).waitForAgents,
	})
	register(tool{
		name:        "savings",
		title:       "Keep-alive savings",
		description: "What keeping caches warm has saved and wasted so far, overall and for each live session.",
		schema:      objSchema(map[string]any{}),
		readOnly:    true,
		run:         (*Server).savings,
	})
}

type sessionBrief struct {
	Key          string        `json:"key"`
	Self         bool          `json:"self,omitempty"`
	SpawnedByMe  bool          `json:"spawnedByMe,omitempty"`
	Harness      model.Harness `json:"harness"`
	Title        string        `json:"title,omitempty"`
	Project      string        `json:"project"`
	Cwd          string        `json:"cwd"`
	Model        string        `json:"model,omitempty"`
	Status       model.Status  `json:"status"`
	CacheExpires string        `json:"cacheExpiresIn,omitempty"`
	Reachable    bool          `json:"reachable"`
	Plan         *planBrief    `json:"plan,omitempty"`
}

func (s *Server) listSessions(ctx context.Context, args json.RawMessage) (any, error) {
	var a struct {
		Target *Target `json:"target"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	w, err := s.look(ctx)
	if err != nil {
		return nil, err
	}
	t := Target{All: true}
	if a.Target != nil {
		t = *a.Target
	}
	picked, missing, err := w.pick(t)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := []sessionBrief{}
	for _, v := range picked {
		b := sessionBrief{
			Key: v.Key, Harness: v.Harness, Title: v.Title, Project: v.Project, Cwd: v.Cwd, Model: v.Model,
			Status: v.Status, Reachable: v.Reach != model.ReachNone, Plan: brief(v.Plan, now),
		}
		if w.self != nil {
			b.Self = v.Key == w.self.Key
			b.SpawnedByMe = w.spawnedBy(v, w.self.PID)
		}
		if !v.CacheExpiresAt.IsZero() {
			if d := v.CacheExpiresAt.Sub(now); d > 0 {
				b.CacheExpires = ago(d)
			} else {
				b.CacheExpires = "expired"
			}
		}
		out = append(out, b)
	}
	res := map[string]any{"sessions": out}
	if w.self == nil {
		res["selfUnknown"] = w.selfWhy
	}
	if len(missing) > 0 {
		res["notFound"] = missing
	}
	return res, nil
}

type planResult struct {
	Key   string     `json:"key"`
	Title string     `json:"title,omitempty"`
	OK    bool       `json:"ok"`
	Plan  *planBrief `json:"plan,omitempty"`
	Note  string     `json:"note,omitempty"`
	Error string     `json:"error,omitempty"`
}

func (s *Server) keepAlive(ctx context.Context, args json.RawMessage) (any, error) {
	var a struct {
		Target       *Target  `json:"target"`
		Hours        *float64 `json:"hours"`
		UntilStopped bool     `json:"untilStopped"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	switch {
	case a.Hours != nil && a.UntilStopped:
		return nil, errors.New("give hours or untilStopped, not both")
	case a.Hours == nil && !a.UntilStopped:
		return nil, errors.New("give hours (how long to keep the cache warm) or untilStopped: true")
	case a.Hours != nil && (*a.Hours <= 0 || math.IsNaN(*a.Hours) || math.IsInf(*a.Hours, 0)):
		return nil, errors.New("hours must be a positive number")
	}
	w, err := s.look(ctx)
	if err != nil {
		return nil, err
	}
	picked, missing, err := w.pick(deref(a.Target))
	if err != nil {
		return nil, err
	}
	if len(picked) == 0 && len(missing) == 0 {
		return nil, errors.New("no live session matches the target")
	}
	results := []planResult{}
	for _, v := range picked {
		r := planResult{Key: v.Key, Title: v.Title}
		var p model.Plan
		var note string
		if a.UntilStopped {
			p, note, err = s.keepIndefinitely(ctx, v)
		} else {
			p, note, err = s.keepFor(ctx, v, *a.Hours)
		}
		if err != nil {
			r.Error = err.Error()
		} else {
			r.OK, r.Plan, r.Note = true, brief(&p, s.now()), note
		}
		results = append(results, r)
	}
	for _, k := range missing {
		results = append(results, planResult{Key: k, Error: "no live session with this key or id"})
	}
	return map[string]any{"results": results}, nil
}

func deref(t *Target) Target {
	if t == nil {
		return Target{}
	}
	return *t
}

func (s *Server) keepIndefinitely(ctx context.Context, v model.SessionView) (model.Plan, string, error) {
	var note string
	if v.Plan != nil && v.Plan.State == model.PlanPaused {
		if _, err := s.Backend.ResumePlan(ctx, v.Key); err != nil {
			return model.Plan{}, "", err
		}
		note = "resumed its paused plan"
	}
	p, err := s.Backend.KeepIndefinitely(ctx, v.Key)
	return p, note, err
}

func (s *Server) keepFor(ctx context.Context, v model.SessionView, hours float64) (model.Plan, string, error) {
	now := s.now()
	want := now.Add(time.Duration(hours * float64(time.Hour)))
	if !live(v.Plan) {
		p, err := s.Backend.StartPlan(ctx, v.Key, hours)
		if errors.Is(err, model.ErrConflict) && v.Reach != model.ReachNone {
			// A plan appeared since the session list was read; extend it.
			return s.extend(ctx, v.Key, &p, want, "")
		}
		if err != nil {
			return model.Plan{}, "", err
		}
		note := ""
		if p.Until.Before(want.Add(-time.Minute)) {
			note = fmt.Sprintf("ends at %s, %s from now and earlier than asked: past %.1f hours of idleness keeping this session warm costs more than re-writing its cache",
				p.Until.Local().Format(time.RFC3339), ago(p.Until.Sub(now)), v.Costs.BreakEvenHours)
		}
		return p, note, nil
	}
	note := ""
	if v.Plan.State == model.PlanPaused {
		if _, err := s.Backend.ResumePlan(ctx, v.Key); err != nil {
			return model.Plan{}, "", err
		}
		note = "resumed its paused plan; "
	}
	if v.Plan.Indefinite {
		return *v.Plan, note + "already kept warm until stopped; left as is", nil
	}
	return s.extend(ctx, v.Key, v.Plan, want, note)
}

func (s *Server) extend(ctx context.Context, key string, cur *model.Plan, want time.Time, note string) (model.Plan, string, error) {
	if cur != nil && !cur.Until.IsZero() && !cur.Until.Before(want) {
		return *cur, note + "already kept warm until " + cur.Until.Local().Format(time.RFC3339), nil
	}
	add := want.Sub(cur.Until).Hours()
	if cur.Until.IsZero() {
		add = want.Sub(s.now()).Hours()
	}
	p, err := s.Backend.ExtendPlan(ctx, key, add)
	if err != nil {
		return p, "", err
	}
	note += "extended its plan"
	if p.Until.Before(want.Add(-time.Minute)) {
		note += fmt.Sprintf(" to %s, the most the settings allow", p.Until.Local().Format(time.RFC3339))
	}
	return p, note, nil
}

func (s *Server) stopKeepAlive(ctx context.Context, args json.RawMessage) (any, error) {
	var a struct {
		Target *Target `json:"target"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	w, err := s.look(ctx)
	if err != nil {
		return nil, err
	}
	picked, missing, err := w.pick(deref(a.Target))
	if err != nil {
		return nil, err
	}
	results := []planResult{}
	for _, v := range picked {
		r := planResult{Key: v.Key, Title: v.Title}
		switch err := s.Backend.CancelPlan(ctx, v.Key); {
		case err == nil:
			r.OK, r.Note = true, "stopped"
		case errors.Is(err, model.ErrNotFound):
			r.OK, r.Note = true, "had no plan"
		default:
			r.Error = err.Error()
		}
		results = append(results, r)
	}
	for _, k := range missing {
		results = append(results, planResult{Key: k, Error: "no live session with this key or id"})
	}
	return map[string]any{"results": results}, nil
}

func (s *Server) savings(ctx context.Context, args json.RawMessage) (any, error) {
	if err := decodeArgs(args, &struct{}{}); err != nil {
		return nil, err
	}
	t, err := s.Backend.Totals(ctx)
	if err != nil {
		return nil, err
	}
	ss, err := s.Backend.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	type row struct {
		Key       string  `json:"key"`
		Title     string  `json:"title,omitempty"`
		SavedUSD  float64 `json:"savedUsd"`
		WastedUSD float64 `json:"wastedUsd"`
	}
	rows := []row{}
	for _, v := range ss {
		if v.SavedTotal != 0 || v.WastedTotal != 0 {
			rows = append(rows, row{v.Key, v.Title, round2(v.SavedTotal), round2(v.WastedTotal)})
		}
	}
	return map[string]any{
		"totals": map[string]any{
			"savedUsd": round2(t.Saved), "wastedUsd": round2(t.Wasted), "netUsd": round2(t.Net),
			"pingCostUsd": round2(t.PingCost), "pings": t.Pings,
			"returns": t.Returns, "expired": t.Expired,
		},
		"liveSessions": rows,
		"explanation":  "saved: what the avoided cache re-writes would have cost, less the pings; wasted: what pings cost for idle periods that did not end in a warm return (the plan ended, the session exited, or the cache went cold anyway). returns and expired count those two outcomes.",
	}, nil
}
