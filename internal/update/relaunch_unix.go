//go:build !windows

package update

import (
	"os/exec"
	"syscall"
)

// detach puts cmd in a session of its own, so it outlives this process and
// its terminal.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// hide does nothing outside Windows.
func hide(*exec.Cmd) {}
