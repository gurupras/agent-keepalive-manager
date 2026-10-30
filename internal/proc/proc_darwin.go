package proc

import (
	"bytes"
	"context"
	"encoding/binary"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// macOS exposes no open-file or cwd table without libproc (cgo), so those
// come from lsof, which every macOS install ships.
var hasOpenFiles = lsofPath() != ""

func startTime(pid int) string {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || k == nil || int(k.Proc.P_pid) != pid {
		return ""
	}
	t := k.Proc.P_starttime
	return strconv.FormatInt(int64(t.Sec), 10) + "." + strconv.FormatInt(int64(t.Usec), 10)
}

func list() []Proc {
	ks, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}
	out := make([]Proc, 0, len(ks))
	for _, k := range ks {
		pid := int(k.Proc.P_pid)
		if pid <= 0 {
			continue
		}
		t := k.Proc.P_starttime
		p := Proc{PID: pid, PPID: int(k.Eproc.Ppid), Start: strconv.FormatInt(int64(t.Sec), 10) + "." + strconv.FormatInt(int64(t.Usec), 10)}
		if exe, args, _, ok := procArgs(pid); ok {
			p.Exe, p.Args = exe, args
		} else {
			// Another user's process: the name is all we may see.
			p.Args = []string{unix.ByteSliceToString(k.Proc.P_comm[:])}
		}
		out = append(out, p)
	}
	return out
}

// procArgs reads KERN_PROCARGS2: a 32-bit argc, the executable path, NUL
// padding, argc arguments, then the environment, all NUL-terminated.
func procArgs(pid int) (exe string, args, envv []string, ok bool) {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(b) < 4 {
		return "", nil, nil, false
	}
	argc := int(binary.LittleEndian.Uint32(b[:4]))
	b = b[4:]
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return "", nil, nil, false
	}
	exe, b = string(b[:i]), b[i:]
	for len(b) > 0 && b[0] == 0 {
		b = b[1:]
	}
	parts := strings.Split(string(b), "\x00")
	if argc > len(parts) {
		argc = len(parts)
	}
	args = parts[:argc]
	for _, kv := range parts[argc:] {
		if kv == "" {
			break
		}
		envv = append(envv, kv)
	}
	return exe, args, envv, true
}

func env(pid int, name string) (string, bool) {
	_, _, envv, ok := procArgs(pid)
	if !ok {
		return "", false
	}
	return lookupEnv(strings.Join(envv, "\x00"), "\x00", name)
}

func cwd(pid int) string {
	names := lsof(pid, "-d", "cwd")
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func openFiles(pid int) []string { return lsof(pid) }

func lsofPath() string {
	for _, p := range []string{"/usr/sbin/lsof", "/usr/bin/lsof"} {
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
	}
	return ""
}

// lsof lists the file names pid holds (-Fn prints one "n<path>" line per file).
func lsof(pid int, extra ...string) []string {
	bin := lsofPath()
	if bin == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	args := append([]string{"-n", "-P", "-w", "-a", "-p", strconv.Itoa(pid)}, extra...)
	out, err := exec.CommandContext(ctx, bin, append(args, "-Fn")...).Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	var names []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(line, "n/") {
			names = append(names, line[1:])
		}
	}
	return names
}
