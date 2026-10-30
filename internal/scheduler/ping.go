package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/store"
)

// pingRun is a ping whose outcome may still change.
type pingRun struct {
	store.Ping
	model       string        // model to price the reply with if the reply names none
	sess        model.Session // the session it went to
	prevRefresh time.Time     // the session's LastCall when the ping was decided
	// restored: loaded after a restart. Its calls are being counted again
	// from the transcript; the stored cost is replaced once at least
	// storedCalls have been seen, never with a smaller count.
	restored    bool
	storedCalls int
	// reserve: a treatment ping's estimated cost, held against the daily
	// budget from its send until its reply is priced.
	reserve float64
}

// job is one delivery decided under the lock and made outside it.
type job struct {
	id     string
	planID int64
	sess   model.Session
}

// newPing records a ping as "sending" before anything is delivered (rule 3)
// and returns the delivery to make. pr may be nil (PingNow without a plan).
// treat: a treatment ping of the plan's episode. Requires s.mu.
func (s *Service) newPing(ctx context.Context, now time.Time, pr *planRun, sess model.Session, treat bool) (job, bool) {
	id := newPingID()
	rec := store.Ping{PingRecord: model.PingRecord{ID: id, Key: sess.Key, SentAt: now, State: model.PingSending}}
	mdl := sess.Model
	var newSeg *store.Segment
	if pr != nil {
		pr.lastTry = now
		rec.PlanID = pr.ID
		if treat && pr.ep != nil {
			rec.EpisodeID, rec.Experiment = pr.ep.ID, true
		}
		if pr.seg == nil {
			g := store.Segment{
				PlanID: pr.ID, Key: pr.Key, Harness: sess.Harness, Model: sess.Model, Title: sess.Title,
				Project: sess.Project, IdleFrom: pr.BaseFrom, ContextSize: pr.BaseContext, OpenedAt: now,
			}
			if pr.BaseModel != "" {
				g.Model = pr.BaseModel
			}
			if g.IdleFrom.IsZero() || sess.LastCall.Before(g.IdleFrom) {
				g.IdleFrom = sess.LastCall
			}
			if g.ContextSize == 0 {
				g.ContextSize = sess.ContextTokens
			}
			newSeg = &g
		}
		if newSeg != nil {
			rec.SegFrom = newSeg.IdleFrom
			mdl = newSeg.Model
		} else {
			rec.SegFrom = pr.seg.IdleFrom
			mdl = pr.seg.Model
		}
	}
	if err := s.cfg.Store.InsertPing(context.WithoutCancel(ctx), rec, newSeg); err != nil {
		s.log.Error("scheduler: recording ping failed", "key", sess.Key, "err", err)
		return job{}, false
	}
	if newSeg != nil {
		pr.seg = newSeg
	}
	run := &pingRun{Ping: rec, model: mdl, sess: sess, prevRefresh: sess.LastCall}
	if rec.EpisodeID != 0 {
		pr.ep.pings++
		s.expPings++
		// A cache read of the context, so that pings sent before earlier
		// replies are priced cannot overrun the daily budget.
		if price, ok := s.cfg.Pricing.Lookup(pr.ep.Model); ok {
			run.reserve = float64(pr.ep.ContextSize) * price.Read / 1e6
			s.expCost += run.reserve
		}
	}
	s.pings[id] = run
	s.inflight[sess.Key] = id
	s.follow(sess.Key)
	s.publish("ping", rec.PingRecord)
	return job{id: id, planID: rec.PlanID, sess: sess}, true
}

// deliver hands the ping to the session's deliverer, with a timeout. It
// runs without the lock (rule 2).
func (s *Service) deliver(ctx context.Context, j job) error {
	d := s.deliverers[j.sess.Harness]
	if d == nil {
		return fmt.Errorf("%w: no delivery mechanism for %s sessions", model.ErrUnreachable, j.sess.Harness)
	}
	dctx, cancel := context.WithTimeout(ctx, s.sendTimeout)
	defer cancel()
	// The text is always model.FormatPing: nothing configures it (rule 12).
	return d.Ping(dctx, j.sess, j.id, model.FormatPing(j.id))
}

// commit records the outcome of a delivery under the lock.
func (s *Service) commit(ctx context.Context, j job, err error) model.PingRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx = context.WithoutCancel(ctx)
	now := s.now()
	if s.inflight[j.sess.Key] == j.id {
		delete(s.inflight, j.sess.Key)
	}
	p := s.pings[j.id]
	if p == nil {
		return model.PingRecord{ID: j.id}
	}
	pp := s.planByID(j.planID)
	if err == nil {
		if p.State == model.PingSending {
			p.State = model.PingDelivered
		}
		if pp != nil {
			pp.LastPingAt = p.SentAt
			pp.Pings++
			pp.deliveryErr, pp.LastError = "", ""
		}
		s.log.Info("scheduler: ping delivered", "key", j.sess.Key, "ping", j.id)
	} else {
		p.State, p.Error = model.PingFailed, err.Error()
		delete(s.pings, j.id)
		// Stored before the plan reacts, so a segment booked now does not
		// count it.
		s.savePing(ctx, p)
		s.log.Warn("scheduler: ping delivery failed", "key", j.sess.Key, "ping", j.id, "err", err)
		if pp != nil {
			pp.lastTry = now
			if errors.Is(err, model.ErrSessionChanged) {
				s.endPlan(ctx, now, pp, model.PlanEnded, model.EndSwitched, "the process moved to another conversation")
				pp = nil
			} else {
				pp.deliveryErr = err.Error()
				pp.LastError = "ping delivery failed: " + err.Error()
			}
		}
	}
	if err == nil {
		s.savePing(ctx, p)
	}
	if pp != nil {
		s.persist(ctx, pp)
	}
	s.rebuildViews(now)
	return p.PingRecord
}

// savePing stores a ping (correcting a booked ledger entry if its cost
// changed) and publishes it. Requires s.mu.
func (s *Service) savePing(ctx context.Context, p *pingRun) {
	fixed, err := s.cfg.Store.UpdatePing(context.WithoutCancel(ctx), p.Ping)
	if err != nil {
		s.log.Error("scheduler: ping write failed", "ping", p.ID, "err", err)
	}
	if len(fixed) > 0 {
		s.reloadTotals(ctx)
		for _, e := range fixed {
			s.publish("ledger", e)
		}
	}
	s.publish("ping", p.PingRecord)
}

// unconsumed returns the session's ping that has not been answered yet
// (sending, delivered, or seen without a reply), or nil. Requires s.mu.
func (s *Service) unconsumed(key string) *pingRun {
	var out *pingRun
	for _, p := range s.pings {
		if p.Key != key {
			continue
		}
		switch p.State {
		case model.PingSending, model.PingDelivered, model.PingSeen:
			if out == nil || p.SentAt.After(out.SentAt) {
				out = p
			}
		}
	}
	return out
}

// isCold reports whether a ping reply re-wrote its context: at least
// ColdFraction of the prompt was not read from the cache.
func isCold(u model.Usage) bool {
	ctx := u.Context()
	return ctx > 0 && float64(u.Input+u.CacheWrite()) >= ColdFraction*float64(ctx)
}

// matchPing applies a transcript event to the ping it names: its marker
// appearing (seen) and its reply's API calls (verified, cost, cold).
// Requires s.mu.
func (s *Service) matchPing(ctx context.Context, now time.Time, key string, a model.Activity) {
	if a.PingID == "" {
		return
	}
	p := s.pings[a.PingID]
	if p == nil || p.Key != key {
		return
	}
	switch a.Kind {
	case model.ActPing:
		if p.State == model.PingSending || p.State == model.PingDelivered {
			p.State = model.PingSeen
			s.savePing(ctx, p)
		}
	case model.ActPingReply:
		if a.Usage == nil {
			return
		}
		first := p.Calls == 0
		p.Calls++
		p.Usage = p.Usage.Add(*a.Usage)
		if a.Model != "" {
			p.model = a.Model
		}
		if first {
			p.Cold = isCold(*a.Usage)
		}
		wasVerified := p.State == model.PingVerified
		p.State, p.Error = model.PingVerified, ""
		if p.restored && p.Calls < p.storedCalls {
			return // still catching up with what was stored
		}
		pp := s.planByID(p.PlanID)
		old := p.Cost
		if price, ok := s.cfg.Pricing.Lookup(p.model); ok {
			p.Cost = price.Cost(p.Usage)
		}
		if pp != nil {
			pp.PingCost += p.Cost - old
		}
		if p.EpisodeID != 0 && store.Day(p.SentAt) == s.expDay {
			s.expCost += p.Cost - old - p.reserve
		}
		p.reserve = 0
		s.savePing(ctx, p)
		if !wasVerified && pp != nil {
			s.onVerified(ctx, now, pp, p, a)
		}
	}
}

// onVerified reacts to a ping's first reply: a warm one resets the cold
// streak; a cold one books its segment as wasted, teaches a shorter TTL
// and, the second time in a row, pauses the plan (rule 6). Treatment pings
// neither teach nor pause. Requires s.mu.
func (s *Service) onVerified(ctx context.Context, now time.Time, pr *planRun, p *pingRun, reply model.Activity) {
	if !p.Cold {
		if p.EpisodeID == 0 {
			pr.ColdStreak = 0
		}
		return
	}
	// The reply re-wrote the cache: the next gap starts at it.
	if g := pr.seg; g != nil && g.IdleFrom.Equal(p.SegFrom) {
		pr.seg = nil
		s.book(ctx, pr, *g, p.SentAt, model.EndCold, model.OutcomeWasted, 0)
	}
	pr.BaseFrom, pr.BaseContext = reply.Anchor(), reply.Usage.Context()
	if p.EpisodeID != 0 {
		// A busy session may hold a treatment ping before answering it, so
		// a cold reply says nothing about the TTL and does not pause.
		return
	}
	pr.ColdStreak++
	sess := pr.sess
	if sess.Key == "" {
		sess = p.sess
	}
	cur := s.ttlOf(sess)
	var gap time.Duration
	if !p.prevRefresh.IsZero() {
		gap = reply.Anchor().Sub(p.prevRefresh)
	}
	nt := s.learn(ctx, sess.Harness, sess.Model, cur, gap)
	if pr.ColdStreak >= 2 {
		s.pause(ctx, pr, fmt.Sprintf("two pings in a row found the cache cold (last: %s); the cache TTL seems shorter than %s",
			p.ID, nt.Round(time.Second)))
		return
	}
	pr.LastError = fmt.Sprintf("ping %s found the cache cold; pinging every %s from now on",
		p.ID, (nt - s.margin()).Round(time.Second))
}

// settlePings ends the verification of pings: delivered but never seen
// within VerifyWindow is lost, and its plan is paused rather than retried
// (rule 3); seen and verified pings stop being followed after the window.
// Requires s.mu.
func (s *Service) settlePings(ctx context.Context, now time.Time) {
	for _, id := range sortedKeys(s.pings) {
		p := s.pings[id]
		if s.inflight[p.Key] == id || now.Sub(p.SentAt) < VerifyWindow {
			continue
		}
		delete(s.pings, id)
		if p.State != model.PingSending && p.State != model.PingDelivered {
			continue
		}
		p.State = model.PingLost
		p.Error = "delivered but never seen in the transcript"
		s.savePing(ctx, p)
		if pp := s.planByID(p.PlanID); pp != nil && p.EpisodeID != 0 {
			// A busy session may hold a ping back (a permission prompt):
			// the treatment stops for this episode, the plan goes on.
			if pp.ep != nil && pp.ep.ID == p.EpisodeID {
				pp.ep.maxPings = pp.ep.pings
			}
		} else if pp != nil {
			s.pause(ctx, pp, fmt.Sprintf("ping %s was delivered but never appeared in the transcript", p.ID))
		}
	}
}

// sweep asks every Withdrawer to remove queued pings whose session is not
// live or that are older than VerifyWindow (rule 4), then marks the
// unconsumed pings of sessions that are gone as withdrawn.
func (s *Service) sweep(ctx context.Context) {
	if !s.sweeping.TryLock() {
		return
	}
	defer s.sweeping.Unlock()
	s.mu.Lock()
	s.lastSweep = s.now()
	live := make([]model.Session, 0, len(s.sessions))
	for _, k := range sortedKeys(s.sessions) {
		live = append(live, s.sessions[k])
	}
	s.mu.Unlock()

	swept := map[model.Harness]bool{}
	for _, d := range s.cfg.Deliverers {
		w, ok := d.(model.Withdrawer)
		if !ok {
			continue
		}
		n, err := w.Sweep(ctx, live, VerifyWindow)
		if err != nil {
			s.log.Warn("scheduler: withdrawing queued pings failed", "harness", d.Harness(), "err", err)
			continue
		}
		swept[d.Harness()] = true
		if n > 0 {
			s.log.Info("scheduler: withdrew queued pings", "harness", d.Harness(), "n", n)
		}
	}
	if len(swept) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range sortedKeys(s.pings) {
		p := s.pings[id]
		if _, live := s.sessions[p.Key]; live || !swept[harnessOf(p.Key)] || s.inflight[p.Key] == id {
			continue
		}
		if p.State == model.PingSending || p.State == model.PingDelivered {
			p.State = model.PingWithdrawn
			delete(s.pings, id)
			s.savePing(ctx, p)
		}
	}
}
