package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/pricing"
	"github.com/gurupras/agent-keepalive-manager/internal/store"
)

// planRun is an open (active or paused) plan with its in-memory state.
//
// Ledger accounting: the plan's Base* fields describe the last real API
// call, which refreshed the cache. Once a ping is decided in the gap after
// it, seg is the open idle segment (a store row, rule 8). Real activity
// ends the gap: the segment becomes pending until the API call answering
// the return shows whether it hit the cache (rule 7).
type planRun struct {
	store.Plan
	saved   store.Plan // last persisted copy
	seg     *store.Segment
	pending *pendingReturn
	sess    model.Session // as last seen
	misses  int

	lastTry     time.Time // last delivery decision
	deliveryErr string    // last delivery failure, cleared by a success
	// reset: the transcript showed a new session start or resume; the plan
	// is stale until the transcript moves again (rule 9).
	reset bool
	// replayUntil: after a restart, activity at or before this time was
	// already applied.
	replayUntil time.Time
	// ep is the open experiment episode; epDone the QuietFrom of the last
	// one closed, so a stretch opens one episode at most.
	ep     *episodeRun
	epDone time.Time
	// treat: the ping arm pings the working session as if it were idle;
	// pings sent while it is set belong to the episode.
	treat bool
}

// pendingReturn is a segment whose idle gap ended with real activity at
// `at`, waiting for the API call that answers it.
type pendingReturn struct {
	seg      store.Segment
	at       time.Time
	prevSeen time.Time // LastSeen before the return, persisted while pending
}

func newPlanRun(p store.Plan) *planRun { return &planRun{Plan: p, saved: p} }

// planByID returns the open plan with that id. Requires s.mu.
func (s *Service) planByID(id int64) *planRun {
	if id == 0 {
		return nil
	}
	for _, pr := range s.plans {
		if pr.ID == id {
			return pr
		}
	}
	return nil
}

// applyActivity applies one transcript event to a plan. Requires s.mu.
func (s *Service) applyActivity(ctx context.Context, now time.Time, pr *planRun, a model.Activity) {
	if !pr.replayUntil.IsZero() {
		if !a.At.After(pr.replayUntil) {
			return
		}
		pr.replayUntil = time.Time{}
	}
	prevSeen := pr.LastSeen
	if a.At.After(pr.LastSeen) {
		pr.LastSeen = a.At
	}
	if a.Kind == model.ActReset {
		pr.reset = true
		return
	}
	pr.reset = false
	switch a.Kind {
	case model.ActHuman:
		s.realActivity(ctx, pr, a, prevSeen)
		if pr.Indefinite {
			// Rolling window: each prompt re-arms a full window past it.
			if u := a.At.Add(hoursDur(s.window(pr.sess))); u.After(pr.Until) {
				pr.Until = u
				pr.Hours = pr.Until.Sub(pr.StartedAt).Hours()
			}
		}
	case model.ActAssistant:
		s.realActivity(ctx, pr, a, prevSeen)
	}
	// ActPing and ActPingReply are handled by matchPing.
}

// realActivity ends the idle gap: an open segment becomes pending, and the
// API call (this one, or the next) resolves it. prevSeen is LastSeen before
// a. Requires s.mu.
func (s *Service) realActivity(ctx context.Context, pr *planRun, a model.Activity, prevSeen time.Time) {
	if g := pr.seg; g != nil && !s.after(a, g.IdleFrom) {
		// The call that started the gap, or older (a replay after a
		// restart): it cannot end the gap.
		return
	}
	pr.WaitWarm = false
	if pr.seg != nil && pr.pending == nil {
		pr.pending = &pendingReturn{seg: *pr.seg, at: a.At, prevSeen: prevSeen}
		pr.seg = nil
	}
	if a.Kind != model.ActAssistant || a.Usage == nil {
		return
	}
	s.returnEpisode(ctx, pr, a)
	if pr.pending != nil {
		s.resolveReturn(ctx, pr, a)
	}
	pr.BaseFrom, pr.BaseContext = a.Anchor(), a.Usage.Context()
	if a.Model != "" {
		pr.BaseModel = a.Model
	}
}

// after reports whether activity a happened after t: an API call by its
// request time, anything else by its record time.
func (s *Service) after(a model.Activity, t time.Time) bool {
	if a.Kind == model.ActAssistant {
		return a.Anchor().After(t)
	}
	return a.At.After(t)
}

// resolveReturn books a pending return from the call that answered it
// (rule 7):
//
//	gap <= TTL                          wasted, returned: the cache was warm anyway
//	gap > TTL, CacheRead >= 80% context saved: CacheRead x (write - read) - pings
//	gap > TTL, otherwise                wasted, invalidated: the return missed the cache
//
// The gap runs from the last real call's request to the returning call's
// request, the two moments the provider refreshed or read the cache.
// Requires s.mu.
func (s *Service) resolveReturn(ctx context.Context, pr *planRun, call model.Activity) {
	p := pr.pending
	pr.pending = nil
	sess := pr.sess
	if p.seg.Model != "" {
		sess.Model = p.seg.Model
	}
	ttl := s.ttlOf(sess)
	gap := call.Anchor().Sub(p.seg.IdleFrom)
	switch {
	case gap <= ttl:
		s.book(ctx, pr, p.seg, p.at, model.EndReturned, model.OutcomeWasted, 0)
	case float64(call.Usage.CacheRead) >= HitFraction*float64(p.seg.ContextSize):
		var avoided float64
		if price, ok := s.cfg.Pricing.Lookup(sess.Model); ok {
			avoided = price.Avoided(call.Usage.CacheRead, ttl)
		}
		s.book(ctx, pr, p.seg, p.at, model.EndReturned, model.OutcomeSaved, avoided)
	default:
		s.book(ctx, pr, p.seg, p.at, model.EndMissed, model.OutcomeWasted, 0)
	}
}

// advance moves one plan forward and returns a delivery to make, if a ping
// is due. Requires s.mu.
func (s *Service) advance(ctx context.Context, now time.Time, pr *planRun, jump bool) (job, bool) {
	key := pr.Key
	sess, live := s.sessions[key]
	if !live {
		s.whenGone(ctx, now, pr)
		return job{}, false
	}
	pr.misses = 0
	pr.sess = sess
	if sess.PID > 0 && sess.PID != pr.PID {
		// The same conversation now runs in another process (resumed
		// elsewhere): the plan follows the conversation.
		pr.PID, pr.ProcStart = sess.PID, sess.ProcStart
	}
	if jump {
		pr.lastTry = time.Time{}
	}
	if p := pr.pending; p != nil && now.Sub(p.at) >= ReturnWait {
		pr.pending = nil
		s.book(ctx, pr, p.seg, p.at, model.EndMissed, model.OutcomeWasted, 0)
	}
	pr.Blocked = ""

	ttl := s.ttlOf(sess)
	iv := pricing.Interval(ttl, s.margin())
	refresh := sess.LastCall
	stale := s.staleness(pr, sess, now)
	working := sess.Status == model.StatusWorking && stale == ""
	pr.treat = s.experimentStep(ctx, now, pr, sess, working, ttl)
	if pr.treat {
		working = false // the main agent makes no calls; ping as if idle
	}
	waiting := s.unconsumed(key) != nil // a ping may still refresh the cache
	expires := refresh.Add(ttl)
	// Evaluated from the transcript every tick, so a clock jump (rule 10)
	// or a restart sees an expired cache before any ping is sent.
	expired := !working && !waiting && !refresh.IsZero() && !now.Before(expires)

	if !now.Before(pr.Until) && pr.State == model.PlanActive {
		pr.NextPing = time.Time{}
		switch {
		case pr.pending != nil:
			// The user is back; wait for the answer before closing.
		case pr.seg == nil:
			s.endPlan(ctx, now, pr, model.PlanDone, model.EndExpired, "")
		case expired:
			// The last ping kept the cache warm up to expires; a return
			// before then would still have counted as saved.
			s.endPlan(ctx, earliest(now, latest(expires, pr.Until)), pr, model.PlanDone, model.EndExpired, "")
		}
		return job{}, false
	}
	if expired {
		if pr.seg != nil {
			g := *pr.seg
			pr.seg = nil
			s.book(ctx, pr, g, expires, model.EndCold, model.OutcomeWasted, 0)
		}
		if pr.State == model.PlanActive && !pr.WaitWarm {
			if why := s.undeliverable(pr, sess); why != "" {
				s.endPlan(ctx, now, pr, model.PlanFailed, model.EndCold,
					"the cache expired before a ping could be delivered: "+why)
				return job{}, false
			}
			pr.WaitWarm = true
			s.log.Info("scheduler: cache expired; waiting for the session to be used again", "plan", pr.ID, "key", key)
		}
	}
	if pr.State != model.PlanActive {
		pr.NextPing = time.Time{}
		return job{}, false
	}

	next := latest(refresh, pr.LastPingAt).Add(iv)
	if refresh.IsZero() || !next.Before(pr.Until) {
		pr.NextPing = time.Time{}
	} else {
		pr.NextPing = next
	}
	switch {
	case stale != "":
		pr.Blocked = stale
	case pr.WaitWarm:
		pr.Blocked = "the cache expired before it could be kept warm; waiting for the session to be used again"
	case working || pr.pending != nil || pr.NextPing.IsZero():
		// Work and returns refresh the cache themselves.
	case sess.Reach == model.ReachNone || sess.Reach == "":
		pr.Blocked = "the session cannot be reached" + suffix(sess.ReachWhy)
	case s.deliverers[sess.Harness] == nil:
		pr.Blocked = fmt.Sprintf("no delivery mechanism for %s sessions", sess.Harness)
	case !now.Before(next):
		if id, ok := s.inflight[key]; ok {
			pr.Blocked = "ping " + id + " is being delivered"
		} else if p := s.unconsumed(key); p != nil {
			pr.Blocked = fmt.Sprintf("ping %s has not been consumed yet (%s)", p.ID, p.State)
		}
	}
	if pr.Blocked != "" || working || pr.pending != nil || pr.NextPing.IsZero() || now.Before(next) {
		return job{}, false
	}
	if now.Sub(pr.lastTry) < RetryEvery {
		return job{}, false
	}
	if !s.alive(sess.PID, sess.ProcStart) {
		pr.Blocked = fmt.Sprintf("process %d is not running", sess.PID)
		return job{}, false
	}
	return s.newPing(ctx, now, pr, sess)
}

// undeliverable says why the plan could not have pinged in time, or "".
func (s *Service) undeliverable(pr *planRun, sess model.Session) string {
	switch {
	case pr.deliveryErr != "":
		return pr.deliveryErr
	case sess.Reach == model.ReachNone || sess.Reach == "":
		return "the session cannot be reached" + suffix(sess.ReachWhy)
	case s.deliverers[sess.Harness] == nil:
		return fmt.Sprintf("no delivery mechanism for %s sessions", sess.Harness)
	}
	return ""
}

// staleness says why a session is treated as idle-but-stale (rule 9), or
// "". Requires s.mu.
func (s *Service) staleness(pr *planRun, sess model.Session, now time.Time) string {
	if pr.reset {
		return "the transcript shows the conversation was restarted or resumed; waiting for it to move again"
	}
	if sess.Status == model.StatusWorking {
		last := latest(sess.StatusSince, s.lastAct[sess.Key], sess.LastCall)
		if !last.IsZero() && now.Sub(last) > StaleAfter {
			return "the session has reported working for over 2 hours without transcript activity"
		}
	}
	return ""
}

// whenGone handles a plan whose session is not in the scan: the process
// switched conversation (rule 5), or exited. A process that is still alive
// gets a few scans' grace, as a registry file may be mid-rewrite.
// Requires s.mu.
func (s *Service) whenGone(ctx context.Context, now time.Time, pr *planRun) {
	if other, ok := s.byPID[pr.PID]; ok && pr.PID > 0 && other != pr.Key {
		o := s.sessions[other]
		if pr.ProcStart == "" || o.ProcStart == "" || o.ProcStart == pr.ProcStart {
			s.endPlan(ctx, now, pr, model.PlanEnded, model.EndSwitched,
				"the process moved to another conversation")
			return
		}
	}
	if pr.PID > 0 && pr.misses < missLimit && s.alive(pr.PID, pr.ProcStart) {
		pr.misses++
		return
	}
	s.endPlan(ctx, now, pr, model.PlanEnded, model.EndExited, "the session exited")
}

// endPlan closes a plan: a pending return and the open segment are booked
// as wasted (endedBy) together with the final plan row (rule 8).
// Requires s.mu.
func (s *Service) endPlan(ctx context.Context, at time.Time, pr *planRun, state model.PlanState, endedBy, reason string) {
	pr.State, pr.EndedAt, pr.NextPing, pr.Blocked = state, at, time.Time{}, ""
	s.closeEpisode(ctx, pr, at, model.EpisodeEnded, false, 0)
	if reason != "" {
		pr.Reason = reason
	}
	booked := false
	if p := pr.pending; p != nil {
		pr.pending = nil
		s.book(ctx, pr, p.seg, p.at, endedBy, model.OutcomeWasted, 0)
		booked = true
	}
	if g := pr.seg; g != nil {
		pr.seg = nil
		s.book(ctx, pr, *g, at, endedBy, model.OutcomeWasted, 0)
		booked = true
	}
	if !booked {
		s.persist(ctx, pr)
	}
	delete(s.plans, pr.Key)
	s.log.Info("scheduler: plan ended", "plan", pr.ID, "key", pr.Key, "state", state, "by", endedBy, "reason", pr.Reason)
	s.publish("plan", pr.Plan.Plan)
}

// pause stops a plan with a reason (lost ping, second cold ping). The open
// segment stays: a return while the last ping still holds the cache counts.
// Requires s.mu.
func (s *Service) pause(ctx context.Context, pr *planRun, reason string) {
	if pr.State != model.PlanActive {
		return
	}
	pr.State, pr.Reason, pr.NextPing, pr.Blocked = model.PlanPaused, reason, time.Time{}, ""
	s.log.Info("scheduler: plan paused", "plan", pr.ID, "key", pr.Key, "reason", reason)
	s.persist(ctx, pr)
}

func segEntry(g store.Segment, endedAt time.Time, endedBy string, oc model.Outcome, avoided float64) model.LedgerEntry {
	return model.LedgerEntry{
		PlanID: g.PlanID, Key: g.Key, Harness: g.Harness, Title: g.Title, Project: g.Project, Model: g.Model,
		IdleFrom: g.IdleFrom, EndedAt: endedAt, EndedBy: endedBy, Outcome: oc, Avoided: avoided,
		ContextSize: g.ContextSize,
	}
}

// book closes segment g into the ledger, writing the plan in the same
// transaction (rule 8). Requires s.mu.
func (s *Service) book(ctx context.Context, pr *planRun, g store.Segment, endedAt time.Time, endedBy string, oc model.Outcome, avoided float64) {
	snap := s.snapshot(pr)
	changed := snap != pr.saved
	e, inserted, err := s.cfg.Store.CloseSegment(context.WithoutCancel(ctx), segEntry(g, endedAt, endedBy, oc, avoided), &snap, nil)
	if err != nil {
		s.log.Error("scheduler: ledger write failed", "plan", g.PlanID, "err", err)
		return
	}
	pr.saved = snap
	if changed {
		s.publish("plan", pr.Plan.Plan)
	}
	if !inserted {
		return
	}
	s.reloadTotals(ctx)
	s.log.Info("scheduler: ledger", "key", e.Key, "outcome", e.Outcome, "endedBy", e.EndedBy,
		"pings", e.Pings, "net", fmt.Sprintf("%.4f", e.Net))
	s.publish("ledger", e)
}

func (s *Service) reloadTotals(ctx context.Context) {
	t, err := s.cfg.Store.TotalsByKey(context.WithoutCancel(ctx))
	if err != nil {
		s.log.Warn("scheduler: reading ledger totals failed", "err", err)
		return
	}
	s.totals = t
}

// snapshot is the plan row to persist. While a return is pending, LastSeen
// stays before it, so a restart replays the return. Requires s.mu.
func (s *Service) snapshot(pr *planRun) store.Plan {
	p := pr.Plan
	if pr.pending != nil {
		p.LastSeen = pr.pending.prevSeen
	}
	return p
}

// persist writes the plan if it changed and publishes it. Requires s.mu.
func (s *Service) persist(ctx context.Context, pr *planRun) {
	cur := s.snapshot(pr)
	if cur == pr.saved {
		return
	}
	if err := s.cfg.Store.UpdatePlan(context.WithoutCancel(ctx), cur); err != nil {
		s.log.Error("scheduler: plan write failed", "plan", pr.ID, "err", err)
		return
	}
	pr.saved = cur
	s.publish("plan", pr.Plan.Plan)
}

func suffix(why string) string {
	if why == "" {
		return ""
	}
	return ": " + why
}
