package experiment

import (
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

func TestOnePerCause(t *testing.T) {
	ids := map[string]bool{}
	for _, c := range model.QuietCauses {
		e, ok := ForCause(c)
		if !ok || e.Cause != c || ids[e.ID] {
			t.Fatalf("cause %s: %+v %v", c, e, ok)
		}
		ids[e.ID] = true
	}
	if len(All) != len(model.QuietCauses) {
		t.Fatalf("%d experiments for %d causes", len(All), len(model.QuietCauses))
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
