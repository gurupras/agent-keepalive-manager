package scheduler

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

func TestProjectRuleChecks(t *testing.T) {
	h := newHarness(t)
	for _, r := range []model.ProjectRule{
		{Project: "", Hours: 1},
		{Project: "rel/path", Hours: 1},
		{Project: "/p"},
		{Project: "/p", Hours: -1},
		{Project: "/p", Hours: math.NaN()},
		{Project: "/p", Hours: 37},
		{Project: "/p", Hours: 2, Indefinite: true},
	} {
		if _, err := h.svc.SetProjectRule(bg, r); !errors.Is(err, model.ErrInvalid) {
			t.Errorf("%+v: %v", r, err)
		}
	}
	if err := h.svc.RemoveProjectRule(bg, "/p"); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("remove missing: %v", err)
	}
}

// Every session of the project gets a plan, including one that starts
// later; other projects and unreachable sessions are left alone.
func TestProjectRuleKeepsSessionsWarm(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	other := session("claude:o", 300)
	other.Project = "/elsewhere"
	h.src.put(other)
	mute := session("claude:m", 400)
	mute.Reach = model.ReachNone
	h.src.put(mute)
	h.tickAt(time.Minute)

	r, err := h.svc.SetProjectRule(bg, model.ProjectRule{Project: "/p/", Indefinite: true})
	if err != nil || r.Project != "/p" {
		t.Fatalf("set = %+v, %v", r, err)
	}
	p := h.plan(key)
	if p == nil || !p.Indefinite || p.Project != "/p" {
		t.Fatalf("plan = %+v", p)
	}
	if h.plan("claude:o") != nil || h.plan("claude:m") != nil {
		t.Fatal("rule reached another project or an unreachable session")
	}

	h.addSession("claude:s2", 200)
	h.tickAt(2 * time.Minute)
	if p := h.plan("claude:s2"); p == nil || p.Project != "/p" {
		t.Fatalf("new session's plan = %+v", p)
	}
	if rs := h.svc.ProjectRules(); len(rs) != 1 || rs[0].Project != "/p" {
		t.Fatalf("rules = %+v", rs)
	}
}

// A plan the user cancels is not started again; one that ran out starts
// again with the next human prompt.
func TestProjectRuleRespectsEndedPlans(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.addSession("claude:s2", 200)
	h.tickAt(2 * time.Hour) // both caches already cold: the plans wait
	must2(t)(h.svc.SetProjectRule(bg, model.ProjectRule{Project: "/p", Hours: 1}))
	if err := h.svc.CancelPlan(bg, key); err != nil {
		t.Fatal(err)
	}
	h.tickAt(2*time.Hour + time.Minute)
	if h.plan(key) != nil {
		t.Fatal("cancelled plan started again")
	}

	// s2's hour runs out.
	h.tickAt(3*time.Hour + 30*time.Minute)
	if h.plan("claude:s2") != nil {
		t.Fatal("plan did not end")
	}
	h.tickAt(3*time.Hour + 31*time.Minute)
	if h.plan("claude:s2") != nil {
		t.Fatal("ended plan started again without a prompt")
	}
	h.at(3*time.Hour + 32*time.Minute)
	h.returnNow("claude:s2", 0)
	h.tick()
	if p := h.plan("claude:s2"); p == nil || p.Project != "/p" {
		t.Fatalf("plan after a prompt = %+v", p)
	}
	if h.plan(key) != nil {
		t.Fatal("cancelled plan started again")
	}

	// Setting the rule again is a new opt-in: the cancelled one starts too.
	must2(t)(h.svc.SetProjectRule(bg, model.ProjectRule{Project: "/p", Hours: 1}))
	if h.plan(key) == nil {
		t.Fatal("rule set again did not start the cancelled session")
	}
}

// Changing the rule changes the plans it started, not the user's own;
// removing it cancels its plans; both survive a restart.
func TestProjectRuleChangeAndRemove(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.addSession("claude:s2", 200)
	h.tickAt(time.Minute)
	own := must(t)(h.svc.StartPlan(bg, "claude:s2", 2))
	must2(t)(h.svc.SetProjectRule(bg, model.ProjectRule{Project: "/p", Hours: 1}))
	if p := h.plan(key); p == nil || p.Indefinite {
		t.Fatalf("plan = %+v", p)
	}

	h.restart()
	h.tickAt(2 * time.Minute)
	if rs := h.svc.ProjectRules(); len(rs) != 1 || rs[0].Hours != 1 {
		t.Fatalf("rules after restart = %+v", rs)
	}
	must2(t)(h.svc.SetProjectRule(bg, model.ProjectRule{Project: "/p", Indefinite: true}))
	if p := h.plan(key); p == nil || !p.Indefinite || p.Project != "/p" {
		t.Fatalf("changed plan = %+v", p)
	}
	if p := h.plan("claude:s2"); p == nil || p.Indefinite || p.ID != own.ID {
		t.Fatalf("own plan = %+v", p)
	}

	if err := h.svc.RemoveProjectRule(bg, "/p"); err != nil {
		t.Fatal(err)
	}
	if h.plan(key) != nil || h.plan("claude:s2") == nil {
		t.Fatal("remove ended the wrong plans")
	}
	h.tickAt(3 * time.Minute)
	if h.plan(key) != nil || len(h.svc.ProjectRules()) != 0 {
		t.Fatal("rule still applies after removal")
	}
}

func must2(t *testing.T) func(model.ProjectRule, error) model.ProjectRule {
	return func(r model.ProjectRule, err error) model.ProjectRule {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
}
