package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// EnvDesktop names the desktop window's executable, overriding the search
// in desktopShell; "none" turns the window off (`app` then opens a browser).
const EnvDesktop = "AGENT_KEEPALIVE_DESKTOP"

// desktopName is the desktop window's executable (desktop/ in the source).
const desktopName = "agent-keepalive-desktop"

// macApp is the macOS package's app bundle, whose executable is the window.
const macApp = "/Applications/agent-keepalive.app"

// desktopGrace is how long the window has to fail before `app` decides it
// cannot run here (no WebKitGTK, no WebView2) and opens a browser instead.
const desktopGrace = 3 * time.Second

func cmdApp(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("app")
	fs.SetOutput(stderr)
	dataDir := dataDirFlag(fs)
	port := fs.Int("port", 0, "port for a server this starts (default: 4344 or the next free one)")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "app: unexpected argument %q\n", fs.Arg(0))
		return errUsage
	}
	dir, err := dataDir()
	if err != nil {
		return err
	}
	shell, err := desktopShell(dir)
	if err != nil {
		fmt.Fprintf(stderr, "the desktop window cannot run (%v); opening the UI in a browser\n", err)
	}
	if shell == "" {
		return openUI(dir, *port, false, stdout, stderr)
	}
	r, err := ensureServer(context.Background(), dir, *port)
	if err != nil {
		return err
	}
	// The macOS window is an app bundle: Launch Services starts it, so it
	// gets the Dock icon, the menu bar and focus. It finds the server itself.
	if bundle := appBundle(shell); bundle != "" {
		return exec.Command("open", "-a", bundle, "--args", "--data-dir", dir).Run()
	}
	cmd := exec.Command(shell, "--url", r.URL, "--data-dir", dir)
	// The window's messages go to our stderr when that is somewhere: not
	// for a Windows GUI program started from the Start menu, which has none.
	if f, ok := stderr.(*os.File); ok && f != nil {
		cmd.Stdout, cmd.Stderr = f, f
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "could not start the desktop window (%v); opening the UI in a browser\n", err)
		return openUI(dir, *port, false, stdout, stderr)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		// A second window hands over to the first and exits 0. Failing at
		// once is a missing library or webview runtime.
		if err != nil {
			fmt.Fprintf(stderr, "the desktop window did not start (%v); opening the UI in a browser\n", err)
			return openUI(dir, *port, false, stdout, stderr)
		}
		return nil
	case <-time.After(desktopGrace):
	}
	// Wait for the window, so whatever started `app` (a menu entry, the
	// AppImage runtime, which keeps its mount until then) sees it run.
	if err := <-done; err != nil {
		return fmt.Errorf("desktop window: %w", err)
	}
	return nil
}

// desktopShell finds the desktop window's executable: $AGENT_KEEPALIVE_DESKTOP,
// next to this program (Windows setup, AppImage), in ../lib/agent-keepalive
// (the Linux packages), in the macOS app bundle, or embedded in this program
// (Windows portable), written out to the data directory. "" when there is none.
func desktopShell(dataDir string) (string, error) {
	if p := os.Getenv(EnvDesktop); p != "" {
		if p == "none" {
			return "", nil
		}
		return p, nil
	}
	name := desktopName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		d := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(d, name), filepath.Join(d, "..", "lib", "agent-keepalive", name))
	}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, filepath.Join(macApp, "Contents", "MacOS", name))
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.Mode().IsRegular() {
			return c, nil
		}
	}
	if len(embeddedDesktop) > 0 {
		return writeEmbeddedDesktop(dataDir, name)
	}
	return "", nil
}

// appBundle is the .app directory that holds exe as its executable, or "".
func appBundle(exe string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	app := filepath.Dir(contents)
	if filepath.Base(macos) == "MacOS" && filepath.Base(contents) == "Contents" && strings.HasSuffix(app, ".app") {
		return app
	}
	return ""
}

// writeEmbeddedDesktop writes the embedded window out to
// <data dir>/desktop/<hash>/ (once per build) and removes older copies.
func writeEmbeddedDesktop(dataDir, name string) (string, error) {
	sum := sha256.Sum256(embeddedDesktop)
	id := hex.EncodeToString(sum[:8])
	root := filepath.Join(dataDir, "desktop")
	dst := filepath.Join(root, id, name)
	if fi, err := os.Stat(dst); err == nil && fi.Size() == int64(len(embeddedDesktop)) {
		return dst, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, embeddedDesktop, 0o700); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	// Copies from earlier versions; one still running stays until next time.
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if e.Name() != id {
				_ = os.RemoveAll(filepath.Join(root, e.Name()))
			}
		}
	}
	return dst, nil
}

// hasDisplay is false on a Linux or BSD machine with no graphical session
// (a server, an ssh login), where the window cannot open.
func hasDisplay() bool {
	switch runtime.GOOS {
	case "windows", "darwin":
		return true
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}
