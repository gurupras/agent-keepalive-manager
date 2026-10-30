package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

type fakeSettings struct {
	mu        sync.Mutex
	on        bool
	locked    bool
	by        string
	url       string
	token     string
	setCalled int
	expOff    bool // experiments are on unless set
	expLocked bool
}

func (s *fakeSettings) Enabled() (bool, bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.on, s.locked, s.by
}
func (s *fakeSettings) URL() string   { s.mu.Lock(); defer s.mu.Unlock(); return s.url }
func (s *fakeSettings) Token() string { return s.token }
func (s *fakeSettings) SetEnabled(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locked {
		return apitypes.ErrLocked
	}
	s.on = on
	s.setCalled++
	return nil
}
func (s *fakeSettings) Experiments() (bool, bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.expOff, s.expLocked, s.by
}
func (s *fakeSettings) SetExperiments(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expLocked {
		return apitypes.ErrLocked
	}
	s.expOff = !on
	return nil
}

type fakeStats struct {
	mu   sync.Mutex
	days map[string]model.DayStats
}

func (f *fakeStats) DayStats(day time.Time) (model.DayStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := dayOf(day)
	ds, ok := f.days[d]
	if !ok {
		return model.DayStats{Day: d}, nil
	}
	return ds, nil
}

func activeDay(day string, pings int) model.DayStats {
	return model.DayStats{Day: day, Sessions: map[model.Harness]int{model.Claude: 1}, Pings: model.DayPings{Sent: pings}}
}

// hub records reports and answers with the next queued status.
type hub struct {
	*httptest.Server
	mu       sync.Mutex
	status   []int
	got      []Report
	auth     []string
	blocking atomic.Bool
	block    chan struct{} // closed at cleanup
	entered  chan struct{}
}

func newHub(t *testing.T) *hub {
	h := &hub{block: make(chan struct{}), entered: make(chan struct{}, 16)}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/base/v1/reports" || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "bad request", 404)
			return
		}
		if h.blocking.Load() {
			h.entered <- struct{}{}
			select {
			case <-h.block:
			case <-r.Context().Done():
				return
			}
		}
		var rep Report
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&rep); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		code := http.StatusAccepted
		if len(h.status) > 0 {
			code, h.status = h.status[0], h.status[1:]
		}
		if code < 300 {
			h.got = append(h.got, rep)
		}
		h.auth = append(h.auth, r.Header.Get("Authorization"))
		w.WriteHeader(code)
	}))
	t.Cleanup(h.Close)
	t.Cleanup(func() { close(h.block) })
	return h
}

type rig struct {
	r     *Reporter
	set   *fakeSettings
	stats *fakeStats
	hub   *hub
	now   time.Time
	dir   string
	// announce is the AnnounceDelay; tests that do not exercise it leave
	// it off.
	announce time.Duration
	version  string
}

func newRig(t *testing.T, now time.Time) *rig {
	g := &rig{hub: newHub(t), now: now, dir: t.TempDir(), stats: &fakeStats{days: map[string]model.DayStats{}},
		announce: -1, version: "1.4.0"}
	g.set = &fakeSettings{on: true, url: g.hub.URL + "/base/", token: "tok"}
	g.open()
	return g
}

// open (re)creates the reporter over the same data dir, like a restart.
func (g *rig) open() {
	g.r = New(Options{
		DataDir:       g.dir,
		Build:         Build{Version: g.version, OS: "linux", Arch: "amd64", Profile: "public", InstallKind: "deb"},
		Stats:         g.stats,
		Settings:      g.set,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		SendJitter:    -1,
		RetryMin:      time.Minute,
		AnnounceDelay: g.announce,
		now:           func() time.Time { return g.now },
	})
}

func (g *rig) queued() []string { return g.r.q.days() }

var t0 = time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)

func TestReportSchema(t *testing.T) {
	ds := model.DayStats{
		Day:      "2026-10-26",
		Sessions: map[model.Harness]int{model.Claude: 4, model.Codex: 1, "": 0},
		Plans:    model.DayPlans{Started: 3, Indefinite: 1, Hours: 21.5},
		Pings:    model.DayPings{Sent: 17, Verified: 16, Cold: 1, Failed: 0, CostUSD: 0.84},
		Outcomes: []model.OutcomeSum{
			{Harness: model.Claude, Model: "claude-opus-4-1", Outcome: model.OutcomeSaved, EndedBy: model.EndReturned,
				Count: 1, Pings: 5, PingCostUSD: 0.21, AvoidedUSD: 3.9, NetUSD: 3.69, ContextTokens: 150000, IdleMinutes: 200},
			{Harness: model.Claude, Model: "claude-opus-4-1", Outcome: model.OutcomeSaved, EndedBy: model.EndReturned,
				Count: 1, Pings: 4, PingCostUSD: 0.2, AvoidedUSD: 3.9, NetUSD: 3.7, ContextTokens: 160000, IdleMinutes: 210},
		},
		Experiments: []model.ExperimentSum{
			{Experiment: "busy-ping-subagents-1", Arm: model.ArmPing, Harness: model.Claude, Model: "claude-opus-4-1",
				Episodes: 2, Returned: 2, Warm: 2, Pings: 3, PingCalls: 3, PingCostUSD: 0.3, RewriteUSD: 0.1,
				CostUSD: 0.4, CostSqUSD: 0.08, ContextTokens: 300000, QuietMinutes: 90},
			{Experiment: "busy-ping-subagents-1", Arm: model.ArmHold, Harness: model.Claude, Model: "claude opus",
				Episodes: 1, Returned: 1, RewriteUSD: 2.25, CostUSD: 2.25, CostSqUSD: 5.0625, ContextTokens: 150000,
				QuietMinutes: 40},
		},
	}
	r := BuildReport("8d3c6f0e-2b7a-4f5e-9c1d-3a4b5c6d7e8f", Build{"1.4.0", "linux", "amd64", "public", "deb"}, ds)
	b, _ := json.Marshal(r)
	want := `{"schema":2,"installId":"8d3c6f0e-2b7a-4f5e-9c1d-3a4b5c6d7e8f","day":"2026-10-26","version":"1.4.0","os":"linux","arch":"amd64","profile":"public","installKind":"deb",` +
		`"sessions":{"claude":4,"codex":1},"plans":{"started":3,"indefinite":1,"hours":21.5},"pings":{"sent":17,"verified":16,"cold":1,"failed":0,"costUsd":0.84},` +
		`"outcomes":[{"harness":"claude","model":"claude-opus-4-1","outcome":"saved","endedBy":"returned","count":2,"pings":9,"pingCostUsd":0.41,"avoidedUsd":7.8,"netUsd":7.39,"contextTokens":310000,"idleMinutes":410}],` +
		`"experiments":[{"experiment":"busy-ping-subagents-1","arm":"hold","harness":"claude","model":"claude_opus","episodes":1,"returned":1,"warm":0,"pings":0,"pingCalls":0,"pingCostUsd":0,"rewriteUsd":2.25,"costUsd":2.25,"costSqUsd":5.0625,"contextTokens":150000,"quietMinutes":40},` +
		`{"experiment":"busy-ping-subagents-1","arm":"ping","harness":"claude","model":"claude-opus-4-1","episodes":2,"returned":2,"warm":2,"pings":3,"pingCalls":3,"pingCostUsd":0.3,"rewriteUsd":0.1,"costUsd":0.4,"costSqUsd":0.08,"contextTokens":300000,"quietMinutes":90}]}`
	if string(b) != want {
		t.Fatalf("report\n got %s\nwant %s", b, want)
	}

	// Empty stats still marshal as objects and arrays, never null.
	b, _ = json.Marshal(BuildReport("x", Build{}, model.DayStats{Day: "2026-10-26"}))
	if strings.Contains(string(b), "null") {
		t.Fatalf("nulls: %s", b)
	}
}

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"claude-opus-4-1":        "claude-opus-4-1",
		"claude-opus-4-1[1m]":    "claude-opus-4-1_1m_",
		"gpt-5.1-codex:high/x_y": "gpt-5.1-codex:high/x_y",
		"":                       "unknown",
		"a bé":                   "a_b_",
		strings.Repeat("x", 100): strings.Repeat("x", 64),
		"1.4.0+abc":              "1.4.0_abc",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	r := BuildReport("id", Build{Version: "v 1"}, model.DayStats{
		Day:   "2026-10-26",
		Plans: model.DayPlans{Started: -1, Hours: -2},
		Pings: model.DayPings{CostUSD: -1},
		Outcomes: []model.OutcomeSum{{Model: "m[1m]", Outcome: model.OutcomeWasted, NetUSD: -0.5, PingCostUSD: -3, ContextTokens: -5},
			{Model: "m(1m)", Outcome: model.OutcomeWasted, NetUSD: -0.25}},
	})
	if r.Version != "v_1" || r.Plans.Started != 0 || r.Plans.Hours != 0 || r.Pings.CostUSD != 0 {
		t.Fatalf("clamped: %+v", r)
	}
	if len(r.Outcomes) != 1 || r.Outcomes[0].Model != "m_1m_" || r.Outcomes[0].NetUSD != -0.75 || r.Outcomes[0].PingCostUSD != 0 || r.Outcomes[0].ContextTokens != 0 || r.Outcomes[0].Harness != "unknown" {
		t.Fatalf("outcomes merged after sanitising: %+v", r.Outcomes)
	}
}

func TestInstallID(t *testing.T) {
	g := newRig(t, t0)
	if g.r.Status().InstallID != "" {
		t.Fatal("id made before any report")
	}
	if _, err := os.Stat(filepath.Join(g.dir, installIDFile)); err == nil {
		t.Fatal("id file made eagerly")
	}
	if _, err := g.r.Preview(); err != nil {
		t.Fatal(err)
	}
	id := g.r.Status().InstallID
	if !uuidRe.MatchString(id) {
		t.Fatalf("id %q", id)
	}
	g.open()
	if g.r.Status().InstallID != id {
		t.Fatal("id not kept")
	}
	if err := g.r.ResetID(); err != nil {
		t.Fatal(err)
	}
	if nid := g.r.Status().InstallID; nid == id || !uuidRe.MatchString(nid) {
		t.Fatalf("reset gave %q", nid)
	}
}

func TestFirstRunAndDailySend(t *testing.T) {
	g := newRig(t, t0)
	g.stats.days["2026-10-20"] = activeDay("2026-10-20", 3) // before this install reported: never backfilled
	ctx := context.Background()

	wait := g.r.pass(ctx)
	if len(g.queued()) != 0 || len(g.hub.got) != 0 {
		t.Fatalf("first run backfilled: %v", g.queued())
	}
	if want := 14 * time.Hour; wait != want {
		t.Fatalf("wait %v, want until midnight %v", wait, want)
	}

	// The day ends; its report (no activity, but the server ran) goes out.
	g.now = t0.Add(14*time.Hour + time.Second)
	g.r.pass(ctx)
	if len(g.hub.got) != 1 || g.hub.got[0].Day != "2026-10-27" || g.hub.auth[0] != "Bearer tok" {
		t.Fatalf("sent %+v %v", g.hub.got, g.hub.auth)
	}
	st := g.r.Status()
	if st.Queued != 0 || st.LastSent.IsZero() || st.LastError != "" || !st.Available || !st.Enabled || st.Endpoint != g.set.url {
		t.Fatalf("status %+v", st)
	}
	if g.hub.got[0].InstallID != st.InstallID || g.hub.got[0].InstallKind != "deb" {
		t.Fatalf("report %+v", g.hub.got[0])
	}
}

func TestStartupCatchUp(t *testing.T) {
	g := newRig(t, t0.AddDate(0, 0, -5))
	g.r.pass(context.Background()) // ran on 10-22
	g.hub.status = []int{500}      // the 10-22 send fails
	g.now = t0.AddDate(0, 0, -4)
	g.r.pass(context.Background())
	if q := g.queued(); len(q) != 1 || q[0] != "2026-10-22" {
		t.Fatalf("queue %v", q)
	}

	// Down for days; activity on 10-24 (another process recorded it), none on 10-25.
	g.stats.days["2026-10-24"] = activeDay("2026-10-24", 2)
	g.now = t0
	g.open()
	g.r.pass(context.Background())
	var days []string
	for _, r := range g.hub.got {
		days = append(days, r.Day)
	}
	// 10-23 is reported because the server ran on it (it was "today" at the last pass).
	if got := strings.Join(days, ","); got != "2026-10-22,2026-10-23,2026-10-24" {
		t.Fatalf("sent %s", got)
	}
	if g.hub.got[2].Pings.Sent != 2 {
		t.Fatalf("10-24: %+v", g.hub.got[2])
	}
	// Nothing is sent twice.
	g.open()
	g.r.pass(context.Background())
	if len(g.hub.got) != 3 {
		t.Fatalf("resent: %d", len(g.hub.got))
	}
}

func TestSendOutcomes(t *testing.T) {
	g := newRig(t, t0)
	ctx := context.Background()
	g.r.pass(ctx)
	g.stats.days["2026-10-28"] = activeDay("2026-10-28", 1)
	g.now = t0.AddDate(0, 0, 2) // 10-27 and 10-28 are due

	// 500: kept and retried after the backoff.
	g.hub.status = []int{500}
	wait := g.r.pass(ctx)
	if q := g.queued(); len(q) != 2 {
		t.Fatalf("queue after 500: %v", q)
	}
	if wait < 30*time.Second || wait > 90*time.Second {
		t.Fatalf("retry in %v", wait)
	}
	if st := g.r.Status(); st.Queued != 2 || !strings.Contains(st.LastError, "HTTP 500") {
		t.Fatalf("status %+v", st)
	}
	// Not yet due: nothing sent.
	g.r.pass(ctx)
	if len(g.hub.got) != 0 {
		t.Fatal("retried early")
	}

	// 429 with Retry-After is also retried; then 400 drops the first, 202 sends the second.
	g.now = g.now.Add(2 * time.Minute)
	g.hub.status = []int{400, 202}
	g.r.pass(ctx)
	if q := g.queued(); len(q) != 0 {
		t.Fatalf("queue %v", q)
	}
	if len(g.hub.got) != 1 || g.hub.got[0].Day != "2026-10-28" {
		t.Fatalf("sent %+v", g.hub.got)
	}
	// The error of the dropped report is cleared by the later success.
	if st := g.r.Status(); st.LastError != "" || st.LastSent.IsZero() {
		t.Fatalf("status %+v", st)
	}
}

func TestDropRecordsError(t *testing.T) {
	g := newRig(t, t0)
	g.r.pass(context.Background())
	g.now = t0.AddDate(0, 0, 1)
	g.hub.status = []int{401}
	g.r.pass(context.Background())
	st := g.r.Status()
	if st.Queued != 0 || !strings.Contains(st.LastError, "401") {
		t.Fatalf("status %+v", st)
	}
}

func TestRetryAfter(t *testing.T) {
	g := newRig(t, t0)
	g.r.pass(context.Background())
	g.now = t0.AddDate(0, 0, 1)
	g.hub.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
	})
	wait := g.r.pass(context.Background())
	if wait < time.Hour || len(g.queued()) != 1 {
		t.Fatalf("wait %v queue %v", wait, g.queued())
	}
}

func TestTurnOff(t *testing.T) {
	g := newRig(t, t0)
	g.r.pass(context.Background())
	g.now = t0.AddDate(0, 0, 1)
	g.hub.blocking.Store(true)
	var events []apitypes.MetricsStatus
	var mu sync.Mutex
	cancel := g.r.Subscribe(func(st apitypes.MetricsStatus) { mu.Lock(); events = append(events, st); mu.Unlock() })
	defer cancel()

	done := make(chan struct{})
	go func() { g.r.pass(context.Background()); close(done) }()
	select {
	case <-g.hub.entered: // a send is in flight
	case <-time.After(20 * time.Second):
		t.Fatal("no send")
	}
	if err := g.r.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("send not stopped")
	}
	if _, err := os.Stat(filepath.Join(g.dir, queueDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("queue kept: %v", err)
	}
	st := g.r.Status()
	if st.Enabled || st.Queued != 0 || g.set.setCalled != 1 {
		t.Fatalf("status %+v", st)
	}
	mu.Lock()
	n := len(events)
	mu.Unlock()
	if n == 0 {
		t.Fatal("no change notification")
	}

	// While off, nothing is built; turning on again does not report the days spent off.
	g.hub.blocking.Store(false)
	g.now = t0.AddDate(0, 0, 3)
	g.r.pass(context.Background())
	if len(g.queued()) != 0 {
		t.Fatal("built while off")
	}
	g.r.SetEnabled(true)
	g.r.pass(context.Background())
	if len(g.hub.got) != 0 || len(g.queued()) != 0 {
		t.Fatalf("days spent off reported: %+v %v", g.hub.got, g.queued())
	}
	g.now = g.now.AddDate(0, 0, 1)
	g.r.pass(context.Background())
	if len(g.hub.got) != 1 || g.hub.got[0].Day != "2026-10-30" {
		t.Fatalf("after re-enable: %+v", g.hub.got)
	}
}

func TestLockedAndUnavailable(t *testing.T) {
	g := newRig(t, t0)
	g.set.locked, g.set.by = true, "admin"
	if err := g.r.SetEnabled(false); !errors.Is(err, apitypes.ErrLocked) {
		t.Fatalf("locked: %v", err)
	}
	if err := g.r.SetEnabled(true); err != nil {
		t.Fatalf("no-op on a locked setting: %v", err)
	}
	if st := g.r.Status(); !st.Locked || st.LockedBy != "admin" {
		t.Fatalf("status %+v", st)
	}

	g = newRig(t, t0)
	g.set.url = ""
	if st := g.r.Status(); st.Available {
		t.Fatalf("available without a hub: %+v", st)
	}
	if _, err := g.r.Preview(); !errors.Is(err, apitypes.ErrUnavailable) {
		t.Fatalf("preview: %v", err)
	}
	g.r.pass(context.Background())
	g.now = t0.AddDate(0, 0, 2)
	g.r.pass(context.Background())
	if len(g.queued()) != 0 {
		t.Fatal("queued without a hub")
	}
	if _, err := os.Stat(filepath.Join(g.dir, installIDFile)); err == nil {
		t.Fatal("install id made without a hub")
	}
}

func TestPreview(t *testing.T) {
	g := newRig(t, t0)
	g.stats.days["2026-10-27"] = activeDay("2026-10-27", 7)
	p, err := g.r.Preview()
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := p.(Report); !ok || r.Day != "2026-10-27" || r.Pings.Sent != 7 {
		t.Fatalf("today's preview %#v", p)
	}
	g.r.pass(context.Background())
	g.hub.status = []int{503}
	g.now = t0.AddDate(0, 0, 1)
	g.r.pass(context.Background())
	p, _ = g.r.Preview()
	raw, ok := p.(json.RawMessage)
	q, _ := g.r.q.read("2026-10-27")
	if !ok || string(raw) != string(q) {
		t.Fatalf("queued preview %s", p)
	}
}

func TestRunStops(t *testing.T) {
	g := newRig(t, t0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { g.r.Run(ctx); close(done) }()
	g.r.wake()
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestExperimentsOptOut(t *testing.T) {
	g := newRig(t, t0)
	day := t0.Truncate(24 * time.Hour)
	g.stats.mu.Lock()
	g.stats.days[dayOf(day)] = model.DayStats{Experiments: []model.ExperimentSum{{Experiment: "x", Arm: model.ArmHold, Episodes: 1}}}
	g.stats.mu.Unlock()
	r, err := g.r.build(day)
	if err != nil || len(r.Experiments) != 1 {
		t.Fatalf("enrolled: %+v %v", r.Experiments, err)
	}
	if err := g.r.SetExperiments(false); err != nil {
		t.Fatal(err)
	}
	if st := g.r.Status(); st.Experiments {
		t.Fatalf("status %+v", st)
	}
	if r, _ := g.r.build(day); len(r.Experiments) != 0 {
		t.Fatalf("opted out but reported: %+v", r.Experiments)
	}
	g.set.expLocked = true
	if err := g.r.SetExperiments(true); !errors.Is(err, apitypes.ErrLocked) {
		t.Fatalf("locked: %v", err)
	}
}

// A report queued before the opt-out goes out without its experiments, as
// schema 1.
func TestExperimentsOptOutQueued(t *testing.T) {
	g := newRig(t, t0)
	g.stats.mu.Lock()
	g.stats.days[dayOf(t0)] = model.DayStats{Experiments: []model.ExperimentSum{{Experiment: "x", Arm: model.ArmHold,
		Harness: model.Claude, Episodes: 1}}}
	g.stats.mu.Unlock()
	ctx := context.Background()
	g.r.pass(ctx)
	g.hub.status = []int{500}
	g.now = t0.AddDate(0, 0, 1)
	g.r.pass(ctx)
	if len(g.queued()) != 1 {
		t.Fatalf("queue %v", g.queued())
	}
	if err := g.r.SetExperiments(false); err != nil {
		t.Fatal(err)
	}
	if p, err := g.r.Preview(); err != nil || strings.Contains(string(p.(json.RawMessage)), "experiments") {
		t.Fatalf("preview %s %v", p, err)
	}
	g.now = g.now.Add(2 * time.Hour)
	g.r.pass(ctx)
	if len(g.hub.got) != 1 || g.hub.got[0].Schema != 1 || g.hub.got[0].Experiments != nil {
		t.Fatalf("sent %+v", g.hub.got)
	}
}

func TestReportWithoutExperimentsIsSchema1(t *testing.T) {
	r := BuildReport("id", Build{"1.4.0", "linux", "amd64", "public", "deb"}, model.DayStats{Day: "2026-10-26"})
	b, _ := json.Marshal(r)
	if r.Schema != 1 || strings.Contains(string(b), "experiments") {
		t.Fatalf("report %s", b)
	}
}

// A new install, and each new version, reports today so far within
// AnnounceDelay of start-up; the day's full report replaces it later.
func TestAnnounce(t *testing.T) {
	g := newRig(t, t0)
	g.announce = 10 * time.Minute
	g.open()
	g.stats.mu.Lock()
	g.stats.days[dayOf(t0)] = activeDay(dayOf(t0), 1)
	g.stats.mu.Unlock()
	ctx := context.Background()

	if wait := g.r.pass(ctx); wait != 10*time.Minute || len(g.hub.got) != 0 {
		t.Fatalf("before the delay: wait %v, sent %d", wait, len(g.hub.got))
	}
	g.now = t0.Add(10 * time.Minute)
	g.r.pass(ctx)
	if len(g.hub.got) != 1 || g.hub.got[0].Day != dayOf(t0) || g.hub.got[0].Version != "1.4.0" {
		t.Fatalf("announce %+v", g.hub.got)
	}
	// Once per version, restarts included.
	g.open()
	g.now = t0.Add(30 * time.Minute)
	if wait := g.r.pass(ctx); len(g.hub.got) != 1 || wait < time.Hour {
		t.Fatalf("announced again: %d, wait %v", len(g.hub.got), wait)
	}
	// The day ends: its full report goes out, the same day again.
	g.now = t0.Add(14*time.Hour + time.Second)
	g.r.pass(ctx)
	if len(g.hub.got) != 2 || g.hub.got[1].Day != dayOf(t0) {
		t.Fatalf("day report %+v", g.hub.got)
	}
	// An update announces itself.
	g.version = "1.5.0"
	g.open()
	g.now = g.now.Add(10 * time.Minute)
	g.r.pass(ctx)
	if len(g.hub.got) != 3 || g.hub.got[2].Version != "1.5.0" {
		t.Fatalf("update %+v", g.hub.got)
	}
}

// Turning reporting off before the delay is up sends nothing.
func TestAnnounceOptOut(t *testing.T) {
	g := newRig(t, t0)
	g.announce = 10 * time.Minute
	g.open()
	ctx := context.Background()
	g.r.pass(ctx)
	if err := g.r.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	g.now = t0.Add(time.Hour)
	g.r.pass(ctx)
	if len(g.hub.got) != 0 || len(g.queued()) != 0 {
		t.Fatalf("sent %+v, queued %v", g.hub.got, g.queued())
	}
}
