package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDefaultCommand(t *testing.T) {
	for kind, want := range map[string]string{
		"windows-portable": "app", "windows-setup": "app", "appimage": "app", "macos-pkg": "app",
		"deb": "app", "rpm": "app", "arch": "app",
		"archive": "serve", "dev": "serve", "brew": "serve",
	} {
		if got := defaultCommand(kind); got != want {
			t.Errorf("%s: %s, want %s", kind, got, want)
		}
	}
}

func TestVersionAndUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "profile: ") || !strings.Contains(out.String(), "install: ") {
		t.Fatalf("%d %q", code, out.String())
	}
	if code := run([]string{"bogus"}, &out, &errb); code != 2 {
		t.Fatalf("unknown command: %d", code)
	}
}

func TestStatusWithoutServer(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"status", "--data-dir", t.TempDir()}, &out, &errb); code != 3 {
		t.Fatalf("status: %d %s", code, errb.String())
	}
	if code := run([]string{"stop", "--data-dir", t.TempDir()}, &out, &errb); code != 3 {
		t.Fatalf("stop: %d", code)
	}
}

func TestParseSub(t *testing.T) {
	cases := []struct {
		args []string
		sub  string
		dir  string
		ok   bool
	}{
		{nil, "status", "", true},
		{[]string{"on"}, "on", "", true},
		{[]string{"on", "--data-dir", "/x"}, "on", "/x", true},
		{[]string{"--data-dir", "/x", "off"}, "off", "/x", true},
		{[]string{"on", "off"}, "", "", false},
	}
	for _, c := range cases {
		fs := newFlags("t")
		d := fs.String("data-dir", "", "")
		sub, err := parseSub(fs, c.args, "status")
		if (err == nil) != c.ok || (c.ok && (sub != c.sub || *d != c.dir)) {
			t.Errorf("%v: %q %q %v", c.args, sub, *d, err)
		}
	}
}
