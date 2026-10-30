// Package scheduler is the application core. It scans the live sessions of
// every Source, runs the keepalive plans the user started, delivers pings
// and verifies them against the transcripts, and keeps the money ledger.
// It implements model.Service and model.Stats.
//
// The rules it follows are listed in docs/architecture.md ("Scheduler
// rules"); comments refer to them as "rule N". In short: ping timing is
// anchored on the request time of the last API call (1); delivery runs
// outside the lock (2) with one unconsumed ping per session (3); queued
// pings are withdrawn (4); a plan ends when its process switches
// conversation (5); cold pings teach the TTL (6); "saved" needs a cache hit
// (7); the ledger is durable and idempotent (8); busy is bounded (9); only
// wall-clock time is used and clock jumps are re-evaluated (10); the ping
// text is fixed (12).
//
// All mutable state is guarded by one mutex. A tick takes it to decide,
// releases it while pings are delivered, and takes it again to commit, so
// API reads never wait on a delivery.
package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/pricing"
	"github.com/gurupras/agent-keepalive-manager/internal/proc"
	"github.com/gurupras/agent-keepalive-manager/internal/store"
)

// Tunables. They are not settings: each one is part of a rule.
const (
	// VerifyWindow is how long a delivered ping may stay unseen before it
	// is lost (rule 3), and the age past which queued pings are withdrawn
	// (rule 4).
	VerifyWindow = 5 * time.Minute
	// ReturnWait is how long a return waits for the API call answering it
	// before it is booked without a cache-hit check (as EndMissed).
	ReturnWait = 10 * time.Minute
	// SendTimeout bounds one delivery (rule 2).
	SendTimeout = 20 * time.Second
	// RetryEvery is the least time between delivery attempts of a plan.
	RetryEvery = 30 * time.Second
	// StaleAfter is how long "working" without transcript activity is
	// believed (rule 9).
	StaleAfter = 2 * time.Hour
	// JumpThreshold: a tick this much later than expected (or earlier than
	// the last one) is a clock jump (rule 10).
	JumpThreshold = time.Minute
	// UsageSettle: an API call's usage is read as final once it is this
	// old (by At) or followed by another activity.
	UsageSettle = 10 * time.Second
	// SweepEvery is the Withdrawer cadence (rule 4).
	SweepEvery = time.Minute
	// MinTTL is the floor of a learned TTL (rule 6).
	MinTTL = 5 * time.Minute
	// DefaultTTL is used when a source reports no TTL: the shortest tier,
	// so an unknown session is never pinged too late.
	DefaultTTL = 5 * time.Minute
	// ColdFraction: a ping reply that wrote at least this share of its
	// context uncached found the cache cold.
	ColdFraction = 0.5
	// HitFraction: a return is saved only if its call read at least this
	// share of the pre-gap context from the cache (rule 7).
	HitFraction = 0.8

	missLimit = 3 // scans a plan's session may be missing while its pid lives
	subBuffer = 64
)

// Config wires a Service.
type Config struct {
	Store      *store.Store
	Sources    []model.Source
	Deliverers []model.Deliverer // those that also implement model.Withdrawer are swept
	Pricing    *pricing.Table
	Log        *slog.Logger
	// Now is the clock; defaults to time.Now. Readings are always passed
	// through Round(0) so no monotonic reading leaks into comparisons
	// (rule 10).
	Now func() time.Time
	// Alive reports whether pid still runs the process whose start token
	// is start ("" = any). Defaults to proc.Alive.
	Alive func(pid int, start string) bool
	// Experiments reports whether the install takes part in experiments
	// (internal/experiment). Nil means it does not.
	Experiments func() bool
}

// Service implements model.Service and model.Stats.
type Service struct {
	cfg         Config
	log         *slog.Logger
	clock       func() time.Time
	alive       func(int, string) bool
	sources     map[model.Harness]model.Source
	deliverers  map[model.Harness]model.Deliverer
	wake        chan struct{}
	deliveries  sync.WaitGroup // background deliveries started by ticks
	sendTimeout time.Duration  // SendTimeout; shorter in tests
	sweeping    sync.Mutex     // one sweep at a time

	mu        sync.Mutex
	settings  model.Settings
	sessions  map[string]model.Session
	byPID     map[int]string
	plans     map[string]*planRun // active and paused plans by session key
	pings     map[string]*pingRun // pings whose outcome may still change, by id
	inflight  map[string]string   // session key -> id of the ping being delivered
	cursors   map[string]int64    // session key -> last activity Seq consumed
	lastAct   map[string]time.Time
	learned   map[string]time.Duration // learnKey -> learned TTL
	totals    map[string]model.Totals
	sighted   map[string]bool // keys recorded in sightings for sightDay
	sightDay  string
	lastTick  time.Time
	startup   model.Startup
	lastSweep time.Time
	views     []model.SessionView
	viewsFP   string
	subs      map[int]chan model.Event
	nextSub   int

	expSalt    string          // assigns episodes to arms
	expStopped map[string]bool // experiments the stop rule ended on this install
	expDay     string          // UTC day expPings and expCost count
	expPings   int             // treatment pings sent on expDay
	expCost    float64         // what they cost
}

var (
	_ model.Service = (*Service)(nil)
	_ model.Stats   = (*Service)(nil)
)

// New loads settings, learned TTLs, ledger totals, open plans, their open
// segments and unsettled pings from the store, so a restart resumes where
// the last run stopped (rule 8).
func New(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, errors.New("scheduler: Store is required")
	}
	if cfg.Pricing == nil {
		cfg.Pricing = pricing.Default()
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Alive == nil {
		cfg.Alive = proc.Alive
	}
	s := &Service{
		cfg: cfg, log: cfg.Log, clock: cfg.Now, alive: cfg.Alive,
		startup:     model.Startup{Step: "Starting"},
		sources:     map[model.Harness]model.Source{},
		deliverers:  map[model.Harness]model.Deliverer{},
		wake:        make(chan struct{}, 1),
		sendTimeout: SendTimeout,
		sessions:    map[string]model.Session{},
		byPID:       map[int]string{},
		plans:       map[string]*planRun{},
		pings:       map[string]*pingRun{},
		inflight:    map[string]string{},
		cursors:     map[string]int64{},
		lastAct:     map[string]time.Time{},
		learned:     map[string]time.Duration{},
		sighted:     map[string]bool{},
		subs:        map[int]chan model.Event{},
		expStopped:  map[string]bool{},
	}
	for _, src := range cfg.Sources {
		s.sources[src.Harness()] = src
	}
	for _, d := range cfg.Deliverers {
		s.deliverers[d.Harness()] = d
	}
	if err := s.restore(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

// now reads the clock without its monotonic reading (rule 10).
func (s *Service) now() time.Time { return s.clock().Round(0) }

func (s *Service) restore(ctx context.Context) error {
	st := s.cfg.Store
	var err error
	if s.settings, err = st.LoadSettings(ctx); err != nil {
		s.log.Warn("scheduler: settings unreadable, using defaults", "err", err)
	}
	if err := ValidateSettings(s.settings); err != nil {
		s.log.Warn("scheduler: stored settings invalid, using defaults", "err", err)
		s.settings = model.DefaultSettings()
	}
	ttls, err := st.Prefixed(ctx, learnPrefix)
	if err != nil {
		return fmt.Errorf("scheduler: load learned TTLs: %w", err)
	}
	for k, v := range ttls {
		if sec, err := strconv.ParseFloat(v, 64); err == nil && sec > 0 {
			s.learned[strings.TrimPrefix(k, learnPrefix)] = time.Duration(sec * float64(time.Second))
		}
	}
	if err := s.restoreExperiments(ctx); err != nil {
		return err
	}
	if s.totals, err = st.TotalsByKey(ctx); err != nil {
		return fmt.Errorf("scheduler: load totals: %w", err)
	}
	plans, err := st.OpenPlans(ctx)
	if err != nil {
		return fmt.Errorf("scheduler: load plans: %w", err)
	}
	segs, err := st.Segments(ctx)
	if err != nil {
		return fmt.Errorf("scheduler: load segments: %w", err)
	}
	byPlan := map[int64][]store.Segment{}
	for _, g := range segs {
		byPlan[g.PlanID] = append(byPlan[g.PlanID], g)
	}
	for _, p := range plans {
		pr := newPlanRun(p)
		// Activity up to LastSeen was applied before the restart; the
		// source's Seq numbers start over, so skip by time instead.
		pr.replayUntil = p.LastSeen
		s.plans[p.Key] = pr
		s.cursors[p.Key] = 0
		gs := byPlan[p.ID]
		for i, g := range gs {
			if i == len(gs)-1 {
				g := g
				pr.seg = &g
				continue
			}
			// Only one segment is open at a time; an older one was a
			// return whose answering call was never read. Book it without
			// the cache-hit check.
			s.book(ctx, pr, g, p.LastSeen, model.EndMissed, model.OutcomeWasted, 0)
		}
		delete(byPlan, p.ID)
		s.log.Info("scheduler: resumed plan", "plan", p.ID, "key", p.Key, "state", p.State, "until", p.Until)
	}
	// Segments of plans that are no longer open cannot be continued.
	for _, gs := range byPlan {
		for _, g := range gs {
			if _, _, err := st.CloseSegment(ctx, segEntry(g, g.OpenedAt, model.EndExpired, model.OutcomeWasted, 0), nil, nil); err != nil {
				s.log.Warn("scheduler: closing orphan segment failed", "plan", g.PlanID, "err", err)
			}
		}
	}
	pings, err := st.UnsettledPings(ctx, s.now().Add(-VerifyWindow))
	if err != nil {
		return fmt.Errorf("scheduler: load pings: %w", err)
	}
	for _, p := range pings {
		pr := &pingRun{Ping: p, restored: true, storedCalls: p.Calls}
		// The transcript is read again from the start: count the reply's
		// calls afresh and correct the stored cost once at least as many
		// have been seen (rule 8).
		pr.Calls, pr.Usage = 0, model.Usage{}
		if pp := s.planByID(p.PlanID); pp != nil {
			pr.model = pp.BaseModel
		}
		if pr.State == model.PingSending {
			// The process stopped mid-delivery: it may have been written.
			pr.State = model.PingDelivered
		}
		s.pings[p.ID] = pr
		s.cursors[p.Key] = 0
	}
	if n := len(s.totals); n > 0 {
		s.log.Debug("scheduler: ledger totals loaded", "sessions", n)
	}
	return nil
}

// Run scans every Settings.ScanSeconds until ctx is done, sweeping queued
// pings at start-up and every minute (rule 4). It returns nil after a
// normal shutdown, once in-flight deliveries have finished.
func (s *Service) Run(ctx context.Context) error {
	defer s.deliveries.Wait()
	s.setStep("Clearing leftover keepalive messages")
	s.sweep(ctx)
	for {
		s.tick(ctx)
		t := time.NewTimer(s.scanEvery())
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-s.wake:
			t.Stop()
		case <-t.C:
		}
	}
}

// Startup reports what the server does before its first scan is in.
func (s *Service) Startup() model.Startup {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startup
}

func (s *Service) setStep(step string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startup.Ready {
		return
	}
	s.startup.Step = step
	s.publish("startup", s.startup)
}

func readingStep(h model.Harness) string {
	switch h {
	case model.Claude:
		return "Reading Claude Code transcripts"
	case model.Codex:
		return "Reading Codex sessions"
	}
	return "Reading " + string(h) + " sessions"
}

func (s *Service) scanEvery() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return seconds(s.settings.ScanSeconds)
}

// poke asks Run for an early tick.
func (s *Service) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

type scanResult struct {
	h  model.Harness
	ss []model.Session
}

// tick runs one scan: read sessions (no lock), then under the lock apply
// them, consume transcript activity, settle pings and advance every plan;
// deliveries decided on the way run after the lock is released (rule 2).
func (s *Service) tick(ctx context.Context) {
	var results []scanResult
	for _, src := range s.cfg.Sources {
		if !s.Startup().Ready {
			s.setStep(readingStep(src.Harness()))
		}
		ss, err := src.Scan(ctx)
		if err != nil {
			s.log.Warn("scheduler: scan failed", "harness", src.Harness(), "err", err)
			continue
		}
		results = append(results, scanResult{src.Harness(), ss})
	}

	s.mu.Lock()
	now := s.now()
	expected := seconds(s.settings.ScanSeconds)
	jump := s.lastTick.IsZero() || now.Before(s.lastTick) || now.Sub(s.lastTick) > expected+JumpThreshold
	if jump && !s.lastTick.IsZero() {
		s.log.Info("scheduler: clock jump; re-evaluating every plan", "last", s.lastTick, "now", now)
	}
	s.lastTick = now
	s.applyScan(ctx, now, results)
	s.consumeAll(ctx, now)
	s.settlePings(ctx, now)
	var jobs []job
	for _, key := range sortedKeys(s.plans) {
		if pr := s.plans[key]; pr != nil {
			if j, ok := s.advance(ctx, now, pr, jump); ok {
				jobs = append(jobs, j)
			}
		}
	}
	for _, pr := range s.plans {
		s.persist(ctx, pr)
	}
	s.rebuildViews(now)
	if !s.startup.Ready {
		s.startup = model.Startup{Ready: true}
		s.publish("startup", s.startup)
	}
	sweepDue := now.Sub(s.lastSweep) >= SweepEvery || now.Before(s.lastSweep)
	s.mu.Unlock()

	for _, j := range jobs {
		s.deliveries.Add(1)
		go func(j job) {
			defer s.deliveries.Done()
			s.commit(ctx, j, s.deliver(ctx, j))
		}(j)
	}
	if sweepDue {
		s.deliveries.Add(1)
		go func() {
			defer s.deliveries.Done()
			s.sweep(ctx)
		}()
	}
}

// applyScan replaces the session map. Sessions of a harness whose scan
// failed are kept as they were, so a transient error ends no plan.
// Requires s.mu.
func (s *Service) applyScan(ctx context.Context, now time.Time, results []scanResult) {
	ok := map[model.Harness]bool{}
	for _, r := range results {
		ok[r.h] = true
	}
	next := map[string]model.Session{}
	for k, v := range s.sessions {
		if !ok[v.Harness] {
			next[k] = v
		}
	}
	for _, r := range results {
		for _, sess := range r.ss {
			if sess.Harness == "" {
				sess.Harness = r.h
			}
			if sess.Key == "" {
				sess.Key = string(sess.Harness) + ":" + sess.ID
			}
			next[sess.Key] = sess
		}
	}
	s.sessions = next
	s.byPID = make(map[int]string, len(next))
	for k, v := range next {
		if v.PID > 0 {
			s.byPID[v.PID] = k
		}
	}
	day := store.Day(now)
	if day != s.sightDay {
		s.sightDay, s.sighted = day, map[string]bool{}
	}
	for _, k := range sortedKeys(next) {
		if s.sighted[k] {
			continue
		}
		if err := s.cfg.Store.RecordSighting(ctx, day, next[k].Harness, k); err != nil {
			s.log.Warn("scheduler: recording session sighting failed", "err", err)
			continue
		}
		s.sighted[k] = true
	}
}

// consumeAll reads new transcript activity of every session that has a
// plan or a ping to verify, in Seq order. Requires s.mu.
func (s *Service) consumeAll(ctx context.Context, now time.Time) {
	for _, key := range sortedKeys(s.cursors) {
		src := s.sources[harnessOf(key)]
		if src == nil {
			continue
		}
		acts := src.Activity(key, s.cursors[key])
		for i, a := range acts {
			if a.At.After(s.lastAct[key]) {
				s.lastAct[key] = a.At
			}
			if !settled(a, i < len(acts)-1, now) {
				// Stop before it: the cursor stays at Seq-1, so the next
				// tick reads the call again with its final usage.
				break
			}
			s.cursors[key] = a.Seq
			s.matchPing(ctx, now, key, a)
			if pr := s.plans[key]; pr != nil {
				s.applyActivity(ctx, now, pr, a)
			}
		}
	}
	// Stop following sessions nothing refers to any more.
	for key := range s.cursors {
		if s.plans[key] == nil && !s.hasPings(key) {
			delete(s.cursors, key)
			delete(s.lastAct, key)
		}
	}
}

// settled reports whether an activity's usage is final. Claude Code can
// write one API response over several records; the source emits the call
// once and corrects its Usage in place as later records arrive. A call is
// taken as final once a later activity exists or it is UsageSettle old.
func settled(a model.Activity, later bool, now time.Time) bool {
	if a.Usage == nil || later {
		return true
	}
	return now.Sub(a.At) >= UsageSettle
}

// follow starts consuming a session's activity from now on. Requires s.mu.
func (s *Service) follow(key string) {
	if _, ok := s.cursors[key]; ok {
		return
	}
	var last int64
	if src := s.sources[harnessOf(key)]; src != nil {
		if acts := src.Activity(key, 0); len(acts) > 0 {
			last = acts[len(acts)-1].Seq
			s.lastAct[key] = acts[len(acts)-1].At
		}
	}
	s.cursors[key] = last
}

func (s *Service) hasPings(key string) bool {
	for _, p := range s.pings {
		if p.Key == key {
			return true
		}
	}
	return false
}

// ---- TTLs ----

const learnPrefix = "ttl:"

func learnKey(h model.Harness, modelID string) string { return string(h) + ":" + modelID }

// ttlOf is the cache TTL the scheduler plans with: the session's own, or
// the default, lowered to what cold pings taught for its harness and model
// (rule 6). Requires s.mu.
func (s *Service) ttlOf(sess model.Session) time.Duration {
	ttl := sess.CacheTTL.Std()
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if l, ok := s.learned[learnKey(sess.Harness, sess.Model)]; ok && l < ttl {
		ttl = l
	}
	return ttl
}

// learn lowers the TTL of a harness and model after a cold ping found the
// cache expired gap after its last refresh: never below MinTTL, never by
// more than half at once. Requires s.mu.
func (s *Service) learn(ctx context.Context, h model.Harness, modelID string, cur, gap time.Duration) time.Duration {
	floor := max(MinTTL, cur/2)
	nt := cur - time.Minute
	if gap > 0 && gap-time.Minute < nt {
		nt = gap - time.Minute // the cache was gone before the gap ended
	}
	nt = max(nt, floor)
	if nt >= cur {
		return cur // already at the floor
	}
	k := learnKey(h, modelID)
	s.learned[k] = nt
	if err := s.cfg.Store.Set(ctx, learnPrefix+k, strconv.FormatFloat(nt.Seconds(), 'f', -1, 64)); err != nil {
		s.log.Warn("scheduler: saving learned TTL failed", "err", err)
	}
	s.log.Info("scheduler: learned a shorter cache TTL", "harness", h, "model", modelID, "from", cur, "to", nt)
	return nt
}

func (s *Service) margin() time.Duration { return minutes(s.settings.MarginMin) }

func (s *Service) costs(sess model.Session) model.Costs {
	return s.cfg.Pricing.Costs(sess.Model, sess.ContextTokens, s.ttlOf(sess), s.margin())
}

// window is the longest plan for a session: MaxHours, capped at the
// break-even of its model. Requires s.mu.
func (s *Service) window(sess model.Session) float64 {
	h := s.settings.MaxHours
	if c := s.costs(sess); c.Known && c.BreakEvenHours > 0 && c.BreakEvenHours < h {
		h = c.BreakEvenHours
	}
	return h
}

// ---- helpers ----

func harnessOf(key string) model.Harness {
	h, _, _ := strings.Cut(key, ":")
	return model.Harness(h)
}

func seconds(v float64) time.Duration { return time.Duration(v * float64(time.Second)) }
func minutes(v float64) time.Duration { return time.Duration(v * float64(time.Minute)) }

// hoursDur converts hours to a duration rounded to the millisecond the
// store keeps, so a stored Until compares equal after a restart.
func hoursDur(h float64) time.Duration {
	return time.Duration(h * float64(time.Hour)).Round(time.Millisecond)
}

func latest(ts ...time.Time) time.Time {
	var m time.Time
	for _, t := range ts {
		if t.After(m) {
			m = t
		}
	}
	return m
}

func earliest(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func newPingID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
