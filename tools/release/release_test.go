package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/update"
)

func TestClassifyTable(t *testing.T) {
	const v = "1.5.0-rc.1"
	want := map[string]target{
		"agent-keepalive_1.5.0-rc.1_linux_amd64.tar.gz":    {update.KindArchive, "linux", "amd64"},
		"agent-keepalive_1.5.0-rc.1_linux_arm64.tar.gz":    {update.KindArchive, "linux", "arm64"},
		"agent-keepalive_1.5.0-rc.1_darwin_amd64.tar.gz":   {update.KindArchive, "darwin", "amd64"},
		"agent-keepalive_1.5.0-rc.1_darwin_arm64.tar.gz":   {update.KindArchive, "darwin", "arm64"},
		"agent-keepalive_1.5.0-rc.1_windows_amd64.zip":     {update.KindArchive, "windows", "amd64"},
		"agent-keepalive_1.5.0-rc.1_windows_arm64.zip":     {update.KindArchive, "windows", "arm64"},
		"agent-keepalive_1.5.0-rc.1_amd64.deb":             {update.KindDeb, "linux", "amd64"},
		"agent-keepalive_1.5.0-rc.1_arm64.deb":             {update.KindDeb, "linux", "arm64"},
		"agent-keepalive-1.5.0-rc.1-1.x86_64.rpm":          {update.KindRPM, "linux", "amd64"},
		"agent-keepalive-1.5.0-rc.1-1.aarch64.rpm":         {update.KindRPM, "linux", "arm64"},
		"agent-keepalive-1.5.0-rc.1-1-x86_64.pkg.tar.zst":  {update.KindArch, "linux", "amd64"},
		"agent-keepalive-1.5.0-rc.1-1-aarch64.pkg.tar.zst": {update.KindArch, "linux", "arm64"},
		"agent-keepalive-1.5.0-rc.1-x86_64.AppImage":       {update.KindAppImage, "linux", "amd64"},
		"agent-keepalive-1.5.0-rc.1-aarch64.AppImage":      {update.KindAppImage, "linux", "arm64"},
		"agent-keepalive-1.5.0-rc.1-setup-x64.exe":         {update.KindWindowsSetup, "windows", "amd64"},
		"agent-keepalive-1.5.0-rc.1-setup-arm64.exe":       {update.KindWindowsSetup, "windows", "arm64"},
		"agent-keepalive-1.5.0-rc.1-portable-x64.exe":      {update.KindWindowsPortable, "windows", "amd64"},
		"agent-keepalive-1.5.0-rc.1-portable-arm64.exe":    {update.KindWindowsPortable, "windows", "arm64"},
		"agent-keepalive-1.5.0-rc.1-darwin-x64.pkg":        {update.KindMacOSPkg, "darwin", "amd64"},
		"agent-keepalive-1.5.0-rc.1-darwin-arm64.pkg":      {update.KindMacOSPkg, "darwin", "arm64"},
	}
	for name, w := range want {
		got, ok := classify(name, v)
		if !ok || got != w {
			t.Errorf("%s: %+v %v, want %+v", name, got, ok, w)
		}
	}
	if n := len(artifactNames(v)); n != len(want) {
		t.Errorf("table has %d names, test lists %d", n, len(want))
	}
	for _, name := range []string{
		"agent-keepalive-hub_1.5.0-rc.1_linux_amd64.tar.gz",
		"agent-keepalive-hub_1.5.0-rc.1_linux_arm64.tar.gz",
		"SHA256SUMS", "latest.json", "notes.md",
		"agent-keepalive_1.4.0_linux_amd64.tar.gz", // another version
		"agent-keepalive_1.5.0-rc.1_freebsd_amd64.tar.gz",
	} {
		if got, ok := classify(name, v); ok {
			t.Errorf("%s classified as %+v", name, got)
		}
	}
}

func writeDist(t *testing.T, v string) (dir string, files []string) {
	dir = t.TempDir()
	for _, name := range []string{
		"agent-keepalive_" + v + "_linux_amd64.tar.gz",
		"agent-keepalive-" + v + "-x86_64.AppImage",
		"agent-keepalive_" + v + "_amd64.deb",
		"agent-keepalive-hub_" + v + "_linux_amd64.tar.gz",
		"SHA256SUMS",
	} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("contents of "+name), 0o644)
		files = append(files, p)
	}
	return dir, files
}

func TestBuildFeedSigned(t *testing.T) {
	var buf bytes.Buffer
	keygen(&buf)
	var pub, seed string
	for _, line := range strings.Split(buf.String(), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			switch k {
			case "UPDATE_PUBLIC_KEY":
				pub = v
			case "UPDATE_SIGNING_KEY":
				seed = v
			}
		}
	}
	if len(pub) != 64 || len(seed) != 64 {
		t.Fatalf("keygen: %q", buf.String())
	}
	dir, files := writeDist(t, "1.5.0")
	notes := filepath.Join(dir, "notes.md")
	os.WriteFile(notes, []byte("\n## 1.5.0\n- things\n"), 0o644)
	f, err := buildFeed(feedArgs{
		version: "v1.5.0", baseURL: "https://dl.example.com/1.5.0/", notes: notes, page: "https://example.com/r",
		signingKey: seed, files: files, now: time.Date(2026, 10, 27, 12, 0, 0, 5, time.UTC),
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != "1.5.0" || f.PublishedAt != "2026-10-27T12:00:00Z" || f.Notes != "## 1.5.0\n- things" || len(f.Artifacts) != 3 {
		t.Fatalf("feed %+v", f)
	}
	a, ok := f.Find(update.KindAppImage, "linux", "amd64")
	sum := sha256.Sum256([]byte("contents of " + a.Name))
	if !ok || a.URL != "https://dl.example.com/1.5.0/agent-keepalive-1.5.0-x86_64.AppImage" ||
		a.SHA256 != hex.EncodeToString(sum[:]) || a.Size != int64(len("contents of "+a.Name)) {
		t.Fatalf("artifact %+v", a)
	}
	b, _ := json.MarshalIndent(f, "", "  ")
	if _, err := update.ParseFeed(b, pub); err != nil {
		t.Fatalf("updater refuses the tool's feed: %v", err)
	}
}

func TestBuildFeedUnsigned(t *testing.T) {
	_, files := writeDist(t, "1.5.0")
	args := feedArgs{version: "1.5.0", baseURL: "https://x", files: files, now: time.Now()}
	if _, err := buildFeed(args, io.Discard); err == nil || !strings.Contains(err.Error(), "unsigned") {
		t.Fatalf("unsigned feed written: %v", err)
	}
	args.allowUnsigned = true
	f, err := buildFeed(args, io.Discard)
	if err != nil || f.Signature != "" {
		t.Fatalf("allow-unsigned: %v %+v", err, f)
	}
	args.signingKey = "nothex"
	if _, err := buildFeed(args, io.Discard); err == nil {
		t.Fatal("bad signing key accepted")
	}
}
