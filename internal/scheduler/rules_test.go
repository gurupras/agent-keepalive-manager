package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// Rule 1: the next ping is due TTL − margin after the request time of the
// last API call, not after its response was recorded.
func TestRule1TimingAnchoredOnRequest(t *testing.T) {
	h := newHarness(t)
	h.src.put(session(key, 100))
	// The call went out at t0; its (long) response was recorded at t0+3m.
	h.src.add(key, model.Activity{Kind: model.ActAssistant, Model: opus, Sent: t0, At: t0.Add(3 * time.Minute), Usage: warmUsage(ctxK)})
	h.tickAt(4 * time.Minute)
	must(t)(h.svc.StartPlan(bg, key, 2))
	h.tick()
	if p := h.plan(key); !p.NextPing.Equal(t0.Add(55 * time.Minute)) {
		t.Fatalf("next ping = %v; want request time + 55m", p.NextPing)
	}
	h.tickAt(55*time.Minute - time.Second)
	if h.del.count() != 0 {
		t.Fatal("pinged early")
	}
	h.tickAt(55 * time.Minute)
	if h.del.count() != 1 {
		t.Fatalf("pings = %d at TTL − margin after the request", h.del.count())
	}
	// The ping's reply refreshes the cache at its request time; the next
	// ping is anchored there.
	id := h.del.last().id
	h.at(55*time.Minute + 10*time.Second)
	h.reply(key, id, warmUsage(ctxK))
	h.tickAt(56 * time.Minute)
	if p := h.plan(key); !p.NextPing.Equal(t0.Add(55*time.Minute + 10*time.Second + 55*time.Minute)) {
		t.Fatalf("next ping after reply = %v", p.NextPing)
	}
}

// Rule 2: delivery happens outside the lock, one per session, concurrent
// across sessions, each with a timeout; API reads never wait.
func TestRule2DeliveryOutsideLock(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.addSession("claude:s2", 200)
	h.tickAt(time.Minute)
	must(t)(h.svc.StartPlan(bg, key, 2))
	must(t)(h.svc.StartPlan(bg, "claude:s2", 2))
	block := make(chan struct{})
	h.del.block, h.del.entered = block, make(chan string, 4)

	h.at(55 * time.Minute)
	h.svc.tick(bg) // returns while both deliveries hang
	for i := 0; i < 2; i++ {
		select {
		case <-h.del.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("deliveries to two sessions did not run concurrently")
		}
	}
	done := make(chan struct{})
	go func() {
		h.svc.Sessions()
		h.svc.Session(key)
		h.svc.Settings()
		h.svc.Pings(key, 10)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("API reads waited on a delivery")
	}
	if p := h.plan(key); p == nil {
		t.Fatal("no plan in view")
	}
	// Another tick while the deliveries hang starts no second one.
	h.at(56 * time.Minute)
	h.svc.tick(bg)
	if !strings.Contains(h.plan(key).Blocked, "being delivered") {
		t.Fatalf("blocked = %q", h.plan(key).Blocked)
	}
	select {
	case k := <-h.del.entered:
		t.Fatalf("second delivery to %s while one is in flight", k)
	default:
	}
	close(block)
	h.svc.deliveries.Wait()
	if h.del.count() != 2 {
		t.Fatalf("delivered %d", h.del.count())
	}

	// A hung deliverer is cut off by the timeout and recorded as failed.
	h2 := newHarness(t)
	h2.svc.sendTimeout = 50 * time.Millisecond
	h2.addSession(key, 100)
	h2.tickAt(time.Minute)
	must(t)(h2.svc.StartPlan(bg, key, 2))
	h2.del.block = make(chan struct{})
	h2.tickAt(55 * time.Minute)
	ps, _ := h2.svc.Pings(key, 1)
	if len(ps) != 1 || ps[0].State != model.PingFailed || !strings.Contains(ps[0].Error, "deadline") {
		t.Fatalf("timed-out ping: %+v", ps)
	}
	if p := h2.plan(key); !strings.Contains(p.LastError, "deadline") {
		t.Fatalf("plan last error = %q", p.LastError)
	}
	// Retried after RetryEvery, not on the very next tick.
	h2.del.block = nil
	h2.tickAt(55*time.Minute + 10*time.Second)
	if h2.del.count() != 0 {
		t.Fatal("retried before RetryEvery")
	}
	h2.tickAt(55*time.Minute + 31*time.Second)
	if h2.del.count() != 1 {
		t.Fatal("not retried")
	}
}

// Rule 3: a ping is "sending" before delivery, then delivered, seen,
// verified; nothing new goes out while one is unconsumed; a ping never seen
// is lost and pauses the plan.
func TestRule3OneUnconsumedPing(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.src.edit(key, func(s *model.Session) { s.CacheTTL = model.Duration(5 * time.Minute) })
	h.tickAt(30 * time.Second)
	if _, err := h.svc.UpdateSettings(bg, model.Settings{MaxHours: 36, MarginMin: 1, ScanSeconds: 5}); err != nil {
		t.Fatal(err)
	}
	must(t)(h.svc.StartPlan(bg, key, 2))
	var stateAtDelivery model.PingState
	h.del.hook = func(_ model.Session, id string) { stateAtDelivery = h.ping(id).State }
	h.tickAt(4 * time.Minute) // 5m TTL − 1m margin
	if h.del.count() != 1 || stateAtDelivery != model.PingSending {
		t.Fatalf("pings %d, state during delivery %q", h.del.count(), stateAtDelivery)
	}
	id := h.del.last().id
	if h.ping(id).State != model.PingDelivered {
		t.Fatalf("state = %s", h.ping(id).State)
	}
	// Due again at 8m (anchored on the ping), but the ping is unconsumed.
	h.tickAt(8 * time.Minute)
	if h.del.count() != 1 {
		t.Fatal("second ping while the first is unconsumed")
	}
	if b := h.plan(key).Blocked; !strings.Contains(b, id) {
		t.Fatalf("blocked = %q", b)
	}
	if _, err := h.svc.PingNow(bg, key); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("PingNow with an unconsumed ping: %v", err)
	}
	// Never seen within the verify window: lost, plan paused, no retry.
	h.tickAt(4*time.Minute + VerifyWindow)
	if st := h.ping(id).State; st != model.PingLost {
		t.Fatalf("state = %s; want lost", st)
	}
	p := h.plan(key)
	if p.State != model.PlanPaused || !strings.Contains(p.Reason, id) {
		t.Fatalf("plan = %+v", p)
	}
	h.tickAt(12 * time.Minute)
	if h.del.count() != 1 {
		t.Fatal("paused plan pinged")
	}
}

func TestRule3SeenThenVerified(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	must(t)(h.svc.StartPlan(bg, key, 2))
	h.tickAt(55 * time.Minute)
	id := h.del.last().id
	h.src.add(key, model.Activity{Kind: model.ActPing, At: t0.Add(55 * time.Minute), PingID: id})
	h.tickAt(55*time.Minute + 5*time.Second)
	if st := h.ping(id).State; st != model.PingSeen {
		t.Fatalf("state = %s", st)
	}
	h.src.add(key, model.Activity{Kind: model.ActPingReply, Model: opus, Sent: t0.Add(55 * time.Minute), At: t0.Add(55*time.Minute + 8*time.Second),
		PingID: id, Usage: warmUsage(ctxK)})
	// Too fresh to trust its usage: still seen.
	h.tickAt(55*time.Minute + 10*time.Second)
	if st := h.ping(id).State; st != model.PingSeen {
		t.Fatalf("state with an unsettled reply = %s", st)
	}
	h.tickAt(55*time.Minute + 20*time.Second)
	p := h.ping(id)
	if p.State != model.PingVerified || p.Calls != 1 || p.Cold || !near(p.Cost, warmPingCost) {
		t.Fatalf("ping = %+v", p)
	}
	if pl := h.plan(key); pl.Pings != 1 || !near(pl.PingCost, warmPingCost) {
		t.Fatalf("plan = %+v", pl)
	}
	// A second reply call adds to the same ping.
	h.src.add(key, model.Activity{Kind: model.ActPingReply, Model: opus, Sent: t0.Add(55*time.Minute + 8*time.Second),
		At: t0.Add(55*time.Minute + 12*time.Second), PingID: id, Usage: warmUsage(ctxK)})
	h.tickAt(55*time.Minute + 30*time.Second)
	if p := h.ping(id); p.Calls != 2 || !near(p.Cost, 2*warmPingCost) {
		t.Fatalf("two-call reply = %+v", p)
	}
}

// Rule 4: the Withdrawer is swept at start-up and every minute; unconsumed
// pings of sessions that are gone become withdrawn.
func TestRule4Sweep(t *testing.T) {
	h := newHarness(t)
	cs := model.Session{Key: "codex:c1", Harness: model.Codex, ID: "c1", PID: 300, ProcStart: "x", Model: "gpt-5",
		Status: model.StatusIdle, Reach: model.ReachQueue, CacheTTL: model.Duration(30 * time.Minute)}
	h.codex.put(cs)
	h.codex.add(cs.Key, model.Activity{Kind: model.ActAssistant, Model: "gpt-5", Sent: t0, At: t0, Usage: warmUsage(ctxK)})

	ctx, cancel := context.WithCancel(bg)
	done := make(chan error)
	go func() { done <- h.svc.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for h.cdel.sweepCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if h.cdel.sweepCount() == 0 {
		t.Fatal("no sweep at start-up")
	}
	n := h.cdel.sweepCount()
	if c := h.cdel.sweeps[0]; c.maxAge != VerifyWindow {
		t.Fatalf("maxAge = %v", c.maxAge)
	}

	// Every minute from the ticks.
	h.tickAt(10 * time.Second)
	h.tickAt(20 * time.Second)
	if h.cdel.sweepCount() != n {
		t.Fatal("swept again within a minute")
	}
	h.tickAt(70 * time.Second)
	if h.cdel.sweepCount() != n+1 {
		t.Fatalf("sweeps = %d, want %d", h.cdel.sweepCount(), n+1)
	}
	if c := h.cdel.sweeps[n]; len(c.live) != 1 || c.live[0] != "codex:c1" {
		t.Fatalf("live = %v", c.live)
	}

	// A queued ping whose session has gone is withdrawn.
	rec, err := h.svc.PingNow(bg, "codex:c1")
	if err != nil {
		t.Fatal(err)
	}
	h.codex.remove("codex:c1")
	h.kill(300)
	h.tickAt(3 * time.Minute)
	if st := h.ping(rec.ID).State; st != model.PingWithdrawn {
		t.Fatalf("state = %s; want withdrawn", st)
	}
	if c := h.cdel.sweeps[len(h.cdel.sweeps)-1]; len(c.live) != 0 {
		t.Fatalf("gone session still passed as live: %v", c.live)
	}
}

// Rule 5: when the plan's process runs another conversation the plan
// ends; the deliverer's ErrSessionChanged and PingNow do the same.
func TestRule5SwitchEndsPlan(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	pl := must(t)(h.svc.StartPlan(bg, key, 2))
	h.tickAt(55 * time.Minute)
	h.reply(key, h.del.last().id, warmUsage(ctxK))
	h.tickAt(56 * time.Minute)
	// /clear: pid 100 now runs s2.
	h.src.remove(key)
	h.src.put(session("claude:s2", 100))
	h.tickAt(57 * time.Minute)
	if h.svc.planByID(pl.ID) != nil {
		t.Fatal("plan followed the pid")
	}
	if sp := h.storedPlan(pl.ID); sp.State != model.PlanEnded || !strings.Contains(sp.Reason, "another conversation") {
		t.Fatalf("plan = %+v", sp.Plan)
	}
	es := h.ledger()
	if len(es) != 1 || es[0].EndedBy != model.EndSwitched || es[0].Outcome != model.OutcomeWasted || !near(es[0].Net, -warmPingCost) {
		t.Fatalf("ledger = %+v", es)
	}
	if v, _ := h.svc.Session("claude:s2"); v.Plan != nil {
		t.Fatal("new conversation inherited the plan")
	}
}

func TestRule5DelivererSessionChanged(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	pl := must(t)(h.svc.StartPlan(bg, key, 2))
	h.del.setErr(model.ErrSessionChanged)
	h.tickAt(55 * time.Minute)
	if sp := h.storedPlan(pl.ID); sp.State != model.PlanEnded {
		t.Fatalf("state = %s", sp.State)
	}
	if es := h.ledger(); len(es) != 0 {
		t.Fatalf("a failed ping was booked: %+v", es)
	}
	// PingNow: the deliverer's check surfaces as ErrSessionChanged.
	if _, err := h.svc.PingNow(bg, key); !errors.Is(err, model.ErrSessionChanged) {
		t.Fatalf("PingNow = %v", err)
	}
}

func TestRule5PingNowChecksPID(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	other := session("claude:s2", 100) // the registry now also maps pid 100 to s2
	h.src.put(other)
	h.tickAt(time.Minute)
	if h.svc.byPID[100] == key {
		h.svc.byPID[100] = "claude:s2"
	}
	if _, err := h.svc.PingNow(bg, key); !errors.Is(err, model.ErrSessionChanged) {
		t.Fatalf("PingNow = %v", err)
	}
	if h.del.count() != 0 {
		t.Fatal("delivered")
	}
}

func TestExitEndsPlan(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	pl := must(t)(h.svc.StartPlan(bg, key, 2))
	h.tickAt(55 * time.Minute)
	h.reply(key, h.del.last().id, warmUsage(ctxK))
	h.tickAt(56 * time.Minute)
	// The registry briefly loses the session while the process lives.
	h.src.remove(key)
	h.tickAt(57 * time.Minute)
	if h.svc.planByID(pl.ID) == nil {
		t.Fatal("plan ended while its process is alive")
	}
	h.kill(100)
	h.tickAt(58 * time.Minute)
	if sp := h.storedPlan(pl.ID); sp.State != model.PlanEnded || sp.Reason != "the session exited" {
		t.Fatalf("plan = %+v", sp.Plan)
	}
	if es := h.ledger(); len(es) != 1 || es[0].EndedBy != model.EndExited {
		t.Fatalf("ledger = %+v", es)
	}
}

// Rule 6: a cold ping lowers the learned TTL (stored, never below 5m, at
// most halving), the plan continues at the shorter interval, and a second
// consecutive cold ping pauses it.
func TestRule6ColdPingsTeachTTL(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	pl := must(t)(h.svc.StartPlan(bg, key, 10))
	h.tickAt(55 * time.Minute)
	id1 := h.del.last().id
	h.reply(key, id1, coldUsage(ctxK))
	h.tickAt(56 * time.Minute)
	if !h.ping(id1).Cold {
		t.Fatal("cold reply not detected")
	}
	// Last refresh t0, reply 55m later: the cache lived < 55m, so the TTL
	// becomes 54m (within the 30m halving floor).
	want := 54 * time.Minute
	if got := h.svc.learned[learnKey(model.Claude, opus)]; got != want {
		t.Fatalf("learned = %v; want %v", got, want)
	}
	if v, ok, _ := h.st.Get(bg, learnPrefix+learnKey(model.Claude, opus)); !ok || v != "3240" {
		t.Fatalf("stored ttl = %q", v)
	}
	es := h.ledger()
	if len(es) != 1 || es[0].EndedBy != model.EndCold || es[0].Outcome != model.OutcomeWasted {
		t.Fatalf("cold segment = %+v", es)
	}
	p := h.plan(key)
	if p.State != model.PlanActive || !strings.Contains(p.LastError, id1) {
		t.Fatalf("plan after first cold ping = %+v", p)
	}
	// Next ping: the reply's request (55m) + 54m − 5m.
	if !p.NextPing.Equal(t0.Add(55*time.Minute + 49*time.Minute)) {
		t.Fatalf("next ping = %v", p.NextPing)
	}
	if v, _ := h.svc.Session(key); v.CacheTTL.Std() != want {
		t.Fatalf("view TTL = %v", v.CacheTTL.Std())
	}
	// The learned TTL survives a restart.
	h.restart()
	if got := h.svc.learned[learnKey(model.Claude, opus)]; got != want {
		t.Fatalf("learned after restart = %v", got)
	}
	h.tickAt(104 * time.Minute)
	id2 := h.del.last().id
	if id2 == id1 {
		t.Fatal("no second ping")
	}
	h.reply(key, id2, coldUsage(ctxK))
	h.tickAt(105 * time.Minute)
	p = h.plan(key)
	if p.State != model.PlanPaused || !strings.Contains(p.Reason, "two pings in a row") {
		t.Fatalf("plan after second cold ping = %+v", p)
	}
	if sp := h.storedPlan(pl.ID); sp.State != model.PlanPaused || sp.ColdStreak != 2 {
		t.Fatalf("stored = %+v", sp)
	}
	// Resume clears the streak.
	p2 := must(t)(h.svc.ResumePlan(bg, key))
	if p2.State != model.PlanActive || p2.Reason != "" {
		t.Fatalf("resumed = %+v", p2)
	}
	if _, err := h.svc.ResumePlan(bg, key); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("resume active: %v", err)
	}
}

func TestRule6LearnBounds(t *testing.T) {
	h := newHarness(t)
	h.svc.mu.Lock()
	defer h.svc.mu.Unlock()
	// At most halving: a tiny gap on a 1h TTL gives 30m.
	if got := h.svc.learn(bg, model.Claude, "a", time.Hour, 2*time.Minute); got != 30*time.Minute {
		t.Fatalf("halving floor: %v", got)
	}
	// Never below 5 minutes.
	if got := h.svc.learn(bg, model.Claude, "b", 8*time.Minute, time.Minute); got != MinTTL {
		t.Fatalf("min floor: %v", got)
	}
	if got := h.svc.learn(bg, model.Claude, "c", MinTTL, time.Minute); got != MinTTL {
		t.Fatalf("already at floor: %v", got)
	}
	if _, ok := h.svc.learned["claude:c"]; ok {
		t.Fatal("stored a TTL that did not change")
	}
	// Unknown gap: one minute less.
	if got := h.svc.learn(bg, model.Claude, "d", time.Hour, 0); got != 59*time.Minute {
		t.Fatalf("unknown gap: %v", got)
	}
}

// Rule 7: a return is saved only if its call read at least 80% of the
// pre-gap context from the cache; avoided is computed from that read.
func TestRule7SavedNeedsCacheHit(t *testing.T) {
	cases := []struct {
		name      string
		cacheRead int64
		back      time.Duration
		outcome   model.Outcome
		endedBy   string
		avoided   float64
	}{
		// 90,000 × (8 − 0.2) / 1e6 = 0.702
		{"hit", 90_000, 70 * time.Minute, model.OutcomeSaved, model.EndReturned, 0.702},
		{"miss", 50_000, 70 * time.Minute, model.OutcomeWasted, model.EndMissed, 0},
		// Back within the TTL: the cache was warm anyway.
		{"early", ctxK, 58 * time.Minute, model.OutcomeWasted, model.EndReturned, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.addSession(key, 100)
			h.tickAt(time.Minute)
			must(t)(h.svc.StartPlan(bg, key, 5))
			h.tickAt(55 * time.Minute)
			h.reply(key, h.del.last().id, warmUsage(ctxK))
			h.tickAt(56 * time.Minute)
			h.at(c.back)
			h.returnNow(key, c.cacheRead)
			h.tickAt(c.back + time.Minute)
			es := h.ledger()
			if len(es) != 1 {
				t.Fatalf("ledger = %+v", es)
			}
			e := es[0]
			if e.Outcome != c.outcome || e.EndedBy != c.endedBy || !near(e.Avoided, c.avoided) ||
				!near(e.Net, c.avoided-warmPingCost) || e.Pings != 1 || !near(e.PingCost, warmPingCost) {
				t.Fatalf("entry = %+v", e)
			}
			if !e.IdleFrom.Equal(t0) || !e.EndedAt.Equal(t0.Add(c.back)) || e.ContextSize != ctxK+10 {
				t.Fatalf("entry times/context = %+v", e)
			}
			if e.Title != "t" || e.Project != "/p" || e.Model != opus || e.Harness != model.Claude {
				t.Fatalf("entry labels = %+v", e)
			}
			if p := h.plan(key); p.State != model.PlanActive {
				t.Fatalf("plan state after return = %s", p.State)
			}
		})
	}
}

func TestReturnWithoutCallIsMissed(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	must(t)(h.svc.StartPlan(bg, key, 5))
	h.tickAt(55 * time.Minute)
	h.reply(key, h.del.last().id, warmUsage(ctxK))
	h.tickAt(56 * time.Minute)
	h.src.add(key, model.Activity{Kind: model.ActHuman, At: t0.Add(70 * time.Minute)})
	h.tickAt(71 * time.Minute)
	if len(h.ledger()) != 0 {
		t.Fatal("booked before the answering call could arrive")
	}
	h.tickAt(70*time.Minute + ReturnWait)
	if es := h.ledger(); len(es) != 1 || es[0].EndedBy != model.EndMissed {
		t.Fatalf("ledger = %+v", es)
	}
}

// Rule 8: restarts resume plans, open segments and ping verification;
// booking is idempotent; a late reply corrects the booked cost.
func TestRule8RestartResumesVerification(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	pl := must(t)(h.svc.StartPlan(bg, key, 5))
	h.tickAt(55 * time.Minute)
	id := h.del.last().id
	h.restart() // the ping is delivered, its reply not read yet
	if h.svc.planByID(pl.ID) == nil || h.svc.plans[key].seg == nil {
		t.Fatal("plan or open segment not restored")
	}
	h.reply(key, id, warmUsage(ctxK))
	h.tickAt(56 * time.Minute)
	if p := h.ping(id); p.State != model.PingVerified || !near(p.Cost, warmPingCost) {
		t.Fatalf("ping after restart = %+v", p)
	}
	h.at(70 * time.Minute)
	h.returnNow(key, ctxK)
	h.tickAt(71 * time.Minute)
	es := h.ledger()
	if len(es) != 1 || es[0].Outcome != model.OutcomeSaved {
		t.Fatalf("ledger = %+v", es)
	}
	// 100,000 × 7.8 / 1e6 − one ping.
	if !near(es[0].Net, 0.78-warmPingCost) {
		t.Fatalf("net = %v", es[0].Net)
	}
	// Restarting again replays nothing twice.
	h.restart()
	h.tickAt(72 * time.Minute)
	h.restart()
	h.tickAt(73 * time.Minute)
	if es := h.ledger(); len(es) != 1 {
		t.Fatalf("replayed into %d entries", len(es))
	}
	if p := h.ping(id); p.Calls != 1 || !near(p.Cost, warmPingCost) {
		t.Fatalf("ping recount = %+v", p)
	}
}

func TestRule8RestartDuringPendingReturn(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	must(t)(h.svc.StartPlan(bg, key, 5))
	h.tickAt(55 * time.Minute)
	h.reply(key, h.del.last().id, warmUsage(ctxK))
	h.tickAt(56 * time.Minute)
	// The user is back; the answering call has not been recorded yet.
	h.src.add(key, model.Activity{Kind: model.ActHuman, At: t0.Add(70 * time.Minute)})
	h.tickAt(70*time.Minute + 5*time.Second)
	h.restart()
	h.src.add(key, model.Activity{Kind: model.ActAssistant, Model: opus, Sent: t0.Add(70 * time.Minute),
		At: t0.Add(71 * time.Minute), Usage: &model.Usage{Input: 50, CacheRead: ctxK}})
	h.tickAt(72 * time.Minute)
	es := h.ledger()
	if len(es) != 1 || es[0].Outcome != model.OutcomeSaved || !es[0].EndedAt.Equal(t0.Add(70*time.Minute)) {
		t.Fatalf("ledger = %+v", es)
	}
	h.restart()
	h.tickAt(73 * time.Minute)
	if len(h.ledger()) != 1 {
		t.Fatal("pending return booked twice")
	}
}

func TestRule8LateReplyCorrectsLedger(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.src.edit(key, func(s *model.Session) { s.CacheTTL = model.Duration(5 * time.Minute) })
	h.tickAt(30 * time.Second)
	if _, err := h.svc.UpdateSettings(bg, model.Settings{MaxHours: 36, MarginMin: 1, ScanSeconds: 5}); err != nil {
		t.Fatal(err)
	}
	must(t)(h.svc.StartPlan(bg, key, 1))
	h.tickAt(4 * time.Minute)
	id := h.del.last().id
	// The user returns before the ping's reply has been read.
	h.at(6 * time.Minute)
	h.returnNow(key, ctxK)
	h.tickAt(6*time.Minute + 30*time.Second)
	// 5m tier: 100,000 x (5 - 0.2) / 1e6 = 0.48.
	es := h.ledger()
	if len(es) != 1 || es[0].Pings != 1 || es[0].PingCost != 0 || !near(es[0].Net, 0.48) {
		t.Fatalf("booked = %+v", es)
	}
	h.src.add(key,
		model.Activity{Kind: model.ActPing, At: t0.Add(4 * time.Minute), PingID: id},
		model.Activity{Kind: model.ActPingReply, Model: opus, Sent: t0.Add(4 * time.Minute), At: t0.Add(4*time.Minute + 5*time.Second), PingID: id, Usage: warmUsage(ctxK)})
	h.tickAt(7 * time.Minute)
	es = h.ledger()
	if !near(es[0].PingCost, warmPingCost) || !near(es[0].Net, 0.48-warmPingCost) {
		t.Fatalf("corrected = %+v", es[0])
	}
	v, _ := h.svc.Session(key)
	if !near(v.SavedTotal, 0.48-warmPingCost) {
		t.Fatalf("view totals = %v", v.SavedTotal)
	}
	if sp := h.storedPlan(es[0].PlanID); !near(sp.PingCost, warmPingCost) {
		t.Fatalf("plan ping cost = %v", sp.PingCost)
	}
}

// Rule 9: working for over 2 hours without transcript activity, or a
// transcript reset, blocks the plan until the transcript moves again.
func TestRule9BusyIsBounded(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	must(t)(h.svc.StartPlan(bg, key, 10))
	h.src.edit(key, func(s *model.Session) { s.Status, s.StatusSince = model.StatusWorking, t0.Add(time.Minute) })
	h.tickAt(90 * time.Minute)
	if p := h.plan(key); p.Blocked != "" || h.del.count() != 0 {
		t.Fatalf("working session: blocked %q, pings %d", p.Blocked, h.del.count())
	}
	h.tickAt(2*time.Hour + 2*time.Minute)
	p := h.plan(key)
	if !strings.Contains(p.Blocked, "2 hours") || h.del.count() != 0 {
		t.Fatalf("stale: blocked %q, pings %d", p.Blocked, h.del.count())
	}
	// Stale means idle: its cache is treated as expired, not kept warm.
	if p.State != model.PlanActive {
		t.Fatalf("state = %s", p.State)
	}
	// The transcript moves: the session is really working again.
	h.src.add(key, model.Activity{Kind: model.ActAssistant, Model: opus, Sent: t0.Add(2*time.Hour + 3*time.Minute),
		At: t0.Add(2*time.Hour + 4*time.Minute), Usage: warmUsage(ctxK), Working: true})
	h.tickAt(2*time.Hour + 5*time.Minute)
	if p := h.plan(key); p.Blocked != "" {
		t.Fatalf("still blocked after activity: %q", p.Blocked)
	}
}

func TestRule9ResetBlocks(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	must(t)(h.svc.StartPlan(bg, key, 10))
	h.src.add(key, model.Activity{Kind: model.ActReset, At: t0.Add(2 * time.Minute)})
	h.tickAt(55 * time.Minute)
	if p := h.plan(key); !strings.Contains(p.Blocked, "restarted or resumed") || h.del.count() != 0 {
		t.Fatalf("after reset: blocked %q, pings %d", p.Blocked, h.del.count())
	}
	h.at(56 * time.Minute)
	h.returnNow(key, ctxK)
	h.tickAt(57 * time.Minute)
	if p := h.plan(key); p.Blocked != "" {
		t.Fatalf("blocked after the transcript moved: %q", p.Blocked)
	}
}

// Rule 10: wall-clock readings only; a jump re-evaluates warm/cold before
// any ping.
func TestRule10WallClockOnly(t *testing.T) {
	h := newHarness(t)
	base := time.Now() // carries a monotonic reading
	h.svc.clock = func() time.Time { return base }
	h.addSession(key, 100)
	h.tick()
	p := must(t)(h.svc.StartPlan(bg, key, 1))
	if strings.Contains(p.StartedAt.String(), "m=") || strings.Contains(p.Until.String(), "m=") {
		t.Fatalf("monotonic reading leaked: %v", p.StartedAt)
	}
	if strings.Contains(h.svc.lastTick.String(), "m=") {
		t.Fatal("tick time has a monotonic reading")
	}
}

func TestRule10ClockJump(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	must(t)(h.svc.StartPlan(bg, key, 10))
	h.tickAt(55 * time.Minute)
	h.reply(key, h.del.last().id, warmUsage(ctxK))
	h.tickAt(56 * time.Minute)
	// Suspended: the next tick is 3 hours later, well past expiry.
	h.tickAt(3*time.Hour + 56*time.Minute)
	if h.del.count() != 1 {
		t.Fatal("pinged a cache that expired during the suspend")
	}
	p := h.plan(key)
	if !strings.Contains(p.Blocked, "waiting for the session to be used again") {
		t.Fatalf("blocked = %q", p.Blocked)
	}
	es := h.ledger()
	if len(es) != 1 || es[0].EndedBy != model.EndCold || !es[0].EndedAt.Equal(t0.Add(55*time.Minute+time.Hour)) {
		t.Fatalf("ledger = %+v", es)
	}
	// Real activity warms the cache again; pings resume after it.
	h.at(4 * time.Hour)
	h.returnNow(key, 0)
	h.tickAt(4*time.Hour + time.Minute)
	if p := h.plan(key); p.Blocked != "" || !p.NextPing.Equal(t0.Add(4*time.Hour+55*time.Minute)) {
		t.Fatalf("after activity: %+v", p)
	}
	h.tickAt(4*time.Hour + 55*time.Minute)
	if h.del.count() != 2 {
		t.Fatal("pings did not resume")
	}
}

// A restart after a long downtime is a jump too: nothing is pinged cold.
func TestRule10RestartAfterDowntime(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	must(t)(h.svc.StartPlan(bg, key, 10))
	h.restart()
	h.tickAt(2 * time.Hour)
	if h.del.count() != 0 {
		t.Fatal("pinged after restart into an expired cache")
	}
	if p := h.plan(key); p == nil || p.State != model.PlanActive || p.Blocked == "" {
		t.Fatalf("plan = %+v", p)
	}
}

// Rule 12: the ping text is model.FormatPing(id), always.
func TestRule12FixedPingText(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	rec, err := h.svc.PingNow(bg, key)
	if err != nil {
		t.Fatal(err)
	}
	d := h.del.last()
	if d.text != model.FormatPing(rec.ID) || d.id != rec.ID {
		t.Fatalf("text = %q", d.text)
	}
	if id, ok := model.ParsePing(d.text); !ok || id != rec.ID {
		t.Fatal("marker not parseable")
	}
}

// A reply's usage published in pieces (one response over several
// transcript records, corrected in place by the source) is read only once
// final: an early partial record must not book the ping cold.
func TestUsageCorrectedInPlace(t *testing.T) {
	h := newHarness(t)
	h.addSession(key, 100)
	h.tickAt(time.Minute)
	must(t)(h.svc.StartPlan(bg, key, 3))
	h.tickAt(55 * time.Minute)
	id := h.del.last().id
	at := t0.Add(55 * time.Minute)
	h.src.add(key,
		model.Activity{Kind: model.ActPing, At: at, PingID: id},
		// First record: the usage is not complete yet.
		model.Activity{Kind: model.ActPingReply, Model: opus, Sent: at, At: at.Add(2 * time.Second), PingID: id,
			Usage: &model.Usage{Input: 10, CacheWrite1h: 50}})
	h.tickAt(55*time.Minute + 4*time.Second)
	if st := h.ping(id).State; st != model.PingSeen {
		t.Fatalf("state = %s; the reply's usage is not final yet", st)
	}
	// The source corrects the same activity (same Seq) in place.
	h.src.mu.Lock()
	as := h.src.acts[key]
	as[len(as)-1].Usage = warmUsage(ctxK)
	h.src.mu.Unlock()
	h.tickAt(55*time.Minute + 20*time.Second)
	p := h.ping(id)
	if p.State != model.PingVerified || p.Cold || p.Calls != 1 || !near(p.Cost, warmPingCost) {
		t.Fatalf("ping = %+v", p)
	}
	if es := h.ledger(); len(es) != 0 {
		t.Fatalf("booked cold: %+v", es)
	}
	if v := h.svc.learned[learnKey(model.Claude, opus)]; v != 0 {
		t.Fatalf("learned a TTL from a partial record: %v", v)
	}
	// The same holds for a return: rule 7 reads the final CacheRead.
	h.at(80 * time.Minute)
	now := h.clock.Now()
	h.src.add(key,
		model.Activity{Kind: model.ActHuman, At: now},
		model.Activity{Kind: model.ActAssistant, Model: opus, Sent: now, At: now.Add(time.Second), Usage: &model.Usage{Input: 5}})
	h.tickAt(80*time.Minute + 3*time.Second)
	h.src.mu.Lock()
	as = h.src.acts[key]
	as[len(as)-1].Usage = &model.Usage{Input: 5, CacheRead: ctxK, Output: 100}
	h.src.mu.Unlock()
	h.tickAt(81 * time.Minute)
	es := h.ledger()
	if len(es) != 1 || es[0].Outcome != model.OutcomeSaved {
		t.Fatalf("return = %+v", es)
	}
}
