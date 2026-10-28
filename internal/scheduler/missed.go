package scheduler

import (
	"math"
	"sort"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/pricing"
)

// Missed replays the API calls of every live session since `since` as if a
// keepalive had been started at each idle gap, lasting the longest plan the
// UI would offer (MaxHours, capped at the model's break-even).
//
// A gap the user came back to while the simulated keepalive held the cache
// counts as saved: the re-cache avoided minus its pings. Pings for a gap
// that outlasted the plan count as wasted. The gap still open now counts
// only once it has outlasted the plan. Gaps while the agent was working
// (stopped for a tool) are skipped, as the scheduler sends no pings then.
// Gaps are measured between request times (Activity.Anchor), the moments
// the provider refreshed the cache (rule 1); keepalive pings and their
// replies are not real calls and are left out.
func (s *Service) Missed(since time.Time) model.Missed {
	s.mu.Lock()
	sessions := make([]model.Session, 0, len(s.sessions))
	for _, k := range sortedKeys(s.sessions) {
		sess := s.sessions[k]
		sess.CacheTTL = model.Duration(s.ttlOf(sess))
		sessions = append(sessions, sess)
	}
	st, margin, now := s.settings, s.margin(), s.now()
	s.mu.Unlock()

	out := model.Missed{Since: since, Hours: st.MaxHours, Sessions: []model.MissedSession{}}
	for _, sess := range sessions {
		src := s.sources[sess.Harness]
		if src == nil {
			continue
		}
		var calls []model.Activity
		for _, a := range src.Activity(sess.Key, 0) {
			if a.Kind == model.ActAssistant && a.Usage != nil && !a.Anchor().Before(since) {
				calls = append(calls, a)
			}
		}
		ms := simulate(s.cfg.Pricing, sess, calls, sess.CacheTTL.Std(), margin, hoursDur(st.MaxHours), now)
		if ms.Returns+ms.Expired == 0 {
			continue
		}
		out.Saved += ms.Saved
		out.Wasted += ms.Wasted
		out.Returns += ms.Returns
		out.Expired += ms.Expired
		out.Sessions = append(out.Sessions, ms)
	}
	out.Net = out.Saved - out.Wasted
	sort.Slice(out.Sessions, func(i, j int) bool {
		a, b := out.Sessions[i], out.Sessions[j]
		if a.Net != b.Net {
			return a.Net > b.Net
		}
		return a.Key < b.Key
	})
	return out
}

// simulate is Missed for one session; calls are its real API calls, oldest
// first.
func simulate(t *pricing.Table, sess model.Session, calls []model.Activity, ttl, margin, maxPlan time.Duration, now time.Time) model.MissedSession {
	ms := model.MissedSession{Key: sess.Key, Title: sess.Title, Project: sess.Project, Model: sess.Model}
	iv := pricing.Interval(ttl, margin)
	var prev *model.Activity
	for i := range calls {
		a := &calls[i]
		if prev != nil && !prev.Working {
			gap(t, &ms, prev, a.Anchor().Sub(prev.Anchor()), true, ttl, margin, iv, maxPlan)
		}
		prev = a
	}
	if prev != nil && !prev.Working && sess.Status != model.StatusWorking {
		open := now.Sub(prev.Anchor())
		if open > planFor(t, ms.Model, prev, ttl, margin, maxPlan) {
			gap(t, &ms, prev, open, false, ttl, margin, iv, maxPlan)
		}
	}
	ms.Net = ms.Saved - ms.Wasted
	return ms
}

// planFor is the plan the UI would offer after the call `last`: maxPlan
// capped at the break-even of that call's model.
func planFor(t *pricing.Table, fallback string, last *model.Activity, ttl, margin, maxPlan time.Duration) time.Duration {
	m := last.Model
	if m == "" {
		m = fallback
	}
	if c := t.Costs(m, last.Usage.Context(), ttl, margin); c.Known && c.BreakEvenHours > 0 {
		return min(maxPlan, hoursDur(c.BreakEvenHours))
	}
	return maxPlan
}

// gap books one idle gap of length d after the call `last`; returned says
// whether a call ended it.
func gap(t *pricing.Table, ms *model.MissedSession, last *model.Activity, d time.Duration, returned bool, ttl, margin, iv, maxPlan time.Duration) {
	if d <= iv {
		return // no ping would have been due
	}
	m := last.Model
	if m == "" {
		m = ms.Model
	}
	p, ok := t.Lookup(m)
	if !ok {
		return // unknown models count nothing
	}
	ms.Known = true
	plan := planFor(t, ms.Model, last, ttl, margin, maxPlan)
	// A ping goes out every interval while the gap is open and the plan runs.
	n := min(int(math.Ceil(d.Seconds()/iv.Seconds()))-1, int(plan/iv))
	ctx := float64(last.Usage.Context())
	pings := float64(n) * (ctx*p.Read + pricing.PingOutputTokens*p.Output) / 1e6
	warm := d < time.Duration(n)*iv+ttl
	if returned && warm && d > ttl {
		ms.Returns++
		ms.Saved += p.Avoided(last.Usage.Context(), ttl) - pings
		return
	}
	if n > 0 {
		ms.Expired++
		ms.Wasted += pings
	}
}
