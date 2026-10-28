package update

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// relaunchEnv marks a process started by Relaunch with the pid of the
// process it replaces.
const relaunchEnv = "AGENT_KEEPALIVE_RELAUNCHED_BY"

// Relaunch starts exe with args, detached from this process and its
// terminal, marked so that it waits (WaitForPredecessor) until this process
// has released the instance lock. The caller then shuts down.
func Relaunch(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), relaunchEnv+"="+strconv.Itoa(os.Getpid()))
	if wd, err := os.Getwd(); err == nil {
		cmd.Dir = wd
	}
	return startDetached(cmd)
}

// RelaunchedBy returns the pid of the process that started this one with
// Relaunch.
func RelaunchedBy() (pid int, ok bool) {
	pid, err := strconv.Atoi(os.Getenv(relaunchEnv))
	return pid, err == nil && pid > 0
}

// WaitForPredecessor, in a process started by Relaunch, polls free (for
// example: the instance lock can be taken) until it reports true, ctx ends
// or timeout passes, and reports whether the predecessor let go. In any
// other process it returns true at once. It clears the marker so that
// processes this one starts do not wait.
func WaitForPredecessor(ctx context.Context, free func() bool, timeout time.Duration) bool {
	if _, ok := RelaunchedBy(); !ok {
		return true
	}
	os.Unsetenv(relaunchEnv)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for !free() {
		select {
		case <-ctx.Done():
			return free()
		case <-t.C:
		}
	}
	return true
}

// startDetached starts cmd so that it outlives this process, without
// inheriting its standard streams.
func startDetached(cmd *exec.Cmd) error {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
