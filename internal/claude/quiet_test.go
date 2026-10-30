package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func req(at string) string {
	return `{"type":"api-request","sessionId":"s","timestamp":"` + at + `","querySource":"repl_main_thread"}` + "\n"
}

func resp(at, stop string, tools ...string) string {
	content := `{"type":"text","text":"ok"}`
	for _, n := range tools {
		content += `,{"type":"tool_use","id":"t","name":"` + n + `","input":{}}`
	}
	return `{"type":"assistant","sessionId":"s","timestamp":"` + at + `","message":{"id":"m` + at + `","model":"claude-opus-5-5",` +
		`"stop_reason":"` + stop + `","content":[` + content + `],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
}

func toolResult(at string) string {
	return `{"type":"user","sessionId":"s","timestamp":"` + at + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"done"}]}}` + "\n"
}

func prompt(at string) string {
	return `{"type":"user","sessionId":"s","timestamp":"` + at + `","message":{"role":"user","content":"go"}}` + "\n"
}

// The main transcript's api-request records tell a request in flight from
// a quiet main agent.
func TestRequestTracking(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	appendFile(t, p, []byte(resp("2026-10-30T10:00:00Z", "end_turn")))
	tr := openTranscript(t, p)
	if tr.published().tracksRequests() {
		t.Fatal("tracks requests without api-request records")
	}
	appendFile(t, p, []byte(req("2026-10-30T10:01:00Z")))
	tr.poll()
	s := tr.published()
	now := ts("2026-10-30T10:02:00Z")
	if !s.tracksRequests() || !s.calling(now) || !s.quietSince().Equal(ts("2026-10-30T10:01:00Z")) {
		t.Fatalf("request in flight: %+v", s)
	}
	if s.calling(now.Add(requestTimeout)) {
		t.Fatal("a request never answered is believed forever")
	}
	appendFile(t, p, []byte(resp("2026-10-30T10:01:30Z", "end_turn")))
	tr.poll()
	s = tr.published()
	if s.calling(now) || !s.quietSince().Equal(ts("2026-10-30T10:01:30Z")) {
		t.Fatalf("answered: %+v", s)
	}
}

func TestSubagentDecide(t *testing.T) {
	for _, c := range []struct {
		name    string
		lines   []string
		running bool
	}{
		{"turn ended", []string{prompt("2026-10-30T10:00:00Z"), resp("2026-10-30T10:00:01Z", "end_turn")}, false},
		{"tool call", []string{resp("2026-10-30T10:00:01Z", "tool_use", "Bash")}, true},
		{"tool answered", []string{resp("2026-10-30T10:00:01Z", "tool_use", "Bash"), toolResult("2026-10-30T10:00:02Z")}, true},
		{"handed back", []string{resp("2026-10-30T10:00:01Z", "tool_use", handback), toolResult("2026-10-30T10:00:02Z")}, false},
		{"request in flight", []string{resp("2026-10-30T10:00:01Z", "end_turn"), req("2026-10-30T10:00:05Z")}, true},
		{"request after tool result", []string{resp("2026-10-30T10:00:01Z", "tool_use", "Bash"),
			toolResult("2026-10-30T10:00:02Z"), req("2026-10-30T10:00:03Z")}, true},
		{"prompt waiting", []string{resp("2026-10-30T10:00:01Z", "end_turn"), prompt("2026-10-30T10:00:05Z")}, true},
		{"partial record", []string{prompt("2026-10-30T10:00:00Z"), resp("2026-10-30T10:00:01Z", "end_turn"),
			resp("2026-10-30T10:00:02Z", "")}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var lines [][]byte
			for _, l := range c.lines {
				lines = append(lines, []byte(strings.TrimSpace(l)))
			}
			running, ok := decide(lines)
			if !ok || running != c.running {
				t.Fatalf("running=%v ok=%v; want %v", running, ok, c.running)
			}
		})
	}
}

func TestSubagentsRunning(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "s.jsonl")
	subs := filepath.Join(dir, "s", "subagents")
	if err := os.MkdirAll(subs, 0o700); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(subs, "agent-a.jsonl")
	appendFile(t, a, []byte(resp("2026-10-30T10:00:01Z", "tool_use", "Bash")))
	s := newSubagents(main)
	start := time.Now().Add(-time.Minute)
	if !s.running(start) {
		t.Fatal("a sub-agent mid-tool is not running")
	}
	if s.running(time.Now().Add(time.Hour)) {
		t.Fatal("a sub-agent written before the process started counts")
	}
	if err := os.WriteFile(filepath.Join(subs, "agent-a.meta.json"), []byte(`{"stoppedByUser":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	appendFile(t, a, []byte(toolResult("2026-10-30T10:00:02Z")))
	if s.running(start) {
		t.Fatal("a sub-agent the user stopped is running")
	}
}
