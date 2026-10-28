// Package model holds the types shared by every agent-keepalive package:
// sessions found by a Source, activity read from their transcripts,
// keepalive plans, pings, ledger entries, settings, and the views and
// events served to the UI. The JSON tags are the wire format (docs/api.md).
package model

import (
	"context"
	"time"
)

// Harness identifies the agent CLI that owns a session.
type Harness string

const (
	Claude Harness = "claude"
	Codex  Harness = "codex"
)

// Status is what the agent is doing right now.
type Status string

const (
	StatusIdle    Status = "idle"
	StatusWorking Status = "working"
)

// Reach is how a ping can be delivered to a session.
type Reach string

const (
	ReachSocket Reach = "socket" // Claude Code's per-session messaging socket
	ReachQueue  Reach = "queue"  // `codex queue --thread`
	ReachNone   Reach = "none"
)

// Usage is the token usage of one API call, or a sum of calls.
type Usage struct {
	Input        int64 `json:"input"`
	Output       int64 `json:"output"`
	CacheRead    int64 `json:"cacheRead"`
	CacheWrite5m int64 `json:"cacheWrite5m"`
	CacheWrite1h int64 `json:"cacheWrite1h"`
}

// Add returns the sum of u and o.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		Input:        u.Input + o.Input,
		Output:       u.Output + o.Output,
		CacheRead:    u.CacheRead + o.CacheRead,
		CacheWrite5m: u.CacheWrite5m + o.CacheWrite5m,
		CacheWrite1h: u.CacheWrite1h + o.CacheWrite1h,
	}
}

// Context is the prompt size of the call: everything that was sent as input.
func (u Usage) Context() int64 { return u.Input + u.CacheRead + u.CacheWrite5m + u.CacheWrite1h }

// CacheWrite is all tokens written to the cache, at either TTL tier.
func (u Usage) CacheWrite() int64 { return u.CacheWrite5m + u.CacheWrite1h }

// ActivityKind classifies a transcript event.
type ActivityKind string

const (
	ActHuman     ActivityKind = "human"     // the user typed (or queued) a prompt
	ActAssistant ActivityKind = "assistant" // an API call made for a real turn
	ActPing      ActivityKind = "ping"      // our keepalive prompt arrived in the transcript
	ActPingReply ActivityKind = "pingReply" // an API call answering a keepalive prompt
	ActReset     ActivityKind = "reset"     // the conversation was cleared, compacted or resumed elsewhere
)

// Activity is one event read from a session transcript. Every API call
// (deduplicated by message/response id) is one ActAssistant or ActPingReply
// carrying its Usage.
type Activity struct {
	// Seq increases by one per activity of a session, in transcript order.
	// Consumers keep a Seq cursor instead of comparing timestamps, which can
	// be equal.
	Seq int64 `json:"seq"`
	// At is when the event was recorded. For an API call that is after the
	// response, which can be minutes after the provider read the cache.
	At time.Time `json:"at"`
	// Sent, for an API call, is when its request went out: the time of the
	// record that triggered it (the prompt or tool result before it). The
	// provider refreshes the cache TTL then, so ping timing is anchored on
	// Sent, never on At. Zero when unknown; use At then.
	Sent   time.Time    `json:"sent,omitzero"`
	Kind   ActivityKind `json:"kind"`
	Model  string       `json:"model,omitempty"`
	Usage  *Usage       `json:"usage,omitempty"`
	PingID string       `json:"pingId,omitempty"` // ActPing / ActPingReply whose marker has an id
	// Working marks an API call after which the turn went on (it stopped
	// for a tool): the gap that follows is the agent working, not the user away.
	Working bool `json:"working,omitempty"`
}

// Anchor is when the call refreshed the cache: Sent, or At if Sent is unknown.
func (a Activity) Anchor() time.Time {
	if !a.Sent.IsZero() {
		return a.Sent
	}
	return a.At
}

// Session is a live agent session as seen by a Source on one scan.
type Session struct {
	Key       string    `json:"key"` // "<harness>:<id>"
	Harness   Harness   `json:"harness"`
	ID        string    `json:"id"` // Claude sessionId / Codex thread id
	PID       int       `json:"pid"`
	ProcStart string    `json:"-"` // proc.StartTime of PID: guards against pid reuse
	StartedAt time.Time `json:"startedAt"`
	Cwd       string    `json:"cwd"`
	Project   string    `json:"project"` // nearest VCS root above Cwd, else Cwd
	Title     string    `json:"title"`
	Model     string    `json:"model"`
	Status    Status    `json:"status"`
	// StatusSince is when Status last changed. A session "working" for far
	// longer than any turn takes is reported as stale (see Blocked).
	StatusSince time.Time `json:"statusSince,omitzero"`
	Reach       Reach     `json:"reach"`
	// ReachWhy explains ReachNone.
	ReachWhy string `json:"reachWhy,omitempty"`

	ContextTokens int64     `json:"contextTokens"` // prompt size of the last API call
	LastCall      time.Time `json:"lastCall"`      // anchor (request time) of the last API call of any kind
	LastHuman     time.Time `json:"lastHuman"`     // last human prompt (never a ping)
	// CacheTTL is the TTL the session's calls were made with (Claude: from
	// the write tier; Codex: the harness default, or what was learned).
	CacheTTL Duration `json:"cacheTtl"`
	Totals   Usage    `json:"totals"`

	// Delivery details, not serialized.
	SocketPath string `json:"-"` // Claude messaging socket
	CodexBin   string `json:"-"` // codex executable of the live process
	CodexHome  string `json:"-"`
}

// Source discovers sessions of one harness and exposes their activity.
type Source interface {
	Harness() Harness
	// Scan returns the currently live sessions. It tails transcripts
	// incrementally, so repeated calls are cheap.
	Scan(ctx context.Context) ([]Session, error)
	// Activity returns the session's events with Seq > after, oldest first.
	// Only data read by earlier Scan calls is returned.
	Activity(key string, after int64) []Activity
}

// Deliverer sends a keepalive prompt into a live session.
type Deliverer interface {
	Harness() Harness
	// Ping delivers text, which already carries the marker with pingID. It
	// must re-check, immediately before writing, that s.PID still runs s.ID
	// (a /clear or /resume may have happened since the scan) and return
	// ErrSessionChanged if not. It never types into a terminal.
	Ping(ctx context.Context, s Session, pingID, text string) error
}

// Withdrawer is implemented by deliverers whose pings wait in a queue that
// can outlive the session (Codex). Sweep removes every queued keepalive
// ping whose session is not in live, and any queued longer than maxAge.
type Withdrawer interface {
	Sweep(ctx context.Context, live []Session, maxAge time.Duration) (removed int, err error)
}

// PlanState is the lifecycle of a keepalive plan.
type PlanState string

const (
	PlanActive    PlanState = "active"
	PlanPaused    PlanState = "paused"    // stopped by us (two cold pings in a row); see Plan.Reason
	PlanDone      PlanState = "done"      // reached Until
	PlanCancelled PlanState = "cancelled" // the user cancelled
	PlanEnded     PlanState = "ended"     // the session exited, or its process switched conversation
	PlanFailed    PlanState = "failed"    // could not deliver before the cache expired
)

// Plan is a request to keep one session warm until Until.
type Plan struct {
	ID        int64     `json:"id"`
	Key       string    `json:"key"`
	PID       int       `json:"pid"`
	Hours     float64   `json:"hours"`
	StartedAt time.Time `json:"startedAt"`
	Until     time.Time `json:"until"`
	State     PlanState `json:"state"`
	// Indefinite plans roll: each human prompt moves Until to one window
	// (MaxHours, capped at break-even) past it, so the plan only ends after
	// an absence longer than that window.
	Indefinite bool      `json:"indefinite,omitempty"`
	EndedAt    time.Time `json:"endedAt,omitzero"`
	NextPing   time.Time `json:"nextPing,omitzero"`
	Pings      int       `json:"pings"`
	PingCost   float64   `json:"pingCost"` // USD spent on pings in this plan so far
	// Reason says why a plan is paused, failed or ended, in a sentence.
	Reason    string `json:"reason,omitempty"`
	LastError string `json:"lastError,omitempty"`
	// Blocked is set while the plan wants to ping but cannot (session busy
	// for too long, unreachable, an earlier ping not consumed yet).
	Blocked string `json:"blocked,omitempty"`
}

// PingState is the lifecycle of one ping.
type PingState string

const (
	PingSending   PingState = "sending"   // recorded before delivery is attempted
	PingDelivered PingState = "delivered" // handed to the session's channel
	PingSeen      PingState = "seen"      // the prompt appeared in the transcript
	PingVerified  PingState = "verified"  // its reply (API call) was read
	PingFailed    PingState = "failed"    // delivery failed
	PingWithdrawn PingState = "withdrawn" // removed from a queue unconsumed
	PingLost      PingState = "lost"      // delivered but never seen within the verify window
)

// PingRecord is one keepalive prompt.
type PingRecord struct {
	ID     string    `json:"id"`
	PlanID int64     `json:"planId"`
	Key    string    `json:"key"`
	SentAt time.Time `json:"sentAt"`
	State  PingState `json:"state"`
	Calls  int       `json:"calls"` // API calls the reply took (1 is ideal)
	Usage  Usage     `json:"usage"`
	Cost   float64   `json:"cost"`
	// Cold: the reply re-wrote the context, so the cache had already expired.
	Cold  bool   `json:"cold"`
	Error string `json:"error,omitempty"`
}

// Outcome of a ledger segment.
type Outcome string

const (
	OutcomeSaved  Outcome = "saved"  // the user came back while warm, after a gap longer than the TTL
	OutcomeWasted Outcome = "wasted" // pings were sent but nobody came back in time
)

// Why a ledger segment ended.
const (
	EndReturned  = "returned"    // the user came back
	EndExpired   = "expired"     // the plan ran out
	EndCancelled = "cancelled"   // the user cancelled the plan
	EndExited    = "exited"      // the process exited
	EndSwitched  = "switched"    // the process moved to another conversation (/clear, /resume)
	EndCold      = "cold"        // a ping found the cache already cold
	EndMissed    = "invalidated" // the user came back, but the return call did not hit the cache
)

// LedgerEntry closes one idle segment of a plan.
type LedgerEntry struct {
	ID          int64     `json:"id"`
	PlanID      int64     `json:"planId"`
	Key         string    `json:"key"`
	Harness     Harness   `json:"harness"`
	Title       string    `json:"title"`
	Project     string    `json:"project"`
	Model       string    `json:"model"`
	IdleFrom    time.Time `json:"idleFrom"` // last real activity before the gap
	EndedAt     time.Time `json:"endedAt"`
	EndedBy     string    `json:"endedBy"` // one of the End* constants
	Outcome     Outcome   `json:"outcome"`
	Pings       int       `json:"pings"`
	PingCost    float64   `json:"pingCost"`
	Avoided     float64   `json:"avoided"` // re-cache cost avoided (saved entries only)
	Net         float64   `json:"net"`     // +saved / -wasted
	ContextSize int64     `json:"contextSize"`
}

// Costs are derived per session from the pricing table.
type Costs struct {
	ReadPerM       float64  `json:"readPerM"`
	WritePerM      float64  `json:"writePerM"` // cache write at the session's TTL tier
	OutputPerM     float64  `json:"outputPerM"`
	Recache        float64  `json:"recache"`        // ContextTokens at the write price
	Ping           float64  `json:"ping"`           // ContextTokens at the read price + a few output tokens
	PingInterval   Duration `json:"pingInterval"`   // TTL minus safety margin
	BreakEvenHours float64  `json:"breakEvenHours"` // floor(write/read - 1) pings × interval
	Known          bool     `json:"known"`          // the model is in the pricing table
}

// SessionView is what the API serves for one session.
type SessionView struct {
	Session
	Costs          Costs     `json:"costs"`
	CacheExpiresAt time.Time `json:"cacheExpiresAt"`
	Plan           *Plan     `json:"plan,omitempty"`
	SavedTotal     float64   `json:"savedTotal"`
	WastedTotal    float64   `json:"wastedTotal"`
}

// Totals summarises the ledger.
type Totals struct {
	Saved    float64 `json:"saved"`
	Wasted   float64 `json:"wasted"`
	Net      float64 `json:"net"`
	PingCost float64 `json:"pingCost"`
	Pings    int     `json:"pings"`
	Returns  int     `json:"returns"`
	Expired  int     `json:"expired"`
}

// Missed is a what-if over past transcripts: what keeping every idle session
// warm for up to Hours (capped at each model's break-even) would have netted.
type Missed struct {
	Since    time.Time       `json:"since"`
	Hours    float64         `json:"hours"`
	Saved    float64         `json:"saved"`
	Wasted   float64         `json:"wasted"`
	Net      float64         `json:"net"`
	Returns  int             `json:"returns"`
	Expired  int             `json:"expired"`
	Sessions []MissedSession `json:"sessions"`
}

// MissedSession is one session's share of a Missed.
type MissedSession struct {
	Key     string  `json:"key"`
	Title   string  `json:"title"`
	Project string  `json:"project"`
	Model   string  `json:"model"`
	Saved   float64 `json:"saved"`
	Wasted  float64 `json:"wasted"`
	Net     float64 `json:"net"`
	Returns int     `json:"returns"`
	Expired int     `json:"expired"`
	Known   bool    `json:"known"` // the model was priced; unknown models count nothing
}

// Settings are user-tunable scheduler settings.
type Settings struct {
	MaxHours    float64 `json:"maxHours"`    // cap for any plan (default 36)
	MarginMin   float64 `json:"marginMin"`   // ping this many minutes before the TTL (default 5)
	ScanSeconds float64 `json:"scanSeconds"` // scan cadence (default 5)
	ShowMissed  bool    `json:"showMissed"`  // show what keepalive could have saved
}

// DefaultSettings returns the settings of a new install.
func DefaultSettings() Settings {
	return Settings{MaxHours: 36, MarginMin: 5, ScanSeconds: 5}
}

// Event is pushed to UI subscribers over the WebSocket.
type Event struct {
	// Type is "sessions" | "plan" | "ping" | "ledger" | "settings" | "update" | "metrics".
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Duration marshals as (possibly fractional) seconds.
type Duration time.Duration

// Std returns d as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// DayStats is one UTC day of activity, the input of a usage report
// (docs/metrics.md). Implemented by scheduler.Service.
type DayStats struct {
	Day      string          `json:"day"` // YYYY-MM-DD, UTC
	Sessions map[Harness]int `json:"sessions"`
	Plans    DayPlans        `json:"plans"`
	Pings    DayPings        `json:"pings"`
	Outcomes []OutcomeSum    `json:"outcomes"`
}

// DayPlans counts the plans started on a day.
type DayPlans struct {
	Started    int     `json:"started"`
	Indefinite int     `json:"indefinite"`
	Hours      float64 `json:"hours"`
}

// DayPings counts the pings sent on a day, by result.
type DayPings struct {
	Sent     int     `json:"sent"`
	Verified int     `json:"verified"`
	Cold     int     `json:"cold"`
	Failed   int     `json:"failed"`
	CostUSD  float64 `json:"costUsd"`
}

// OutcomeSum is the ledger entries closed on a day with the same harness,
// model, outcome and reason, summed.
type OutcomeSum struct {
	Harness       Harness `json:"harness"`
	Model         string  `json:"model"`
	Outcome       Outcome `json:"outcome"`
	EndedBy       string  `json:"endedBy"`
	Count         int     `json:"count"`
	Pings         int     `json:"pings"`
	PingCostUSD   float64 `json:"pingCostUsd"`
	AvoidedUSD    float64 `json:"avoidedUsd"`
	NetUSD        float64 `json:"netUsd"`
	ContextTokens int64   `json:"contextTokens"`
	IdleMinutes   int64   `json:"idleMinutes"`
}

// Stats provides daily activity for usage reports.
type Stats interface {
	// DayStats returns the activity of the UTC day that contains day.
	DayStats(day time.Time) (DayStats, error)
}
