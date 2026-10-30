package scheduler

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/experiment"
	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/store"
)

const subagentsExp = "busy-ping-subagents-1"

// quietHarness has an enrolled install whose session went quiet waiting
// for sub-agents after its last call at t0, with a plan, and the salt
// chosen so the stretch falls in arm.
func quietHarness(t *testing.T, arm string) *harness {
	t.Helper()
	h := newHarness(t)
	h.exp = true
	h.addSession(key, 100)
	h.src.edit(key, func(s *model.Session) { s.Status, s.Quiet = model.StatusWorking, model.QuietSubagents })
	h.tickAt(time.Minute)
	must(t)(h.svc.StartPlan(bg, key, 4))
	for i := 0; ; i++ {
		salt := strconv.Itoa(i)
		if experiment.Arm(salt, key, t0, subagentsExp) == arm {
			h.svc.mu.Lock()
			h.svc.expSalt = salt
			h.svc.mu.Unlock()
			break
		}
	}
	h.tickAt(2 * time.Minute)
	return h
}

// mainReturns records the main agent's next call, reading cacheRead
// tokens from the cache, and the session going idle.
func (h *harness) mainReturns(cacheRead int64) {
	now := h.clock.Now()
	h.src.add(key, model.Activity{Kind: model.ActAssistant, Model: opus, Sent: now, At: now.Add(20 * time.Second),
		Usage: &model.Usage{Input: 50, Output: 300, CacheRead: cacheRead, CacheWrite1h: ctxK - cacheRead}})
	h.src.edit(key, func(s *model.Session) { s.Quiet = "" })
}

func (h *harness) experiments(day time.Time) []model.ExperimentSum {
	h.t.Helper()
	ds, err := h.st.DayStats(bg, day)
	if err != nil {
		h.t.Fatal(err)
	}
	return ds.Experiments
}

func TestExperimentPingArm(t *testing.T) {
	h := quietHarness(t, model.ArmPing)
	v, _ := h.svc.Session(key)
	if v.Experiment == nil || v.Experiment.Arm != model.ArmPing || v.Experiment.Cause != model.QuietSubagents {
		t.Fatalf("episode view %+v", v.Experiment)
	}
	if v.CacheExpiresAt != t0.Add(time.Hour) {
		t.Fatalf("a quiet session's cache expires at %v; want its last call + TTL", v.CacheExpiresAt)
	}
	h.tickAt(55 * time.Minute)
	if h.del.count() != 1 {
		t.Fatalf("treatment pings = %d; want the busy session pinged as if idle", h.del.count())
	}
	id := h.del.last().id
	if p := h.ping(id); p.EpisodeID == 0 || !p.Experiment {
		t.Fatalf("treatment ping not tagged: %+v", p)
	}
	h.at(55*time.Minute + 5*time.Second)
	h.reply(key, id, warmUsage(ctxK))
	h.tickAt(56 * time.Minute)
	h.at(80 * time.Minute)
	h.mainReturns(ctxK)
	h.tickAt(81 * time.Minute)
	if v, _ := h.svc.Session(key); v.Experiment != nil {
		t.Fatalf("episode still open: %+v", v.Experiment)
	}
	got := h.experiments(t0)
	if len(got) != 1 {
		t.Fatalf("experiments %+v", got)
	}
	e := got[0]
	if e.Experiment != subagentsExp || e.Arm != model.ArmPing || e.Episodes != 1 || e.Returned != 1 || e.Warm != 1 ||
		e.Pings != 1 || e.PingCalls != 1 || !near(e.PingCostUSD, warmPingCost) || e.RewriteUSD != 0 ||
		!near(e.CostSqUSD, e.CostUSD*e.CostUSD) || e.ContextTokens != ctxK+10 || e.QuietMinutes != 80 {
		t.Fatalf("sum %+v", e)
	}
}

func TestExperimentHoldArm(t *testing.T) {
	h := quietHarness(t, model.ArmHold)
	h.tickAt(55 * time.Minute)
	h.tickAt(70 * time.Minute)
	if h.del.count() != 0 {
		t.Fatal("the hold arm pinged a working session")
	}
	h.mainReturns(0)
	h.tickAt(71 * time.Minute)
	got := h.experiments(t0)
	if len(got) != 1 {
		t.Fatalf("experiments %+v", got)
	}
	e := got[0]
	// 100k tokens re-written at the Opus 5.5 1h write price ($8/M).
	if e.Arm != model.ArmHold || e.Returned != 1 || e.Warm != 0 || e.Pings != 0 || !near(e.RewriteUSD, 0.8) ||
		!near(e.CostUSD, 0.8) {
		t.Fatalf("sum %+v", e)
	}
}

func TestExperimentNotEnrolled(t *testing.T) {
	h := quietHarness(t, model.ArmPing)
	h.mu.Lock()
	h.exp = false
	h.mu.Unlock()
	h.tickAt(55 * time.Minute)
	if h.del.count() != 0 {
		t.Fatal("pinged a working session after opting out")
	}
	if v, _ := h.svc.Session(key); v.Experiment != nil {
		t.Fatalf("episode kept after opting out: %+v", v.Experiment)
	}
	if got := h.experiments(t0); len(got) != 0 {
		t.Fatalf("an interrupted episode was reported: %+v", got)
	}
}

func TestExperimentBudget(t *testing.T) {
	h := newHarness(t)
	h.exp = true
	h.addSession(key, 100)
	h.src.edit(key, func(s *model.Session) { s.Status, s.Quiet = model.StatusWorking, model.QuietSubagents })
	h.tickAt(time.Minute)
	must(t)(h.svc.StartPlan(bg, key, 4))
	h.svc.mu.Lock()
	h.svc.expDay, h.svc.expPings = store.Day(t0), experiment.DailyPings
	h.svc.mu.Unlock()
	h.tickAt(2 * time.Minute)
	if v, _ := h.svc.Session(key); v.Experiment != nil {
		t.Fatalf("episode opened past the daily budget: %+v", v.Experiment)
	}
}

func TestExperimentLeftAndRestart(t *testing.T) {
	h := quietHarness(t, model.ArmHold)
	// The session goes idle without a call from the main agent.
	h.src.edit(key, func(s *model.Session) { s.Status, s.Quiet = model.StatusIdle, "" })
	h.tickAt(3 * time.Minute)
	h.tickAt(4 * time.Minute)
	got := h.experiments(t0)
	if len(got) != 1 || got[0].Returned != 0 || got[0].Episodes != 1 || got[0].CostUSD != 0 {
		t.Fatalf("left while warm: %+v", got)
	}
	// The same stretch does not open a second episode.
	h.src.edit(key, func(s *model.Session) { s.Status, s.Quiet = model.StatusWorking, model.QuietSubagents })
	h.tickAt(5 * time.Minute)
	if v, _ := h.svc.Session(key); v.Experiment != nil {
		t.Fatalf("stretch reopened: %+v", v.Experiment)
	}

	// A new stretch opens one; a restart cuts it off, and it is not reported.
	h.at(10 * time.Minute)
	h.mainReturns(ctxK)
	h.src.edit(key, func(s *model.Session) { s.Quiet = model.QuietSubagents })
	h.tickAt(11 * time.Minute)
	h.tickAt(13 * time.Minute)
	if v, _ := h.svc.Session(key); v.Experiment == nil {
		t.Fatal("no episode for the new stretch")
	}
	h.restart()
	h.tickAt(14 * time.Minute)
	if got := h.experiments(t0); len(got) != 1 || got[0].Episodes != 1 {
		t.Fatalf("after restart: %+v", got)
	}
}

func TestExperimentStopRule(t *testing.T) {
	h := quietHarness(t, model.ArmPing)
	// Earlier treatment episodes that each cost more than a re-cache.
	for i := range experiment.StopAfter {
		e, ok, err := h.st.InsertEpisode(bg, store.Episode{Experiment: subagentsExp, Arm: model.ArmPing,
			Cause: model.QuietSubagents, PlanID: 999, Key: "claude:old", QuietFrom: t0.Add(-time.Duration(i+1) * time.Hour),
			StartedAt: t0, ContextSize: ctxK, Recache: 1})
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		e.EndedAt, e.EndedBy, e.Rewrite = t0, model.EpisodeReturned, 1.5
		if err := h.st.CloseEpisode(bg, e); err != nil {
			t.Fatal(err)
		}
	}
	h.mainReturns(ctxK)
	h.tickAt(3 * time.Minute)
	if !h.svc.expStopped[subagentsExp] {
		t.Fatal("experiment not stopped")
	}
	h.restart()
	if !h.svc.expStopped[subagentsExp] {
		t.Fatal("stop not kept across a restart")
	}
}

func TestExperimentReservesBudget(t *testing.T) {
	h := quietHarness(t, model.ArmPing)
	h.tickAt(55 * time.Minute)
	if h.del.count() != 1 {
		t.Fatalf("treatment pings = %d", h.del.count())
	}
	// Until the reply is priced, a cache read of the context is held
	// against the day's budget.
	h.svc.mu.Lock()
	held := h.svc.expCost
	h.svc.mu.Unlock()
	if !near(held, (ctxK+10)*0.2/1e6) { // the context includes the input tokens
		t.Fatalf("held %v before the reply", held)
	}
	h.at(55*time.Minute + 5*time.Second)
	h.reply(key, h.del.last().id, warmUsage(ctxK))
	h.tickAt(56 * time.Minute)
	h.svc.mu.Lock()
	spent := h.svc.expCost
	h.svc.mu.Unlock()
	if !near(spent, warmPingCost) {
		t.Fatalf("spent %v after the reply; want %v", spent, warmPingCost)
	}
}

// The ping arm must not fail or pause a plan that, working, would have
// run on: an unreachable session or failing deliveries just stop the
// treatment.
func TestExperimentTreatmentNeverFailsPlan(t *testing.T) {
	for _, c := range []struct {
		name   string
		break_ func(h *harness)
	}{
		{"unreachable", func(h *harness) {
			h.src.edit(key, func(s *model.Session) { s.Reach = model.ReachNone })
		}},
		{"delivery fails", func(h *harness) { h.del.setErr(errors.New("boom")) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := quietHarness(t, model.ArmPing)
			c.break_(h)
			for m := 50; m <= 70; m++ {
				h.tickAt(time.Duration(m) * time.Minute)
			}
			if p := h.plan(key); p == nil || p.State != model.PlanActive {
				t.Fatalf("plan %+v", p)
			}
		})
	}
}

func TestExperimentNoneMidTurn(t *testing.T) {
	h := newHarness(t)
	h.exp = true
	h.addSession(key, 100)
	h.src.edit(key, func(s *model.Session) { s.Status, s.Quiet = model.StatusWorking, model.QuietTool })
	h.tickAt(time.Minute)
	must(t)(h.svc.StartPlan(bg, key, 4))
	h.tickAt(2 * time.Minute)
	h.tickAt(55 * time.Minute)
	if v, _ := h.svc.Session(key); v.Experiment != nil || h.del.count() != 0 {
		t.Fatalf("episode %+v, pings %d for a session mid-turn", v.Experiment, h.del.count())
	}
}

// A failed treatment leaves nothing behind that fails the plan once the
// session stops working (the user presses Esc while the cache is cold).
func TestExperimentFailedTreatmentThenIdle(t *testing.T) {
	for _, arm := range []string{model.ArmHold, model.ArmPing} {
		t.Run(arm, func(t *testing.T) {
			h := quietHarness(t, arm)
			h.del.setErr(errors.New("busy"))
			for m := 50; m < 60; m++ {
				h.tickAt(time.Duration(m) * time.Minute)
			}
			h.del.setErr(nil)
			h.src.edit(key, func(s *model.Session) { s.Status, s.Quiet = model.StatusIdle, "" })
			for m := 61; m <= 64; m++ {
				h.tickAt(time.Duration(m) * time.Minute)
			}
			if p := h.plan(key); p == nil || p.State != model.PlanActive {
				t.Fatalf("plan %+v", p)
			}
		})
	}
}

// The last treatment ping of the day must fit in the budget.
func TestExperimentPingFitsBudget(t *testing.T) {
	h := quietHarness(t, model.ArmPing)
	h.svc.mu.Lock()
	h.svc.expCost = experiment.DailyCostUSD - 0.01 // less than one ping's read
	h.svc.mu.Unlock()
	h.tickAt(55 * time.Minute)
	if h.del.count() != 0 {
		t.Fatal("pinged past the daily budget")
	}
}
