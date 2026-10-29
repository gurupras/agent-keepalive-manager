// Package claude discovers live Claude Code sessions and delivers keepalive
// prompts to them.
//
// Discovery reads the per-process registry the CLI maintains under
// <config>/sessions/<pid>.json and tails each session's transcript at
// <config>/projects/<dir>/<sessionId>.jsonl with internal/tail. Delivery
// writes one message to the session's own messaging socket; nothing is ever
// typed into a terminal.
//
// A transcript whose first read is large is read in the background. Until
// that read finishes the session is listed with Reach "none" and ReachWhy
// saying so, and its activity is not published, so the scheduler neither
// pings it on partial data nor mistakes it for an exited session.
package claude

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/proc"
	"github.com/gurupras/agent-keepalive-manager/internal/tail"
)

// Options configure a Source. Zero values select defaults.
type Options struct {
	// ConfigDir is the Claude Code config dir: $CLAUDE_CONFIG_DIR, else ~/.claude.
	ConfigDir string
	Log       *slog.Logger
	Now       func() time.Time
	// PeerName is the from-name our pings carry (default "keepalive").
	PeerName string
	// DefaultWriteTTL is assumed for cache writes reported without a 5m/1h
	// breakdown, and for sessions that have written nothing yet (default
	// 1h, which is what Claude Code requests).
	DefaultWriteTTL time.Duration
	// MaxActivities bounds the events kept per session (default 20000).
	MaxActivities int
	// Retain keeps the state of sessions that disappeared for this long, so
	// callers can still read their activity (default 15m).
	Retain time.Duration
	// Tail tunes transcript reading (see internal/tail).
	Tail tail.Options
	// Live decides whether a registry entry's pid is still the process that
	// wrote it and returns its proc.StartTime token (default: liveStart).
	Live func(e regEntry) (procStart string, ok bool)
}

// DefaultConfigDir returns $CLAUDE_CONFIG_DIR or ~/.claude.
func DefaultConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

func (o Options) withDefaults() Options {
	if o.ConfigDir == "" {
		o.ConfigDir = DefaultConfigDir()
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().Round(0) }
	}
	if o.PeerName == "" {
		o.PeerName = DefaultPeerName
	}
	if o.DefaultWriteTTL <= 0 {
		o.DefaultWriteTTL = time.Hour
	}
	if o.MaxActivities <= 0 {
		o.MaxActivities = tail.DefaultMaxActivities
	}
	if o.Retain <= 0 {
		o.Retain = 15 * time.Minute
	}
	if o.Live == nil {
		o.Live = liveStart
	}
	return o
}

// Source implements model.Source for Claude Code.
type Source struct {
	opts Options
	log  *slog.Logger

	scanMu sync.Mutex // serializes Scan

	mu       sync.Mutex
	byID     map[string]*tracked // sessionId -> state
	warnedAt map[string]time.Time
}

type tracked struct {
	t           *transcript       // nil until the transcript file exists
	log         *tail.ActivityLog // outlives t, so Seq keeps counting if the file is relocated
	lastSeen    time.Time
	lookedAt    time.Time // last glob for the transcript
	status      model.Status
	statusSince time.Time
}

var _ model.Source = (*Source)(nil)

// globEvery limits how often a missing transcript is searched for across
// all project folders (transcripts are created lazily on the first prompt).
const globEvery = 30 * time.Second

// NewSource returns a Source; see Options for defaults.
func NewSource(opts Options) *Source {
	opts = opts.withDefaults()
	return &Source{
		opts:     opts,
		log:      opts.Log.With("harness", "claude"),
		byID:     map[string]*tracked{},
		warnedAt: map[string]time.Time{},
	}
}

func (s *Source) Harness() model.Harness { return model.Claude }

// Key returns the session key for a Claude sessionId.
func Key(sessionID string) string { return string(model.Claude) + ":" + sessionID }

// Scan re-reads the registry and tails the transcripts of live sessions.
func (s *Source) Scan(ctx context.Context) ([]model.Session, error) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()

	entries, errs := readRegistry(filepath.Join(s.opts.ConfigDir, "sessions"))
	for _, err := range errs {
		s.log.Debug("registry entry skipped", "err", err)
	}
	if len(entries) == 0 && len(errs) == 1 && !errors.Is(errs[0], fs.ErrNotExist) {
		return nil, errs[0]
	}
	now := s.opts.Now()
	var out []model.Session
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if !e.interactive() {
			continue
		}
		start, ok := s.opts.Live(e)
		if !ok {
			continue
		}
		tr := s.track(e.SessionID, e.Cwd, now)
		loading := false
		if tr.t != nil {
			var err error
			loading, err = tr.t.poll()
			if errors.Is(err, fs.ErrNotExist) {
				s.mu.Lock()
				tr.t = nil // moved or deleted: locate it again
				s.mu.Unlock()
			} else if err != nil {
				s.warn(tr.t.path, "transcript read failed", err)
			}
		}
		out = append(out, s.session(e, start, tr, loading, now))
	}
	s.gc(now)
	return out, nil
}

// track returns the state for a session, locating its transcript if needed.
func (s *Source) track(id, cwd string, now time.Time) *tracked {
	s.mu.Lock()
	tr := s.byID[id]
	if tr == nil {
		tr = &tracked{log: tail.NewActivityLog(s.opts.MaxActivities)}
		s.byID[id] = tr
	}
	tr.lastSeen = now
	need := tr.t == nil
	glob := need && now.Sub(tr.lookedAt) >= globEvery
	if glob {
		tr.lookedAt = now
	}
	s.mu.Unlock()
	if need {
		if p := s.findTranscript(id, cwd, glob); p != "" {
			// A transcript found again after it went missing starts a new
			// read; tell consumers with an ActReset.
			relocated := tr.log.Seq() > 0
			t := newTranscript(p, s.opts.PeerName, s.opts.DefaultWriteTTL, tr.log, s.opts.Tail)
			if relocated {
				t.Reset()
			}
			s.mu.Lock()
			tr.t = t
			s.mu.Unlock()
		}
	}
	return tr
}

// findTranscript locates <projects>/*/<id>.jsonl. The folder derived from
// cwd is tried first; resumed sessions keep their original folder, so the
// glob is the fallback. If several match, the most recently modified wins.
func (s *Source) findTranscript(id, cwd string, glob bool) string {
	projects := filepath.Join(s.opts.ConfigDir, "projects")
	if cwd != "" {
		p := filepath.Join(projects, projectDirName(cwd), id+".jsonl")
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	if !glob {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(projects, "*", globEscape(id)+".jsonl"))
	var best string
	var bestMod time.Time
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if best == "" || fi.ModTime().After(bestMod) {
			best, bestMod = m, fi.ModTime()
		}
	}
	return best
}

// projectDirName mirrors how Claude Code names a project folder: every
// character outside [A-Za-z0-9] becomes '-' (so `C:\src\x` is `C--src-x`).
func projectDirName(cwd string) string {
	b := []byte(cwd)
	for i, c := range b {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

// globEscape quotes glob metacharacters. On Windows the backslash is a path
// separator, not an escape, so the bracket form is used throughout.
func globEscape(s string) string {
	r := strings.NewReplacer(`*`, `[*]`, `?`, `[?]`, `[`, `[[]`)
	return r.Replace(s)
}

// statusOf maps the registry status. Only "idle" (and "", which a session
// that never had a turn reports) is idle; every other state (busy,
// compacting, waiting for a permission answer, ...) is treated as working,
// so nothing is pinged into it.
func statusOf(reg string) model.Status {
	switch reg {
	case "", "idle":
		return model.StatusIdle
	}
	return model.StatusWorking
}

// session builds the model view of one registry entry.
func (s *Source) session(e regEntry, procStart string, tr *tracked, loading bool, now time.Time) model.Session {
	ss := model.Session{
		Key:       Key(e.SessionID),
		Harness:   model.Claude,
		ID:        e.SessionID,
		PID:       e.PID,
		ProcStart: procStart,
		StartedAt: msTime(e.StartedAt),
		Cwd:       e.Cwd,
		Project:   proc.ProjectOf(e.Cwd),
		CacheTTL:  model.Duration(s.opts.DefaultWriteTTL),
	}
	ss.Reach, ss.ReachWhy = reachOf(e.MessagingSocketPath)
	if ss.Reach == model.ReachSocket {
		ss.SocketPath = e.MessagingSocketPath
	}
	status := statusOf(e.Status)

	s.mu.Lock()
	t := tr.t
	s.mu.Unlock()
	var p summary
	if t != nil {
		p = t.published()
		if e.Status == "" && p.toolPending {
			status = model.StatusWorking
		}
	}
	ss.Title = title(p.customTitle, e, p.aiTitle)
	ss.Summary = p.awaySummary
	ss.Model = p.lastModel
	ss.ContextTokens = p.lastUsage.Context()
	ss.LastCall = p.lastCall
	ss.LastHuman = p.lastHuman
	ss.Totals = p.totals
	if p.ttl > 0 {
		ss.CacheTTL = model.Duration(p.ttl)
	}
	if loading {
		ss.Reach, ss.ReachWhy, ss.SocketPath = model.ReachNone, "Reading the transcript; this session is available once that finishes.", ""
	}

	// StatusSince: the registry's own timestamp when the status is the
	// registry's, else the scan that first saw the current status.
	s.mu.Lock()
	if tr.status != status || tr.statusSince.IsZero() {
		tr.status, tr.statusSince = status, now
	}
	since := tr.statusSince
	s.mu.Unlock()
	if at := msTime(e.StatusUpdatedAt); !at.IsZero() && status == statusOf(e.Status) {
		since = at
	}
	ss.Status, ss.StatusSince = status, since
	return ss
}

// reachOf says whether a messaging socket path can be used.
func reachOf(path string) (model.Reach, string) {
	if path == "" {
		return model.ReachNone, "This Claude Code session has no messaging socket (it needs a newer Claude Code)."
	}
	if !socketExists(path) {
		return model.ReachNone, "The session's messaging socket is missing."
	}
	return model.ReachSocket, ""
}

// title picks the best display name: an explicit /rename (custom-title or a
// user-set registry name), then the AI title, then the derived name.
func title(custom string, e regEntry, ai string) string {
	switch {
	case custom != "":
		return custom
	case e.NameSource == "user" && e.Name != "":
		return e.Name
	case ai != "":
		return ai
	}
	return e.Name
}

// gc forgets sessions not seen for longer than Retain.
func (s *Source) gc(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, tr := range s.byID {
		if now.Sub(tr.lastSeen) > s.opts.Retain {
			delete(s.byID, id)
		}
	}
	for k, at := range s.warnedAt {
		if now.Sub(at) > time.Hour {
			delete(s.warnedAt, k)
		}
	}
}

// warn logs at most once an hour per subject.
func (s *Source) warn(subject, msg string, err error) {
	s.mu.Lock()
	_, done := s.warnedAt[subject]
	if !done {
		s.warnedAt[subject] = s.opts.Now()
	}
	s.mu.Unlock()
	if !done {
		s.log.Warn(msg, "path", subject, "err", err)
	}
}

// Activity returns the session's events with Seq > after, oldest first.
func (s *Source) Activity(key string, after int64) []model.Activity {
	id, ok := strings.CutPrefix(key, string(model.Claude)+":")
	if !ok {
		return nil
	}
	s.mu.Lock()
	var t *transcript
	if tr := s.byID[id]; tr != nil {
		t = tr.t
	}
	s.mu.Unlock()
	if t == nil {
		return nil
	}
	return t.activity(after)
}
