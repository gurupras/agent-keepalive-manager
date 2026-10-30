package model

import "time"

// QuietCause says why a session the harness reports as working makes no API
// calls of its own, so that its cache cools as if it were idle. Empty when
// the session is not quiet, or the cause cannot be told.
type QuietCause string

const (
	// QuietSubagents: the main agent's turn ended and background
	// sub-agents are still running.
	QuietSubagents QuietCause = "subagents"
	// QuietTool: the main agent waits on a long tool call (a build, a
	// foreground sub-agent, an MCP call).
	QuietTool QuietCause = "tool"
	// QuietWaiting: a permission prompt or question waits for the user.
	QuietWaiting QuietCause = "waiting"
	// QuietOther: the turn ended, but the harness stays busy for another
	// reason (hooks, teammates, a task with no sub-agent transcript).
	QuietOther QuietCause = "other"
)

// QuietCauses lists every cause, in display order.
var QuietCauses = []QuietCause{QuietSubagents, QuietTool, QuietWaiting, QuietOther}

// Experiment arms. Every experiment so far compares holding pings while the
// harness is busy (today's behaviour) with pinging anyway.
const (
	ArmHold = "hold"
	ArmPing = "ping"
)

// How an experiment episode ended.
const (
	// EpisodeReturned: the main agent made its next real API call.
	EpisodeReturned = "returned"
	// EpisodeLeft: the session stopped working without a call from the
	// main agent (it went idle), or stayed loud without one.
	EpisodeLeft = "left"
	// EpisodeEnded: the plan or the session ended first.
	EpisodeEnded = "ended"
	// EpisodeInterrupted: the server restarted or the install opted out
	// mid-episode. Not reported.
	EpisodeInterrupted = "interrupted"
)

// ExperimentView is a session's open experiment episode, as the UI shows it.
type ExperimentView struct {
	ID    string     `json:"id"`
	Arm   string     `json:"arm"`
	Cause QuietCause `json:"cause"`
	Since time.Time  `json:"since"` // when the episode opened
	Pings int        `json:"pings"` // treatment pings sent in it so far
}

// ExperimentSum is the experiment episodes closed on a day with the same
// experiment, arm, harness and model, summed (docs/metrics.md).
//
// An episode is one quiet stretch of a session with a keepalive plan: it
// starts when the session turns quiet for the experiment's cause and ends
// when the main agent makes its next real API call (Returned) or the plan
// or session ends first. Its cost is what the stretch cost: the pings sent
// in it plus the cache writes of the returning call. CostSqUSD sums the
// square of each episode's cost so a reader can compute the variance.
type ExperimentSum struct {
	Experiment    string  `json:"experiment"`
	Arm           string  `json:"arm"`
	Harness       Harness `json:"harness"`
	Model         string  `json:"model"`
	Episodes      int     `json:"episodes"`
	Returned      int     `json:"returned"`
	Warm          int     `json:"warm"` // of Returned: the call read the cache
	Pings         int     `json:"pings"`
	PingCalls     int     `json:"pingCalls"` // API calls the ping replies took
	PingCostUSD   float64 `json:"pingCostUsd"`
	RewriteUSD    float64 `json:"rewriteUsd"` // cache writes of the returning calls
	CostUSD       float64 `json:"costUsd"`    // PingCostUSD + RewriteUSD
	CostSqUSD     float64 `json:"costSqUsd"`  // sum of each episode's cost squared
	ContextTokens int64   `json:"contextTokens"`
	QuietMinutes  int64   `json:"quietMinutes"`
}
