//go:build nowire

package app

// With -tags nowire the package builds without the concrete parts, so the
// wiring around them can be tested on its own (see wire.go).

import (
	"context"
	"errors"
	"log/slog"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
	"github.com/gurupras/agent-keepalive-manager/internal/config"
	"github.com/gurupras/agent-keepalive-manager/internal/distro"
)

var errNoWire = errors.New("built with -tags nowire: no scheduler")

// InstallKind is the linked install kind.
func InstallKind() string { return distro.Current().InstallKind }

// WaitForPredecessor does not wait.
func WaitForPredecessor(ctx context.Context, free func() bool) bool { return free() }

func buildCore(context.Context, CoreDeps) (*Core, error) { return nil, errNoWire }

// LocalMetrics is unavailable without the concrete parts.
func LocalMetrics(string, *config.Config, apitypes.Info, *slog.Logger) (apitypes.Metrics, func(), error) {
	return nil, nil, errNoWire
}

// LocalUpdater is unavailable without the concrete parts.
func LocalUpdater(string, bool, *slog.Logger) (apitypes.Updater, func()) { return nil, func() {} }
