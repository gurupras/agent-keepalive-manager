package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/tail"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

func eq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func openRollout(t *testing.T, path string) *rollout {
	t.Helper()
	r := newRollout(path, tail.NewActivityLog(0), tail.Options{})
	if _, err := r.poll(); err != nil {
		t.Fatal(err)
	}
	return r
}

type wantAct struct {
	kind    model.ActivityKind
	at      string
	sent    string
	pingID  string
	working bool
}

func checkActs(t *testing.T, got []model.Activity, want []wantAct) {
	t.Helper()
	if len(got) != len(want) {
		for _, a := range got {
			t.Logf("  %d %s at=%s sent=%s", a.Seq, a.Kind, a.At.Format(time.RFC3339Nano), a.Sent.Format(time.RFC3339Nano))
		}
		t.Fatalf("%d activities, want %d", len(got), len(want))
	}
	for i, w := range want {
		a := got[i]
		var sent time.Time
		if w.sent != "" {
			sent = ts(w.sent)
		}
		if a.Seq != int64(i+1) || a.Kind != w.kind || !a.At.Equal(ts(w.at)) || !a.Sent.Equal(sent) ||
			a.PingID != w.pingID || a.Working != w.working {
			t.Errorf("#%d: got seq %d %s at %s sent %s ping %q working %v; want %+v", i, a.Seq, a.Kind,
				a.At.Format(time.RFC3339Nano), a.Sent.Format(time.RFC3339Nano), a.PingID, a.Working, w)
		}
	}
}

func TestRolloutBasic(t *testing.T) {
	r := openRollout(t, filepath.Join("testdata", "basic.jsonl"))
	checkActs(t, r.activity(0), []wantAct{
		{kind: model.ActHuman, at: "2026-10-26T10:00:00.150Z"},
		// Injected context is not a prompt; the typed one is the trigger.
		{kind: model.ActAssistant, at: "2026-10-26T10:00:02.010Z", sent: "2026-10-26T10:00:00.150Z", working: true},
		// Sent when the tool output was recorded; the turn's last call; r2's
		// duplicate record and the token_count are not counted.
		{kind: model.ActAssistant, at: "2026-10-26T10:00:04.010Z", sent: "2026-10-26T10:00:03Z"},
		{kind: model.ActPing, at: "2026-10-26T10:25:00.020Z", pingID: "p1"},
		{kind: model.ActPingReply, at: "2026-10-26T10:25:01.010Z", sent: "2026-10-26T10:25:00.020Z", pingID: "p1"},
		{kind: model.ActHuman, at: "2026-10-26T10:40:00.020Z"},
	})
	p := r.published()
	eq(t, "totals", p.totals, model.Usage{Input: 20, Output: 49, CacheRead: 20090, CacheWrite5m: 10110})
	eq(t, "context", p.contextTokens, int64(10120))
	eq(t, "model", p.model, "gpt-6-astra")
	eq(t, "busy", p.busy, true)
	eq(t, "busySince", p.busySince, ts("2026-10-26T10:40:00Z"))
	eq(t, "lastCall", p.lastCall, ts("2026-10-26T10:25:00.020Z"))
	eq(t, "lastHuman", p.lastHuman, ts("2026-10-26T10:40:00.020Z"))
	eq(t, "cwd", p.metaCwd, "/work/proj")
	eq(t, "firstHuman", p.firstHuman, "Fix the failing test in parser.go")
	eq(t, "subagent", p.subagent, false)
}

func TestRolloutLegacyTokenCount(t *testing.T) {
	r := openRollout(t, filepath.Join("testdata", "legacy.jsonl"))
	checkActs(t, r.activity(0), []wantAct{
		{kind: model.ActHuman, at: "2025-09-10T10:00:00.120Z"},
		{kind: model.ActAssistant, at: "2025-09-10T10:00:01Z", sent: "2025-09-10T10:00:00.120Z", working: true},
		// No trigger since the previous call: sent after its response.
		{kind: model.ActAssistant, at: "2025-09-10T10:00:05Z", sent: "2025-09-10T10:00:01Z"},
	})
	p := r.published()
	eq(t, "totals", p.totals, model.Usage{Input: 5108, Output: 15, CacheRead: 4992})
	eq(t, "busy after abort", p.busy, false)
}

func TestRolloutSubagent(t *testing.T) {
	eq(t, "subagent", openRollout(t, filepath.Join("testdata", "subagent.jsonl")).published().subagent, true)
}

// A fork's copy of its parent's history is not the fork's activity.
func TestRolloutForkSkipsParentHistory(t *testing.T) {
	r := openRollout(t, filepath.Join("testdata", "fork.jsonl"))
	checkActs(t, r.activity(0), []wantAct{
		{kind: model.ActHuman, at: "2026-10-26T12:01:00.010Z"},
		{kind: model.ActAssistant, at: "2026-10-26T12:01:02Z", sent: "2026-10-26T12:01:00.010Z"},
	})
	eq(t, "totals", r.published().totals, model.Usage{CacheRead: 5000, CacheWrite5m: 100, Output: 3})
}

func TestRolloutResetDetection(t *testing.T) {
	r := openRollout(t, filepath.Join("testdata", "reset.jsonl"))
	checkActs(t, r.activity(0), []wantAct{
		{kind: model.ActHuman, at: "2026-10-26T09:00:01.010Z"},
		{kind: model.ActAssistant, at: "2026-10-26T09:00:03Z", sent: "2026-10-26T09:00:01.010Z"},
		{kind: model.ActReset, at: "2026-10-26T09:30:00Z"}, // compacted
		{kind: model.ActReset, at: "2026-10-26T10:00:00Z"}, // resumed into the same file
		{kind: model.ActHuman, at: "2026-10-26T10:00:05Z"},
	})
}

func TestRolloutTruncateKeepsSeq(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "basic.jsonl"))
	must(t, err)
	p := filepath.Join(t.TempDir(), "rollout.jsonl")
	must(t, os.WriteFile(p, b, 0o600))
	r := openRollout(t, p)
	must(t, os.WriteFile(p, b[:len(b)/2], 0o600))
	_, err = r.poll()
	must(t, err)
	after := r.activity(6)
	if len(after) == 0 || after[0].Kind != model.ActReset || after[0].Seq != 7 {
		t.Fatalf("after truncate: %+v", after)
	}
	if len(after) < 2 || after[1].Kind != model.ActHuman {
		t.Errorf("re-read missing: %+v", after)
	}
}

func TestReadHeader(t *testing.T) {
	h, ok := readHeader(filepath.Join("testdata", "basic.jsonl"))
	if !ok || h.ID != "01a17900-0000-7000-8000-000000000001" || h.Cwd != "/work/proj" || h.Subagent ||
		!h.Start.Equal(ts("2026-10-26T10:00:00Z")) {
		t.Errorf("header %+v %v", h, ok)
	}
	if h, ok := readHeader(filepath.Join("testdata", "subagent.jsonl")); !ok || !h.Subagent {
		t.Errorf("subagent header %+v", h)
	}
}
