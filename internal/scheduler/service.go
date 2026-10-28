package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/pricing"
	"github.com/gurupras/agent-keepalive-manager/internal/store"
)

// ---- sessions ----

// Sessions returns the views built by the last scan. It never waits on a
// delivery (rule 2).
func (s *Service) Sessions() []model.SessionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.SessionView(nil), s.views...)
}

// Session returns one view.
func (s *Service) Session(key string) (model.SessionView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.views {
		if v.Key == key {
			return v, true
		}
	}
	return model.SessionView{}, false
}

// ---- plans ----

// reachable returns the live session for key, or why no plan or ping can
// go to it. Requires s.mu.
func (s *Service) reachable(key string) (model.Session, error) {
	sess, ok := s.sessions[key]
	if !ok {
		return sess, fmt.Errorf("%w: no live session %s", model.ErrNotFound, key)
	}
	if sess.Reach == model.ReachNone || sess.Reach == "" {
		return sess, fmt.Errorf("%w%s", model.ErrUnreachable, suffix(sess.ReachWhy))
	}
	if s.deliverers[sess.Harness] == nil {
		return sess, fmt.Errorf("%w: no delivery mechanism for %s sessions", model.ErrUnreachable, sess.Harness)
	}
	return sess, nil
}

// StartPlan keeps a session warm for hours, capped at its model's
// break-even (the plan's Hours is the effective length).
func (s *Service) StartPlan(ctx context.Context, key string, hours float64) (model.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if math.IsNaN(hours) || hours <= 0 || hours > s.settings.MaxHours {
		return model.Plan{}, fmt.Errorf("%w: hours must be in (0, %g]", model.ErrInvalid, s.settings.MaxHours)
	}
	return s.start(ctx, key, hours, false)
}

// KeepIndefinitely starts a rolling plan, or turns the session's plan into
// one: each human prompt moves Until to one window (MaxHours, capped at
// break-even) past it.
func (s *Service) KeepIndefinitely(ctx context.Context, key string) (model.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pr, ok := s.plans[key]; ok {
		now := s.now()
		pr.Indefinite = true
		if u := latest(now, pr.sess.LastHuman).Add(hoursDur(s.window(pr.sess))); u.After(pr.Until) {
			pr.Until = u
		}
		pr.Hours = pr.Until.Sub(pr.StartedAt).Hours()
		s.persist(ctx, pr)
		s.rebuildViews(now)
		return pr.Plan.Plan, nil
	}
	return s.start(ctx, key, 0, true)
}

// start requires s.mu.
func (s *Service) start(ctx context.Context, key string, hours float64, indefinite bool) (model.Plan, error) {
	now := s.now()
	sess, err := s.reachable(key)
	if err != nil {
		return model.Plan{}, err
	}
	if _, ok := s.plans[key]; ok {
		return model.Plan{}, fmt.Errorf("%w: the session already has a plan", model.ErrConflict)
	}
	win := s.window(sess)
	p := store.Plan{
		Plan:      model.Plan{Key: key, PID: sess.PID, StartedAt: now, State: model.PlanActive, Indefinite: indefinite},
		ProcStart: sess.ProcStart, BaseFrom: sess.LastCall, BaseContext: sess.ContextTokens, BaseModel: sess.Model,
	}
	if indefinite {
		p.RequestedHours = win
		p.Until = latest(now, sess.LastHuman).Add(hoursDur(win))
	} else {
		p.RequestedHours = hours
		p.Until = now.Add(hoursDur(min(hours, win)))
	}
	p.Hours = p.Until.Sub(now).Hours()
	ttl := s.ttlOf(sess)
	if sess.Status != model.StatusWorking && !sess.LastCall.IsZero() && !now.Before(sess.LastCall.Add(ttl)) {
		// Already cold: pinging now would re-write the whole context. The
		// plan starts keeping it warm after the next real call.
		p.WaitWarm = true
	}
	p, err = s.cfg.Store.InsertPlan(context.WithoutCancel(ctx), p)
	if err != nil {
		return model.Plan{}, fmt.Errorf("scheduler: save plan: %w", err)
	}
	pr := newPlanRun(p)
	pr.sess = sess
	s.plans[key] = pr
	s.follow(key)
	if !p.WaitWarm && !sess.LastCall.IsZero() {
		pr.NextPing = sess.LastCall.Add(pricing.Interval(ttl, s.margin()))
	}
	s.log.Info("scheduler: plan started", "plan", p.ID, "key", key, "hours", p.Hours, "indefinite", indefinite)
	s.persist(ctx, pr)
	s.publish("plan", pr.Plan.Plan)
	s.rebuildViews(now)
	s.poke()
	return pr.Plan.Plan, nil
}

// ExtendPlan moves Until out by addHours, at most MaxHours from now.
func (s *Service) ExtendPlan(ctx context.Context, key string, addHours float64) (model.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if math.IsNaN(addHours) || addHours <= 0 {
		return model.Plan{}, fmt.Errorf("%w: hours to add must be positive", model.ErrInvalid)
	}
	pr, ok := s.plans[key]
	if !ok {
		return model.Plan{}, fmt.Errorf("%w: no plan for %s", model.ErrNotFound, key)
	}
	now := s.now()
	want := pr.Until.Add(hoursDur(addHours))
	if limit := now.Add(hoursDur(s.settings.MaxHours)); want.After(limit) {
		want = limit
	}
	if !want.After(pr.Until) {
		return pr.Plan.Plan, fmt.Errorf("%w: the plan already ends %g hours from now, the maximum", model.ErrInvalid, s.settings.MaxHours)
	}
	pr.Until = want
	pr.Hours = pr.Until.Sub(pr.StartedAt).Hours()
	s.persist(ctx, pr)
	s.rebuildViews(now)
	s.poke()
	return pr.Plan.Plan, nil
}

// ResumePlan restarts a paused plan.
func (s *Service) ResumePlan(ctx context.Context, key string) (model.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr, ok := s.plans[key]
	if !ok {
		return model.Plan{}, fmt.Errorf("%w: no plan for %s", model.ErrNotFound, key)
	}
	if pr.State != model.PlanPaused {
		return pr.Plan.Plan, fmt.Errorf("%w: the plan is not paused", model.ErrConflict)
	}
	now := s.now()
	pr.State, pr.Reason, pr.LastError, pr.ColdStreak, pr.deliveryErr = model.PlanActive, "", "", 0, ""
	sess := pr.sess
	if sess.Status != model.StatusWorking && !sess.LastCall.IsZero() && !now.Before(sess.LastCall.Add(s.ttlOf(sess))) {
		pr.WaitWarm = true
	}
	s.persist(ctx, pr)
	s.rebuildViews(now)
	s.poke()
	return pr.Plan.Plan, nil
}

// CancelPlan stops a plan; the pings of its open segment are booked as
// wasted (EndCancelled).
func (s *Service) CancelPlan(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr, ok := s.plans[key]
	if !ok {
		return fmt.Errorf("%w: no plan for %s", model.ErrNotFound, key)
	}
	now := s.now()
	s.endPlan(ctx, now, pr, model.PlanCancelled, model.EndCancelled, "cancelled")
	s.rebuildViews(now)
	return nil
}

// PingNow sends one ping right away, under the same checks as a planned
// one: reachable, not working, nothing unconsumed (rule 3), and the
// process still running this conversation (rule 5). It is attached to the
// session's active plan, if any.
func (s *Service) PingNow(ctx context.Context, key string) (model.PingRecord, error) {
	s.mu.Lock()
	sess, err := s.reachable(key)
	if err != nil {
		s.mu.Unlock()
		return model.PingRecord{}, err
	}
	pr := s.plans[key]
	var stale string
	if pr != nil {
		stale = s.staleness(pr, sess, s.now())
	}
	switch {
	case s.inflight[key] != "":
		err = fmt.Errorf("%w: ping %s is being delivered", model.ErrConflict, s.inflight[key])
	case s.unconsumed(key) != nil:
		err = fmt.Errorf("%w: ping %s has not been consumed yet", model.ErrConflict, s.unconsumed(key).ID)
	case sess.Status == model.StatusWorking && stale == "":
		err = fmt.Errorf("%w: the session is working; pings are only sent while it is idle", model.ErrConflict)
	case !s.alive(sess.PID, sess.ProcStart):
		err = fmt.Errorf("%w: process %d is not running", model.ErrNotFound, sess.PID)
	case s.byPID[sess.PID] != key:
		err = model.ErrSessionChanged
	}
	if err != nil {
		s.mu.Unlock()
		return model.PingRecord{}, err
	}
	if pr != nil && pr.State != model.PlanActive {
		pr = nil
	}
	j, ok := s.newPing(ctx, s.now(), pr, sess)
	s.mu.Unlock()
	if !ok {
		return model.PingRecord{}, fmt.Errorf("scheduler: the ping could not be recorded")
	}
	derr := s.deliver(ctx, j)
	rec := s.commit(ctx, j, derr)
	if derr != nil {
		return rec, derr
	}
	return rec, nil
}

// ---- ledger and pings ----

// Ledger returns the entries that ended in [from, to) and their totals.
func (s *Service) Ledger(from, to time.Time) ([]model.LedgerEntry, model.Totals, error) {
	es, err := s.cfg.Store.Ledger(context.Background(), from, to)
	if err != nil {
		return nil, model.Totals{}, err
	}
	if es == nil {
		es = []model.LedgerEntry{}
	}
	return es, store.SumTotals(es), nil
}

// Pings returns the newest pings of a session (50 when limit <= 0).
func (s *Service) Pings(key string, limit int) ([]model.PingRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	ps, err := s.cfg.Store.PingsByKey(context.Background(), key, limit)
	if err != nil {
		return nil, err
	}
	out := make([]model.PingRecord, len(ps))
	for i, p := range ps {
		out[i] = p.PingRecord
	}
	return out, nil
}

// DayStats implements model.Stats.
func (s *Service) DayStats(day time.Time) (model.DayStats, error) {
	return s.cfg.Store.DayStats(context.Background(), day)
}

// ---- settings ----

// ValidateSettings reports why st is unusable (wrapping model.ErrInvalid),
// or nil. The ranges are the API's (docs/api.md).
func ValidateSettings(st model.Settings) error {
	bad := func(v, lo, hi float64) bool { return math.IsNaN(v) || v < lo || v > hi }
	switch {
	case bad(st.MaxHours, 0.5, 168):
		return fmt.Errorf("%w: maxHours must be between 0.5 and 168", model.ErrInvalid)
	case bad(st.MarginMin, 1, 15):
		return fmt.Errorf("%w: marginMin must be between 1 and 15", model.ErrInvalid)
	case bad(st.ScanSeconds, 2, 60):
		return fmt.Errorf("%w: scanSeconds must be between 2 and 60", model.ErrInvalid)
	}
	return nil
}

// Settings returns the current settings.
func (s *Service) Settings() model.Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// UpdateSettings validates and stores st. Running plans keep their Until.
func (s *Service) UpdateSettings(ctx context.Context, st model.Settings) (model.Settings, error) {
	if err := ValidateSettings(st); err != nil {
		return model.Settings{}, err
	}
	if err := s.cfg.Store.SaveSettings(ctx, st); err != nil {
		return model.Settings{}, fmt.Errorf("scheduler: save settings: %w", err)
	}
	s.mu.Lock()
	s.settings = st
	s.publish("settings", st)
	s.rebuildViews(s.now())
	s.mu.Unlock()
	s.poke()
	return st, nil
}

// ---- events ----

// Subscribe returns a buffered event channel and its cancel func. A
// subscriber that falls behind loses events instead of blocking the
// scheduler; the next scan that changes anything sends a fresh snapshot.
func (s *Service) Subscribe() (<-chan model.Event, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSub
	s.nextSub++
	ch := make(chan model.Event, subBuffer)
	s.subs[id] = ch
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.subs, id)
			s.mu.Unlock()
			close(ch)
		})
	}
}

// publish requires s.mu.
func (s *Service) publish(typ string, data any) {
	ev := model.Event{Type: typ, Data: data}
	for _, ch := range s.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// rebuildViews recomputes the session views and publishes them when what
// the UI shows changed. Requires s.mu.
func (s *Service) rebuildViews(now time.Time) {
	views := make([]model.SessionView, 0, len(s.sessions))
	for _, sess := range s.sessions {
		ttl := s.ttlOf(sess)
		sess.CacheTTL = model.Duration(ttl)
		v := model.SessionView{Session: sess, Costs: s.costs(sess)}
		switch {
		case sess.Status == model.StatusWorking:
			v.CacheExpiresAt = now.Add(ttl)
		case !sess.LastCall.IsZero():
			v.CacheExpiresAt = sess.LastCall.Add(ttl)
		}
		if pr, ok := s.plans[sess.Key]; ok {
			p := pr.Plan.Plan
			v.Plan = &p
		}
		t := s.totals[sess.Key]
		v.SavedTotal, v.WastedTotal = t.Saved, t.Wasted
		views = append(views, v)
	}
	sort.Slice(views, func(i, j int) bool {
		if !views[i].LastCall.Equal(views[j].LastCall) {
			return views[i].LastCall.After(views[j].LastCall)
		}
		return views[i].Key < views[j].Key
	})
	s.views = views
	if fp := fingerprint(views); fp != s.viewsFP {
		s.viewsFP = fp
		s.publish("sessions", append([]model.SessionView(nil), views...))
	}
}

// fingerprint hashes the views, ignoring the expiry of working sessions,
// which moves on every scan.
func fingerprint(vs []model.SessionView) string {
	h := fnv.New128a()
	enc := json.NewEncoder(h)
	for _, v := range vs {
		if v.Status == model.StatusWorking {
			v.CacheExpiresAt = time.Time{}
		}
		_ = enc.Encode(v)
	}
	return string(h.Sum(nil))
}
