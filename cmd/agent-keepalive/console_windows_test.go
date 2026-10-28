//go:build windows

package main

import (
	"os"
	"testing"
)

// A console-subsystem test binary already has its streams: attaching must
// change nothing.
func TestAttachParentConsoleNoop(t *testing.T) {
	in, out, errf := os.Stdin, os.Stdout, os.Stderr
	attachParentConsole()
	if os.Stdin != in || os.Stdout != out || os.Stderr != errf {
		t.Fatal("standard streams replaced although they were usable")
	}
}
