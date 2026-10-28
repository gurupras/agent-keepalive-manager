package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
)

func testKey(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(pub), priv
}

func sampleFeed() Feed {
	return Feed{
		Version:     "1.5.0",
		PublishedAt: "2026-10-27T12:00:00Z",
		Notes:       "## Changes\n- <fixed> & improved  ",
		Page:        "https://example.com/releases/v1.5.0",
		Artifacts: []Artifact{{
			Kind: KindAppImage, OS: "linux", Arch: "amd64",
			Name: "agent-keepalive-1.5.0-x86_64.AppImage", URL: "https://example.com/a",
			SHA256: strings.Repeat("ab", 32), Size: 12345678,
		}},
	}
}

func TestFeedSignVerify(t *testing.T) {
	pub, priv := testKey(t)
	f := sampleFeed()
	if err := f.Sign(priv); err != nil {
		t.Fatal(err)
	}
	// What the tool writes (indented) verifies after a round trip.
	b, _ := json.MarshalIndent(f, "", "  ")
	got, err := ParseFeed(b, pub)
	if err != nil {
		t.Fatalf("good feed refused: %v", err)
	}
	if got.Version != "1.5.0" || len(got.Artifacts) != 1 {
		t.Fatalf("parsed %+v", got)
	}

	t.Run("tampered", func(t *testing.T) {
		g := f
		g.Artifacts = []Artifact{f.Artifacts[0]}
		g.Artifacts[0].SHA256 = strings.Repeat("cd", 32)
		b, _ := json.Marshal(g)
		if _, err := ParseFeed(b, pub); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("tampered feed: %v", err)
		}
	})
	t.Run("wrong key", func(t *testing.T) {
		other, _ := testKey(t)
		if _, err := ParseFeed(b, other); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("wrong key: %v", err)
		}
	})
	t.Run("missing key refused", func(t *testing.T) {
		if _, err := ParseFeed(b, ""); !errors.Is(err, ErrNoPublicKey) {
			t.Fatalf("no key: %v", err)
		}
	})
	t.Run("unsigned refused", func(t *testing.T) {
		g := f
		g.Signature = ""
		b, _ := json.Marshal(g)
		if _, err := ParseFeed(b, pub); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("unsigned: %v", err)
		}
		if _, err := ParseFeed(b, ""); err == nil {
			t.Fatal("unsigned feed accepted without a key")
		}
	})
	t.Run("canonical field order", func(t *testing.T) {
		c, _ := f.Canonical()
		want := `{"version":"1.5.0","publishedAt":"2026-10-27T12:00:00Z","notes":`
		if !strings.HasPrefix(string(c), want) || strings.Contains(string(c), "signature") {
			t.Fatalf("canonical: %s", c)
		}
		if !strings.Contains(string(c), `{"kind":"appimage","os":"linux","arch":"amd64","name":`) {
			t.Fatalf("artifact order: %s", c)
		}
	})
}

func TestVersions(t *testing.T) {
	order := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0"}
	for i := range order {
		for j := range order {
			a, _ := ParseVersion(order[i])
			b, ok := ParseVersion(order[j])
			if !ok {
				t.Fatalf("parse %s", order[j])
			}
			if got, want := a.Compare(b), cmpInt(i, j); got != want {
				t.Errorf("%s vs %s = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	for _, bad := range []string{"", "1.0", "1.0.0.0", "a.b.c", "1.0.0-", "01.0.0", "1..0"} {
		if _, ok := ParseVersion(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"1.5.0", "1.4.0", true},
		{"v1.5.0", "1.4.0", true},
		{"1.4.0", "1.4.0", false},
		{"1.3.9", "1.4.0", false},
		{"1.5.0-rc.1", "1.4.0", false}, // stable builds never get prereleases
		{"1.5.0-rc.2", "1.5.0-rc.1", true},
		{"1.5.0", "1.5.0-rc.1", true},
		{"1.6.0-beta", "1.5.0-rc.1", true},
		{"1.5.0", "dev", false},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.latest, c.current, got)
		}
	}
}

func TestDetectInstall(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "Cellar", "agent-keepalive", "1.4.0", "bin")
	libexec := filepath.Join(dir, "Cellar", "agent-keepalive", "1.4.0", "libexec")
	os.MkdirAll(bin, 0o755)
	os.MkdirAll(libexec, 0o755)
	exe := filepath.Join(bin, "agent-keepalive")
	os.WriteFile(exe, []byte("x"), 0o755)
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	none := env(nil)

	if in := detectInstall(KindArchive, exe, none); in.Kind != KindArchive || in.Target != exe {
		t.Fatalf("linked: %+v", in)
	}
	if in := detectInstall("bogus", exe, none); in.Kind != KindDev {
		t.Fatalf("unknown linked kind: %+v", in)
	}
	os.WriteFile(filepath.Join(libexec, KindFile), []byte("brew\n"), 0o644)
	// Reached through a symlink, as Homebrew's bin/ link is.
	link := filepath.Join(dir, "agent-keepalive")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(exe, link); err != nil {
			t.Fatal(err)
		}
		if in := detectInstall(KindArchive, link, none); in.Kind != KindBrew || in.Exe != exe {
			t.Fatalf("libexec file via symlink: %+v", in)
		}
	}
	os.WriteFile(filepath.Join(bin, KindFile), []byte("rpm"), 0o644)
	if in := detectInstall(KindArchive, exe, none); in.Kind != KindRPM {
		t.Fatalf("file next to exe wins: %+v", in)
	}
	os.WriteFile(filepath.Join(bin, KindFile), []byte("nonsense"), 0o644)
	if in := detectInstall(KindArchive, exe, none); in.Kind != KindBrew {
		t.Fatalf("invalid file ignored: %+v", in)
	}

	// AppImage: inside the mount.
	mount := filepath.Join(dir, "mnt")
	os.MkdirAll(filepath.Join(mount, "usr", "bin"), 0o755)
	inner := filepath.Join(mount, "usr", "bin", "agent-keepalive")
	os.WriteFile(inner, []byte("x"), 0o755)
	img := filepath.Join(dir, "agent-keepalive.AppImage")
	in := detectInstall(KindArchive, inner, env(map[string]string{"APPIMAGE": img, "APPDIR": mount}))
	if in.Kind != KindAppImage || in.Target != img {
		t.Fatalf("appimage: %+v", in)
	}
	// $APPIMAGE inherited from some other AppImage: not us.
	in = detectInstall(KindArchive, exe, env(map[string]string{"APPIMAGE": img, "APPDIR": mount}))
	if in.Kind == KindAppImage || in.Target != exe {
		t.Fatalf("leaked APPIMAGE: %+v", in)
	}
}

func TestCommands(t *testing.T) {
	no := func(string) bool { return false }
	zypper := func(p string) bool { return p == "/usr/bin/zypper" }
	cases := map[string]string{
		KindDeb:  "sudo apt update && sudo apt install --only-upgrade agent-keepalive",
		KindRPM:  "sudo dnf upgrade --refresh agent-keepalive",
		KindArch: "sudo pacman -Syu agent-keepalive",
		KindBrew: "brew upgrade agent-keepalive",
	}
	for kind, want := range cases {
		if got := Command(kind, no); got != want {
			t.Errorf("%s: %q", kind, got)
		}
	}
	if got := Command(KindRPM, zypper); got != "sudo zypper update agent-keepalive" {
		t.Errorf("zypper: %q", got)
	}
	for _, k := range []string{KindAppImage, KindArchive, KindWindowsSetup, KindWindowsPortable, KindMacOSPkg, KindDev} {
		if Command(k, no) != "" {
			t.Errorf("%s has a command", k)
		}
	}
}

func TestSwapFile(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			dir := t.TempDir()
			dst := filepath.Join(dir, "agent-keepalive")
			os.WriteFile(dst, []byte("old"), 0o750)
			os.Chmod(dst, 0o750)
			src := filepath.Join(dir, tempPrefix+"x")
			os.WriteFile(src, []byte("new"), 0o600)
			if err := swapFile(dst, src, goos); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(dst); string(b) != "new" {
				t.Fatalf("content %q", b)
			}
			if runtime.GOOS != "windows" {
				if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0o750 {
					t.Fatalf("mode %v", fi.Mode())
				}
			}
			_, err := os.Stat(dst + ".old")
			if goos == "windows" {
				if err != nil {
					t.Fatal("no .old kept")
				}
				cleanup(dst, time.Now())
				if _, err := os.Stat(dst + ".old"); err == nil {
					t.Fatal(".old not cleaned up")
				}
			} else if err == nil {
				t.Fatal(".old on unix")
			}
		})
	}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func zipOf(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

func TestExtractBinary(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"agent-keepalive_1.5.0/README.md": "readme", "agent-keepalive_1.5.0/agent-keepalive": "BIN", "agent-keepalive_1.5.0/agent-keepalive.exe": "EXE"}
	tgz := filepath.Join(dir, "a.tar.gz")
	os.WriteFile(tgz, tarGz(t, files), 0o644)
	zp := filepath.Join(dir, "a.zip")
	os.WriteFile(zp, zipOf(t, files), 0o644)
	out := filepath.Join(dir, "out")
	if err := extractBinary(tgz, "x.tar.gz", "agent-keepalive", out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "BIN" {
		t.Fatalf("tar: %q", b)
	}
	if err := extractBinary(zp, "x.zip", "agent-keepalive.exe", out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "EXE" {
		t.Fatalf("zip: %q", b)
	}
	if err := extractBinary(tgz, "x.tar.gz", "missing", out); err == nil {
		t.Fatal("missing binary extracted")
	}
}

// hub serves a feed and its artifacts.
type feedServer struct {
	*httptest.Server
	mu    sync.Mutex
	feed  []byte
	files map[string][]byte
}

func newFeedServer(t *testing.T) *feedServer {
	s := &feedServer{files: map[string][]byte{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path == "/latest.json" {
			w.Write(s.feed)
			return
		}
		b, ok := s.files[strings.TrimPrefix(r.URL.Path, "/dl/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(s.Close)
	return s
}

// publish signs a feed of version listing the given artifacts.
func (s *feedServer) publish(t *testing.T, priv ed25519.PrivateKey, version string, arts map[Artifact][]byte) {
	f := Feed{Version: version, PublishedAt: "2026-10-27T12:00:00Z", Notes: "notes", Page: "https://example.com/r"}
	s.mu.Lock()
	for a, body := range arts {
		sum := sha256.Sum256(body)
		a.URL = s.URL + "/dl/" + a.Name
		if a.SHA256 == "" {
			a.SHA256 = hex.EncodeToString(sum[:])
		}
		if a.Size == 0 {
			a.Size = int64(len(body))
		}
		f.Artifacts = append(f.Artifacts, a)
		s.files[a.Name] = body
	}
	s.mu.Unlock()
	if priv != nil {
		if err := f.Sign(priv); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := json.Marshal(f)
	s.mu.Lock()
	s.feed = b
	s.mu.Unlock()
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// waitState waits until the updater reaches one of states.
func waitState(t *testing.T, u *Updater, states ...string) apitypes.UpdateStatus {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st := u.Status()
		for _, s := range states {
			if st.State == s {
				return st
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state %q, want %v (error %q)", u.Status().State, states, u.Status().Error)
	return apitypes.UpdateStatus{}
}

func TestUpdaterDisabled(t *testing.T) {
	pub, _ := testKey(t)
	for name, o := range map[string]Options{
		"no feed":     {Version: "1.4.0", PublicKey: pub},
		"no key":      {Version: "1.4.0", FeedURL: "http://127.0.0.1:1/latest.json"},
		"bad key":     {Version: "1.4.0", FeedURL: "http://127.0.0.1:1/latest.json", PublicKey: "zz"},
		"--no-update": {Version: "1.4.0", FeedURL: "http://127.0.0.1:1/latest.json", PublicKey: pub, Disabled: true},
		"dev version": {Version: "dev", FeedURL: "http://127.0.0.1:1/latest.json", PublicKey: pub},
	} {
		o.Log = quiet()
		o.Install = &Install{Kind: KindArchive}
		u := New(o)
		if u.Status().Enabled {
			t.Errorf("%s: enabled", name)
		}
		if err := u.Check(context.Background()); !errors.Is(err, apitypes.ErrUnavailable) {
			t.Errorf("%s: Check = %v", name, err)
		}
		if err := u.Apply(context.Background()); !errors.Is(err, apitypes.ErrUnavailable) {
			t.Errorf("%s: Apply = %v", name, err)
		}
	}
}

func TestUpdaterArchiveApply(t *testing.T) {
	pub, priv := testKey(t)
	srv := newFeedServer(t)
	archive := tarGz(t, map[string]string{"agent-keepalive_1.5.0_linux_amd64/agent-keepalive": "NEW BINARY"})
	srv.publish(t, priv, "1.5.0", map[Artifact][]byte{
		{Kind: KindArchive, OS: "linux", Arch: "amd64", Name: "agent-keepalive_1.5.0_linux_amd64.tar.gz"}: archive,
		{Kind: KindAppImage, OS: "linux", Arch: "amd64", Name: "agent-keepalive-1.5.0-x86_64.AppImage"}:   []byte("IMG"),
	})
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-keepalive")
	os.WriteFile(target, []byte("OLD"), 0o755)

	restarted := make(chan string, 1)
	u := New(Options{
		Version: "1.4.0", FeedURL: srv.URL + "/latest.json", PublicKey: pub,
		Install: &Install{Kind: KindArchive, Exe: target, Target: target},
		GOOS:    "linux", GOARCH: "amd64", Log: quiet(),
		Restart: func(exe string) { restarted <- exe },
	})
	var mu sync.Mutex
	var seen []string
	cancel := u.Subscribe(func(st apitypes.UpdateStatus) {
		mu.Lock()
		if len(seen) == 0 || seen[len(seen)-1] != st.State {
			seen = append(seen, st.State)
		}
		mu.Unlock()
	})
	defer cancel()

	if err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := u.Status()
	if st.State != apitypes.UpdateAvailable || st.Action != apitypes.ActionApply || st.Latest == nil || st.Latest.Version != "1.5.0" || st.LastCheck.IsZero() {
		t.Fatalf("after check: %+v", st)
	}
	if st.Latest.PublishedAt.IsZero() || st.Latest.Page == "" {
		t.Fatalf("latest: %+v", st.Latest)
	}
	if err := u.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case exe := <-restarted:
		if exe != target {
			t.Fatalf("restart %s", exe)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("no restart: %+v", u.Status())
	}
	if b, _ := os.ReadFile(target); string(b) != "NEW BINARY" {
		t.Fatalf("target %q", b)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm()&0o100 == 0 {
		t.Fatal("not executable")
	}
	left, _ := filepath.Glob(filepath.Join(dir, tempPrefix+"*"))
	if len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
	mu.Lock()
	got := strings.Join(seen, ",")
	mu.Unlock()
	if got != "checking,available,downloading,installing" {
		t.Fatalf("states %s", got)
	}
}

func TestUpdaterActions(t *testing.T) {
	pub, priv := testKey(t)
	srv := newFeedServer(t)
	srv.publish(t, priv, "1.5.0", map[Artifact][]byte{
		{Kind: KindAppImage, OS: "linux", Arch: "amd64", Name: "agent-keepalive-1.5.0-x86_64.AppImage"}: []byte("IMG"),
		{Kind: KindDeb, OS: "linux", Arch: "amd64", Name: "agent-keepalive_1.5.0_amd64.deb"}:            []byte("DEB"),
	})
	check := func(kind, goarch string) apitypes.UpdateStatus {
		u := New(Options{Version: "1.4.0", FeedURL: srv.URL + "/latest.json", PublicKey: pub,
			Install: &Install{Kind: kind, Target: filepath.Join(t.TempDir(), "x")}, GOOS: "linux", GOARCH: goarch, Log: quiet(),
			exists: func(string) bool { return false }})
		if err := u.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
		return u.Status()
	}
	if st := check(KindDeb, "amd64"); st.Action != apitypes.ActionCommand || st.Command != Command(KindDeb, nil) {
		t.Errorf("deb: %+v", st)
	}
	if st := check(KindBrew, "arm64"); st.Action != apitypes.ActionCommand || st.Command != "brew upgrade agent-keepalive" {
		t.Errorf("brew: %+v", st)
	}
	if st := check(KindAppImage, "amd64"); st.Action != apitypes.ActionApply {
		t.Errorf("appimage: %+v", st)
	}
	if st := check(KindAppImage, "arm64"); st.Action != apitypes.ActionDownload {
		t.Errorf("appimage without artifact: %+v", st)
	}
	if st := check(KindDev, "amd64"); st.Action != apitypes.ActionDownload {
		t.Errorf("dev: %+v", st)
	}
	u := New(Options{Version: "1.4.0", FeedURL: srv.URL + "/latest.json", PublicKey: pub,
		Install: &Install{Kind: KindDeb}, GOOS: "linux", GOARCH: "amd64", Log: quiet()})
	u.Check(context.Background())
	if err := u.Apply(context.Background()); !errors.Is(err, apitypes.ErrUnavailable) {
		t.Errorf("apply on deb: %v", err)
	}
}

func TestUpdaterCheckOutcomes(t *testing.T) {
	pub, priv := testKey(t)
	srv := newFeedServer(t)
	art := map[Artifact][]byte{{Kind: KindArchive, OS: "linux", Arch: "amd64", Name: "a.tar.gz"}: []byte("A")}
	u := New(Options{Version: "1.4.0", FeedURL: srv.URL + "/latest.json", PublicKey: pub,
		Install: &Install{Kind: KindArchive, Target: filepath.Join(t.TempDir(), "x")}, GOOS: "linux", GOARCH: "amd64", Log: quiet()})

	srv.publish(t, priv, "1.4.0", art)
	if err := u.Check(context.Background()); err != nil || u.Status().State != apitypes.UpdateIdle {
		t.Fatalf("same version: %v %+v", err, u.Status())
	}
	srv.publish(t, priv, "1.5.0-rc.1", art)
	if err := u.Check(context.Background()); err != nil || u.Status().State != apitypes.UpdateIdle || u.Status().Latest != nil {
		t.Fatalf("prerelease offered to stable: %+v", u.Status())
	}
	srv.publish(t, nil, "1.5.0", art)
	if err := u.Check(context.Background()); !errors.Is(err, ErrBadSignature) || u.Status().State != apitypes.UpdateError {
		t.Fatalf("unsigned feed: %v %+v", err, u.Status())
	}
	_, other := testKey(t)
	srv.publish(t, other, "1.5.0", art)
	if err := u.Check(context.Background()); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("foreign key: %v", err)
	}
	srv.publish(t, priv, "1.5.0", art)
	if err := u.Check(context.Background()); err != nil || u.Status().State != apitypes.UpdateAvailable || u.Status().Error != "" {
		t.Fatalf("recovered: %v %+v", err, u.Status())
	}
}

func TestUpdaterBadDownload(t *testing.T) {
	pub, priv := testKey(t)
	srv := newFeedServer(t)
	srv.publish(t, priv, "1.5.0", map[Artifact][]byte{
		{Kind: KindAppImage, OS: "linux", Arch: "amd64", Name: "img", SHA256: strings.Repeat("00", 32)}: []byte("IMG"),
	})
	dir := t.TempDir()
	target := filepath.Join(dir, "a.AppImage")
	os.WriteFile(target, []byte("OLD"), 0o755)
	u := New(Options{Version: "1.4.0", FeedURL: srv.URL + "/latest.json", PublicKey: pub,
		Install: &Install{Kind: KindAppImage, Target: target}, GOOS: "linux", GOARCH: "amd64", Log: quiet(),
		Restart: func(string) { t.Error("restarted after a bad download") }})
	u.Check(context.Background())
	if err := u.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, u, apitypes.UpdateError)
	if !strings.Contains(st.Error, "sha256") || st.Latest == nil || st.Action != apitypes.ActionApply {
		t.Fatalf("status %+v", st)
	}
	if b, _ := os.ReadFile(target); string(b) != "OLD" {
		t.Fatal("target replaced")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, tempPrefix+"*")); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
}

func TestUpdaterNoRestartHook(t *testing.T) {
	pub, priv := testKey(t)
	srv := newFeedServer(t)
	srv.publish(t, priv, "1.5.0", map[Artifact][]byte{
		{Kind: KindWindowsPortable, OS: "windows", Arch: "amd64", Name: "agent-keepalive-1.5.0-portable-x64.exe"}: []byte("EXE"),
	})
	dir := t.TempDir()
	target := filepath.Join(dir, "agent-keepalive.exe")
	os.WriteFile(target, []byte("OLD"), 0o755)
	u := New(Options{Version: "1.4.0", FeedURL: srv.URL + "/latest.json", PublicKey: pub,
		Install: &Install{Kind: KindWindowsPortable, Target: target}, GOOS: "windows", GOARCH: "amd64", Log: quiet()})
	u.Check(context.Background())
	u.Apply(context.Background())
	waitState(t, u, apitypes.UpdateReady)
	if b, _ := os.ReadFile(target); string(b) != "EXE" {
		t.Fatalf("target %q", b)
	}
	if b, _ := os.ReadFile(target + ".old"); string(b) != "OLD" {
		t.Fatalf(".old %q", b)
	}
	// A later check of the same version keeps "ready".
	u.Check(context.Background())
	if st := u.Status(); st.State != apitypes.UpdateReady {
		t.Fatalf("after recheck %+v", st)
	}
}

func TestUpdaterInstallers(t *testing.T) {
	pub, priv := testKey(t)
	srv := newFeedServer(t)
	srv.publish(t, priv, "1.5.0", map[Artifact][]byte{
		{Kind: KindWindowsSetup, OS: "windows", Arch: "arm64", Name: "agent-keepalive-1.5.0-setup-arm64.exe"}: []byte("SETUP"),
		{Kind: KindMacOSPkg, OS: "darwin", Arch: "arm64", Name: "agent-keepalive-1.5.0-darwin-arm64.pkg"}:     []byte("PKG"),
	})

	t.Run("windows-setup", func(t *testing.T) {
		ran, quit := make(chan string, 1), make(chan struct{}, 1)
		u := New(Options{Version: "1.4.0", FeedURL: srv.URL + "/latest.json", PublicKey: pub,
			Install: &Install{Kind: KindWindowsSetup}, GOOS: "windows", GOARCH: "arm64", Log: quiet(), TempDir: t.TempDir(),
			Quit:     func() { quit <- struct{}{} },
			runSetup: func(p string) error { ran <- p; return nil }})
		u.Check(context.Background())
		if err := u.Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		p := <-ran
		if filepath.Base(p) != "agent-keepalive-1.5.0-setup-arm64.exe" {
			t.Fatalf("ran %s", p)
		}
		if b, _ := os.ReadFile(p); string(b) != "SETUP" {
			t.Fatalf("setup %q", b)
		}
		<-quit
		waitState(t, u, apitypes.UpdateInstalling)
	})
	t.Run("macos-pkg", func(t *testing.T) {
		opened := make(chan string, 2)
		u := New(Options{Version: "1.4.0", FeedURL: srv.URL + "/latest.json", PublicKey: pub,
			Install: &Install{Kind: KindMacOSPkg}, GOOS: "darwin", GOARCH: "arm64", Log: quiet(), TempDir: t.TempDir(),
			openPkg: func(p string) error { opened <- p; return nil }})
		u.Check(context.Background())
		if err := u.Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		p := <-opened
		if !strings.HasSuffix(p, ".pkg") {
			t.Fatalf("opened %s", p)
		}
		waitState(t, u, apitypes.UpdateReady)
		// Applying again reopens the same package without downloading.
		if err := u.Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		if p2 := <-opened; p2 != p {
			t.Fatalf("reopened %s", p2)
		}
	})
}

func TestWaitForPredecessor(t *testing.T) {
	t.Setenv(relaunchEnv, "")
	if !WaitForPredecessor(context.Background(), func() bool { return false }, time.Second) {
		t.Fatal("unmarked process waited")
	}
	t.Setenv(relaunchEnv, "12345")
	if pid, ok := RelaunchedBy(); !ok || pid != 12345 {
		t.Fatal("marker")
	}
	n := 0
	if !WaitForPredecessor(context.Background(), func() bool { n++; return n > 2 }, 5*time.Second) {
		t.Fatal("did not see the lock released")
	}
	if _, ok := RelaunchedBy(); ok {
		t.Fatal("marker not cleared")
	}
	t.Setenv(relaunchEnv, "12345")
	if WaitForPredecessor(context.Background(), func() bool { return false }, 200*time.Millisecond) {
		t.Fatal("timeout reported as free")
	}
}
