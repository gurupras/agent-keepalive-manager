// Package experiment defines the A/B experiments this release runs
// (docs/experiments.md).
//
// Definitions ship in releases: nothing configures them remotely. Every
// experiment so far asks one question for one quiet cause (model.QuietCause):
// while the harness reports a session as working but its main agent makes no
// API calls, is it cheaper to hold pings (today's behaviour) or to ping as if
// the session were idle? Each quiet stretch of a session with a keepalive
// plan is an episode, assigned to an arm at random.
//
// Treatments spend the user's money, so they are bounded: only sessions with
// an active plan take part, a treatment episode sends no more pings than the
// break-even count of its model, an install spends at most DailyPings pings
// and DailyCostUSD a UTC day on treatments, and an experiment stops on an
// install once its treatments have cost more than letting the cache go cold
// would have (StopAfter).
package experiment

import (
	"crypto/sha256"
	"encoding/binary"
	"strconv"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// Experiment is one A/B comparison.
type Experiment struct {
	ID      string           // stable; a new version of an experiment gets a new ID
	Cause   model.QuietCause // the quiet stretches it applies to
	Summary string
}

// All is every experiment this release runs. Several run at once: each
// takes the episodes of its own cause.
var All = []Experiment{
	{ID: "busy-ping-subagents-1", Cause: model.QuietSubagents,
		Summary: "Ping while the main agent waits for background sub-agents"},
	{ID: "busy-ping-tool-1", Cause: model.QuietTool,
		Summary: "Ping while the main agent waits on a long tool call"},
	{ID: "busy-ping-waiting-1", Cause: model.QuietWaiting,
		Summary: "Ping while a permission prompt waits for the user"},
	{ID: "busy-ping-other-1", Cause: model.QuietOther,
		Summary: "Ping while the session stays busy for another reason"},
}

// Guards on treatment spend.
const (
	// DailyPings and DailyCostUSD bound the treatment pings of an install
	// per UTC day. Once either is reached no new episode opens that day, in
	// either arm, so the arms stay comparable.
	DailyPings   = 20
	DailyCostUSD = 1.0
	// StopAfter: once this many treatment episodes of an experiment have
	// closed on an install and they cost more in total (pings plus the
	// cache writes on return) than re-writing the cache every time would
	// have, the experiment stops on that install.
	StopAfter = 5
)

// ForCause returns the experiment that takes episodes of cause c.
func ForCause(c model.QuietCause) (Experiment, bool) {
	for _, e := range All {
		if e.Cause == c {
			return e, true
		}
	}
	return Experiment{}, false
}

// Arm assigns an episode to an arm: half of them to each, decided by a hash
// of the install's salt, the session key and when the stretch began, so the
// same episode gets the same arm after a restart.
func Arm(salt, key string, quietFrom time.Time, id string) string {
	h := sha256.New()
	for _, s := range []string{salt, key, strconv.FormatInt(quietFrom.UnixMilli(), 10), id} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	if binary.BigEndian.Uint64(h.Sum(nil))%2 == 0 {
		return model.ArmHold
	}
	return model.ArmPing
}
