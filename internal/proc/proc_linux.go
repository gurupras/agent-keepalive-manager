package proc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Root is the procfs mount; tests point it at a fixture tree.
var Root = "/proc"

const hasOpenFiles = true

// startTime is field 22 of /proc/<pid>/stat: the start time in clock ticks
// since boot.
func startTime(pid int) string {
	b, err := os.ReadFile(filepath.Join(Root, strconv.Itoa(pid), "stat"))
	if err != nil {
		return ""
	}
	return statStart(string(b))
}

// statStart extracts field 22 from a stat line. comm (field 2) may contain
// spaces and parentheses, so fields are counted after the last ')'.
func statStart(s string) string {
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return ""
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 { // f[0] is field 3, so field 22 is f[19]
		return ""
	}
	return f[19]
}

func parent(pid int) int {
	b, err := os.ReadFile(filepath.Join(Root, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	return statPPID(string(b))
}

// statPPID extracts field 4, the parent pid, from a stat line.
func statPPID(s string) int {
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(f[1])
	return n
}

func list() []Proc {
	ents, err := os.ReadDir(Root)
	if err != nil {
		return nil
	}
	var out []Proc
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		dir := filepath.Join(Root, e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if err != nil || len(raw) == 0 {
			continue // kernel thread or gone
		}
		exe, _ := os.Readlink(filepath.Join(dir, "exe"))
		stat, _ := os.ReadFile(filepath.Join(dir, "stat"))
		out = append(out, Proc{
			PID:   pid,
			Exe:   exe,
			Args:  strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00"),
			Start: statStart(string(stat)),
			PPID:  statPPID(string(stat)),
		})
	}
	return out
}

func env(pid int, name string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(Root, strconv.Itoa(pid), "environ"))
	if err != nil {
		return "", false
	}
	return lookupEnv(string(b), "\x00", name)
}

func cwd(pid int) string {
	s, _ := os.Readlink(filepath.Join(Root, strconv.Itoa(pid), "cwd"))
	return s
}

func openFiles(pid int) []string {
	dir := filepath.Join(Root, strconv.Itoa(pid), "fd")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		if t, err := os.Readlink(filepath.Join(dir, e.Name())); err == nil {
			out = append(out, t)
		}
	}
	return out
}
