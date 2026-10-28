package instance

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

func TestDefaultDataDir(t *testing.T) {
	home := func() (string, error) { return "/home/u", nil }
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"linux", nil, filepath.Join("/home/u", ".local", "state", Name)},
		{"linux", map[string]string{"XDG_STATE_HOME": "/xdg"}, filepath.Join("/xdg", Name)},
		{"linux", map[string]string{"XDG_STATE_HOME": "relative"}, filepath.Join("/home/u", ".local", "state", Name)},
		{"darwin", nil, filepath.Join("/home/u", "Library", "Application Support", Name)},
		{"windows", map[string]string{"LOCALAPPDATA": "/lad"}, filepath.Join("/lad", Name)},
	}
	for _, c := range cases {
		got, err := defaultDataDir(c.goos, env(c.env), home)
		if err != nil || got != c.want {
			t.Errorf("%s %v: got %q %v, want %q", c.goos, c.env, got, err, c.want)
		}
	}
}

func TestDataDirPrecedence(t *testing.T) {
	t.Setenv(EnvDataDir, "/from-env")
	if d, _ := DataDir("/from-flag", ""); d != filepath.Clean("/from-flag") && runtime.GOOS != "windows" {
		t.Fatalf("flag: %q", d)
	}
	if d, _ := DataDir("", "windows-portable"); d != filepath.Clean("/from-env") && runtime.GOOS != "windows" {
		t.Fatalf("env: %q", d)
	}
	t.Setenv(EnvDataDir, "")
	d, _ := DataDir("", "windows-portable")
	if filepath.Base(d) != PortableDataDir {
		t.Fatalf("portable: %q", d)
	}
}

func TestListen(t *testing.T) {
	// Exact port.
	ln, port, err := Listen(0)
	if err != nil {
		t.Skipf("default ports busy: %v", err)
	}
	defer ln.Close()
	if port < DefaultPort || port >= DefaultPort+PortTries {
		t.Fatalf("port %d", port)
	}
	// The same port again: exact fails, default moves on.
	if _, _, err := Listen(port); err == nil {
		t.Fatal("exact busy port must fail")
	}
	ln2, port2, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln2.Close()
	if port2 == port {
		t.Fatal("same port twice")
	}
	if a := ln2.Addr().(*net.TCPAddr); !a.IP.IsLoopback() {
		t.Fatalf("bound %v", a)
	}
}

func TestPortFromEnv(t *testing.T) {
	t.Setenv("PORT", "5555")
	if p, err := PortFromEnv(); p != 5555 || err != nil {
		t.Fatal(p, err)
	}
	t.Setenv("PORT", "x")
	if _, err := PortFromEnv(); err == nil {
		t.Fatal("bad PORT accepted")
	}
}

func TestPortFileAndFindRunning(t *testing.T) {
	dir := t.TempDir()
	if _, ok := FindRunning(context.Background(), dir); ok {
		t.Fatal("found without a port file")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(Health{Name: Name, Version: "1.2.3", Port: port})
	}))
	srv.Listener = ln
	srv.Start()
	defer srv.Close()

	if err := WritePortFile(dir, port); err != nil {
		t.Fatal(err)
	}
	r, ok := FindRunning(context.Background(), dir)
	if !ok || r.Version != "1.2.3" || r.URL != "http://127.0.0.1:"+strconv.Itoa(port)+"/" {
		t.Fatalf("%+v %v", r, ok)
	}
	RemovePortFile(dir, port+1) // not ours: kept
	if _, err := ReadPortFile(dir); err != nil {
		t.Fatal("removed a port file naming another port")
	}
	RemovePortFile(dir, port)
	if _, err := ReadPortFile(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("port file not removed")
	}
}

func TestToken(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "d")
	a, err := Token(dir)
	if err != nil || len(a) != 64 {
		t.Fatalf("%q %v", a, err)
	}
	b, err := Token(dir)
	if err != nil || a != b {
		t.Fatal("token changed")
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(dir, TokenFile))
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode())
		}
		os.Chmod(filepath.Join(dir, TokenFile), 0o644)
		Token(dir)
		fi, _ = os.Stat(filepath.Join(dir, TokenFile))
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("not tightened: %v", fi.Mode())
		}
	}
	if r, err := ReadToken(dir); err != nil || r != a {
		t.Fatal(r, err)
	}
}

func TestSchedulerLock(t *testing.T) {
	lockDir := t.TempDir()
	base := t.TempDir()
	dirs := AgentDirs{Claude: filepath.Join(base, ".claude"), Codex: filepath.Join(base, ".codex")}
	l, err := AcquireSchedulerLock(lockDir, dirs)
	if err != nil {
		t.Fatal(err)
	}
	// Another spelling of the same directories collides.
	same := AgentDirs{Claude: filepath.Join(base, "x", "..", ".claude"), Codex: dirs.Codex}
	if _, err := AcquireSchedulerLock(lockDir, same); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock: %v", err)
	}
	// Different agent directories are independent.
	other := AgentDirs{Claude: filepath.Join(base, "other"), Codex: dirs.Codex}
	l2, err := AcquireSchedulerLock(lockDir, other)
	if err != nil {
		t.Fatalf("other dirs: %v", err)
	}
	l2.Release()

	// Publish + ReadHolder with a live health endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Health{Name: Name})
	}))
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	if err := l.Publish(Holder{PID: os.Getpid(), Port: port, DataDir: "/d"}); err != nil {
		t.Fatal(err)
	}
	h, ok := ReadHolder(context.Background(), lockDir, same)
	if !ok || h.Port != port || h.DataDir != "/d" {
		t.Fatalf("%+v %v", h, ok)
	}

	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadHolder(context.Background(), lockDir, dirs); ok {
		t.Fatal("holder still published after release")
	}
	l3, err := AcquireSchedulerLock(lockDir, dirs)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	l3.Release()
}
