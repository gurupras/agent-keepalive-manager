package proc

import (
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows keeps another process's environment, working directory and
// command line in its PEB, and open files behind privileged handle queries;
// none of that is read here. Callers fall back to transcript metadata.
const hasOpenFiles = false

func startTime(pid int) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil || code != 259 { // STILL_ACTIVE
		return ""
	}
	var creation, exit, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &creation, &exit, &kernel, &user) != nil {
		return ""
	}
	return strconv.FormatInt(creation.Nanoseconds(), 10)
}

func list() []Proc {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var out []Proc
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		pid := int(e.ProcessID)
		if pid == 0 {
			continue
		}
		name := windows.UTF16ToString(e.ExeFile[:])
		p := Proc{PID: pid, Args: []string{name}, Start: startTime(pid), PPID: int(e.ParentProcessID)}
		if exe := imagePath(pid); exe != "" {
			p.Exe = exe
		}
		out = append(out, p)
	}
	return out
}

func imagePath(pid int) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func env(int, string) (string, bool) { return "", false }

func cwd(int) string { return "" }

func openFiles(int) []string { return nil }
