package claude

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

func appendFile(t *testing.T, p string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
}

func openTranscript(t *testing.T, p string) *transcript {
	t.Helper()
	tr := newTranscript(p, DefaultPeerName, time.Hour, tail.NewActivityLog(0), tail.Options{})
	if _, err := tr.poll(); err != nil {
		t.Fatal(err)
	}
	return tr
}

func fixture(t *testing.T, name string) *transcript {
	return openTranscript(t, filepath.Join("testdata", name))
}

type wantAct struct {
	kind    model.ActivityKind
	at      string
	sent    string // "" = zero
	pingID  string
	working bool
}

func checkActs(t *testing.T, got []model.Activity, want []wantAct) {
	t.Helper()
	if len(got) != len(want) {
		for _, a := range got {
			t.Logf("  %d %s at=%s sent=%s ping=%q", a.Seq, a.Kind, a.At.Format(time.RFC3339Nano), a.Sent.Format(time.RFC3339Nano), a.PingID)
		}
		t.Fatalf("%d activities, want %d", len(got), len(want))
	}
	for i, w := range want {
		a := got[i]
		if a.Seq != int64(i+1) {
			t.Errorf("#%d: Seq %d", i, a.Seq)
		}
		if a.Kind != w.kind || !a.At.Equal(ts(w.at)) || a.PingID != w.pingID || a.Working != w.working {
			t.Errorf("#%d: got %s at %s ping %q working %v; want %+v", i, a.Kind, a.At.Format(time.RFC3339Nano), a.PingID, a.Working, w)
		}
		var sent time.Time
		if w.sent != "" {
			sent = ts(w.sent)
		}
		if !a.Sent.Equal(sent) {
			t.Errorf("#%d %s: Sent %s, want %s", i, a.Kind, a.Sent.Format(time.RFC3339Nano), sent.Format(time.RFC3339Nano))
		}
		if (a.Usage != nil) != (a.Kind == model.ActAssistant || a.Kind == model.ActPingReply) {
			t.Errorf("#%d %s: usage presence wrong", i, a.Kind)
		}
	}
}

func TestClassifyBasic(t *testing.T) {
	tr := fixture(t, "basic.jsonl")
	checkActs(t, tr.activity(0), []wantAct{
		{kind: model.ActHuman, at: "2026-10-26T10:00:00Z"},
		// Split over two records: counted once, sent when the attachment
		// (the last record of the prompt) was written.
		{kind: model.ActAssistant, at: "2026-10-26T10:00:05Z", sent: "2026-10-26T10:00:00.100Z"},
		{kind: model.ActPing, at: "2026-10-26T10:55:00.100Z", pingID: "p1"},
		{kind: model.ActPingReply, at: "2026-10-26T10:55:02Z", sent: "2026-10-26T10:55:00.100Z", pingID: "p1"},
		// A task notification is a new prompt, not human, and ends the ping.
		{kind: model.ActAssistant, at: "2026-10-26T11:00:03Z", sent: "2026-10-26T11:00:00Z", working: true},
		// Synthetic and sidechain calls are skipped; the tool result is the trigger.
		{kind: model.ActAssistant, at: "2026-10-26T11:00:06Z", sent: "2026-10-26T11:00:04Z"},
		// Quoting the marker is not a ping.
		{kind: model.ActHuman, at: "2026-10-26T12:00:00Z"},
		{kind: model.ActAssistant, at: "2026-10-26T12:00:04Z", sent: "2026-10-26T12:00:00.100Z", working: true},
	})
	p := tr.published()
	eq(t, "totals", p.totals, model.Usage{Input: 11, Output: 70, CacheRead: 6730, CacheWrite1h: 1410})
	eq(t, "last usage", p.lastUsage, model.Usage{Input: 3, Output: 20, CacheRead: 1610, CacheWrite1h: 300})
	eq(t, "lastCall", p.lastCall, ts("2026-10-26T12:00:00.100Z"))
	eq(t, "lastHuman", p.lastHuman, ts("2026-10-26T12:00:00Z"))
	eq(t, "ttl", p.ttl, time.Hour)
	eq(t, "toolPending", p.toolPending, true)
	eq(t, "ai title", p.aiTitle, "Greeting")
	eq(t, "away summary", p.awaySummary, "Greeted the user and answered a ping.")
	eq(t, "model", p.lastModel, "claude-opus-5-5")
	if u := tr.activity(1)[0].Usage; u.Output != 10 {
		t.Errorf("dedup kept output %d, want the latest (10)", u.Output)
	}
}

func TestClassifyLegacy(t *testing.T) {
	tr := fixture(t, "legacy.jsonl")
	checkActs(t, tr.activity(0), []wantAct{
		{kind: model.ActHuman, at: "2026-10-20T09:00:00Z"},
		{kind: model.ActAssistant, at: "2026-10-20T09:00:02Z", sent: "2026-10-20T09:00:00Z"},
		{kind: model.ActPing, at: "2026-10-20T09:04:00Z"},
		{kind: model.ActPingReply, at: "2026-10-20T09:04:01Z", sent: "2026-10-20T09:04:00Z"},
	})
	p := tr.published()
	eq(t, "ttl", p.ttl, 5*time.Minute)
	eq(t, "custom title", p.customTitle, "my-renamed")
}

func TestResetDetection(t *testing.T) {
	tr := fixture(t, "reset.jsonl")
	checkActs(t, tr.activity(0), []wantAct{
		{kind: model.ActHuman, at: "2026-10-26T09:00:00Z"},
		{kind: model.ActAssistant, at: "2026-10-26T09:00:03Z", sent: "2026-10-26T09:00:00Z"},
		{kind: model.ActReset, at: "2026-10-26T09:30:00Z"}, // compaction
		{kind: model.ActHuman, at: "2026-10-26T09:31:00Z"}, // the summary is not human
		{kind: model.ActAssistant, at: "2026-10-26T09:31:04Z", sent: "2026-10-26T09:31:00Z"},
		{kind: model.ActReset, at: "2026-10-26T09:40:00Z"}, // /clear
		{kind: model.ActReset, at: "2026-10-26T09:50:00Z"}, // another session continues the file
		{kind: model.ActHuman, at: "2026-10-26T09:50:00Z"},
	})
}

// A call with no prompt or tool result since the previous call was sent
// after that call's response was recorded.
func TestSentContinuation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	call := func(id, at string) string {
		return `{"type":"assistant","timestamp":"` + at + `","message":{"id":"` + id + `","model":"m","stop_reason":"end_turn","usage":{"input_tokens":1}}}` + "\n"
	}
	appendFile(t, p, []byte(`{"type":"user","timestamp":"2026-10-26T10:00:00Z","message":{"content":"hi"},"origin":{"kind":"human"}}`+"\n"+
		call("a", "2026-10-26T10:00:05Z")+call("b", "2026-10-26T10:00:09Z")))
	acts := openTranscript(t, p).activity(0)
	eq(t, "first sent", acts[1].Sent, ts("2026-10-26T10:00:00Z"))
	eq(t, "continuation sent", acts[2].Sent, ts("2026-10-26T10:00:05Z"))
}

// Feeding the fixture in chunks that split lines must give the same result
// as parsing it whole.
func TestIncremental(t *testing.T) {
	whole, err := os.ReadFile(filepath.Join("testdata", "basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	ref := fixture(t, "basic.jsonl")
	for _, chunk := range []int{3, 7, 333, 4096} {
		p := filepath.Join(t.TempDir(), "s.jsonl")
		appendFile(t, p, nil)
		tr := openTranscript(t, p)
		for i := 0; i < len(whole); i += chunk {
			appendFile(t, p, whole[i:min(i+chunk, len(whole))])
			if _, err := tr.poll(); err != nil {
				t.Fatal(err)
			}
		}
		if tr.published() != ref.published() || len(tr.activity(0)) != len(ref.activity(0)) {
			t.Errorf("chunk %d: %+v / %d acts; want %+v / %d", chunk, tr.published(), len(tr.activity(0)), ref.published(), len(ref.activity(0)))
		}
	}
}

// Truncation or replacement re-reads the file: an ActReset, then the new
// content, with Seq still increasing.
func TestTruncateAndReplace(t *testing.T) {
	lines := bytes.SplitAfter(must(os.ReadFile(filepath.Join("testdata", "basic.jsonl"))), []byte("\n"))
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	appendFile(t, p, bytes.Join(lines, nil))
	tr := openTranscript(t, p)
	n := tr.log.Seq()

	if err := os.WriteFile(p, bytes.Join(lines[:6], nil), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.poll(); err != nil {
		t.Fatal(err)
	}
	after := tr.activity(n)
	if len(after) != 3 || after[0].Kind != model.ActReset || after[0].Seq != n+1 ||
		after[1].Kind != model.ActHuman || after[2].Kind != model.ActAssistant {
		t.Fatalf("after truncate: %+v", after)
	}
	eq(t, "totals after truncate", tr.published().totals, model.Usage{Input: 2, Output: 10, CacheRead: 500, CacheWrite1h: 1000})

	np := filepath.Join(dir, "new")
	appendFile(t, np, bytes.Join(lines, nil))
	if err := os.Rename(np, p); err != nil {
		t.Fatal(err)
	}
	n = tr.log.Seq()
	if _, err := tr.poll(); err != nil {
		t.Fatal(err)
	}
	after = tr.activity(n)
	if len(after) != 9 || after[0].Kind != model.ActReset {
		t.Errorf("after replace: %d activities, first %s", len(after), after[0].Kind)
	}
	eq(t, "totals after replace", tr.published().totals, fixture(t, "basic.jsonl").published().totals)
}

func TestDedupeKeepsLatestUsage(t *testing.T) {
	rec := func(out string) string {
		return `{"type":"assistant","timestamp":"2026-10-26T10:00:00Z","message":{"id":"m","model":"claude-opus-5-5","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":` +
			out + `,"cache_read_input_tokens":100,"cache_creation_input_tokens":0}}}` + "\n"
	}
	p := filepath.Join(t.TempDir(), "s.jsonl")
	appendFile(t, p, []byte(rec("9")+rec("99")))
	tr := openTranscript(t, p)
	want := model.Usage{Input: 1, Output: 99, CacheRead: 100}
	if got := tr.published(); got.totals != want || got.lastUsage != want || len(tr.activity(0)) != 1 {
		t.Errorf("totals %+v last %+v acts %d", got.totals, got.lastUsage, len(tr.activity(0)))
	}
}

func TestMaxActivities(t *testing.T) {
	var b strings.Builder
	for i := range 100 {
		b.WriteString(`{"type":"assistant","timestamp":"2026-10-26T10:00:00Z","message":{"id":"m` + strings.Repeat("x", i+1) +
			`","model":"m","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n")
	}
	p := filepath.Join(t.TempDir(), "s.jsonl")
	appendFile(t, p, []byte(b.String()))
	tr := newTranscript(p, DefaultPeerName, time.Hour, tail.NewActivityLog(10), tail.Options{})
	if _, err := tr.poll(); err != nil {
		t.Fatal(err)
	}
	acts := tr.activity(0)
	if n := len(acts); n > 12 || n < 10 || acts[n-1].Seq != 100 {
		t.Errorf("kept %d activities (last seq %d), want ~10 ending at 100", n, acts[n-1].Seq)
	}
	eq(t, "totals cover the whole file", tr.published().totals.Input, int64(100))
}

func TestUsageWithoutBreakdown(t *testing.T) {
	u := rawUsage{InputTokens: 1, CacheCreationInputTokens: 50}
	eq(t, "1h default", u.usage(time.Hour), model.Usage{Input: 1, CacheWrite1h: 50})
	eq(t, "5m default", u.usage(5*time.Minute), model.Usage{Input: 1, CacheWrite5m: 50})
}

func TestPingOf(t *testing.T) {
	type origin = struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		Body string `json:"body"`
	}
	tests := []struct {
		name   string
		origin *origin
		text   string
		wantID string
		want   bool
	}{
		{"peer keepalive with id", &origin{"peer", "keepalive", "[keepalive id=a1] x"}, "wrapped", "a1", true},
		{"peer keepalive no marker", &origin{"peer", "keepalive", "hello"}, "", "", true},
		{"peer other name with marker", &origin{"peer", "other-agent", "[keepalive id=b2] x"}, "", "b2", true},
		{"peer other name plain", &origin{"peer", "other-agent", "please review"}, "", "", false},
		{"human typed ping", &origin{"human", "", ""}, "[keepalive id=c3] Reply only: pong", "c3", true},
		{"human quoting marker", &origin{"human", "", ""}, "see [keepalive id=c3]", "", false},
		{"task notification", &origin{"task-notification", "", ""}, "[keepalive] x", "", false},
		{"legacy no origin", nil, "  [keepalive] ping", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &rawRecord{}
			if tc.origin != nil {
				r.Origin = (*struct {
					Kind string `json:"kind"`
					Name string `json:"name"`
					Body string `json:"body"`
				})(tc.origin)
			}
			id, ok := pingOf(r, tc.text, DefaultPeerName)
			if id != tc.wantID || ok != tc.want {
				t.Errorf("pingOf = %q, %v; want %q, %v", id, ok, tc.wantID, tc.want)
			}
		})
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
