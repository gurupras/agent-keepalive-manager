package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
	"github.com/gurupras/agent-keepalive-manager/internal/instance"
)

// LogFile is where a background server logs, in the data directory.
const LogFile = "server.log"

// notRunningError: the command needs a running server (exit status 3).
type notRunningError struct{ dataDir string }

func (e *notRunningError) Error() string {
	return "no agent-keepalive server is running for " + e.dataDir
}

func openLog(dataDir string) (*os.File, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dataDir, LogFile), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
}

func cmdOpen(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("open")
	fs.SetOutput(stderr)
	dataDir := dataDirFlag(fs)
	port := fs.Int("port", 0, "port for a server this starts (default: 4344 or the next free one)")
	noBrowser := fs.Bool("no-browser", false, "print the URL instead of opening a browser")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	dir, err := dataDir()
	if err != nil {
		return err
	}
	return openUI(dir, *port, *noBrowser, stdout, stderr)
}

// openUI is `open`: the running server's URL, printed and opened in a browser.
func openUI(dir string, port int, noBrowser bool, stdout, stderr io.Writer) error {
	r, err := ensureServer(context.Background(), dir, port)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, r.URL)
	if noBrowser {
		return nil
	}
	if err := openBrowser(r.URL); err != nil {
		fmt.Fprintf(stderr, "could not open a browser (%v); open %s yourself\n", err, r.URL)
	}
	return nil
}

// ensureServer returns the server for the data directory, starting one in
// the background if none is running.
func ensureServer(ctx context.Context, dir string, port int) (instance.Running, error) {
	if r, ok := instance.FindRunning(ctx, dir); ok {
		return r, nil
	}
	return startBackground(ctx, dir, port)
}

// startBackground starts `serve` detached from this process and its
// terminal, logging to <data dir>/server.log, and waits for api/health.
func startBackground(ctx context.Context, dir string, port int) (instance.Running, error) {
	exe, err := os.Executable()
	if err != nil {
		return instance.Running{}, err
	}
	// An AppImage's own binary lives in a mount that goes away with the
	// launcher; start the AppImage instead.
	if img := os.Getenv("APPIMAGE"); img != "" {
		exe = img
	}
	logf, err := openLog(dir)
	if err != nil {
		return instance.Running{}, fmt.Errorf("server log: %w", err)
	}
	defer logf.Close()
	fmt.Fprintf(logf, "--- %s starting server\n", time.Now().Format(time.RFC3339))
	args := []string{"serve", "--data-dir", dir}
	if port != 0 {
		args = append(args, "--port", strconv.Itoa(port))
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, logf, logf
	cmd.Dir = dir
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return instance.Running{}, fmt.Errorf("start server: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.After(20 * time.Second)
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for {
		if r, ok := instance.FindRunning(ctx, dir); ok {
			return r, nil
		}
		select {
		case err := <-exited:
			// It may have found another server for this data directory,
			// which then answers.
			if r, ok := instance.FindRunning(ctx, dir); ok {
				return r, nil
			}
			return instance.Running{}, fmt.Errorf("the server stopped (%v); see %s:\n%s", err, filepath.Join(dir, LogFile), logTail(dir))
		case <-deadline:
			return instance.Running{}, fmt.Errorf("the server did not start within 20 s; see %s", filepath.Join(dir, LogFile))
		case <-tick.C:
		}
	}
}

// logTail is the end of the server log, for an error message.
func logTail(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, LogFile))
	if err != nil {
		return ""
	}
	if len(b) > 2000 {
		b = b[len(b)-2000:]
	}
	if i := bytes.LastIndex(b, []byte("--- ")); i >= 0 {
		b = b[i:]
	}
	return string(b)
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// connect finds the server for the data directory and returns an API
// client for it.
func connect(dir string) (*client, instance.Running, error) {
	r, ok := instance.FindRunning(context.Background(), dir)
	if !ok {
		return nil, r, &notRunningError{dataDir: dir}
	}
	tok, err := instance.ReadToken(dir)
	if err != nil {
		return nil, r, fmt.Errorf("API token: %w", err)
	}
	return &client{base: r.URL, token: tok}, r, nil
}

func cmdStatus(args []string, stdout io.Writer) error {
	fs := newFlags("status")
	dataDir := dataDirFlag(fs)
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	dir, err := dataDir()
	if err != nil {
		return err
	}
	c, r, err := connect(dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "running: %s (version %s)\n", r.URL, r.Version)
	var info apitypes.Info
	if err := c.do("GET", "api/info", nil, &info); err == nil {
		fmt.Fprintf(stdout, "data dir: %s\nprofile: %s\ninstall: %s\n", info.DataDir, info.Profile, info.InstallKind)
		if info.Supervised {
			fmt.Fprintln(stdout, "supervised: yes")
		}
	}
	return nil
}

func cmdStop(args []string, stdout io.Writer) error {
	fs := newFlags("stop")
	dataDir := dataDirFlag(fs)
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	dir, err := dataDir()
	if err != nil {
		return err
	}
	c, _, err := connect(dir)
	if err != nil {
		return err
	}
	if err := c.do("POST", "api/quit", nil, nil); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := instance.FindRunning(context.Background(), dir); !ok {
			fmt.Fprintln(stdout, "stopped")
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("the server did not stop within 15 s")
}
