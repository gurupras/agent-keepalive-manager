package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/pricing"
	"github.com/gurupras/agent-keepalive-manager/internal/store"
)

var (
	bg = context.Background()
	t0 = time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
)

const (
	opus = "claude-opus-5-5"
	ctxK = 100_000
	// The cost of one warm ping reply on Opus 5.5 with 100k context:
	// (10 input x 4 + 5 output x 20 + 100,000 read x 0.2) / 1e6.
	warmPingCost = (10*4 + 5*20 + ctxK*0.2) / 1e6
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func warmUsage(ctx int64) *model.Usage {
	return &model.Usage{Input: 10, Output: 5, CacheRead: ctx}
}

func coldUsage(ctx int64) *model.Usage {
	return &model.Usage{Input: 10, Output: 5, CacheWrite1h: ctx}
}

// ---- fake clock ----

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// ---- fake source ----

// fakeSource serves sessions and their activity. Like a real source it
// derives LastCall (request time of the last API call of any kind),
// ContextTokens and LastHuman from the activity it has.
type fakeSource struct {
	h        model.Harness
	mu       sync.Mutex
	sessions map[string]*model.Session
	acts     map[string][]model.Activity
	err      error
}

func newFakeSource(h model.Harness) *fakeSource {
	return &fakeSource{h: h, sessions: map[string]*model.Session{}, acts: map[string][]model.Activity{}}
}

func (f *fakeSource) Harness() model.Harness { return f.h }

func (f *fakeSource) Scan(context.Context) ([]model.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	var out []model.Session
	for _, s := range f.sessions {
		c := *s
		for _, a := range f.acts[c.Key] {
			switch {
			case (a.Kind == model.ActAssistant || a.Kind == model.ActPingReply) && a.Usage != nil:
				if !a.Anchor().Before(c.LastCall) {
					c.LastCall, c.ContextTokens = a.Anchor(), a.Usage.Context()
				}
			case a.Kind == model.ActHuman:
				c.LastHuman = latest(c.LastHuman, a.At)
			}
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (f *fakeSource) Activity(key string, after int64) []model.Activity {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Activity
	for _, a := range f.acts[key] {
		if a.Seq > after {
			out = append(out, a)
		}
	}
	return out
}

func (f *fakeSource) add(key string, as ...model.Activity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range as {
		a.Seq = int64(len(f.acts[key]) + 1)
		f.acts[key] = append(f.acts[key], a)
	}
}

func (f *fakeSource) put(s model.Session) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[s.Key] = &s
}

func (f *fakeSource) edit(key string, fn func(*model.Session)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.sessions[key])
}

func (f *fakeSource) remove(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, key)
}

// clone copies sessions and activity, as a new process would read them.
func (f *fakeSource) clone() *fakeSource {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := newFakeSource(f.h)
	for k, s := range f.sessions {
		v := *s
		c.sessions[k] = &v
	}
	for k, as := range f.acts {
		c.acts[k] = append([]model.Activity(nil), as...)
	}
	return c
}

// ---- fake deliverer ----

type delivery struct {
	key, id, text string
	sess          model.Session
}

type fakeDeliverer struct {
	h       model.Harness
	mu      sync.Mutex
	sent    []delivery
	err     error
	block   chan struct{} // when set, Ping waits for it (or ctx)
	entered chan string   // when set, receives the key on entry
	hook    func(s model.Session, id string)
}

func (d *fakeDeliverer) Harness() model.Harness { return d.h }

func (d *fakeDeliverer) Ping(ctx context.Context, s model.Session, id, text string) error {
	d.mu.Lock()
	hook, block, entered, err := d.hook, d.block, d.entered, d.err
	d.mu.Unlock()
	if hook != nil {
		hook(s, id)
	}
	if entered != nil {
		entered <- s.Key
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil {
		return err
	}
	d.sent = append(d.sent, delivery{key: s.Key, id: id, text: text, sess: s})
	return nil
}

func (d *fakeDeliverer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.sent)
}

func (d *fakeDeliverer) last() delivery {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sent[len(d.sent)-1]
}

func (d *fakeDeliverer) setErr(err error) {
	d.mu.Lock()
	d.err = err
	d.mu.Unlock()
}

type sweepCall struct {
	live   []string
	maxAge time.Duration
}

// fakeWithdrawer is a deliverer whose pings wait in a queue (Codex).
type fakeWithdrawer struct {
	fakeDeliverer
	sweeps []sweepCall
}

func (w *fakeWithdrawer) Sweep(_ context.Context, live []model.Session, maxAge time.Duration) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var keys []string
	for _, s := range live {
		keys = append(keys, s.Key)
	}
	w.sweeps = append(w.sweeps, sweepCall{keys, maxAge})
	return 1, nil
}

func (w *fakeWithdrawer) sweepCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.sweeps)
}

// ---- harness ----

type harness struct {
	t     *testing.T
	clock *fakeClock
	src   *fakeSource
	codex *fakeSource
	del   *fakeDeliverer
	cdel  *fakeWithdrawer
	path  string
	st    *store.Store
	svc   *Service
	mu    sync.Mutex
	dead  map[int]bool
	exp   bool // the install takes part in experiments
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t:     t,
		clock: &fakeClock{t: t0},
		src:   newFakeSource(model.Claude),
		codex: newFakeSource(model.Codex),
		del:   &fakeDeliverer{h: model.Claude},
		cdel:  &fakeWithdrawer{fakeDeliverer: fakeDeliverer{h: model.Codex}},
		path:  filepath.Join(t.TempDir(), "state.db"),
		dead:  map[int]bool{},
	}
	h.openService()
	return h
}

func (h *harness) alive(pid int, _ string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.dead[pid]
}

func (h *harness) enrolled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.exp
}

func (h *harness) kill(pid int) {
	h.mu.Lock()
	h.dead[pid] = true
	h.mu.Unlock()
}

// openService (re)creates the store and service on the same database, as
// a restart does.
func (h *harness) openService() {
	h.t.Helper()
	if h.st != nil {
		h.svc.deliveries.Wait()
		h.st.Close()
	}
	st, err := store.Open(h.path)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { st.Close() })
	svc, err := New(Config{
		Store: st, Sources: []model.Source{h.src, h.codex},
		Deliverers: []model.Deliverer{h.del, h.cdel},
		Pricing:    pricing.Default(), Now: h.clock.Now, Alive: h.alive,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Experiments: h.enrolled,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.st, h.svc = st, svc
}

// restart simulates a new process: the sources are read afresh.
func (h *harness) restart() {
	h.src, h.codex = h.src.clone(), h.codex.clone()
	h.openService()
}

func (h *harness) at(d time.Duration) { h.clock.Set(t0.Add(d)) }

// tick runs one scan and waits for the deliveries it started.
func (h *harness) tick() {
	h.svc.tick(bg)
	h.svc.deliveries.Wait()
}

// tickAt sets the clock to t0+d and ticks.
func (h *harness) tickAt(d time.Duration) {
	h.at(d)
	h.tick()
}

const key = "claude:s1"

// session is an idle, reachable Opus 5.5 session with a 1h TTL whose last
// call went out at t0 with 100k context.
func session(k string, pid int) model.Session {
	return model.Session{
		Key: k, Harness: model.Claude, ID: k[len("claude:"):], PID: pid, ProcStart: "start", Title: "t",
		Project: "/p", Model: opus, Status: model.StatusIdle, Reach: model.ReachSocket,
		CacheTTL: model.Duration(time.Hour),
	}
}

// addSession registers a session whose last real call was requested at
// t0 (and recorded 30 s later).
func (h *harness) addSession(k string, pid int) {
	h.src.put(session(k, pid))
	h.src.add(k,
		model.Activity{Kind: model.ActHuman, At: t0.Add(-time.Second)},
		model.Activity{Kind: model.ActAssistant, Model: opus, Sent: t0, At: t0.Add(30 * time.Second), Usage: warmUsage(ctxK)},
	)
}

// reply makes the session consume ping id now: its marker appears and its
// reply call (request at now) is recorded a few seconds later.
func (h *harness) reply(k, id string, u *model.Usage) {
	now := h.clock.Now()
	h.src.add(k,
		model.Activity{Kind: model.ActPing, At: now, PingID: id},
		model.Activity{Kind: model.ActPingReply, Model: opus, Sent: now, At: now.Add(3 * time.Second), PingID: id, Usage: u},
	)
}

// returnAt makes the user come back at now: a prompt, and the call that
// answers it reading cacheRead tokens from the cache.
func (h *harness) returnNow(k string, cacheRead int64) {
	now := h.clock.Now()
	h.src.add(k,
		model.Activity{Kind: model.ActHuman, At: now},
		model.Activity{Kind: model.ActAssistant, Model: opus, Sent: now, At: now.Add(20 * time.Second),
			Usage: &model.Usage{Input: 50, Output: 300, CacheRead: cacheRead, CacheWrite1h: ctxK - cacheRead}},
	)
}

func (h *harness) plan(k string) *model.Plan {
	v, ok := h.svc.Session(k)
	if !ok {
		return nil
	}
	return v.Plan
}

func (h *harness) ledger() []model.LedgerEntry {
	h.t.Helper()
	es, _, err := h.svc.Ledger(time.Time{}, time.Time{})
	if err != nil {
		h.t.Fatal(err)
	}
	return es
}

func (h *harness) ping(id string) store.Ping {
	h.t.Helper()
	p, err := h.st.GetPing(bg, id)
	if err != nil {
		h.t.Fatalf("ping %s: %v", id, err)
	}
	return p
}

func (h *harness) storedPlan(id int64) store.Plan {
	h.t.Helper()
	p, err := h.st.GetPlan(bg, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return p
}

// must(t)(plan, err) fails the test on err.
func must(t *testing.T) func(model.Plan, error) model.Plan {
	return func(p model.Plan, err error) model.Plan {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
}

var errBoom = errors.New("boom")
