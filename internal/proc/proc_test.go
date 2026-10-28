package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// These run against the real process table, so they hold on every OS CI
// covers: the test binary itself is a process we can always see.

func TestSelfIsAlive(t *testing.T) {
	st := StartTime(os.Getpid())
	if st == "" {
		t.Fatal("StartTime(self) is empty")
	}
	if StartTime(os.Getpid()) != st {
		t.Fatal("StartTime(self) is not stable")
	}
	if !Alive(os.Getpid(), st) {
		t.Fatal("Alive(self, start) = false")
	}
	if Alive(os.Getpid(), st+"0") {
		t.Fatal("Alive with a different start token = true")
	}
}

func TestExitedChildIsNotAlive(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait()
	if Alive(pid, "") {
		t.Fatalf("reaped child %d still alive", pid)
	}
}

func TestListFindsSelf(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range List(baseName(exe)) {
		if p.PID == os.Getpid() {
			if p.Start != StartTime(p.PID) {
				t.Errorf("List start %q != StartTime %q", p.Start, StartTime(p.PID))
			}
			return
		}
	}
	t.Fatalf("List(%q) does not include pid %d", baseName(exe), os.Getpid())
}

func TestEnvOfSelf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Env is not read on Windows")
	}
	// The environment of a process is what it was started with, so set the
	// variable for a child rather than for ourselves.
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperSleep$")
	cmd.Env = append(os.Environ(), "PROC_TEST_HELPER=1", "PROC_TEST_VALUE=hello=world")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	for range 50 {
		if v, ok := Env(cmd.Process.Pid, "PROC_TEST_VALUE"); ok {
			if v != "hello=world" {
				t.Fatalf("Env = %q", v)
			}
			return
		}
		sleep()
	}
	t.Fatal("Env never found PROC_TEST_VALUE")
}

func TestHelperSleep(t *testing.T) {
	if os.Getenv("PROC_TEST_HELPER") != "1" {
		t.Skip("helper process")
	}
	for range 100 {
		sleep()
	}
}

func TestBaseName(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/bin/codex":       "codex",
		`C:\Tools\Codex.EXE`:   "codex",
		"/opt/codex (deleted)": "codex",
		"codex":                "codex",
		`C:\a/b\node.exe`:      "node",
	} {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProjectOf(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ProjectOf(sub); got != root {
		t.Errorf("ProjectOf(sub) = %q, want %q", got, root)
	}
	// No marker anywhere up to the volume root: falls back to cwd, and
	// terminates (filepath.Dir of a root is the root itself).
	lone := t.TempDir()
	if got := ProjectOf(lone); got != lone && !hasMarker(got) {
		t.Errorf("ProjectOf(lone) = %q", got)
	}
}
