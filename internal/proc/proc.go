// Package proc inspects running processes: whether a pid is still the
// process it was, which processes run a given program, and (where the OS
// allows) a process's working directory, open files and one environment
// variable.
//
// Each OS has its own implementation (proc_linux.go, proc_darwin.go,
// proc_windows.go). What an OS cannot provide comes back empty, never as an
// error, and callers fall back to something else.
package proc

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Proc is a running process.
type Proc struct {
	PID   int
	Exe   string   // absolute path of the executable, "" if unknown
	Args  []string // argv; at least the program name when known
	Start string   // opaque start-time token, see StartTime
}

// StartTime returns an opaque token for when pid started, or "" if no such
// process is running. Two calls for the same process return the same token;
// a process that reused the pid returns a different one.
func StartTime(pid int) string {
	if pid <= 0 {
		return ""
	}
	return startTime(pid)
}

// Alive reports whether pid is running and, if start is not empty, is the
// same process whose StartTime was start.
func Alive(pid int, start string) bool {
	st := StartTime(pid)
	return st != "" && (start == "" || st == start)
}

// List returns the processes whose executable or argv[0] base name, without
// an .exe suffix, is one of names.
func List(names ...string) []Proc {
	want := map[string]bool{}
	for _, n := range names {
		want[strings.ToLower(n)] = true
	}
	var out []Proc
	for _, p := range list() {
		if want[baseName(p.Exe)] || (len(p.Args) > 0 && want[baseName(p.Args[0])]) {
			out = append(out, p)
		}
	}
	return out
}

// baseName is the lower-cased file name of a path (either separator) without
// a trailing " (deleted)" or ".exe".
func baseName(p string) string {
	p = strings.TrimSuffix(p, " (deleted)")
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	return strings.TrimSuffix(strings.ToLower(p), ".exe")
}

// Env returns the value of one environment variable of pid, if the OS lets
// us read it. Only that variable is extracted; nothing else is retained.
func Env(pid int, name string) (string, bool) { return env(pid, name) }

// Cwd returns the working directory of pid, or "".
func Cwd(pid int) string { return cwd(pid) }

// OpenFiles returns the paths pid holds open, or nil where the OS does not
// expose them (only Linux does, without privileges).
func OpenFiles(pid int) []string { return openFiles(pid) }

// HasOpenFiles reports whether OpenFiles works on this OS.
func HasOpenFiles() bool { return hasOpenFiles }

var (
	projMu    sync.Mutex
	projCache = map[string]string{}
)

// projectMarkers identify a project root.
var projectMarkers = []string{".git", ".hg", ".svn", ".jj"}

// ProjectOf returns the nearest ancestor of cwd (cwd included) that holds a
// VCS marker, never the user's home directory itself (often a dotfiles
// repository). It falls back to cwd. Results are cached.
func ProjectOf(cwd string) string {
	if cwd == "" {
		return ""
	}
	projMu.Lock()
	defer projMu.Unlock()
	if p, ok := projCache[cwd]; ok {
		return p
	}
	home, _ := os.UserHomeDir()
	found := cwd
	for d := filepath.Clean(cwd); ; {
		if d == home {
			break
		}
		if hasMarker(d) {
			found = d
			break
		}
		parent := filepath.Dir(d)
		if parent == d { // "/" or a volume root such as `C:\`
			break
		}
		d = parent
	}
	projCache[cwd] = found
	return found
}

func hasMarker(dir string) bool {
	for _, m := range projectMarkers {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}
