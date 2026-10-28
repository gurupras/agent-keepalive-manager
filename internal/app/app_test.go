package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
	"github.com/gurupras/agent-keepalive-manager/internal/distro"
	"github.com/gurupras/agent-keepalive-manager/internal/instance"
	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// fakeService answers the few calls the tests make.
type fakeService struct {
	model.Service // unimplemented methods panic
	name          string
}

func (f *fakeService) Sessions() []model.SessionView {
	return []model.SessionView{{Session: model.Session{Key: "claude:" + f.name}}}
}

func (f *fakeService) Subscribe() (<-chan model.Event, func()) {
	return make(chan model.Event), func() {}
}

type world struct {
	lockDir string
	dirs    instance.AgentDirs
}

func newWorld(t *testing.T) world {
	base := t.TempDir()
	return world{
		lockDir: filepath.Join(base, "locks"),
		dirs:    instance.AgentDirs{Claude: filepath.Join(base, ".claude"), Codex: filepath.Join(base, ".codex")},
	}
}

type running struct {
	ready   Ready
	dataDir string
	done    chan struct{} // closed when Serve returned
	err     error
	cancel  context.CancelFunc
	mu      sync.Mutex
	built   int
	ran     int
	closed  int
	restart apitypes.RestartFunc
}

func (w world) start(t *testing.T, name string, mod func(*Config)) *running {
	t.Helper()
	r := &running{dataDir: filepath.Join(t.TempDir(), name), done: make(chan struct{})}
	readyCh := make(chan Ready, 1)
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	p := distro.Profile{Name: "test", InstallKind: "dev"}
	cfg := Config{
		Version: "1.0.0", DataDir: r.dataDir, UI: fstest.MapFS{}, Profile: &p,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		LockDir: w.lockDir, AgentDirs: &w.dirs, AdminConfig: "-",
		RetryLock: 50 * time.Millisecond,
		Ready:     func(rd Ready) { readyCh <- rd },
		Build: func(ctx context.Context, d CoreDeps) (*Core, error) {
			r.mu.Lock()
			r.built++
			r.restart = d.Restart
			r.mu.Unlock()
			return &Core{
				Service: &fakeService{name: name},
				Run: []func(context.Context){func(ctx context.Context) {
					r.mu.Lock()
					r.ran++
					r.mu.Unlock()
					<-ctx.Done()
				}},
				Close: func() { r.mu.Lock(); r.closed++; r.mu.Unlock() },
			}, nil
		},
	}
	if mod != nil {
		mod(&cfg)
	}
	go func() { r.err = Serve(ctx, cfg); close(r.done) }()
	select {
	case r.ready = <-readyCh:
	case <-r.done:
		t.Fatalf("Serve: %v", r.err)
	case <-time.After(5 * time.Second):
		t.Fatal("not ready")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
		}
	})
	return r
}

func (r *running) counts() (built, ran, closed int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.built, r.ran, r.closed
}

func (r *running) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-r.done:
		return r.err
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return")
		return nil
	}
}

func get(t *testing.T, url, token string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("X-Keepalive-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func post(t *testing.T, url, token string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", url, nil)
	req.Header.Set("X-Keepalive-Token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestServeAndQuit(t *testing.T) {
	w := newWorld(t)
	r := w.start(t, "a", nil)
	if r.ready.Proxy {
		t.Fatal("first server must run the scheduler")
	}
	if p, err := instance.ReadPortFile(r.dataDir); err != nil || p != r.ready.Port {
		t.Fatalf("port file %d %v", p, err)
	}
	token, err := instance.ReadToken(r.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	code, body := get(t, r.ready.URL, "")
	if code != 200 || !strings.Contains(body, token) {
		t.Fatalf("UI: %d %s", code, body)
	}
	code, body = get(t, r.ready.URL+"api/info", token)
	var info apitypes.Info
	if code != 200 || json.Unmarshal([]byte(body), &info) != nil || info.DataDir != r.dataDir || info.Profile != "test" {
		t.Fatalf("info: %d %s", code, body)
	}
	if built, ran, _ := r.counts(); built != 1 || ran != 1 {
		t.Fatalf("built %d ran %d", built, ran)
	}

	// The same data directory cannot be served twice.
	err = Serve(context.Background(), Config{DataDir: r.dataDir, LockDir: w.lockDir, AgentDirs: &w.dirs, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	var already *AlreadyRunningError
	if !errors.As(err, &already) || already.URL != r.ready.URL {
		t.Fatalf("second Serve on same dir: %v", err)
	}

	if c := post(t, r.ready.URL+"api/quit", token); c != 202 {
		t.Fatalf("quit: %d", c)
	}
	if err := r.wait(t); err != nil {
		t.Fatal(err)
	}
	if _, _, closed := r.counts(); closed != 1 {
		t.Fatal("core not closed")
	}
	if _, err := instance.ReadPortFile(r.dataDir); err == nil {
		t.Fatal("port file left behind")
	}
}

func TestSupervisedRefusesQuit(t *testing.T) {
	w := newWorld(t)
	r := w.start(t, "a", func(c *Config) { c.Supervised = true })
	token, _ := instance.ReadToken(r.dataDir)
	if c := post(t, r.ready.URL+"api/quit", token); c != 409 {
		t.Fatalf("quit: %d", c)
	}
	r.cancel()
	if err := r.wait(t); err != nil {
		t.Fatal(err)
	}
}

func TestSecondServerProxiesThenTakesOver(t *testing.T) {
	w := newWorld(t)
	a := w.start(t, "a", nil)
	b := w.start(t, "b", nil)
	if !b.ready.Proxy {
		t.Fatal("second server should proxy")
	}
	if built, _, _ := b.counts(); built != 0 {
		t.Fatal("proxying server built a scheduler")
	}
	tokB, _ := instance.ReadToken(b.dataDir)
	code, body := get(t, b.ready.URL+"api/sessions", tokB)
	if code != 200 || !strings.Contains(body, "claude:a") {
		t.Fatalf("proxied sessions: %d %s", code, body)
	}

	a.cancel()
	if err := a.wait(t); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, body = get(t, b.ready.URL+"api/sessions", tokB)
		if code == 200 && strings.Contains(body, "claude:b") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no takeover: %d %s", code, body)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if built, ran, _ := b.counts(); built != 1 || ran != 1 {
		t.Fatalf("takeover built %d ran %d", built, ran)
	}
}

func TestRestart(t *testing.T) {
	w := newWorld(t)
	r := w.start(t, "a", nil)
	r.mu.Lock()
	restart := r.restart
	r.mu.Unlock()
	restart("/new/agent-keepalive")
	err := r.wait(t)
	var re *RestartError
	if !errors.As(err, &re) || re.Exe != "/new/agent-keepalive" {
		t.Fatalf("got %v", err)
	}
	// Everything was released: a successor can take the lock at once.
	l, err := instance.AcquireSchedulerLock(w.lockDir, w.dirs)
	if err != nil {
		t.Fatalf("lock after restart: %v", err)
	}
	l.Release()
}

func TestBuildFailureReleasesLock(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(t.TempDir(), "x")
	err := Serve(context.Background(), Config{
		DataDir: dir, LockDir: w.lockDir, AgentDirs: &w.dirs, AdminConfig: "-",
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Build: func(context.Context, CoreDeps) (*Core, error) { return nil, errors.New("boom") },
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatal(err)
	}
	l, err := instance.AcquireSchedulerLock(w.lockDir, w.dirs)
	if err != nil {
		t.Fatalf("lock leaked: %v", err)
	}
	l.Release()
}

func TestAddonMustBeDir(t *testing.T) {
	w := newWorld(t)
	err := Serve(context.Background(), Config{
		DataDir: t.TempDir(), LockDir: w.lockDir, AgentDirs: &w.dirs, AdminConfig: "-",
		UIAddon: filepath.Join(t.TempDir(), "missing"), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err == nil || !strings.Contains(err.Error(), "--ui-addon") {
		t.Fatal(err)
	}
}

func TestFrameAncestors(t *testing.T) {
	w := newWorld(t)
	err := Serve(context.Background(), Config{
		DataDir: t.TempDir(), LockDir: w.lockDir, AgentDirs: &w.dirs, AdminConfig: "-",
		FrameAncestors: "'self'; script-src *", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err == nil || !strings.Contains(err.Error(), "--frame-ancestors") {
		t.Fatal(err)
	}
	r := w.start(t, "a", func(c *Config) { c.FrameAncestors = "https://host.example" })
	resp, err := http.Get(r.ready.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.HasSuffix(csp, "frame-ancestors https://host.example") || resp.Header.Get("X-Frame-Options") != "" {
		t.Fatalf("%q %v", csp, resp.Header)
	}
}
