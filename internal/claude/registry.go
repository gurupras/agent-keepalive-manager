package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/proc"
)

// regEntry is one <config>/sessions/<pid>.json record, written and kept up
// to date by a running Claude Code process. Only the fields we use are
// decoded.
type regEntry struct {
	PID                 int    `json:"pid"`
	SessionID           string `json:"sessionId"`
	Cwd                 string `json:"cwd"`
	StartedAt           int64  `json:"startedAt"` // unix ms
	ProcStart           string `json:"procStart"` // the CLI's own reading of its start time
	Kind                string `json:"kind"`
	Entrypoint          string `json:"entrypoint"`
	Status              string `json:"status"` // idle | busy | other states | "" (never had a turn)
	StatusUpdatedAt     int64  `json:"statusUpdatedAt"`
	UpdatedAt           int64  `json:"updatedAt"`
	Name                string `json:"name"`
	NameSource          string `json:"nameSource"` // derived | user | collision
	MessagingSocketPath string `json:"messagingSocketPath"`
}

// interactive reports whether the entry is a TUI session (not an SDK or -p
// process). Builds that predate the kind field are accepted when they were
// started from the CLI entrypoint.
func (e regEntry) interactive() bool {
	switch e.Kind {
	case "interactive":
		return true
	case "":
		return e.Entrypoint == "" || e.Entrypoint == "cli"
	}
	return false
}

func msTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// readRegistry returns the entries in dir. Only files named <digits>.json
// are opened: the directory also holds per-session key files that must
// never be read. Unparseable files are skipped and reported in errs.
func readRegistry(dir string) (entries []regEntry, errs []error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{err}
	}
	for _, de := range ents {
		stem, ok := strings.CutSuffix(de.Name(), ".json")
		if !ok || de.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(stem)
		if err != nil || pid <= 0 {
			continue
		}
		e, err := readEntry(dir, pid)
		if err != nil {
			if !os.IsNotExist(err) { // removed between ReadDir and ReadFile
				errs = append(errs, err)
			}
			continue
		}
		if e.SessionID != "" {
			entries = append(entries, e)
		}
	}
	return entries, errs
}

// readEntry reads <dir>/<pid>.json. A record naming another pid is treated
// as missing.
func readEntry(dir string, pid int) (regEntry, error) {
	name := strconv.Itoa(pid) + ".json"
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return regEntry{}, err
	}
	var e regEntry
	if err := json.Unmarshal(b, &e); err != nil {
		// A file being rewritten can be momentarily truncated.
		return regEntry{}, fmt.Errorf("%s: %w", name, err)
	}
	if e.PID == 0 {
		e.PID = pid
	}
	if e.PID != pid {
		return regEntry{}, fmt.Errorf("%s: names pid %d: %w", name, e.PID, os.ErrNotExist)
	}
	return e, nil
}

// startSlack absorbs the difference between when the OS says a process
// started and when the CLI first wrote its registry record.
const startSlack = 2 * time.Second

// liveStart decides whether the registry entry's pid is still the process
// that wrote it, and returns that process's proc.StartTime token.
//
// On Linux the CLI records the same token proc.StartTime reads (field 22
// of /proc/<pid>/stat), so the two must be equal. Elsewhere the CLI's
// reading need not be in our format. Then the token is converted to wall
// time where its format allows (macOS "sec.usec", Windows nanoseconds
// since the epoch) and the process must have started no later than the
// entry's startedAt: a pid reused by a later process started after the
// record was written. With no usable start information on either side the
// pid being alive is all that can be checked.
func liveStart(e regEntry) (string, bool) {
	st := proc.StartTime(e.PID)
	if st == "" {
		return "", false
	}
	if e.ProcStart != "" && e.ProcStart == st {
		return st, true
	}
	if e.ProcStart != "" && runtime.GOOS == "linux" {
		return "", false // comparable formats that differ: the pid was reused
	}
	if wall, ok := startWall(runtime.GOOS, st); ok && e.StartedAt > 0 {
		if wall.After(msTime(e.StartedAt).Add(startSlack)) {
			return "", false
		}
	}
	return st, true
}

// startWall converts a proc.StartTime token to wall-clock time where the
// OS's format allows it.
func startWall(goos, tok string) (time.Time, bool) {
	switch goos {
	case "darwin": // "<sec>.<usec>"
		sec, usec, ok := strings.Cut(tok, ".")
		s, err1 := strconv.ParseInt(sec, 10, 64)
		u, err2 := strconv.ParseInt(usec, 10, 64)
		if !ok || err1 != nil || err2 != nil {
			return time.Time{}, false
		}
		return time.Unix(s, u*1000), true
	case "windows": // nanoseconds since the Unix epoch
		ns, err := strconv.ParseInt(tok, 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(0, ns), true
	}
	return time.Time{}, false
}
