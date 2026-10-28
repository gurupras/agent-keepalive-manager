package scheduler

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/pricing"
)

func TestStartPlanChecks(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	unreachable := session("claude:u", 200)
	unreachable.Reach, unreachable.ReachWhy = model.ReachNone, "no socket"
	h.src.put(unreachable)
	h.tickAt(time.Minute)
	for _, hrs := range []float64{0, -1, math.NaN(), 37} {
		if _, err := h.svc.StartPlan(bg, key, hrs); !errors.Is(err, model.ErrInvalid) {
			t.Errorf("hours %v: %v", hrs, err)
		}
	}
	if _, err := h.svc.StartPlan(bg, "claude:nope", 1); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("unknown key: %v", err)
	}
	if _, err := h.svc.StartPlan(bg, "claude:u", 1); !errors.Is(err, model.ErrUnreachable) {
		t.Errorf("unreachable: %v", err)
	}
	// Capped at break-even: Opus 5.5 at the 1h tier, 39 pings x 55 min.
	p := must(t)(h.svc.StartPlan(bg, key, 36))
	if !near(p.Hours, 35.75) || !p.Until.Equal(t0.Add(time.Minute+hoursDur(35.75))) {
		t.Fatalf("capped plan = %+v", p)
	}
	if _, err := h.svc.StartPlan(bg, key, 1); !errors.Is(err, model.ErrConflict) {
		t.Errorf("second plan: %v", err)
	}
	if sp := h.storedPlan(p.ID); sp.RequestedHours != 36 {
		t.Fatalf("requested hours = %v", sp.RequestedHours)
	}
}

func TestStartPlanOnColdCacheWaits(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(2 * time.Hour) // last call at t0, 1h TTL: cold
	must(t)(h.svc.StartPlan(bg, key, 5))
	h.tickAt(2*time.Hour + time.Minute)
	if p := h.plan(key); p.Blocked == "" || h.del.count() != 0 {
		t.Fatalf("cold start: %+v", p)
	}
}

func TestPlanDoneAtUntil(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(4 * time.Minute)
	p := must(t)(h.svc.StartPlan(bg, key, 1)) // until 1h04m
	h.tickAt(55 * time.Minute)
	h.reply(key, h.del.last().id, warmUsage(ctxK))
	h.tickAt(56 * time.Minute)
	// Past Until but the last ping keeps the cache warm until 1h55m.
	h.tickAt(time.Hour + 30*time.Minute)
	if h.plan(key) == nil {
		t.Fatal("plan closed while its last ping still holds the cache")
	}
	if h.del.count() != 1 {
		t.Fatal("pinged after Until")
	}
	h.tickAt(2 * time.Hour)
	sp := h.storedPlan(p.ID)
	if sp.State != model.PlanDone || !sp.EndedAt.Equal(t0.Add(time.Hour+55*time.Minute)) {
		t.Fatalf("plan = %+v", sp.Plan)
	}
	es := h.ledger()
	if len(es) != 1 || es[0].EndedBy != model.EndExpired || !near(es[0].Net, -warmPingCost) {
		t.Fatalf("ledger = %+v", es)
	}
}

func TestPlanDoneWithoutPings(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	p := must(t)(h.svc.StartPlan(bg, key, 0.5))
	h.tickAt(32 * time.Minute)
	if sp := h.storedPlan(p.ID); sp.State != model.PlanDone || len(h.ledger()) != 0 {
		t.Fatalf("plan = %+v, ledger %v", sp.Plan, h.ledger())
	}
}

func TestCancelBooksWasted(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	p := must(t)(h.svc.StartPlan(bg, key, 3))
	h.tickAt(55 * time.Minute)
	h.reply(key, h.del.last().id, warmUsage(ctxK))
	h.tickAt(56 * time.Minute)
	if err := h.svc.CancelPlan(bg, key); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.CancelPlan(bg, key); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("second cancel: %v", err)
	}
	if sp := h.storedPlan(p.ID); sp.State != model.PlanCancelled {
		t.Fatalf("state = %s", sp.State)
	}
	es, tot, _ := h.svc.Ledger(time.Time{}, time.Time{})
	if len(es) != 1 || es[0].EndedBy != model.EndCancelled || !near(tot.Wasted, warmPingCost) || tot.Expired != 1 {
		t.Fatalf("ledger = %+v %+v", es, tot)
	}
}

func TestExtendPlan(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(0)
	must(t)(h.svc.StartPlan(bg, key, 2))
	p := must(t)(h.svc.ExtendPlan(bg, key, 3))
	if !p.Until.Equal(t0.Add(5 * time.Hour)) {
		t.Fatalf("until = %v", p.Until)
	}
	p = must(t)(h.svc.ExtendPlan(bg, key, 100))
	if !p.Until.Equal(t0.Add(36 * time.Hour)) {
		t.Fatalf("not capped at MaxHours: %v", p.Until)
	}
	if _, err := h.svc.ExtendPlan(bg, key, 1); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("beyond cap: %v", err)
	}
	if _, err := h.svc.ExtendPlan(bg, key, -1); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("negative: %v", err)
	}
	if _, err := h.svc.ExtendPlan(bg, "claude:x", 1); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("no plan: %v", err)
	}
}

func TestIndefiniteRolls(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	p := must(t)(h.svc.KeepIndefinitely(bg, key))
	win := hoursDur(35.75)
	if !p.Indefinite || !p.Until.Equal(t0.Add(time.Minute+win)) {
		t.Fatalf("plan = %+v", p)
	}
	h.at(3 * time.Hour)
	h.returnNow(key, ctxK)
	h.tickAt(3*time.Hour + time.Minute)
	if p := h.plan(key); !p.Until.Equal(t0.Add(3*time.Hour + win)) {
		t.Fatalf("not rolled: %v", p.Until)
	}
	// Converting an existing fixed plan.
	h2 := newHarness(t)
	h2.addSession(key, 100)
	h2.tickAt(time.Minute)
	must(t)(h2.svc.StartPlan(bg, key, 1))
	p2 := must(t)(h2.svc.KeepIndefinitely(bg, key))
	if !p2.Indefinite || !p2.Until.Equal(t0.Add(time.Minute+win)) {
		t.Fatalf("converted = %+v", p2)
	}
}

func TestSettingsValidation(t *testing.T) {
	h := newHarness(t)
	good := model.Settings{MaxHours: 12, MarginMin: 3, ScanSeconds: 10, ShowMissed: true}
	if got, err := h.svc.UpdateSettings(bg, good); err != nil || got != good {
		t.Fatalf("good: %v", err)
	}
	for _, s := range []model.Settings{
		{MaxHours: 0.4, MarginMin: 5, ScanSeconds: 5},
		{MaxHours: 169, MarginMin: 5, ScanSeconds: 5},
		{MaxHours: math.NaN(), MarginMin: 5, ScanSeconds: 5},
		{MaxHours: 36, MarginMin: 0.5, ScanSeconds: 5},
		{MaxHours: 36, MarginMin: 16, ScanSeconds: 5},
		{MaxHours: 36, MarginMin: 5, ScanSeconds: 1},
		{MaxHours: 36, MarginMin: 5, ScanSeconds: 61},
	} {
		if _, err := h.svc.UpdateSettings(bg, s); !errors.Is(err, model.ErrInvalid) {
			t.Errorf("%+v accepted: %v", s, err)
		}
	}
	for _, s := range []model.Settings{
		{MaxHours: 0.5, MarginMin: 1, ScanSeconds: 2},
		{MaxHours: 168, MarginMin: 15, ScanSeconds: 60},
	} {
		if err := ValidateSettings(s); err != nil {
			t.Errorf("bounds %+v refused: %v", s, err)
		}
	}
	if h.svc.Settings() != good {
		t.Fatal("invalid update changed settings")
	}
	h.restart()
	if h.svc.Settings() != good {
		t.Fatal("settings not persisted")
	}
}

func TestSubscribeNeverBlocks(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	ch, cancel := h.svc.Subscribe()
	// Never read: the scheduler must keep going.
	for i := 0; i < subBuffer+10; i++ {
		h.svc.mu.Lock()
		h.svc.publish("plan", i)
		h.svc.mu.Unlock()
	}
	h.tickAt(time.Minute)
	if len(ch) != subBuffer {
		t.Fatalf("buffered %d", len(ch))
	}
	cancel()
	cancel()
	for range ch {
	}
	// A live subscriber sees the sessions snapshot after a change.
	ch2, cancel2 := h.svc.Subscribe()
	defer cancel2()
	h.addSession("claude:s2", 200)
	h.tickAt(2 * time.Minute)
	got := false
	for len(ch2) > 0 {
		if ev := <-ch2; ev.Type == "sessions" {
			got = true
		}
	}
	if !got {
		t.Fatal("no sessions event")
	}
}

func TestPingNowAttachesToPlanAndViews(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	if _, err := h.svc.PingNow(bg, "claude:none"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	h.src.edit(key, func(s *model.Session) { s.Status = model.StatusWorking; s.StatusSince = t0 })
	h.tickAt(2 * time.Minute)
	if _, err := h.svc.PingNow(bg, key); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("working: %v", err)
	}
	h.src.edit(key, func(s *model.Session) { s.Status = model.StatusIdle })
	h.tickAt(3 * time.Minute)
	p := must(t)(h.svc.StartPlan(bg, key, 2))
	rec, err := h.svc.PingNow(bg, key)
	if err != nil || rec.PlanID != p.ID || rec.State != model.PingDelivered {
		t.Fatalf("ping now = %+v, %v", rec, err)
	}
	ps, _ := h.svc.Pings(key, 0)
	if len(ps) != 1 || ps[0].ID != rec.ID {
		t.Fatalf("pings = %+v", ps)
	}
	v, _ := h.svc.Session(key)
	c := v.Costs
	if !c.Known || !near(c.Recache, (ctxK+10)*8/1e6) || !v.CacheExpiresAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("view = %+v", v)
	}
}

func TestDayStatsFromService(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.addSession("claude:s2", 200)
	h.tickAt(time.Minute)
	must(t)(h.svc.StartPlan(bg, key, 2))
	must(t)(h.svc.KeepIndefinitely(bg, "claude:s2"))
	h.tickAt(55 * time.Minute)
	for _, d := range h.del.sent {
		if d.key == key {
			h.reply(key, d.id, warmUsage(ctxK))
		} else {
			h.reply(d.key, d.id, coldUsage(ctxK))
		}
	}
	h.tickAt(56 * time.Minute)
	ds, err := h.svc.DayStats(t0)
	if err != nil {
		t.Fatal(err)
	}
	if ds.Sessions[model.Claude] != 2 || ds.Plans.Started != 2 || ds.Plans.Indefinite != 1 || !near(ds.Plans.Hours, 2+35.75) {
		t.Fatalf("stats = %+v", ds)
	}
	if ds.Pings.Sent != 2 || ds.Pings.Verified != 2 || ds.Pings.Cold != 1 {
		t.Fatalf("pings = %+v", ds.Pings)
	}
	if len(ds.Outcomes) != 1 || ds.Outcomes[0].EndedBy != model.EndCold || ds.Outcomes[0].Count != 1 {
		t.Fatalf("outcomes = %+v", ds.Outcomes)
	}
}

func TestScanFailureKeepsSessions(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	p := must(t)(h.svc.StartPlan(bg, key, 2))
	h.src.mu.Lock()
	h.src.err = errBoom
	h.src.mu.Unlock()
	h.kill(100)
	for i := 2; i < 6; i++ {
		h.tickAt(time.Duration(i) * time.Minute)
	}
	if h.svc.planByID(p.ID) == nil {
		t.Fatal("a failing scan ended the plan")
	}
}

func TestMissedSimulation(t *testing.T) {
	tb := pricing.Default()
	sess := session(key, 1)
	u := warmUsage(ctxK - 10) // context exactly 100k
	calls := []model.Activity{
		{Kind: model.ActAssistant, Model: opus, Sent: t0, At: t0.Add(time.Minute), Usage: u},
		{Kind: model.ActAssistant, Model: opus, Sent: t0.Add(2 * time.Hour), At: t0.Add(2*time.Hour + time.Minute), Usage: u},
		{Kind: model.ActAssistant, Model: opus, Sent: t0.Add(150 * time.Minute), At: t0.Add(151 * time.Minute), Usage: u},
	}
	now := t0.Add(40 * time.Hour)
	ms := simulate(tb, sess, calls, time.Hour, 5*time.Minute, 36*time.Hour, now)
	ping := (ctxK*0.2 + 5*20) / 1e6 // 0.0201
	// Gap 1 (2h): two pings, back while warm: 0.78 - 2 pings.
	// Gap 2 (30m): shorter than the interval, nothing.
	// Open gap (37.5h): longer than the 35.75h plan: 39 pings wasted.
	wantSaved := 0.78 - 2*ping
	wantWasted := 39 * ping
	if ms.Returns != 1 || ms.Expired != 1 || !near(ms.Saved, wantSaved) || !near(ms.Wasted, wantWasted) ||
		!near(ms.Net, wantSaved-wantWasted) || !ms.Known {
		t.Fatalf("missed = %+v", ms)
	}
	// Unknown models count nothing.
	unk := sess
	unk.Model = "mystery"
	for i := range calls {
		calls[i].Model = "mystery"
	}
	if ms := simulate(tb, unk, calls, time.Hour, 5*time.Minute, 36*time.Hour, now); ms.Known || ms.Returns+ms.Expired != 0 {
		t.Fatalf("unknown = %+v", ms)
	}
}

func TestMissedService(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.at(2 * time.Hour)
	h.returnNow(key, ctxK)
	h.tickAt(2*time.Hour + time.Minute)
	m := h.svc.Missed(t0.Add(-time.Hour))
	if m.Returns != 1 || len(m.Sessions) != 1 || m.Sessions[0].Key != key || m.Hours != 36 {
		t.Fatalf("missed = %+v", m)
	}
	if m := h.svc.Missed(t0.Add(time.Hour)); m.Returns != 0 {
		t.Fatalf("since filter: %+v", m)
	}
}
