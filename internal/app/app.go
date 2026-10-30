// Package app wires the local server together: Serve resolves the data
// directory, takes the scheduler lock, builds the scheduler, usage
// reporting and the updater, serves the API and the UI, and shuts all of it
// down in order.
//
// A server that cannot take the scheduler lock (another server runs the
// scheduler for the same agent config directories, architecture.md rule
// 11) serves its own UI and proxies the API to that server. It keeps trying
// the lock, and takes over the scheduler when the other server goes away.
//
// The concrete parts (store, pricing, sources, deliverers, scheduler,
// telemetry, update) are built in wire.go; everything else here works
// against interfaces so it can be tested with fakes.
package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/api"
	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
	"github.com/gurupras/agent-keepalive-manager/internal/config"
	"github.com/gurupras/agent-keepalive-manager/internal/distro"
	"github.com/gurupras/agent-keepalive-manager/internal/instance"
	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// Config configures Serve.
type Config struct {
	Version string
	// DataDir is the resolved data directory (instance.DataDir).
	DataDir string
	// Port is taken exactly when non-zero; 0 selects one (instance.Listen).
	Port int
	// PricingPath is a pricing.json override; "" means <data dir>/pricing.json.
	PricingPath string
	// UI is the built web app; UIAddon a directory for --ui-addon ("" none).
	UI      fs.FS
	UIAddon string
	// FrameAncestors is the CSP frame-ancestors source list ("" = 'self'),
	// for embedders that frame the UI from another origin.
	FrameAncestors string
	// Supervised: another program runs this server (no self-update, no
	// api/quit). NoUpdate turns self-update off alone.
	Supervised bool
	NoUpdate   bool
	// IdleExit stops the server once it has gone this long unused with no
	// active plan (0: never; ignored when Supervised). Use is any request
	// but api/health and a passive WebSocket (api/ws?passive=1); an open
	// WebSocket is not use in itself.
	IdleExit time.Duration
	// IdleCheck is how often IdleExit is checked (1 min).
	IdleCheck time.Duration
	// Profile defaults to distro.Current(); InstallKind to the detected one.
	Profile     *distro.Profile
	InstallKind string
	Log         *slog.Logger
	// Ready is called once the server accepts connections.
	Ready func(Ready)

	// LockDir and AgentDirs locate the scheduler lock (defaults:
	// instance.LockDir(), instance.CurrentAgentDirs()).
	LockDir   string
	AgentDirs *instance.AgentDirs
	// AdminConfig overrides the admin config path ("-" none), for tests.
	AdminConfig string
	// Build makes the core; defaults to the real one in wire.go.
	Build BuildFunc
	// RetryLock is how often a proxying server retries the lock (5 s).
	RetryLock time.Duration
	// WaitPredecessor, when set, is called before the lock is first tried:
	// a server started by a self-update waits there until the old process
	// has let go (free reports whether the lock could be taken).
	WaitPredecessor func(ctx context.Context, free func() bool) bool
}

// Ready describes a server that has started.
type Ready struct {
	Port int
	URL  string
	// Proxy is true when another server runs the scheduler and this one
	// forwards the API to it.
	Proxy bool
}

// Core is what a server that holds the scheduler lock runs.
type Core struct {
	Service model.Service
	Updater apitypes.Updater // nil: updates disabled
	Metrics apitypes.Metrics // nil: no usage reporting
	// Run holds the background loops; each returns when ctx ends.
	Run []func(ctx context.Context)
	// Close releases what the core holds (store, sockets), after Run returned.
	Close func()
}

// CoreDeps is what Build gets to make a Core.
type CoreDeps struct {
	DataDir     string
	PricingPath string
	Config      *config.Config
	Info        apitypes.Info
	NoUpdate    bool // --no-update or supervised
	Restart     apitypes.RestartFunc
	Quit        func()
	Log         *slog.Logger
}

// BuildFunc makes the Core.
type BuildFunc func(ctx context.Context, d CoreDeps) (*Core, error)

// RestartError is returned by Serve after a self-update asked for a
// restart: the server has shut down and released everything, and the
// caller should start Exe in its place.
type RestartError struct{ Exe string }

func (e *RestartError) Error() string { return "restart into " + e.Exe }

// AlreadyRunningError is returned when a server already serves this data
// directory.
type AlreadyRunningError struct{ URL string }

func (e *AlreadyRunningError) Error() string {
	return "agent-keepalive is already running at " + e.URL
}

// Serve runs the server until ctx ends, api/quit is called, or a
// self-update restarts it (RestartError).
func Serve(ctx context.Context, cfg Config) error {
	if cfg.DataDir == "" {
		return errors.New("app: no data directory")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Profile == nil {
		p := distro.Current()
		cfg.Profile = &p
	}
	if cfg.InstallKind == "" {
		cfg.InstallKind = cfg.Profile.InstallKind
	}
	if cfg.Build == nil {
		cfg.Build = buildCore
	}
	if cfg.RetryLock <= 0 {
		cfg.RetryLock = 5 * time.Second
	}
	if cfg.LockDir == "" {
		d, err := instance.LockDir()
		if err != nil {
			return err
		}
		cfg.LockDir = d
	}
	if cfg.AgentDirs == nil {
		d := instance.CurrentAgentDirs()
		cfg.AgentDirs = &d
	}
	if cfg.PricingPath == "" {
		cfg.PricingPath = cfg.DataDir + string(os.PathSeparator) + "pricing.json"
	}
	log := cfg.Log

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	if r, ok := instance.FindRunning(ctx, cfg.DataDir); ok {
		return &AlreadyRunningError{URL: r.URL}
	}
	token, err := instance.Token(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("API token: %w", err)
	}
	conf, err := config.Load(config.Options{DataDir: cfg.DataDir, AdminPath: cfg.AdminConfig, Profile: cfg.Profile})
	if err != nil {
		return err
	}
	for _, p := range conf.Problems() {
		log.Warn("config ignored", "err", p)
	}
	if cfg.FrameAncestors != "" {
		if err := api.ValidFrameAncestors(cfg.FrameAncestors); err != nil {
			return fmt.Errorf("--frame-ancestors: %w", err)
		}
	}
	var addon fs.FS
	if cfg.UIAddon != "" {
		st, err := os.Stat(cfg.UIAddon)
		if err != nil || !st.IsDir() {
			return fmt.Errorf("--ui-addon %s: not a directory", cfg.UIAddon)
		}
		addon = os.DirFS(cfg.UIAddon)
	}

	s := &server{cfg: cfg, conf: conf, token: token, addon: addon, log: log, done: make(chan struct{})}

	// A server started by a self-update waits for its predecessor before
	// trying the lock, so it takes over the scheduler instead of proxying.
	if cfg.WaitPredecessor != nil {
		cfg.WaitPredecessor(ctx, s.tryLock)
	} else {
		s.tryLock()
	}

	ln, port, err := instance.Listen(cfg.Port)
	if err != nil {
		s.releaseLock()
		return err
	}
	s.port = port
	s.info = apitypes.Info{
		Name: api.Name, Version: cfg.Version, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Profile: cfg.Profile.Name, InstallKind: cfg.InstallKind, DataDir: cfg.DataDir,
		Supervised: cfg.Supervised, Homepage: cfg.Profile.Homepage, DownloadURL: cfg.Profile.DownloadURL,
	}
	return s.run(ctx, ln)
}

type server struct {
	cfg   Config
	conf  *config.Config
	token string
	addon fs.FS
	log   *slog.Logger
	port  int
	info  apitypes.Info

	lockMu sync.Mutex
	lock   *instance.SchedulerLock

	handler atomic.Pointer[http.Handler]

	stopOnce   sync.Once
	done       chan struct{} // closed to stop: quit, restart
	restartExe atomic.Pointer[string]

	coreWG sync.WaitGroup
	core   *Core
	coreMu sync.Mutex
	cancel context.CancelFunc // of the core's loops

	lastUse atomic.Int64 // unix nanoseconds of the latest request that counts as use
}

// noteUse records a request as use of the server (see Config.IdleExit).
func (s *server) noteUse(r *http.Request) {
	switch r.URL.Path {
	case "/api/health":
		return
	case "/api/ws":
		if r.URL.Query().Get("passive") == "1" {
			return
		}
	}
	s.lastUse.Store(time.Now().UnixNano())
}

// idle reports whether the server has gone IdleExit unused while no plan
// keeps a session warm. A proxying server has no plans of its own.
func (s *server) idle(now time.Time) bool {
	if now.Sub(time.Unix(0, s.lastUse.Load())) < s.cfg.IdleExit {
		return false
	}
	s.coreMu.Lock()
	core := s.core
	s.coreMu.Unlock()
	if core == nil {
		return true
	}
	for _, v := range core.Service.Sessions() {
		if v.Plan != nil && v.Plan.State == model.PlanActive {
			return false
		}
	}
	return true
}

// tryLock takes the scheduler lock if it is free; it reports whether we
// hold it.
func (s *server) tryLock() bool {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.lock != nil {
		return true
	}
	l, err := instance.AcquireSchedulerLock(s.cfg.LockDir, *s.cfg.AgentDirs)
	if err != nil {
		if !errors.Is(err, instance.ErrLocked) {
			s.log.Warn("scheduler lock", "err", err)
		}
		return false
	}
	s.lock = l
	return true
}

func (s *server) holdsLock() bool {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	return s.lock != nil
}

func (s *server) releaseLock() {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.lock != nil {
		s.lock.Release()
		s.lock = nil
	}
}

func (s *server) stop() { s.stopOnce.Do(func() { close(s.done) }) }

func (s *server) restart(exe string) {
	s.restartExe.Store(&exe)
	s.stop()
}

func (s *server) run(ctx context.Context, ln net.Listener) error {
	coreCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.cancel = cancel
	defer cancel()

	proxy := !s.holdsLock()
	if proxy {
		if err := s.serveProxy(); err != nil {
			ln.Close()
			return err
		}
	} else if err := s.startCore(coreCtx); err != nil {
		ln.Close()
		s.releaseLock()
		return err
	}

	baseCtx, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.noteUse(r)
			(*s.handler.Load()).ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 10 * time.Second,
		// WebSocket requests outlive any write deadline; they are cut by
		// cancelling BaseContext on shutdown.
		BaseContext: func(net.Listener) context.Context { return baseCtx },
		ErrorLog:    slog.NewLogLogger(s.log.Handler(), slog.LevelDebug),
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	if err := instance.WritePortFile(s.cfg.DataDir, s.port); err != nil {
		s.log.Warn("port file", "err", err)
	}
	defer instance.RemovePortFile(s.cfg.DataDir, s.port)
	s.publishHolder()

	url := instance.BaseURL(s.port)
	s.log.Info("serving", "url", url, "dataDir", s.cfg.DataDir, "scheduler", !proxy, "version", s.cfg.Version)
	if proxy {
		s.log.Info("another agent-keepalive server runs the scheduler for these sessions; forwarding to it")
	}
	if s.cfg.Ready != nil {
		s.cfg.Ready(Ready{Port: s.port, URL: url, Proxy: proxy})
	}

	retry := time.NewTicker(s.cfg.RetryLock)
	defer retry.Stop()
	s.lastUse.Store(time.Now().UnixNano())
	var idleTick <-chan time.Time
	if s.cfg.IdleExit > 0 && !s.cfg.Supervised {
		every := s.cfg.IdleCheck
		if every <= 0 {
			every = time.Minute
		}
		t := time.NewTicker(every)
		defer t.Stop()
		idleTick = t.C
	}
	var runErr error
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-s.done:
			break loop
		case err := <-serveErr:
			runErr = err
			break loop
		case now := <-idleTick:
			if s.idle(now) {
				s.log.Info("unused with no active plan; stopping", "after", s.cfg.IdleExit)
				break loop
			}
		case <-retry.C:
			if s.holdsLock() || !s.tryLock() {
				continue
			}
			// The other server went away: run the scheduler here.
			s.log.Info("took over the scheduler")
			if err := s.startCore(coreCtx); err != nil {
				s.log.Error("start scheduler", "err", err)
				s.releaseLock()
				continue
			}
			s.publishHolder()
		}
	}

	// Shutdown order: stop taking requests, end WebSockets, stop the
	// loops, close the core, release the lock (so a successor can take it
	// only once nothing of ours is left running).
	shCtx, shCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = srv.Shutdown(shCtx)
	shCancel()
	cancelBase()
	cancel()
	s.coreWG.Wait()
	s.coreMu.Lock()
	if s.core != nil && s.core.Close != nil {
		s.core.Close()
	}
	s.coreMu.Unlock()
	s.releaseLock()
	s.log.Info("stopped")

	if exe := s.restartExe.Load(); exe != nil {
		return &RestartError{Exe: *exe}
	}
	if runErr != nil && !errors.Is(runErr, http.ErrServerClosed) {
		return runErr
	}
	return nil
}

func (s *server) quitFunc() func() {
	if s.cfg.Supervised {
		return nil
	}
	return s.stop
}

// startCore builds the scheduler and friends and switches the handler to
// serve them.
func (s *server) startCore(ctx context.Context) error {
	core, err := s.cfg.Build(ctx, CoreDeps{
		DataDir: s.cfg.DataDir, PricingPath: s.cfg.PricingPath, Config: s.conf, Info: s.info,
		NoUpdate: s.cfg.NoUpdate || s.cfg.Supervised, Restart: s.restart, Quit: s.quitFunc(), Log: s.log,
	})
	if err != nil {
		return err
	}
	h, err := api.New(api.Options{
		Service: core.Service, Updater: core.Updater, Metrics: core.Metrics, Info: s.info,
		Token: s.token, Port: s.port, UI: s.cfg.UI, Addon: s.addon, FrameAncestors: s.cfg.FrameAncestors, Quit: s.quitFunc(), Log: s.log,
	})
	if err != nil {
		if core.Close != nil {
			core.Close()
		}
		return err
	}
	s.coreMu.Lock()
	s.core = core
	s.coreMu.Unlock()
	for _, fn := range core.Run {
		s.coreWG.Add(1)
		go func() {
			defer s.coreWG.Done()
			fn(ctx)
		}()
	}
	s.handler.Store(&h)
	return nil
}

// serveProxy installs a handler that forwards the API to the lock holder.
func (s *server) serveProxy() error {
	var (
		mu      sync.Mutex
		cached  instance.Holder
		cachedT string
		at      time.Time
	)
	up := func() (int, string, error) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(at) < 2*time.Second && cached.Port != 0 {
			return cached.Port, cachedT, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		h, ok := instance.ReadHolder(ctx, s.cfg.LockDir, *s.cfg.AgentDirs)
		if !ok {
			return 0, "", errors.New("no scheduler server found")
		}
		tok, err := instance.ReadToken(h.DataDir)
		if err != nil {
			return 0, "", err
		}
		cached, cachedT, at = h, tok, time.Now()
		return h.Port, tok, nil
	}
	h, err := api.New(api.Options{
		Upstream: up, Info: s.info, Token: s.token, Port: s.port, UI: s.cfg.UI, Addon: s.addon, FrameAncestors: s.cfg.FrameAncestors,
		Quit: s.quitFunc(), Log: s.log,
	})
	if err != nil {
		return err
	}
	s.handler.Store(&h)
	return nil
}

func (s *server) publishHolder() {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.lock == nil || s.port == 0 {
		return
	}
	if err := s.lock.Publish(instance.Holder{PID: os.Getpid(), Port: s.port, DataDir: s.cfg.DataDir}); err != nil {
		s.log.Warn("publish scheduler holder", "err", err)
	}
}
