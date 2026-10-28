// Package apitypes holds the JSON types and the small interfaces that the
// HTTP API (internal/api) consumes from the parts of the program it does not
// own: the self-updater (internal/update) and usage reporting
// (internal/telemetry). Keeping them here lets those packages and the API be
// built and tested independently; the field names are the wire format of
// docs/api.md.
package apitypes

import (
	"context"
	"errors"
	"time"
)

// ErrLocked is returned when a setting cannot be changed because an
// administrator or the distribution profile fixed it. The API answers 403.
var ErrLocked = errors.New("this setting is locked")

// ErrUnavailable is returned by an operation the build or install does not
// support (no update feed, no hub, supervised). The API answers 409.
var ErrUnavailable = errors.New("not available in this installation")

// Info describes this build and install (GET /api/info, and the WebSocket
// hello frame).
type Info struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Profile     string `json:"profile"`
	InstallKind string `json:"installKind"`
	DataDir     string `json:"dataDir"`
	Supervised  bool   `json:"supervised"`
	Homepage    string `json:"homepage,omitempty"`
	DownloadURL string `json:"downloadUrl,omitempty"`
}

// Update states (UpdateStatus.State).
const (
	UpdateIdle        = "idle"
	UpdateChecking    = "checking"
	UpdateAvailable   = "available"
	UpdateDownloading = "downloading"
	UpdateReady       = "ready"
	UpdateInstalling  = "installing"
	UpdateError       = "error"
)

// Update actions (UpdateStatus.Action): what the UI offers for an
// available update.
const (
	ActionApply    = "apply"    // this process can update itself (POST api/update/apply)
	ActionCommand  = "command"  // show Command (a package manager updates it)
	ActionDownload = "download" // open Latest.Page (no artifact for this platform)
)

// Latest is the newest release the feed announced.
type Latest struct {
	Version     string    `json:"version"`
	Notes       string    `json:"notes,omitempty"` // markdown
	Page        string    `json:"page,omitempty"`
	PublishedAt time.Time `json:"publishedAt,omitzero"`
}

// UpdateStatus is the self-updater's state (GET /api/update, `update` events).
type UpdateStatus struct {
	// Enabled is false when the build has no feed, with --no-update, or
	// when supervised; the UI then shows nothing.
	Enabled   bool      `json:"enabled"`
	Current   string    `json:"current"`
	Latest    *Latest   `json:"latest,omitempty"`
	State     string    `json:"state"`              // one of the Update* constants
	Progress  float64   `json:"progress"`           // 0..1 while downloading
	Action    string    `json:"action,omitempty"`   // one of the Action* constants
	Command   string    `json:"command,omitempty"`  // with ActionCommand
	Error     string    `json:"error,omitempty"`    // with UpdateError
	LastCheck time.Time `json:"lastCheck,omitzero"` // last completed feed check
}

// Updater is the self-updater as the API uses it. Implemented by
// internal/update.
type Updater interface {
	Status() UpdateStatus
	// Check fetches and verifies the feed now. It returns when the check is
	// done; the outcome is in Status (an error is also returned, so the CLI
	// can report it).
	Check(ctx context.Context) error
	// Apply starts installing the available update and returns once it has
	// started (ErrUnavailable when Action is not "apply" or nothing is
	// available). Progress is reported through Subscribe. When the new
	// program is in place the updater calls the RestartFunc it was built
	// with.
	Apply(ctx context.Context) error
	// Subscribe calls fn with the new status after every change, from any
	// goroutine, until cancel is called. fn must not block.
	Subscribe(fn func(UpdateStatus)) (cancel func())
}

// RestartFunc is given to the updater by internal/app. Calling it makes the
// server shut down gracefully (closing the listener and releasing the
// instance lock) and then start exe with the same arguments in its place.
// It returns at once; the restart happens after the caller returns.
type RestartFunc func(exe string)

// MetricsStatus is the state of usage reporting (GET/PUT /api/metrics,
// `metrics` events).
type MetricsStatus struct {
	// Available is false when the build has no hub and none is configured;
	// the UI then hides the feature.
	Available bool   `json:"available"`
	Enabled   bool   `json:"enabled"`
	Locked    bool   `json:"locked"`             // `enabled` is fixed
	LockedBy  string `json:"lockedBy,omitempty"` // "admin" | "profile" when Locked
	Endpoint  string `json:"endpoint,omitempty"`
	InstallID string `json:"installId,omitempty"`
	// LastSent is when a report was last accepted by the hub.
	LastSent  time.Time `json:"lastSent,omitzero"`
	LastError string    `json:"lastError,omitempty"`
	Queued    int       `json:"queued"` // reports waiting to be sent
}

// Metrics is usage reporting as the API and the CLI use it. Implemented by
// internal/telemetry.
type Metrics interface {
	Status() MetricsStatus
	// SetEnabled turns reporting on or off and persists the choice in the
	// user config. Off takes effect at once and drops queued reports.
	// Returns ErrLocked when the choice is fixed.
	SetEnabled(on bool) error
	// Preview returns the next report exactly as it would be sent
	// (docs/metrics.md); it is marshalled as-is.
	Preview() (any, error)
	// ResetID replaces the install id with a new random one.
	ResetID() error
}

// MetricsNotifier is optionally implemented by a Metrics whose status
// changes on its own (a report sent, an error). The API forwards the
// changes as `metrics` events; without it, events are sent only after
// SetEnabled and ResetID.
type MetricsNotifier interface {
	Subscribe(fn func(MetricsStatus)) (cancel func())
}
