package model

import (
	"context"
	"errors"
	"time"
)

// Errors shared across packages. The API maps them to status codes.
var (
	ErrNotFound       = errors.New("not found")                                 // 404
	ErrConflict       = errors.New("conflict")                                  // 409
	ErrInvalid        = errors.New("invalid")                                   // 400
	ErrUnreachable    = errors.New("session cannot be reached")                 // 409
	ErrSessionChanged = errors.New("the process moved to another conversation") // 409
)

// Service is the application core consumed by the HTTP API.
// Implemented by scheduler.Service.
type Service interface {
	Sessions() []SessionView
	Session(key string) (SessionView, bool)

	StartPlan(ctx context.Context, key string, hours float64) (Plan, error)
	// KeepIndefinitely starts (or converts to) an indefinite, rolling plan.
	KeepIndefinitely(ctx context.Context, key string) (Plan, error)
	ExtendPlan(ctx context.Context, key string, addHours float64) (Plan, error)
	// ResumePlan restarts a paused plan.
	ResumePlan(ctx context.Context, key string) (Plan, error)
	CancelPlan(ctx context.Context, key string) error
	PingNow(ctx context.Context, key string) (PingRecord, error)

	Ledger(from, to time.Time) ([]LedgerEntry, Totals, error)
	Pings(key string, limit int) ([]PingRecord, error)
	// Missed simulates keepalive over the transcripts of live sessions since `since`.
	Missed(since time.Time) Missed

	Settings() Settings
	UpdateSettings(ctx context.Context, s Settings) (Settings, error)

	// Subscribe returns a channel of events and a cancel func. A slow
	// subscriber loses events rather than blocking the scheduler; it gets a
	// fresh "sessions" snapshot on the next scan.
	Subscribe() (<-chan Event, func())
}
