//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach puts cmd in a session of its own, so the server outlives the
// terminal `open` ran in.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
