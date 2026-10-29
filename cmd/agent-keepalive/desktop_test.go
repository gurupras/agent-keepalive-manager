package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDesktopShellEnv(t *testing.T) {
	t.Setenv(EnvDesktop, "none")
	if p, err := desktopShell(t.TempDir()); p != "" || err != nil {
		t.Fatalf("none: %q %v", p, err)
	}
	t.Setenv(EnvDesktop, "/opt/x/agent-keepalive-desktop")
	if p, _ := desktopShell(t.TempDir()); p != "/opt/x/agent-keepalive-desktop" {
		t.Fatalf("override: %q", p)
	}
}

func TestWriteEmbeddedDesktop(t *testing.T) {
	saved := embeddedDesktop
	t.Cleanup(func() { embeddedDesktop = saved })
	dir := t.TempDir()
	stale := filepath.Join(dir, "desktop", "0123456789abcdef")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}

	embeddedDesktop = []byte("window v1")
	p1, err := writeEmbeddedDesktop(dir, "agent-keepalive-desktop.exe")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p1); string(b) != "window v1" {
		t.Fatalf("content %q", b)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("an older copy was kept: %v", err)
	}
	if again, err := writeEmbeddedDesktop(dir, "agent-keepalive-desktop.exe"); err != nil || again != p1 {
		t.Fatalf("second write: %q %v", again, err)
	}

	embeddedDesktop = []byte("window v2")
	p2, err := writeEmbeddedDesktop(dir, "agent-keepalive-desktop.exe")
	if err != nil || p2 == p1 {
		t.Fatalf("new build: %q %v", p2, err)
	}
	if _, err := os.Stat(p1); !os.IsNotExist(err) {
		t.Fatalf("the previous build's copy was kept: %v", err)
	}
}

func TestAppBundle(t *testing.T) {
	exe := "/Applications/agent-keepalive.app/Contents/MacOS/agent-keepalive-desktop"
	want := ""
	if runtime.GOOS == "darwin" {
		want = "/Applications/agent-keepalive.app"
	}
	if got := appBundle(exe); got != want {
		t.Fatalf("%q, want %q", got, want)
	}
	if got := appBundle("/usr/lib/agent-keepalive/agent-keepalive-desktop"); got != "" {
		t.Fatalf("not a bundle: %q", got)
	}
}
