package store

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

var ctx = context.Background()

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

var t0 = time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)

func TestMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := s.SchemaVersion(ctx); v != len(migrations) {
		t.Fatalf("version = %d", v)
	}
	// A later migration is applied on top, once.
	more := append(append([]migration(nil), migrations...), migration{99, `CREATE TABLE extra (x INTEGER)`})
	if err := s.migrateTo(ctx, more); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateTo(ctx, more); err != nil {
		t.Fatalf("re-running migrations: %v", err)
	}
	s.Close()
	// Reopening with the shipped list refuses a database from the future.
	if _, err := Open(path); err == nil {
		t.Fatal("opened a newer schema")
	}
	// Reopening a current database is a no-op.
	p2 := filepath.Join(t.TempDir(), "n.db")
	s2, _ := Open(p2)
	s2.Close()
	s3, err := Open(p2)
	if err != nil {
		t.Fatal(err)
	}
	s3.Close()
}

func TestMemory(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.InsertPlan(ctx, Plan{Plan: model.Plan{Key: "k", StartedAt: t0, Until: t0, State: model.PlanActive}}); err != nil {
		t.Fatal(err)
	}
}

func TestPlanRoundTrip(t *testing.T) {
	s := open(t)
	p := Plan{
		Plan: model.Plan{Key: "claude:a", PID: 42, Hours: 2, StartedAt: t0, Until: t0.Add(2 * time.Hour),
			State: model.PlanActive, Indefinite: true, NextPing: t0.Add(time.Minute), Pings: 3, PingCost: 0.5,
			Reason: "r", LastError: "e"},
		ProcStart: "123", RequestedHours: 3, ColdStreak: 1, WaitWarm: true, LastSeen: t0.Add(time.Second),
		BaseFrom: t0.Add(-time.Minute), BaseContext: 1000, BaseModel: "m", LastPingAt: t0.Add(2 * time.Second),
	}
	p, err := s.InsertPlan(ctx, p)
	if err != nil || p.ID == 0 {
		t.Fatal(err)
	}
	got, err := s.GetPlan(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StartedAt.Equal(p.StartedAt) || !got.LastSeen.Equal(p.LastSeen) || got.ProcStart != "123" ||
		!got.Indefinite || !got.WaitWarm || got.BaseContext != 1000 || got.ColdStreak != 1 || got.RequestedHours != 3 {
		t.Fatalf("round trip: %+v", got)
	}
	p.State = model.PlanPaused
	if err := s.UpdatePlan(ctx, p); err != nil {
		t.Fatal(err)
	}
	done := Plan{Plan: model.Plan{Key: "claude:b", StartedAt: t0, Until: t0, State: model.PlanDone}}
	s.InsertPlan(ctx, done)
	open, _ := s.OpenPlans(ctx)
	if len(open) != 1 || open[0].State != model.PlanPaused {
		t.Fatalf("open plans: %+v", open)
	}
	if err := s.UpdatePlan(ctx, Plan{Plan: model.Plan{ID: 999}}); err != ErrNotFound {
		t.Fatalf("update missing: %v", err)
	}
}

// setup makes a plan with an open segment holding two pings (0.1 + 0.2).
func setup(t *testing.T, s *Store) (Plan, Segment) {
	t.Helper()
	p, _ := s.InsertPlan(ctx, Plan{Plan: model.Plan{Key: "claude:a", StartedAt: t0, Until: t0.Add(time.Hour), State: model.PlanActive}})
	g := Segment{PlanID: p.ID, Key: p.Key, Harness: model.Claude, Model: "claude-opus-5-5", IdleFrom: t0, ContextSize: 100_000, OpenedAt: t0}
	if err := s.InsertPing(ctx, Ping{PingRecord: model.PingRecord{ID: "p1", PlanID: p.ID, Key: p.Key, SentAt: t0.Add(4 * time.Minute),
		State: model.PingVerified, Cost: 0.1}, SegFrom: t0}, &g); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertPing(ctx, Ping{PingRecord: model.PingRecord{ID: "p2", PlanID: p.ID, Key: p.Key, SentAt: t0.Add(8 * time.Minute),
		State: model.PingDelivered, Cost: 0.2}, SegFrom: t0}, &g); err != nil {
		t.Fatal(err)
	}
	// A failed ping is not counted.
	s.InsertPing(ctx, Ping{PingRecord: model.PingRecord{ID: "p3", PlanID: p.ID, Key: p.Key, SentAt: t0.Add(9 * time.Minute),
		State: model.PingFailed}, SegFrom: t0}, nil)
	return p, g
}

func TestSegmentCloseIdempotent(t *testing.T) {
	s := open(t)
	p, g := setup(t, s)
	segs, _ := s.Segments(ctx)
	if len(segs) != 1 || segs[0].ContextSize != 100_000 {
		t.Fatalf("segments: %+v", segs)
	}
	e := model.LedgerEntry{PlanID: p.ID, Key: p.Key, Harness: model.Claude, Model: g.Model, IdleFrom: g.IdleFrom,
		EndedAt: t0.Add(10 * time.Minute), EndedBy: model.EndReturned, Outcome: model.OutcomeSaved, Avoided: 1.0,
		ContextSize: g.ContextSize}
	p.Pings = 2
	next := &Segment{PlanID: p.ID, Key: p.Key, IdleFrom: t0.Add(10 * time.Minute)}
	got, ins, err := s.CloseSegment(ctx, e, &p, next)
	if err != nil || !ins {
		t.Fatalf("close: %v %v", ins, err)
	}
	if got.Pings != 2 || !near(got.PingCost, 0.3) || !near(got.Net, 0.7) {
		t.Fatalf("entry: %+v", got)
	}
	segs, _ = s.Segments(ctx)
	if len(segs) != 1 || !segs[0].IdleFrom.Equal(next.IdleFrom) {
		t.Fatalf("segments after close: %+v", segs)
	}
	// Booking the same segment again (a restart replays it) is a no-op.
	again, ins, err := s.CloseSegment(ctx, e, nil, nil)
	if err != nil || ins || again.ID != got.ID {
		t.Fatalf("second close: %v %v %+v", ins, err, again)
	}
	es, _ := s.Ledger(ctx, time.Time{}, time.Time{})
	if len(es) != 1 {
		t.Fatalf("ledger has %d entries", len(es))
	}
	// A wasted entry never carries an avoided amount.
	w := model.LedgerEntry{PlanID: p.ID, Key: p.Key, IdleFrom: next.IdleFrom, EndedAt: t0.Add(time.Hour),
		EndedBy: model.EndExpired, Outcome: model.OutcomeWasted, Avoided: 5}
	w, _, _ = s.CloseSegment(ctx, w, nil, nil)
	if w.Avoided != 0 || w.Net != 0 || w.Pings != 0 || w.ID != 0 {
		t.Fatalf("empty segment booked: %+v", w)
	}
	if es, _ := s.Ledger(ctx, time.Time{}, time.Time{}); len(es) != 1 {
		t.Fatalf("a segment without pings reached the ledger")
	}
	if segs, _ := s.Segments(ctx); len(segs) != 0 {
		t.Fatalf("empty segment not deleted: %+v", segs)
	}
}

func TestLatePingCostCorrectsLedger(t *testing.T) {
	s := open(t)
	p, g := setup(t, s)
	e := model.LedgerEntry{PlanID: p.ID, Key: p.Key, IdleFrom: g.IdleFrom, EndedAt: t0.Add(10 * time.Minute),
		EndedBy: model.EndExpired, Outcome: model.OutcomeWasted}
	s.CloseSegment(ctx, e, nil, nil)
	// p2's reply is read after the segment was booked: 0.2 -> 0.25.
	p2, err := s.GetPing(ctx, "p2")
	if err != nil {
		t.Fatal(err)
	}
	p2.State, p2.Cost, p2.Calls = model.PingVerified, 0.25, 1
	fixed, err := s.UpdatePing(ctx, p2)
	if err != nil || len(fixed) != 1 {
		t.Fatalf("update: %v %v", fixed, err)
	}
	if !near(fixed[0].PingCost, 0.35) || !near(fixed[0].Net, -0.35) {
		t.Fatalf("corrected: %+v", fixed[0])
	}
	pl, _ := s.GetPlan(ctx, p.ID)
	if !near(pl.PingCost, 0.05) {
		t.Fatalf("plan ping cost delta = %v", pl.PingCost)
	}
	// Unchanged cost: nothing to correct.
	if fixed, _ := s.UpdatePing(ctx, p2); len(fixed) != 0 {
		t.Fatal("no-op update corrected the ledger")
	}
	if _, err := s.UpdatePing(ctx, Ping{PingRecord: model.PingRecord{ID: "nope"}}); err != ErrNotFound {
		t.Fatalf("missing ping: %v", err)
	}
}

func TestPingQueries(t *testing.T) {
	s := open(t)
	setup(t, s)
	ps, _ := s.PingsByKey(ctx, "claude:a", 2)
	if len(ps) != 2 || ps[0].ID != "p3" || ps[1].ID != "p2" {
		t.Fatalf("newest first: %+v", ps)
	}
	un, _ := s.UnsettledPings(ctx, t0.Add(5*time.Minute))
	if len(un) != 1 || un[0].ID != "p2" {
		t.Fatalf("unsettled (recent after p1): %+v", un)
	}
	un, _ = s.UnsettledPings(ctx, t0)
	if len(un) != 2 {
		t.Fatalf("unsettled incl. recent verified: %+v", un)
	}
	got, _ := s.GetPing(ctx, "p1")
	if !got.SegFrom.Equal(t0) || got.State != model.PingVerified {
		t.Fatalf("ping: %+v", got)
	}
}

func TestLedgerRangeAndTotals(t *testing.T) {
	s := open(t)
	for i, e := range []model.LedgerEntry{
		{PlanID: 1, Key: "a", IdleFrom: t0, EndedAt: t0.Add(time.Hour), EndedBy: model.EndReturned, Outcome: model.OutcomeSaved, Avoided: 2},
		{PlanID: 1, Key: "a", IdleFrom: t0.Add(2 * time.Hour), EndedAt: t0.Add(3 * time.Hour), EndedBy: model.EndExpired, Outcome: model.OutcomeWasted},
		{PlanID: 2, Key: "b", IdleFrom: t0, EndedAt: t0.Add(5 * time.Hour), EndedBy: model.EndCold, Outcome: model.OutcomeWasted},
	} {
		s.InsertPing(ctx, Ping{PingRecord: model.PingRecord{ID: string(rune('x' + i)), PlanID: e.PlanID, Key: e.Key,
			SentAt: e.IdleFrom.Add(time.Minute), State: model.PingVerified, Cost: 0.5}, SegFrom: e.IdleFrom}, nil)
		if _, _, err := s.CloseSegment(ctx, e, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	es, _ := s.Ledger(ctx, t0.Add(30*time.Minute), t0.Add(4*time.Hour))
	if len(es) != 2 {
		t.Fatalf("range: %d", len(es))
	}
	tot := SumTotals(es)
	if !near(tot.Saved, 1.5) || !near(tot.Wasted, 0.5) || !near(tot.Net, 1) || tot.Returns != 1 || tot.Expired != 1 || tot.Pings != 2 {
		t.Fatalf("totals: %+v", tot)
	}
	byKey, _ := s.TotalsByKey(ctx)
	if !near(byKey["a"].Net, 1) || !near(byKey["b"].Wasted, 0.5) {
		t.Fatalf("by key: %+v", byKey)
	}
}

func TestSettingsAndKV(t *testing.T) {
	s := open(t)
	st, _ := s.LoadSettings(ctx)
	if st != model.DefaultSettings() {
		t.Fatalf("defaults: %+v", st)
	}
	st.MaxHours = 12
	s.SaveSettings(ctx, st)
	if got, _ := s.LoadSettings(ctx); got.MaxHours != 12 {
		t.Fatalf("saved: %+v", got)
	}
	if _, ok, _ := s.Get(ctx, "x"); ok {
		t.Fatal("missing key found")
	}
	s.Set(ctx, "ttl:claude:m", "300")
	s.Set(ctx, "ttl:claude:m", "240")
	s.Set(ctx, "other", "1")
	if v, ok, _ := s.Get(ctx, "ttl:claude:m"); !ok || v != "240" {
		t.Fatalf("get = %q", v)
	}
	if m, _ := s.Prefixed(ctx, "ttl:"); len(m) != 1 {
		t.Fatalf("prefixed: %v", m)
	}
}

func TestDayStats(t *testing.T) {
	s := open(t)
	day := Day(t0)
	s.RecordSighting(ctx, day, model.Claude, "claude:a")
	s.RecordSighting(ctx, day, model.Claude, "claude:a")
	s.RecordSighting(ctx, day, model.Claude, "claude:b")
	s.RecordSighting(ctx, day, model.Codex, "codex:c")
	s.RecordSighting(ctx, Day(t0.AddDate(0, 0, 1)), model.Codex, "codex:d")

	s.InsertPlan(ctx, Plan{Plan: model.Plan{Key: "claude:a", StartedAt: t0, Until: t0, State: model.PlanActive}, RequestedHours: 2})
	s.InsertPlan(ctx, Plan{Plan: model.Plan{Key: "claude:b", StartedAt: t0.Add(time.Hour), Until: t0, State: model.PlanDone, Indefinite: true}, RequestedHours: 36})
	s.InsertPlan(ctx, Plan{Plan: model.Plan{Key: "claude:c", StartedAt: t0.AddDate(0, 0, -1), Until: t0, State: model.PlanDone}, RequestedHours: 5})

	// Pings sent on the day (1-3) and the next day (4), all in plan 1's
	// segment from t0-1h; 3 failed.
	pings := []model.PingRecord{
		{ID: "1", PlanID: 1, Key: "claude:a", SentAt: t0, State: model.PingVerified, Cost: 0.1},
		{ID: "2", PlanID: 1, Key: "claude:a", SentAt: t0.Add(time.Hour), State: model.PingVerified, Cost: 0.4, Cold: true},
		{ID: "3", PlanID: 1, Key: "claude:a", SentAt: t0.Add(2 * time.Hour), State: model.PingFailed},
		{ID: "4", PlanID: 1, Key: "claude:a", SentAt: t0.AddDate(0, 0, 1), State: model.PingVerified, Cost: 9},
	}
	for _, p := range pings {
		s.InsertPing(ctx, Ping{PingRecord: p, SegFrom: t0.Add(-time.Hour)}, nil)
	}
	// One cheap ping (sent the day before) in each other segment.
	for i, plan := range []int64{2, 3, 4} {
		s.InsertPing(ctx, Ping{PingRecord: model.PingRecord{ID: fmt.Sprint("o", i), PlanID: plan, Key: "x",
			SentAt: t0.AddDate(0, 0, -1), State: model.PingVerified, Cost: 0.01}, SegFrom: t0}, nil)
	}
	for _, e := range []model.LedgerEntry{
		{PlanID: 1, Key: "claude:a", Harness: model.Claude, Model: "claude-opus-5-5", IdleFrom: t0.Add(-time.Hour),
			EndedAt: t0.Add(3 * time.Hour), EndedBy: model.EndReturned, Outcome: model.OutcomeSaved, Avoided: 12, ContextSize: 1000},
		{PlanID: 2, Key: "claude:b", Harness: model.Claude, Model: "claude-opus-5-5", IdleFrom: t0,
			EndedAt: t0.Add(4 * time.Hour), EndedBy: model.EndReturned, Outcome: model.OutcomeSaved, Avoided: 1, ContextSize: 500},
		{PlanID: 3, Key: "codex:c", Harness: model.Codex, Model: "gpt-5", IdleFrom: t0,
			EndedAt: t0.Add(30 * time.Minute), EndedBy: model.EndExpired, Outcome: model.OutcomeWasted},
		{PlanID: 4, Key: "codex:c", Harness: model.Codex, Model: "gpt-5", IdleFrom: t0,
			EndedAt: t0.AddDate(0, 0, 2), EndedBy: model.EndExpired, Outcome: model.OutcomeWasted},
	} {
		if _, ins, err := s.CloseSegment(ctx, e, nil, nil); err != nil || !ins {
			t.Fatalf("book %+v: %v %v", e, ins, err)
		}
	}

	ds, err := s.DayStats(ctx, t0.Add(5*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if ds.Day != "2026-10-26" || ds.Sessions[model.Claude] != 2 || ds.Sessions[model.Codex] != 1 {
		t.Fatalf("sessions: %+v", ds)
	}
	if ds.Plans.Started != 2 || ds.Plans.Indefinite != 1 || ds.Plans.Hours != 38 {
		t.Fatalf("plans: %+v", ds.Plans)
	}
	if ds.Pings.Sent != 3 || ds.Pings.Verified != 2 || ds.Pings.Cold != 1 || ds.Pings.Failed != 1 || !near(ds.Pings.CostUSD, 0.5) {
		t.Fatalf("pings: %+v", ds.Pings)
	}
	if len(ds.Outcomes) != 2 {
		t.Fatalf("outcomes: %+v", ds.Outcomes)
	}
	o := ds.Outcomes[0]
	// Entry 1 holds pings 1, 2, 4 (3 failed): cost 9.5, net 12-9.5; entry 2
	// one ping at 0.01, net 0.99.
	if o.Harness != model.Claude || o.Count != 2 || o.Pings != 4 || !near(o.PingCostUSD, 9.51) ||
		!near(o.AvoidedUSD, 13) || !near(o.NetUSD, 3.49) || o.ContextTokens != 1500 || o.IdleMinutes != 240+240 {
		t.Fatalf("claude outcome: %+v", o)
	}
	if c := ds.Outcomes[1]; c.Harness != model.Codex || c.Outcome != model.OutcomeWasted || c.IdleMinutes != 30 {
		t.Fatalf("codex outcome: %+v", c)
	}
	// A day with nothing.
	empty, _ := s.DayStats(ctx, t0.AddDate(0, 0, -10))
	if empty.Plans.Started != 0 || len(empty.Outcomes) != 0 || len(empty.Sessions) != 0 {
		t.Fatalf("empty day: %+v", empty)
	}
}
