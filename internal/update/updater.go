package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
)

// maxFeedSize bounds latest.json.
const maxFeedSize = 1 << 20

// Options configure an Updater.
type Options struct {
	// Version is the running version.
	Version string
	// FeedURL and PublicKey come from the profile
	// (distro.Current().UpdateFeedURL / UpdatePublicKey). With no feed URL,
	// or a feed URL but no public key, the updater is disabled.
	FeedURL   string
	PublicKey string
	// Disabled turns updates off (--no-update, supervised).
	Disabled bool
	// Install is how this copy was installed (default: DetectInstall()).
	Install *Install
	// GOOS and GOARCH select the artifact (default: runtime's).
	GOOS, GOARCH string

	// Restart is called once the new program is in place (AppImage,
	// archive, Windows portable). It shuts the server down and starts exe.
	// Without it the update stops at "ready": the user restarts.
	Restart apitypes.RestartFunc
	// Quit is called after the Windows installer was started: the
	// installer stops the server anyway, Quit lets it go first.
	Quit func()

	Client *http.Client
	Log    *slog.Logger
	// Every is the check interval (default 6h, with ±10% jitter).
	Every time.Duration
	// FirstCheck is the delay before Run's first check (default 10–60 s).
	FirstCheck time.Duration
	// WatchEvery is how often Run looks for a newer program put in place
	// by something else, such as a package manager (default 1 min). Only
	// with Restart.
	WatchEvery time.Duration
	// TempDir receives Windows installers and macOS packages (default os.TempDir()).
	TempDir string

	// Hooks for tests.
	exists   func(string) bool                                      // default: os.Stat
	runSetup func(path string) error                                // default: start `path /S /UPDATE` detached
	openPkg  func(path string) error                                // default: `open path`
	probe    func(ctx context.Context, path string) (string, error) // default: probeVersion
	now      func() time.Time
}

// Updater checks the feed and installs updates. It implements
// apitypes.Updater and is safe for concurrent use.
type Updater struct {
	opts    Options
	in      Install
	enabled bool

	checkMu sync.Mutex // one feed check at a time

	mu      sync.Mutex
	st      apitypes.UpdateStatus
	art     Artifact
	hasArt  bool
	busy    bool
	cancel  context.CancelFunc
	pkgPath string // a downloaded macOS package, for Apply to reopen
	subs    map[int]func(apitypes.UpdateStatus)
	nextSub int
	started stamp // the program file at New, for watch

	notifyMu sync.Mutex
}

// New returns an Updater. It never contacts anything until Check or Run.
func New(opts Options) *Updater {
	if opts.Install == nil {
		in := DetectInstall()
		opts.Install = &in
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtime.GOARCH
	}
	if opts.Client == nil {
		opts.Client = &http.Client{}
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Every <= 0 {
		opts.Every = 6 * time.Hour
	}
	if opts.FirstCheck <= 0 {
		opts.FirstCheck = 10*time.Second + rand.N(50*time.Second)
	}
	if opts.WatchEvery <= 0 {
		opts.WatchEvery = time.Minute
	}
	if opts.probe == nil {
		opts.probe = probeVersion
	}
	if opts.TempDir == "" {
		opts.TempDir = os.TempDir()
	}
	if opts.exists == nil {
		opts.exists = func(p string) bool { _, err := os.Stat(p); return err == nil }
	}
	if opts.runSetup == nil {
		opts.runSetup = func(p string) error { return startDetached(exec.Command(p, "/S", "/UPDATE")) }
	}
	if opts.openPkg == nil {
		opts.openPkg = func(p string) error { return exec.Command("open", p).Run() }
	}
	if opts.now == nil {
		opts.now = func() time.Time { return time.Now().Round(0) }
	}
	u := &Updater{opts: opts, in: *opts.Install, subs: map[int]func(apitypes.UpdateStatus){}}
	if opts.Restart != nil {
		u.started, _ = stampOf(u.in.Target)
	}
	_, versionOK := ParseVersion(opts.Version)
	switch {
	case opts.Disabled, opts.FeedURL == "":
	case opts.PublicKey == "":
		opts.Log.Warn("update: this build has an update feed but no public key; updates are off")
	case !versionOK:
		opts.Log.Info("update: not a release version; updates are off", "version", opts.Version)
	default:
		if _, err := ParsePublicKey(opts.PublicKey); err != nil {
			opts.Log.Warn("update: the build's public key is malformed; updates are off")
			break
		}
		u.enabled = true
	}
	u.st = apitypes.UpdateStatus{Enabled: u.enabled, Current: opts.Version, State: apitypes.UpdateIdle}
	return u
}

// Kind is the detected install kind.
func (u *Updater) Kind() string { return u.in.Kind }

// Status returns a snapshot of the updater's state.
func (u *Updater) Status() apitypes.UpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.snapshot()
}

func (u *Updater) snapshot() apitypes.UpdateStatus {
	st := u.st
	if st.Latest != nil {
		l := *st.Latest
		st.Latest = &l
	}
	return st
}

// Subscribe calls fn with the status after every change until cancel.
func (u *Updater) Subscribe(fn func(apitypes.UpdateStatus)) (cancel func()) {
	u.mu.Lock()
	id := u.nextSub
	u.nextSub++
	u.subs[id] = fn
	u.mu.Unlock()
	return func() {
		u.mu.Lock()
		delete(u.subs, id)
		u.mu.Unlock()
	}
}

// notify sends the current status to subscribers, in order of changes.
func (u *Updater) notify() {
	u.notifyMu.Lock()
	defer u.notifyMu.Unlock()
	u.mu.Lock()
	st := u.snapshot()
	fns := make([]func(apitypes.UpdateStatus), 0, len(u.subs))
	for _, fn := range u.subs {
		fns = append(fns, fn)
	}
	u.mu.Unlock()
	for _, fn := range fns {
		fn(st)
	}
}

// Run checks shortly after start and then every Options.Every, with
// jitter, until ctx ends. With a Restart hook it also watches for a newer
// program installed in place of this one. It returns at once when updates
// are disabled.
func (u *Updater) Run(ctx context.Context) {
	if !u.enabled {
		return
	}
	if u.opts.Restart != nil {
		go u.watch(ctx)
	}
	t := time.NewTimer(u.opts.FirstCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := u.Check(ctx); err != nil && ctx.Err() == nil {
			u.opts.Log.Info("update check failed", "err", err)
		}
		t.Reset(jitter(u.opts.Every, 0.1))
	}
}

// jitter returns d varied by up to ±f.
func jitter(d time.Duration, f float64) time.Duration {
	span := time.Duration(float64(d) * f)
	if span <= 0 {
		return d
	}
	return d - span + rand.N(2*span)
}

// Check fetches and verifies the feed and records whether a newer release
// exists. It does nothing while an update is being downloaded or installed.
func (u *Updater) Check(ctx context.Context) error {
	if !u.enabled {
		return fmt.Errorf("%w: updates are off", apitypes.ErrUnavailable)
	}
	u.checkMu.Lock()
	defer u.checkMu.Unlock()

	u.mu.Lock()
	if u.busy || u.st.State == apitypes.UpdateInstalling || u.st.Installed != "" {
		// A newer version installed in place waits for the restart.
		u.mu.Unlock()
		return nil
	}
	prev := u.st
	u.st.State = apitypes.UpdateChecking
	u.mu.Unlock()
	u.notify()

	feed, err := u.fetchFeed(ctx)

	u.mu.Lock()
	if err != nil {
		u.st.State, u.st.Error = apitypes.UpdateError, err.Error()
		u.mu.Unlock()
		u.notify()
		return err
	}
	u.st.LastCheck = u.opts.now()
	u.st.Error = ""
	if !Newer(feed.Version, u.opts.Version) {
		u.st.Latest, u.st.Action, u.st.Command = nil, "", ""
		u.st.State, u.st.Progress = apitypes.UpdateIdle, 0
		u.hasArt = false
		u.mu.Unlock()
		u.notify()
		return nil
	}
	if prev.State == apitypes.UpdateReady && prev.Latest != nil && prev.Latest.Version == feed.Version {
		u.st.State = apitypes.UpdateReady // already in place or in the Installer
		u.mu.Unlock()
		u.notify()
		return nil
	}
	if prev.Latest == nil || prev.Latest.Version != feed.Version {
		u.pkgPath = ""
	}
	pub, _ := time.Parse(time.RFC3339, feed.PublishedAt)
	u.st.Latest = &apitypes.Latest{Version: feed.Version, Notes: feed.Notes, Page: feed.Page, PublishedAt: pub}
	u.st.State, u.st.Progress = apitypes.UpdateAvailable, 0
	u.art, u.hasArt = feed.Find(u.in.Kind, u.opts.GOOS, u.opts.GOARCH)
	u.st.Action, u.st.Command = u.action()
	u.mu.Unlock()
	u.notify()
	return nil
}

// action decides what the UI offers for an available update.
func (u *Updater) action() (action, command string) {
	if c := Command(u.in.Kind, u.opts.exists); c != "" {
		return apitypes.ActionCommand, c
	}
	if !SelfUpdates(u.in.Kind) || !u.hasArt {
		return apitypes.ActionDownload, ""
	}
	if replacesInPlace(u.in.Kind) && !dirWritable(filepath.Dir(u.in.Target)) {
		return apitypes.ActionDownload, ""
	}
	return apitypes.ActionApply, ""
}

// replacesInPlace reports whether kind is updated by swapping the file.
func replacesInPlace(kind string) bool {
	switch kind {
	case KindAppImage, KindArchive, KindWindowsPortable:
		return true
	}
	return false
}

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, tempPrefix+"probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func (u *Updater) fetchFeed(ctx context.Context) (*Feed, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.opts.FeedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cache-Control", "no-cache")
	res, err := u.opts.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("feed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feed: HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxFeedSize+1))
	if err != nil {
		return nil, fmt.Errorf("feed: %w", err)
	}
	if len(b) > maxFeedSize {
		return nil, errors.New("feed: too large")
	}
	return ParseFeed(b, u.opts.PublicKey)
}

// Apply starts installing the available update and returns once it has
// started; progress follows through Subscribe.
func (u *Updater) Apply(ctx context.Context) error {
	if !u.enabled {
		return fmt.Errorf("%w: updates are off", apitypes.ErrUnavailable)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	switch {
	case u.busy:
		return nil
	case u.st.Latest == nil:
		return fmt.Errorf("%w: no update is available", apitypes.ErrUnavailable)
	case u.st.State == apitypes.UpdateInstalling:
		return nil
	case u.st.Action == apitypes.ActionRestart:
		if u.opts.Restart == nil {
			return fmt.Errorf("%w: restart agent-keepalive to use version %s", apitypes.ErrUnavailable, u.st.Installed)
		}
		u.st.State = apitypes.UpdateInstalling
		go func() {
			u.notify()
			u.opts.Restart(u.in.Target)
		}()
		return nil
	case u.st.Action != apitypes.ActionApply:
		return fmt.Errorf("%w: this installation is updated with %s", apitypes.ErrUnavailable, u.howToUpdate())
	}
	if u.in.Kind == KindMacOSPkg && u.pkgPath != "" {
		if _, err := os.Stat(u.pkgPath); err == nil {
			path := u.pkgPath
			go u.finish(func() error { return u.openPackage(path) })
			return nil
		}
	}
	if u.st.State == apitypes.UpdateReady {
		return nil // in place already; waiting for a restart
	}
	dctx, cancel := context.WithCancel(context.Background())
	u.busy, u.cancel = true, cancel
	u.st.State, u.st.Progress, u.st.Error = apitypes.UpdateDownloading, 0, ""
	art, version := u.art, u.st.Latest.Version
	go func() {
		defer cancel()
		u.notify()
		err := u.install(dctx, art)
		u.mu.Lock()
		u.busy, u.cancel = false, nil
		if err != nil {
			u.st.State, u.st.Error = apitypes.UpdateError, err.Error()
			u.opts.Log.Warn("update failed", "version", version, "err", err)
		}
		u.mu.Unlock()
		u.notify()
	}()
	return nil
}

func (u *Updater) howToUpdate() string {
	if u.st.Command != "" {
		return "`" + u.st.Command + "`"
	}
	return "a download from the release page"
}

// finish runs a final step of an update in the background and records its error.
func (u *Updater) finish(step func() error) {
	if err := step(); err != nil {
		u.setState(apitypes.UpdateError, err.Error())
	}
}

func (u *Updater) setState(state, errText string) {
	u.mu.Lock()
	u.st.State, u.st.Error = state, errText
	u.mu.Unlock()
	u.notify()
}

// Close stops a download in progress.
func (u *Updater) Close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cancel != nil {
		u.cancel()
	}
}

// install downloads, verifies and installs art for this install kind.
func (u *Updater) install(ctx context.Context, art Artifact) error {
	switch u.in.Kind {
	case KindAppImage, KindWindowsPortable, KindArchive:
		dir := filepath.Dir(u.in.Target)
		dl, err := u.download(ctx, art, dir, "")
		if err != nil {
			return err
		}
		defer os.Remove(dl)
		src := dl
		if u.in.Kind == KindArchive {
			f, err := os.CreateTemp(dir, tempPrefix+"*")
			if err != nil {
				return err
			}
			src = f.Name()
			f.Close()
			defer os.Remove(src)
			if err := extractBinary(dl, art.Name, filepath.Base(u.in.Target), src); err != nil {
				return err
			}
		}
		u.setState(apitypes.UpdateInstalling, "")
		if err := swapFile(u.in.Target, src, u.opts.GOOS); err != nil {
			return fmt.Errorf("replace %s: %w", u.in.Target, err)
		}
		u.opts.Log.Info("update installed", "version", u.Status().Latest.Version)
		// Before Restart: it may end this process before the defers run.
		os.Remove(dl)
		os.Remove(src)
		if u.opts.Restart == nil {
			u.setState(apitypes.UpdateReady, "")
			return nil
		}
		u.opts.Restart(u.in.Target)
		return nil

	case KindWindowsSetup, KindMacOSPkg:
		dir, err := os.MkdirTemp(u.opts.TempDir, "agent-keepalive-update-")
		if err != nil {
			return err
		}
		dl, err := u.download(ctx, art, dir, filepath.Base(art.Name))
		if err != nil {
			os.RemoveAll(dir)
			return err
		}
		if u.in.Kind == KindMacOSPkg {
			u.mu.Lock()
			u.pkgPath = dl
			u.mu.Unlock()
			return u.openPackage(dl)
		}
		u.setState(apitypes.UpdateInstalling, "")
		if err := u.opts.runSetup(dl); err != nil {
			return fmt.Errorf("start the installer: %w", err)
		}
		if u.opts.Quit != nil {
			u.opts.Quit()
		}
		return nil
	}
	return fmt.Errorf("%w: a %s installation cannot update itself", apitypes.ErrUnavailable, u.in.Kind)
}

// openPackage opens a downloaded macOS package in Installer; the update is
// then "ready" until the Installer replaces the program.
func (u *Updater) openPackage(path string) error {
	u.setState(apitypes.UpdateInstalling, "")
	if err := u.opts.openPkg(path); err != nil {
		return fmt.Errorf("open the installer: %w", err)
	}
	u.setState(apitypes.UpdateReady, "")
	return nil
}

// download fetches art into dir (as name, or a temporary name when name is
// "") and checks its size and sha256 against the feed. It returns the path
// of the verified file; on error nothing is left behind.
func (u *Updater) download(ctx context.Context, art Artifact, dir, name string) (path string, err error) {
	if art.Size <= 0 || len(art.SHA256) != sha256.Size*2 {
		return "", fmt.Errorf("feed: %s has no size or sha256", art.Name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, art.URL, nil)
	if err != nil {
		return "", err
	}
	res, err := u.opts.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", art.Name, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", art.Name, res.StatusCode)
	}
	var f *os.File
	if name == "" {
		f, err = os.CreateTemp(dir, tempPrefix+"*")
	} else {
		f, err = os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o700)
	}
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	pw := &progressWriter{total: art.Size, report: u.progress}
	n, err := io.Copy(io.MultiWriter(f, h, pw), io.LimitReader(res.Body, art.Size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("download %s: %w", art.Name, err)
	}
	if n != art.Size {
		return "", fmt.Errorf("download %s: got %d bytes, the feed says %d", art.Name, n, art.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, art.SHA256) {
		return "", fmt.Errorf("download %s: sha256 does not match the feed", art.Name)
	}
	u.progress(1)
	return f.Name(), nil
}

func (u *Updater) progress(p float64) {
	u.mu.Lock()
	u.st.Progress = p
	u.mu.Unlock()
	u.notify()
}

// progressWriter reports the fraction written, at most every 1% or 250 ms.
type progressWriter struct {
	n, total int64
	lastP    float64
	lastT    time.Time
	report   func(float64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.n += int64(len(b))
	f := min(float64(p.n)/float64(p.total), 1)
	if f-p.lastP >= 0.01 || time.Since(p.lastT) >= 250*time.Millisecond {
		p.lastP, p.lastT = f, time.Now()
		p.report(f)
	}
	return len(b), nil
}
