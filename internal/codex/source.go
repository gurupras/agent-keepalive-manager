// Package codex discovers live Codex CLI sessions and delivers keepalive
// prompts to them through `codex queue`.
//
// Discovery is read-only. Live interactive processes come from
// internal/proc. Each process's thread is the rollout (or writer-lock file)
// it holds open where the OS exposes open files (Linux, and macOS through
// lsof). Elsewhere (Windows) it is the newest rollout written since the
// process started whose session_meta cwd matches the process (when its cwd
// is known), each rollout going to at most one process, newest process
// first. That guess can pick the wrong thread when two Codex processes
// share a directory; it is re-made every guessEvery.
//
// Metadata comes from $CODEX_HOME/state_5.sqlite (opened read-only) and
// $CODEX_HOME/session_index.jsonl, and usage is tailed from the rollout
// JSONL with internal/tail. A rollout whose first read is large is read in
// the background; until then its session is listed with Reach "none".
package codex

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/proc"
	"github.com/gurupras/agent-keepalive-manager/internal/tail"
)

// CacheTTL is the observed Codex prompt-cache lifetime (sliding).
const CacheTTL = 30 * time.Minute

// retainEnded is how long the state of a vanished thread is kept so
// Activity can still answer questions about its last moments.
const retainEnded = time.Hour

// guessEvery is how often the thread of a process without an open-file
// table is guessed again (to notice /new and /resume).
const guessEvery = 30 * time.Second

// binNames are executable names of the Codex CLI, without ".exe". The npm
// package runs a platform-specific native binary under a node wrapper; the
// native binary is the process that holds the rollout.
var binNames = []string{
	"codex",
	"codex-x86_64-unknown-linux-musl",
	"codex-aarch64-unknown-linux-musl",
	"codex-x86_64-unknown-linux-gnu",
	"codex-aarch64-unknown-linux-gnu",
	"codex-x86_64-apple-darwin",
	"codex-aarch64-apple-darwin",
	"codex-x86_64-pc-windows-msvc",
	"codex-aarch64-pc-windows-msvc",
}

// helperNames are child binaries spawned by a Codex session; never sessions.
var helperNames = map[string]bool{
	"codex-linux-sandbox":   true,
	"codex-code-mode-host":  true,
	"codex-execpolicy":      true,
	"codex-responses-proxy": true,
	"codex-command-runner":  true,
	"codex-windows-sandbox": true,
}

// skipSubcommands are non-interactive invocations.
var skipSubcommands = map[string]bool{
	"exec": true, "app-server": true, "queue": true, "mcp-server": true, "mcp": true, "login": true,
}

// System is the process inspection the package uses; tests replace it.
type System struct {
	List         func(names ...string) []proc.Proc
	Env          func(pid int, name string) (string, bool)
	OpenFiles    func(pid int) []string
	HasOpenFiles func() bool
	Cwd          func(pid int) string
	StartTime    func(pid int) string
	LookPath     func(file string) (string, error)
	GOOS         string
}

func defaultSystem() *System {
	return &System{
		List:         proc.List,
		Env:          proc.Env,
		OpenFiles:    proc.OpenFiles,
		HasOpenFiles: proc.HasOpenFiles,
		Cwd:          proc.Cwd,
		StartTime:    proc.StartTime,
		LookPath:     exec.LookPath,
		GOOS:         runtime.GOOS,
	}
}

// Options configure a Source or Deliverer. All fields are optional.
type Options struct {
	// Home is the CODEX_HOME assumed for processes whose environment does
	// not set it (or cannot be read). Default: $CODEX_HOME, else ~/.codex.
	Home string
	Log  *slog.Logger
	Now  func() time.Time
	// MaxActivities bounds the events kept per thread (default 20000).
	MaxActivities int
	// Tail tunes rollout reading (see internal/tail).
	Tail tail.Options
	Sys  *System
}

func (o Options) withDefaults() Options {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().Round(0) }
	}
	if o.Home == "" {
		o.Home = DefaultHome()
	}
	if o.Sys == nil {
		o.Sys = defaultSystem()
	}
	return o
}

// DefaultHome is $CODEX_HOME, else ~/.codex.
func DefaultHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

// Source implements model.Source for Codex CLI.
type Source struct {
	opts Options
	sys  *System

	scanMu sync.Mutex // serializes Scan

	mu      sync.Mutex
	threads map[string]*threadState // by lower-case thread id
	names   map[string]*nameIndex   // by CODEX_HOME
	guesses map[int]guess           // by pid, without an open-file table
	headers map[string]metaHeader   // rollout path -> its session_meta
}

type threadState struct {
	id       string
	home     string
	log      *tail.ActivityLog // outlives ro, so Seq keeps counting
	ro       *rollout
	meta     threadMeta
	lastSeen time.Time
}

type guess struct {
	start string
	at    time.Time
	id    string
	path  string
}

var _ model.Source = (*Source)(nil)

// NewSource returns a Codex session source.
func NewSource(opts Options) *Source {
	opts = opts.withDefaults()
	return &Source{
		opts:    opts,
		sys:     opts.Sys,
		threads: map[string]*threadState{},
		names:   map[string]*nameIndex{},
		guesses: map[int]guess{},
		headers: map[string]metaHeader{},
	}
}

func (*Source) Harness() model.Harness { return model.Codex }

// Key returns the session key for a Codex thread id.
func Key(id string) string { return string(model.Codex) + ":" + strings.ToLower(id) }

// candidate is one live process and the threads it holds.
type candidate struct {
	p       proc.Proc
	home    string
	threads map[string]string // thread id -> rollout path ("" if only the lock is open)
}

const uuidRe = `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`

// Paths are matched in slash form, so one pattern serves every OS.
var (
	rolloutRe = regexp.MustCompile(`^(.*)/sessions/(?:.*/)?rollout-.*-(` + uuidRe + `)\.jsonl?$`)
	lockRe    = regexp.MustCompile(`^(.*)/thread-writer-locks/(` + uuidRe + `)\.lock$`)
)

// slashed is p with forward slashes. On Windows both separators are
// converted whatever OS this runs on, so the matching is testable anywhere.
func slashed(goos, p string) string {
	if goos == "windows" {
		return strings.ReplaceAll(p, `\`, "/")
	}
	return filepath.ToSlash(p)
}

// samePath compares paths in slash form, ignoring case on Windows and macOS
// (whose default file systems are case-insensitive).
func samePath(goos, a, b string) bool {
	a, b = path.Clean(slashed(goos, a)), path.Clean(slashed(goos, b))
	if goos == "windows" || goos == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// threadFiles extracts thread ids from open file paths under home.
func threadFiles(goos, home string, files []string) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		f = strings.TrimSuffix(f, " (deleted)")
		s := slashed(goos, f)
		if m := rolloutRe.FindStringSubmatch(s); m != nil && samePath(goos, m[1], home) {
			out[strings.ToLower(m[2])] = f
		} else if m := lockRe.FindStringSubmatch(s); m != nil && samePath(goos, m[1], home) {
			id := strings.ToLower(m[2])
			if _, ok := out[id]; !ok {
				out[id] = ""
			}
		}
	}
	return out
}

// baseName is the lower-cased file name of an executable path, either
// separator, without ".exe" or " (deleted)".
func baseName(p string) string {
	p = strings.TrimSuffix(p, " (deleted)")
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	return strings.TrimSuffix(strings.ToLower(p), ".exe")
}

// interactive reports whether p is an interactive Codex TUI process.
func interactive(p proc.Proc) bool {
	if helperNames[baseName(p.Exe)] || (len(p.Args) > 0 && helperNames[baseName(p.Args[0])]) {
		return false
	}
	if len(p.Args) == 0 {
		return false
	}
	for _, a := range p.Args[1:] {
		if skipSubcommands[a] {
			return false
		}
	}
	return true
}

func (s *Source) homeOf(pid int) string {
	if h, ok := s.sys.Env(pid, "CODEX_HOME"); ok && h != "" {
		return h
	}
	return s.opts.Home
}

func (s *Source) candidates(now time.Time) []candidate {
	procs := []proc.Proc{}
	for _, p := range s.sys.List(binNames...) {
		if interactive(p) {
			procs = append(procs, p)
		}
	}
	// Newest process first: it gets first pick of a guessed rollout.
	sort.Slice(procs, func(i, j int) bool { return startOrder(procs[i].Start, procs[j].Start) })
	var out []candidate
	claimed := map[string]bool{}
	open := s.sys.HasOpenFiles()
	for _, p := range procs {
		home := s.homeOf(p.PID)
		var ts map[string]string
		if open {
			ts = threadFiles(s.sys.GOOS, home, s.sys.OpenFiles(p.PID))
		} else if id, path := s.guessThread(p, home, now, claimed); id != "" {
			ts = map[string]string{id: path}
			claimed[path] = true
		}
		if len(ts) == 0 {
			continue // TUI without a thread yet (rollout is created on first prompt)
		}
		out = append(out, candidate{p: p, home: home, threads: ts})
	}
	live := map[int]bool{}
	for _, p := range procs {
		live[p.PID] = true
	}
	s.mu.Lock()
	for pid := range s.guesses {
		if !live[pid] {
			delete(s.guesses, pid)
		}
	}
	s.mu.Unlock()
	return out
}

// startOrder sorts newer start tokens first. Tokens of one OS compare as
// numbers (ticks, "sec.usec", nanoseconds).
func startOrder(a, b string) bool {
	fa, _ := strconv.ParseFloat(a, 64)
	fb, _ := strconv.ParseFloat(b, 64)
	return fa > fb
}

// guessThread returns the cached guess for p, or makes a new one.
func (s *Source) guessThread(p proc.Proc, home string, now time.Time, claimed map[string]bool) (string, string) {
	s.mu.Lock()
	g, ok := s.guesses[p.PID]
	s.mu.Unlock()
	if ok && g.start == p.Start && now.Sub(g.at) < guessEvery && !claimed[g.path] {
		if _, err := os.Stat(g.path); err == nil {
			return g.id, g.path
		}
	}
	id, path := s.matchRollout(p, home, claimed)
	s.mu.Lock()
	s.guesses[p.PID] = guess{start: p.Start, at: now, id: id, path: path}
	s.mu.Unlock()
	return id, path
}

// startSlack absorbs clock granularity between a process start and the
// first write of its rollout.
const startSlack = 5 * time.Second

// matchRollout picks the newest rollout under home written since p
// started whose cwd matches p's (when known), skipping sub-agent threads
// and rollouts claimed by a newer process.
func (s *Source) matchRollout(p proc.Proc, home string, claimed map[string]bool) (id, path string) {
	cwd := s.sys.Cwd(p.PID)
	since, haveStart := startWall(s.sys.GOOS, p.Start)
	if !haveStart {
		since = time.Now().Add(-24 * time.Hour)
	}
	since = since.Add(-startSlack)
	var best time.Time
	for _, f := range rolloutFiles(home) {
		if claimed[f] {
			continue
		}
		fi, err := os.Stat(f)
		if err != nil || !fi.Mode().IsRegular() || fi.ModTime().Before(since) {
			continue
		}
		h, ok := s.header(f)
		if !ok || h.Subagent {
			continue
		}
		if cwd != "" && h.Cwd != "" && !samePath(s.sys.GOOS, cwd, h.Cwd) {
			continue
		}
		if path == "" || fi.ModTime().After(best) {
			id, path, best = h.ID, f, fi.ModTime()
		}
	}
	return id, path
}

func (s *Source) header(path string) (metaHeader, bool) {
	s.mu.Lock()
	h, ok := s.headers[path]
	s.mu.Unlock()
	if ok {
		return h, true
	}
	h, ok = readHeader(path)
	if ok && h.ID != "" {
		s.mu.Lock()
		if len(s.headers) > 10000 {
			clear(s.headers)
		}
		s.headers[path] = h
		s.mu.Unlock()
	}
	return h, ok && h.ID != ""
}

// rolloutFiles lists the rollouts under home (dated and flat layouts).
func rolloutFiles(home string) []string {
	var out []string
	for _, pat := range []string{
		filepath.Join(home, "sessions", "*", "*", "*", "rollout-*.jsonl"),
		filepath.Join(home, "sessions", "rollout-*.jsonl"),
	} {
		m, _ := filepath.Glob(pat)
		out = append(out, m...)
	}
	return out
}

// startWall converts a proc.StartTime token to wall-clock time where the
// OS's format allows it (macOS "sec.usec", Windows ns since the epoch).
func startWall(goos, tok string) (time.Time, bool) {
	switch goos {
	case "darwin":
		sec, usec, ok := strings.Cut(tok, ".")
		a, err1 := strconv.ParseInt(sec, 10, 64)
		b, err2 := strconv.ParseInt(usec, 10, 64)
		if !ok || err1 != nil || err2 != nil {
			return time.Time{}, false
		}
		return time.Unix(a, b*1000), true
	case "windows":
		ns, err := strconv.ParseInt(tok, 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(0, ns), true
	}
	return time.Time{}, false
}

// Scan returns the live Codex sessions, one per process: the most recently
// active non-sub-agent thread it holds.
func (s *Source) Scan(ctx context.Context) ([]model.Session, error) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	now := s.opts.Now()
	cands := s.candidates(now)

	// Metadata, one read-only query per CODEX_HOME.
	byHome := map[string][]string{}
	for _, c := range cands {
		for id := range c.threads {
			byHome[c.home] = append(byHome[c.home], id)
		}
	}
	metas := map[string]threadMeta{}
	for home, ids := range byHome {
		m, err := readThreadMeta(ctx, home, ids)
		if err != nil {
			s.opts.Log.Debug("codex: thread metadata unavailable", "db", filepath.Join(home, stateDBName), "err", err)
		}
		for id, v := range m {
			metas[id] = v
		}
		s.mu.Lock()
		ni := s.names[home]
		if ni == nil {
			ni = newNameIndex(home)
			s.names[home] = ni
		}
		s.mu.Unlock()
		if _, err := ni.tl.Poll(); err != nil && !os.IsNotExist(err) {
			s.opts.Log.Debug("codex: session index unreadable", "home", home, "err", err)
		}
	}

	var out []model.Session
	seen := map[string]bool{}
	for _, c := range cands {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var best *threadState
		var bestSum summary
		bestLoading := false
		for id, path := range c.threads {
			if seen[id] {
				continue
			}
			t := s.thread(id, c.home, path, metas[id])
			if t.ro == nil {
				continue
			}
			loading, err := t.ro.poll()
			if err != nil {
				s.opts.Log.Debug("codex: rollout unreadable", "path", t.ro.path, "err", err)
				if os.IsNotExist(err) {
					s.mu.Lock()
					t.ro = nil // moved or deleted: locate it again
					s.mu.Unlock()
				}
				continue
			}
			t.lastSeen = now
			sum := t.ro.published()
			if t.meta.Subagent || sum.subagent {
				continue
			}
			if best == nil || sum.lastEvent.After(bestSum.lastEvent) {
				best, bestSum, bestLoading = t, sum, loading
			}
		}
		if best == nil {
			continue
		}
		seen[best.id] = true
		out = append(out, s.session(c, best, bestSum, bestLoading))
	}

	s.mu.Lock()
	for id, t := range s.threads {
		if now.Sub(t.lastSeen) > retainEnded {
			delete(s.threads, id)
		}
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, nil
}

// thread returns (creating if needed) the state for id, locating its rollout.
func (s *Source) thread(id, home, fdPath string, meta threadMeta) *threadState {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[id]
	if t == nil {
		t = &threadState{id: id, home: home, log: tail.NewActivityLog(s.opts.MaxActivities)}
		s.threads[id] = t
	}
	if meta != (threadMeta{}) {
		t.meta = meta
	}
	path := fdPath
	if path == "" && t.ro != nil {
		path = t.ro.path
	}
	if path == "" {
		path = t.meta.RolloutPath
	}
	if path == "" {
		path = findRollout(home, id)
	}
	if path == "" {
		return t
	}
	if t.ro == nil || !samePath(s.sys.GOOS, t.ro.path, path) {
		relocated := t.log.Seq() > 0
		t.ro = newRollout(path, t.log, s.opts.Tail)
		if relocated {
			t.ro.Reset()
		}
	}
	return t
}

// findRollout globs for a rollout by thread id (dated and flat layouts).
func findRollout(home, id string) string {
	for _, pat := range []string{
		filepath.Join(home, "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl"),
		filepath.Join(home, "sessions", "rollout-*-"+id+".jsonl"),
	} {
		if m, _ := filepath.Glob(pat); len(m) > 0 {
			return m[len(m)-1]
		}
	}
	return ""
}

func (s *Source) session(c candidate, t *threadState, r summary, loading bool) model.Session {
	cwd := firstNonEmpty(t.meta.Cwd, r.metaCwd, s.sys.Cwd(c.p.PID))
	started := r.metaStart
	if started.IsZero() {
		started = t.meta.CreatedAt
	}
	s.mu.Lock()
	ni := s.names[t.home]
	s.mu.Unlock()
	name := ""
	if ni != nil {
		name = ni.name(t.id)
	}
	title := firstNonEmpty(name, t.meta.Name, t.meta.Title, r.firstHuman, t.meta.FirstUserMessage)
	status := model.StatusIdle
	if r.busy {
		status = model.StatusWorking
	}
	bin := s.codexBin(c.p.Exe)
	reach, why := model.ReachQueue, ""
	switch {
	case bin == "":
		reach, why = model.ReachNone, "No codex executable was found to queue messages with."
	case loading:
		reach, why = model.ReachNone, "Reading the rollout; this session is available once that finishes."
	}
	return model.Session{
		Key:           Key(t.id),
		Harness:       model.Codex,
		ID:            t.id,
		PID:           c.p.PID,
		ProcStart:     c.p.Start,
		StartedAt:     started,
		Cwd:           cwd,
		Project:       proc.ProjectOf(cwd),
		Title:         truncate(oneLine(title), 80),
		Model:         firstNonEmpty(r.model, t.meta.Model),
		Status:        status,
		StatusSince:   r.busySince,
		Reach:         reach,
		ReachWhy:      why,
		ContextTokens: r.contextTokens,
		LastCall:      r.lastCall,
		LastHuman:     r.lastHuman,
		CacheTTL:      model.Duration(CacheTTL),
		Totals:        r.totals,
		CodexBin:      bin,
		CodexHome:     t.home,
	}
}

// codexBin resolves the executable to run `codex queue` with: the live
// process's binary if it still exists, else "codex" on PATH.
func (s *Source) codexBin(exe string) string {
	if exe != "" && !strings.HasSuffix(exe, " (deleted)") && slices.Contains(binNames, baseName(exe)) {
		if st, err := os.Stat(exe); err == nil && !st.IsDir() {
			return exe
		}
	}
	if p, err := s.sys.LookPath("codex"); err == nil {
		return p
	}
	return ""
}

// Owns reports whether pid (started at start) still runs thread id, by the
// same rule Scan used, evaluated afresh. The Deliverer uses it on systems
// without an open-file table.
func (s *Source) Owns(pid int, start, home, id string) bool {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	now := s.opts.Now()
	s.mu.Lock()
	delete(s.guesses, pid) // force a fresh guess for this process
	s.mu.Unlock()
	for _, c := range s.candidates(now) {
		if c.p.PID == pid && (start == "" || c.p.Start == start) {
			_, ok := c.threads[strings.ToLower(id)]
			return ok
		}
	}
	return false
}

// Activity returns the thread's events with Seq > after, oldest first.
func (s *Source) Activity(key string, after int64) []model.Activity {
	id, ok := strings.CutPrefix(key, string(model.Codex)+":")
	if !ok {
		return nil
	}
	s.mu.Lock()
	t := s.threads[id]
	if t == nil {
		s.mu.Unlock()
		return nil
	}
	ro := t.ro
	if ro == nil {
		// No rollout feeds the log now, and a new one is only attached
		// under s.mu, so reading it here does not race.
		defer s.mu.Unlock()
		return t.log.Since(after)
	}
	s.mu.Unlock()
	return ro.activity(after)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
