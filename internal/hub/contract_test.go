package hub

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/telemetry"
)

// TestClientReportAccepted: the report the client builds, experiments
// included, is one the hub accepts.
func TestClientReportAccepted(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	ds := model.DayStats{
		Day:      "2026-10-29",
		Sessions: map[model.Harness]int{model.Claude: 2},
		Plans:    model.DayPlans{Started: 1, Hours: 4},
		Pings:    model.DayPings{Sent: 3, Verified: 3, CostUSD: 0.31},
		Experiments: []model.ExperimentSum{
			{Experiment: "busy-ping-subagents-1", Arm: model.ArmPing, Harness: model.Claude, Model: "claude-opus-5-5",
				Episodes: 2, Returned: 1, Warm: 1, Pings: 3, PingCalls: 4, PingCostUSD: 0.3123456, RewriteUSD: 0.0123456,
				CostUSD: 0.3246912, CostSqUSD: 0.3246912 * 0.3246912 * 0.6, ContextTokens: 300000, QuietMinutes: 70},
			{Experiment: "busy-ping-subagents-1", Arm: model.ArmHold, Harness: model.Claude, Model: "claude-opus-5-5",
				Episodes: 1, Returned: 1, RewriteUSD: 1.6666667, CostUSD: 1.6666667, CostSqUSD: 1.6666667 * 1.6666667,
				ContextTokens: 150000, QuietMinutes: 30},
		},
	}
	r := telemetry.BuildReport("8d3c6f0e-2b7a-4f5e-9c1d-3a4b5c6d7e8f", telemetry.Build{
		Version: "1.5.0", OS: "linux", Arch: "amd64", Profile: "public", InstallKind: "deb"}, ds)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeReport(b, now)
	if err != nil {
		t.Fatalf("refused: %v\n%s", err, b)
	}
	if len(got.Experiments) != 2 {
		t.Fatalf("experiments: %+v", got.Experiments)
	}
}
