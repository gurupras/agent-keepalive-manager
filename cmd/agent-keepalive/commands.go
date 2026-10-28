package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/api"
	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
	"github.com/gurupras/agent-keepalive-manager/internal/app"
	"github.com/gurupras/agent-keepalive-manager/internal/config"
	"github.com/gurupras/agent-keepalive-manager/internal/distro"
	"github.com/gurupras/agent-keepalive-manager/internal/instance"
)

// parseSub parses `<sub> [flags]` or `[flags] <sub>`; Go's flag package
// stops at the first non-flag, so the subcommand is taken off first.
func parseSub(fs *flag.FlagSet, args []string, def string) (string, error) {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return "", errUsage
	}
	if sub == "" && fs.NArg() > 0 {
		sub = fs.Arg(0)
		if fs.NArg() > 1 {
			return "", errUsage
		}
	} else if fs.NArg() > 0 {
		return "", errUsage
	}
	if sub == "" {
		sub = def
	}
	return sub, nil
}

// quietLog keeps library logging out of command output.
var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// metricsFor returns the running server's usage reporting, or, with no
// server, the data directory's own.
func metricsFor(dir string) (apitypes.Metrics, func(), error) {
	c, _, err := connect(dir)
	if err == nil {
		return remoteMetrics{c}, func() {}, nil
	}
	var nr *notRunningError
	if !errors.As(err, &nr) {
		return nil, nil, err
	}
	conf, err := config.Load(config.Options{DataDir: dir})
	if err != nil {
		return nil, nil, err
	}
	p := distro.Current()
	info := apitypes.Info{
		Name: api.Name, Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Profile: p.Name, InstallKind: app.InstallKind(), DataDir: dir,
	}
	return app.LocalMetrics(dir, conf, info, quietLog)
}

func cmdMetrics(args []string, stdout io.Writer) error {
	fs := newFlags("metrics")
	dataDir := dataDirFlag(fs)
	sub, err := parseSub(fs, args, "status")
	if err != nil {
		return err
	}
	dir, err := dataDir()
	if err != nil {
		return err
	}
	m, done, err := metricsFor(dir)
	if err != nil {
		return err
	}
	defer done()
	switch sub {
	case "status":
	case "on", "off":
		if err := m.SetEnabled(sub == "on"); err != nil {
			if errors.Is(err, apitypes.ErrLocked) {
				st := m.Status()
				return fmt.Errorf("usage reporting is locked by the %s configuration", orDefault(st.LockedBy, "admin"))
			}
			return err
		}
	case "preview":
		p, err := m.Preview()
		if err != nil {
			return err
		}
		b, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, string(b))
		return nil
	case "reset-id":
		if err := m.ResetID(); err != nil {
			return err
		}
	default:
		fmt.Fprintf(stdout, "metrics: unknown subcommand %q (on, off, status, preview, reset-id)\n", sub)
		return errUsage
	}
	printMetrics(stdout, m.Status())
	return nil
}

func printMetrics(w io.Writer, st apitypes.MetricsStatus) {
	if !st.Available {
		fmt.Fprintln(w, "usage reports: not available in this build (no hub configured)")
		return
	}
	state := "off"
	if st.Enabled {
		state = "on"
	}
	if st.Locked {
		state += " (locked by " + orDefault(st.LockedBy, "admin") + ")"
	}
	printf(w, "usage reports: %s\nhub: %s\ninstall id: %s\nlast sent: %s\nqueued: %d\n",
		state, st.Endpoint, st.InstallID, fmtTime(st.LastSent), st.Queued)
	if st.LastError != "" {
		printf(w, "last error: %s\n", st.LastError)
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// updaterFor returns the running server's updater, or a local one.
func updaterFor(dir string) (apitypes.Updater, *client, func(), error) {
	c, _, err := connect(dir)
	if err == nil {
		return &remoteUpdater{c: c}, c, func() {}, nil
	}
	var nr *notRunningError
	if !errors.As(err, &nr) {
		return nil, nil, nil, err
	}
	u, done := app.LocalUpdater(version, false, quietLog)
	if u == nil {
		return nil, nil, nil, errors.New("self-update is not available in this build")
	}
	return u, nil, done, nil
}

func cmdUpdate(args []string, stdout io.Writer) error {
	fs := newFlags("update")
	dataDir := dataDirFlag(fs)
	sub, err := parseSub(fs, args, "check")
	if err != nil {
		return err
	}
	dir, err := dataDir()
	if err != nil {
		return err
	}
	u, c, done, err := updaterFor(dir)
	if err != nil {
		return err
	}
	defer done()
	ctx := context.Background()
	switch sub {
	case "status":
	case "check":
		if !u.Status().Enabled {
			return errors.New("self-update is turned off for this installation")
		}
		if err := u.Check(ctx); err != nil {
			return err
		}
	case "apply":
		st := u.Status()
		if !st.Enabled {
			return errors.New("self-update is turned off for this installation")
		}
		if st.State != apitypes.UpdateAvailable && st.State != apitypes.UpdateReady {
			if err := u.Check(ctx); err != nil {
				return err
			}
		}
		if st = u.Status(); st.State == apitypes.UpdateIdle {
			fmt.Fprintf(stdout, "agent-keepalive %s is the latest version\n", st.Current)
			return nil
		}
		switch st.Action {
		case apitypes.ActionCommand:
			return fmt.Errorf("this installation is updated by its package manager: %s", st.Command)
		case apitypes.ActionDownload:
			page := ""
			if st.Latest != nil {
				page = st.Latest.Page
			}
			return fmt.Errorf("no update for this platform can be installed automatically; download it from %s", page)
		}
		if err := u.Apply(ctx); err != nil {
			return err
		}
		return waitApply(stdout, u, c, dir)
	default:
		fmt.Fprintf(stdout, "update: unknown subcommand %q (check, apply, status)\n", sub)
		return errUsage
	}
	printUpdate(stdout, u.Status())
	return nil
}

// waitApply follows an update until it is installed or failed. A running
// server restarts into the update, so its going away and coming back with
// a new version is success.
func waitApply(w io.Writer, u apitypes.Updater, c *client, dir string) error {
	deadline := time.Now().Add(15 * time.Minute)
	lastPct := -1
	for time.Now().Before(deadline) {
		st := u.Status()
		if ru, ok := u.(*remoteUpdater); ok && ru.lastErr != nil {
			// The server is restarting.
			for time.Now().Before(deadline) {
				if r, ok := instance.FindRunning(context.Background(), dir); ok {
					fmt.Fprintf(w, "updated; the server now runs version %s\n", r.Version)
					return nil
				}
				time.Sleep(500 * time.Millisecond)
			}
			break
		}
		switch st.State {
		case apitypes.UpdateDownloading:
			if pct := int(st.Progress * 100); pct/10 != lastPct/10 {
				fmt.Fprintf(w, "downloading %d%%\n", pct)
				lastPct = pct
			}
		case apitypes.UpdateReady:
			if c == nil {
				fmt.Fprintln(w, "update installed; it is used from the next start")
				return nil
			}
		case apitypes.UpdateError:
			return errors.New(st.Error)
		case apitypes.UpdateIdle:
			if st.Latest == nil || st.Current == st.Latest.Version {
				fmt.Fprintln(w, "updated")
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("the update did not finish in time")
}

func printUpdate(w io.Writer, st apitypes.UpdateStatus) {
	if !st.Enabled {
		fmt.Fprintf(w, "version %s; self-update is off for this installation\n", st.Current)
		return
	}
	printf(w, "current: %s\n", st.Current)
	if st.Latest != nil {
		printf(w, "latest: %s\n", st.Latest.Version)
	}
	printf(w, "state: %s\nlast check: %s\n", st.State, fmtTime(st.LastCheck))
	if st.State == apitypes.UpdateAvailable {
		switch st.Action {
		case apitypes.ActionApply:
			fmt.Fprintln(w, "run: agent-keepalive update apply")
		case apitypes.ActionCommand:
			printf(w, "run: %s\n", st.Command)
		case apitypes.ActionDownload:
			if st.Latest != nil {
				printf(w, "download: %s\n", st.Latest.Page)
			}
		}
	}
	if st.Error != "" {
		printf(w, "error: %s\n", st.Error)
	}
}
