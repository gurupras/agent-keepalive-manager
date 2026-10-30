package experiment

import (
	"slices"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

func TestAtMostOnePerCause(t *testing.T) {
	ids, causes := map[string]bool{}, map[model.QuietCause]bool{}
	for _, e := range All {
		if ids[e.ID] || causes[e.Cause] || !slices.Contains(model.QuietCauses, e.Cause) {
			t.Fatalf("experiment %+v", e)
		}
		ids[e.ID], causes[e.Cause] = true, true
		if got, ok := ForCause(e.Cause); !ok || got.ID != e.ID {
			t.Fatalf("ForCause(%s) = %+v %v", e.Cause, got, ok)
		}
	}
	// Mid-turn causes take no experiment: a ping would interrupt the task.
	for _, c := range []model.QuietCause{model.QuietTool, model.QuietWaiting} {
		if e, ok := ForCause(c); ok {
			t.Fatalf("cause %s has %+v", c, e)
		}
	}
}

func TestArmStableAndBalanced(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	ping := 0
	const n = 4000
	for i := range n {
		from := t0.Add(time.Duration(i) * time.Minute)
		a := Arm("salt", "claude:s", from, "busy-ping-tool-1")
		if a != Arm("salt", "claude:s", from, "busy-ping-tool-1") {
			t.Fatal("arm not stable")
		}
		if a == model.ArmPing {
			ping++
		}
	}
	if ping < n*45/100 || ping > n*55/100 {
		t.Fatalf("%d of %d in the ping arm", ping, n)
	}
}
