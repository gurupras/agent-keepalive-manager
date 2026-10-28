// Package instance holds the process-level helpers of the local server:
// where its state lives, which port it listens on, how a running server is
// found, and the scheduler lock that keeps two servers from pinging the
// same sessions.
package instance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Name is the program name, used for directories and the health check.
const Name = "agent-keepalive"

// Host is the only address the server binds.
const Host = "127.0.0.1"

// Ports tried when no port is given: DefaultPort and the next PortTries-1.
const (
	DefaultPort = 4344
	PortTries   = 50
)

// EnvDataDir overrides the data directory (weaker than --data-dir).
const EnvDataDir = "AGENT_KEEPALIVE_DATA_DIR"

// PortableDataDir is the data directory's name next to a portable
// executable.
const PortableDataDir = "agent-keepalive-data"

// DataDir returns the directory for this server's state: flag
// (--data-dir) if set, else $AGENT_KEEPALIVE_DATA_DIR, else, for the
// windows-portable install kind, agent-keepalive-data next to the
// executable (so the program and its data travel together), else the OS
// default (DefaultDataDir). The result is absolute; it is not created.
func DataDir(flag, installKind string) (string, error) {
	if flag != "" {
		return filepath.Abs(flag)
	}
	if d := os.Getenv(EnvDataDir); d != "" {
		return filepath.Abs(d)
	}
	if installKind == "windows-portable" {
		exe, err := os.Executable()
		if err == nil {
			if r, err := filepath.EvalSymlinks(exe); err == nil {
				exe = r
			}
			return filepath.Join(filepath.Dir(exe), PortableDataDir), nil
		}
	}
	return DefaultDataDir()
}

// DefaultDataDir is the per-user state directory, ignoring every override:
// Linux and other unixes $XDG_STATE_HOME/agent-keepalive (else
// ~/.local/state/agent-keepalive), macOS ~/Library/Application
// Support/agent-keepalive, Windows %LOCALAPPDATA%\agent-keepalive.
func DefaultDataDir() (string, error) {
	return defaultDataDir(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func defaultDataDir(goos string, getenv func(string) string, home func() (string, error)) (string, error) {
	switch goos {
	case "windows":
		if d := getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, Name), nil
		}
		h, err := home()
		if err != nil {
			return "", fmt.Errorf("locate data dir: %w", err)
		}
		return filepath.Join(h, "AppData", "Local", Name), nil
	case "darwin":
		h, err := home()
		if err != nil {
			return "", fmt.Errorf("locate data dir: %w", err)
		}
		return filepath.Join(h, "Library", "Application Support", Name), nil
	default:
		if d := getenv("XDG_STATE_HOME"); d != "" && filepath.IsAbs(d) {
			return filepath.Join(d, Name), nil
		}
		h, err := home()
		if err != nil {
			return "", fmt.Errorf("locate data dir: %w", err)
		}
		return filepath.Join(h, ".local", "state", Name), nil
	}
}

// PortFromEnv returns the port a supervisor assigned with $PORT (0 if
// unset).
func PortFromEnv() (int, error) {
	s := os.Getenv("PORT")
	if s == "" {
		return 0, nil
	}
	p, err := strconv.Atoi(s)
	if err != nil || p <= 0 || p > 65535 {
		return 0, fmt.Errorf("invalid PORT %q", s)
	}
	return p, nil
}

// Listen binds 127.0.0.1. A non-zero port is taken exactly (a supervisor
// or the user chose it); otherwise DefaultPort is tried, then the next
// ports, up to PortTries. It returns the listener and its port.
func Listen(port int) (net.Listener, int, error) {
	start, tries := DefaultPort, PortTries
	if port != 0 {
		start, tries = port, 1
	}
	var lastErr error
	for p := start; p < start+tries && p <= 65535; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(Host, strconv.Itoa(p)))
		if err == nil {
			return ln, ln.Addr().(*net.TCPAddr).Port, nil
		}
		lastErr = err
		if !isAddrInUse(err) {
			return nil, 0, err
		}
	}
	if tries == 1 {
		return nil, 0, fmt.Errorf("port %d: %w", start, lastErr)
	}
	return nil, 0, fmt.Errorf("no free port in %d..%d: %w", start, start+tries-1, lastErr)
}

func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	// Windows reports WSAEADDRINUSE (10048), which syscall does not map
	// to EADDRINUSE; Windows also refuses an address another socket has
	// exclusively (WSAEACCES, 10013).
	var se syscall.Errno
	if errors.As(err, &se) && (se == 10048 || se == 10013) && runtime.GOOS == "windows" {
		return true
	}
	return false
}

func portFile(dataDir string) string { return filepath.Join(dataDir, "port") }

// WritePortFile records the listening port in <dataDir>/port, atomically.
func WritePortFile(dataDir string, port int) error {
	return writeFileAtomic(portFile(dataDir), []byte(strconv.Itoa(port)+"\n"), 0o600)
}

// ReadPortFile returns the port recorded in <dataDir>/port.
func ReadPortFile(dataDir string) (int, error) {
	b, err := os.ReadFile(portFile(dataDir))
	if err != nil {
		return 0, err
	}
	p, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || p <= 0 || p > 65535 {
		return 0, fmt.Errorf("bad port file %s", portFile(dataDir))
	}
	return p, nil
}

// RemovePortFile deletes the port file if it still names port, so a server
// that is shutting down never removes its successor's.
func RemovePortFile(dataDir string, port int) {
	if p, err := ReadPortFile(dataDir); err == nil && p == port {
		os.Remove(portFile(dataDir))
	}
}

// Health is the answer of GET api/health.
type Health struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Port    int    `json:"port"`
}

// Running is a server found by FindRunning or Probe.
type Running struct {
	Port    int
	URL     string // the UI, e.g. "http://127.0.0.1:4344/"
	Version string
}

// FindRunning reads <dataDir>/port and confirms with GET api/health that
// an agent-keepalive server answers there.
func FindRunning(ctx context.Context, dataDir string) (Running, bool) {
	port, err := ReadPortFile(dataDir)
	if err != nil {
		return Running{}, false
	}
	return Probe(ctx, port)
}

// Probe asks 127.0.0.1:port for api/health.
func Probe(ctx context.Context, port int) (Running, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	base := BaseURL(port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"api/health", nil)
	if err != nil {
		return Running{}, false
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return Running{}, false
	}
	defer resp.Body.Close()
	var h Health
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&h) != nil || h.Name != Name {
		return Running{}, false
	}
	return Running{Port: port, URL: base, Version: h.Version}, true
}

// BaseURL is the root URL of a server on port.
func BaseURL(port int) string {
	return "http://" + net.JoinHostPort(Host, strconv.Itoa(port)) + "/"
}

func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
