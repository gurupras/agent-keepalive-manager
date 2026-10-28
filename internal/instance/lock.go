package instance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrLocked is returned by AcquireSchedulerLock when another server holds
// the lock.
var ErrLocked = errors.New("another agent-keepalive server runs the scheduler")

// AgentDirs are the agent config directories whose sessions a scheduler
// pings. Two servers that see the same directories must not both run a
// scheduler (architecture.md, rule 11), whatever their data directories.
type AgentDirs struct {
	Claude string // $CLAUDE_CONFIG_DIR, default ~/.claude
	Codex  string // $CODEX_HOME, default ~/.codex
}

// CurrentAgentDirs resolves the agent config directories of this process's
// environment.
func CurrentAgentDirs() AgentDirs {
	home, _ := os.UserHomeDir()
	d := AgentDirs{Claude: os.Getenv("CLAUDE_CONFIG_DIR"), Codex: os.Getenv("CODEX_HOME")}
	if d.Claude == "" {
		d.Claude = filepath.Join(home, ".claude")
	}
	if d.Codex == "" {
		d.Codex = filepath.Join(home, ".codex")
	}
	return d
}

// key is a stable name for the pair: each path made absolute, symlinks
// resolved where the directory exists, and case folded on the OSes whose
// file systems usually ignore case, so spellings of one directory agree.
func (d AgentDirs) key() string {
	h := sha256.New()
	for _, p := range []string{d.Claude, d.Codex} {
		h.Write([]byte(canonical(p)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func canonical(p string) string {
	if p == "" {
		return ""
	}
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		p = strings.ToLower(p)
	}
	return p
}

// LockDir is where scheduler locks live: under the user's default data
// directory, never under --data-dir, so that servers with different data
// directories still meet at the same lock.
func LockDir() (string, error) {
	d, err := DefaultDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "locks"), nil
}

// Holder describes the server holding a scheduler lock, so a second server
// can proxy to it.
type Holder struct {
	PID     int    `json:"pid"`
	Port    int    `json:"port"`
	DataDir string `json:"dataDir"`
}

// SchedulerLock is held by the one server that runs the scheduler for a
// set of agent directories. The OS releases it when the process exits, so
// a crash never leaves a stale lock.
type SchedulerLock struct {
	f        *os.File
	infoPath string
}

// AcquireSchedulerLock takes the scheduler lock for dirs in lockDir
// without waiting. It returns ErrLocked when another process holds it.
func AcquireSchedulerLock(lockDir string, dirs AgentDirs) (*SchedulerLock, error) {
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return nil, err
	}
	base := filepath.Join(lockDir, "scheduler-"+dirs.key())
	f, err := os.OpenFile(base+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := tryLock(f); err != nil {
		f.Close()
		if errors.Is(err, ErrLocked) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("lock %s: %w", f.Name(), err)
	}
	return &SchedulerLock{f: f, infoPath: base + ".json"}, nil
}

// Publish records where the holder serves, for ReadHolder. It is a
// separate file because Windows forbids reading a locked range.
func (l *SchedulerLock) Publish(h Holder) error {
	b, _ := json.Marshal(h)
	return writeFileAtomic(l.infoPath, b, 0o600)
}

// Release gives up the lock.
func (l *SchedulerLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	os.Remove(l.infoPath)
	err := l.f.Close() // closing the handle releases the lock
	l.f = nil
	return err
}

// ReadHolder returns the published holder of the lock for dirs, confirmed
// by a health check on its port.
func ReadHolder(ctx context.Context, lockDir string, dirs AgentDirs) (Holder, bool) {
	b, err := os.ReadFile(filepath.Join(lockDir, "scheduler-"+dirs.key()+".json"))
	if err != nil {
		return Holder{}, false
	}
	var h Holder
	if json.Unmarshal(b, &h) != nil || h.Port == 0 {
		return Holder{}, false
	}
	if _, ok := Probe(ctx, h.Port); !ok {
		return Holder{}, false
	}
	return h, true
}
