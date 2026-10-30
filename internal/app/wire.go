//go:build !nowire

package app

// This file builds the concrete parts of the server. It is the only file of
// the package that imports them, so the rest is tested with fakes (and, with
// -tags nowire, without them).

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
	"github.com/gurupras/agent-keepalive-manager/internal/claude"
	"github.com/gurupras/agent-keepalive-manager/internal/codex"
	"github.com/gurupras/agent-keepalive-manager/internal/config"
	"github.com/gurupras/agent-keepalive-manager/internal/distro"
	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/pricing"
	"github.com/gurupras/agent-keepalive-manager/internal/scheduler"
	"github.com/gurupras/agent-keepalive-manager/internal/store"
	"github.com/gurupras/agent-keepalive-manager/internal/telemetry"
	"github.com/gurupras/agent-keepalive-manager/internal/update"
)

// DBFile is the scheduler's SQLite database in the data directory.
const DBFile = "keepalive.db"

// InstallKind is how the running program was installed (the linked kind,
// an install-kind file, or an AppImage).
func InstallKind() string { return update.DetectInstall().Kind }

// WaitForPredecessor is Config.WaitPredecessor for a server started by a
// self-update: it waits up to 30 s for the old process to let go.
func WaitForPredecessor(ctx context.Context, free func() bool) bool {
	return update.WaitForPredecessor(ctx, free, 30*time.Second)
}

func buildCore(ctx context.Context, d CoreDeps) (*Core, error) {
	// The core is built once the predecessor of a self-update has let go,
	// so its leftovers (the old Windows exe, stale downloads) can go.
	update.Cleanup()
	table, err := pricing.Load(d.PricingPath)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(d.DataDir, DBFile))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	log := d.Log

	csrc := claude.NewSource(claude.Options{Log: log.With("harness", "claude")})
	cdel := claude.NewDeliverer(claude.DelivererOptions{Log: log.With("harness", "claude")})
	xopts := codex.Options{Log: log.With("harness", "codex")}
	xsrc := codex.NewSource(xopts)
	xdel := codex.NewDeliverer(xopts, xsrc)

	svc, err := scheduler.New(scheduler.Config{
		Store:       st,
		Sources:     []model.Source{csrc, xsrc},
		Deliverers:  []model.Deliverer{cdel, xdel},
		Pricing:     table,
		Log:         log.With("component", "scheduler"),
		Experiments: MetricsSettings{Config: d.Config}.Enrolled,
	})
	if err != nil {
		cdel.Close()
		st.Close()
		return nil, err
	}

	rep := newReporter(d.DataDir, d.Config, d.Info, svc, log)
	upd := update.New(update.Options{
		Version:   d.Info.Version,
		FeedURL:   distro.Current().UpdateFeedURL,
		PublicKey: distro.Current().UpdatePublicKey,
		Disabled:  d.NoUpdate,
		Restart:   d.Restart,
		Quit:      d.Quit,
		Log:       log.With("component", "update"),
	})

	return &Core{
		Service: svc,
		Updater: upd,
		Metrics: rep,
		Run: []func(context.Context){
			func(ctx context.Context) {
				if err := svc.Run(ctx); err != nil && ctx.Err() == nil {
					log.Error("scheduler stopped", "err", err)
				}
			},
			rep.Run,
			upd.Run,
		},
		Close: func() {
			upd.Close()
			cdel.Close()
			st.Close()
		},
	}, nil
}

func newReporter(dataDir string, conf *config.Config, info apitypes.Info, stats model.Stats, log *slog.Logger) *telemetry.Reporter {
	return telemetry.New(telemetry.Options{
		DataDir: dataDir,
		Build: telemetry.Build{
			Version: info.Version, OS: info.OS, Arch: info.Arch,
			Profile: info.Profile, InstallKind: info.InstallKind,
		},
		Stats:    stats,
		Settings: MetricsSettings{Config: conf},
		Log:      log.With("component", "metrics"),
	})
}

// LocalMetrics is usage reporting for the CLI when no server runs: it
// reads the data directory directly. The returned func releases it.
func LocalMetrics(dataDir string, conf *config.Config, info apitypes.Info, log *slog.Logger) (apitypes.Metrics, func(), error) {
	st, err := store.Open(filepath.Join(dataDir, DBFile))
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	svc, err := scheduler.New(scheduler.Config{Store: st, Log: log})
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	return newReporter(dataDir, conf, info, svc, log), func() { st.Close() }, nil
}

// LocalUpdater is the updater for the CLI when no server runs. Without a
// restart hook an update stops at "ready" and the next start uses it.
func LocalUpdater(version string, noUpdate bool, log *slog.Logger) (apitypes.Updater, func()) {
	u := update.New(update.Options{
		Version:   version,
		FeedURL:   distro.Current().UpdateFeedURL,
		PublicKey: distro.Current().UpdatePublicKey,
		Disabled:  noUpdate,
		Log:       log,
	})
	return u, u.Close
}
