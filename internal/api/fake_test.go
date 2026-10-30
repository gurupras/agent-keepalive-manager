package api

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// fakeService is an in-memory model.Service.
type fakeService struct {
	mu       sync.Mutex
	sessions map[string]model.SessionView
	plans    map[string]model.Plan
	settings model.Settings
	subs     map[chan model.Event]struct{}
	lastFrom time.Time
	since    time.Time
	calls    []string
}

var _ model.Service = (*fakeService)(nil)

func newFakeService(keys ...string) *fakeService {
	f := &fakeService{
		sessions: map[string]model.SessionView{},
		plans:    map[string]model.Plan{},
		settings: model.DefaultSettings(),
		subs:     map[chan model.Event]struct{}{},
	}
	for _, k := range keys {
		f.sessions[k] = model.SessionView{Session: model.Session{Key: k, Harness: model.Claude, Reach: model.ReachSocket}}
	}
	return f
}

func (f *fakeService) call(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fakeService) Sessions() []model.SessionView {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.SessionView
	for _, v := range f.sessions {
		out = append(out, v)
	}
	return out
}

func (f *fakeService) Session(key string) (model.SessionView, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.sessions[key]
	return v, ok
}

func (f *fakeService) startPlan(key string, hours float64, indefinite bool) (model.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[key]
	if !ok {
		return model.Plan{}, model.ErrNotFound
	}
	if s.Reach == model.ReachNone {
		return model.Plan{}, fmt.Errorf("%w: %s", model.ErrUnreachable, s.ReachWhy)
	}
	if _, ok := f.plans[key]; ok {
		return model.Plan{}, fmt.Errorf("%w: a plan already exists", model.ErrConflict)
	}
	p := model.Plan{ID: 1, Key: key, Hours: hours, Indefinite: indefinite, State: model.PlanActive}
	f.plans[key] = p
	return p, nil
}

func (f *fakeService) StartPlan(_ context.Context, key string, hours float64) (model.Plan, error) {
	f.call("start")
	return f.startPlan(key, hours, false)
}

func (f *fakeService) KeepIndefinitely(_ context.Context, key string) (model.Plan, error) {
	f.call("indefinite")
	return f.startPlan(key, 0, true)
}

func (f *fakeService) plan(key string) (model.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sessions[key]; !ok {
		return model.Plan{}, model.ErrNotFound
	}
	p, ok := f.plans[key]
	if !ok {
		return model.Plan{}, fmt.Errorf("%w: no plan", model.ErrNotFound)
	}
	return p, nil
}

func (f *fakeService) ExtendPlan(_ context.Context, key string, add float64) (model.Plan, error) {
	f.call("extend")
	p, err := f.plan(key)
	p.Hours += add
	return p, err
}

func (f *fakeService) ResumePlan(_ context.Context, key string) (model.Plan, error) {
	f.call("resume")
	return f.plan(key)
}

func (f *fakeService) CancelPlan(_ context.Context, key string) error {
	f.call("cancel")
	_, err := f.plan(key)
	return err
}

func (f *fakeService) PingNow(_ context.Context, key string) (model.PingRecord, error) {
	f.call("ping")
	if _, ok := f.Session(key); !ok {
		return model.PingRecord{}, model.ErrNotFound
	}
	if key == "claude:changed" {
		return model.PingRecord{}, model.ErrSessionChanged
	}
	return model.PingRecord{ID: "p1", Key: key, State: model.PingDelivered}, nil
}

func (f *fakeService) Ledger(from, to time.Time) ([]model.LedgerEntry, model.Totals, error) {
	f.mu.Lock()
	f.lastFrom = from
	f.mu.Unlock()
	return nil, model.Totals{Saved: 1}, nil
}

func (f *fakeService) Pings(key string, limit int) ([]model.PingRecord, error) {
	return nil, nil
}

func (f *fakeService) Missed(since time.Time) model.Missed {
	f.mu.Lock()
	f.since = since
	f.mu.Unlock()
	return model.Missed{Since: since}
}

func (f *fakeService) Settings() model.Settings {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings
}

func (f *fakeService) UpdateSettings(_ context.Context, s model.Settings) (model.Settings, error) {
	f.mu.Lock()
	f.settings = s
	f.mu.Unlock()
	f.publish(model.Event{Type: "settings", Data: s})
	return s, nil
}

func (f *fakeService) Subscribe() (<-chan model.Event, func()) {
	ch := make(chan model.Event, 16)
	f.mu.Lock()
	f.subs[ch] = struct{}{}
	f.mu.Unlock()
	return ch, func() {
		f.mu.Lock()
		delete(f.subs, ch)
		f.mu.Unlock()
	}
}

func (f *fakeService) publish(ev model.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (f *fakeService) subscribers() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subs)
}

// fakeMetrics implements apitypes.Metrics.
type fakeMetrics struct {
	mu     sync.Mutex
	st     apitypes.MetricsStatus
	resets int
}

func (m *fakeMetrics) Status() apitypes.MetricsStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}

func (m *fakeMetrics) SetEnabled(on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.Locked {
		return fmt.Errorf("metrics.enabled: %w", apitypes.ErrLocked)
	}
	m.st.Enabled = on
	return nil
}

func (m *fakeMetrics) SetExperiments(on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.ExperimentsLocked {
		return fmt.Errorf("experiments.enabled: %w", apitypes.ErrLocked)
	}
	m.st.Experiments = on
	return nil
}

func (m *fakeMetrics) Preview() (any, error) { return map[string]any{"schema": 1}, nil }

func (m *fakeMetrics) ResetID() error { m.resets++; return nil }

// fakeUpdater implements apitypes.Updater.
type fakeUpdater struct {
	mu   sync.Mutex
	st   apitypes.UpdateStatus
	subs []func(apitypes.UpdateStatus)
	err  error
}

func (u *fakeUpdater) Status() apitypes.UpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

func (u *fakeUpdater) Check(context.Context) error {
	u.mu.Lock()
	u.st.State = apitypes.UpdateAvailable
	u.st.Latest = &apitypes.Latest{Version: "9.9.9"}
	st, subs := u.st, append([]func(apitypes.UpdateStatus){}, u.subs...)
	u.mu.Unlock()
	for _, fn := range subs {
		fn(st)
	}
	return u.err
}

func (u *fakeUpdater) Apply(context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.st.Action != apitypes.ActionApply {
		return apitypes.ErrUnavailable
	}
	u.st.State = apitypes.UpdateDownloading
	return nil
}

func (u *fakeUpdater) Subscribe(fn func(apitypes.UpdateStatus)) func() {
	u.mu.Lock()
	u.subs = append(u.subs, fn)
	u.mu.Unlock()
	return func() {}
}
