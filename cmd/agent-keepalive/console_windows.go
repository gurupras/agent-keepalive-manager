//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// Windows installs are linked with -H windowsgui so a double-click opens no
// console window. A GUI-subsystem program gets no console, so a CLI command
// typed in a terminal would print nothing. attachParentConsole borrows the
// terminal's console for the streams that have nowhere to go.
//
// It is a no-op for console-subsystem builds (AttachConsole fails: we have a
// console already) and when double-clicked (no parent console). Streams the
// caller redirected (`> file`, a pipe, the server log `open` hands its
// child) are valid handles and are never replaced; when all three are
// valid we do not attach at all, so a background server is never tied to a
// console whose closing would end it.
func init() { attachParentConsole() }

var procAttachConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")

const attachParentProcess = ^uintptr(0) // ATTACH_PARENT_PROCESS, (DWORD)-1

func attachParentConsole() {
	type stream struct {
		std  uint32
		file **os.File
		name string // console device to reopen on
		flag int
	}
	streams := []stream{
		{windows.STD_INPUT_HANDLE, &os.Stdin, "CONIN$", os.O_RDWR},
		{windows.STD_OUTPUT_HANDLE, &os.Stdout, "CONOUT$", os.O_RDWR},
		{windows.STD_ERROR_HANDLE, &os.Stderr, "CONOUT$", os.O_RDWR},
	}
	var missing []stream
	for _, s := range streams {
		if !usable(s.std) {
			missing = append(missing, s)
		}
	}
	if len(missing) == 0 || procAttachConsole.Find() != nil {
		return
	}
	if r, _, _ := procAttachConsole.Call(attachParentProcess); r == 0 {
		return // no parent console, or we already have one
	}
	for _, s := range missing {
		f, err := os.OpenFile(s.name, s.flag, 0)
		if err != nil {
			continue
		}
		_ = windows.SetStdHandle(s.std, windows.Handle(f.Fd()))
		*s.file = f
	}
}

// usable reports whether a standard handle refers to something: a file,
// pipe or console.
func usable(std uint32) bool {
	h, err := windows.GetStdHandle(std)
	if err != nil || h == 0 || h == windows.InvalidHandle {
		return false
	}
	t, err := windows.GetFileType(h)
	return err == nil && t != windows.FILE_TYPE_UNKNOWN
}
