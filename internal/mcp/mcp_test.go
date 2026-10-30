package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

var t0 = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

type fakeBackend struct {
	mu       sync.Mutex
	sessions []model.SessionView
	calls    []string
	maxHours float64
	cap      float64 // break-even cap on new plans, hours
}

func (f *fakeBackend) Sessions(context.Context) ([]model.SessionView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.SessionView(nil), f.sessions...), nil
}

func (f *fakeBackend) find(key string) *model.SessionView {
	for i := range f.sessions {
		if f.sessions[i].Key == key {
			return &f.sessions[i]
		}
	}
	return nil
}

func (f *fakeBackend) StartPlan(_ context.Context, key string, hours float64) (model.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("start %s %g", key, hours))
	v := f.find(key)
	if v.Reach == model.ReachNone {
		return model.Plan{}, fmt.Errorf("%w: cannot reach it", model.ErrConflict)
	}
	if hours > f.maxHours {
		return model.Plan{}, fmt.Errorf("%w: hours must be in (0, %g]", model.ErrInvalid, f.maxHours)
	}
	h := hours
	if f.cap > 0 && f.cap < h {
		h = f.cap
	}
	v.Plan = &model.Plan{Key: key, State: model.PlanActive, StartedAt: t0, Until: t0.Add(time.Duration(h * float64(time.Hour)))}
	return *v.Plan, nil
}

func (f *fakeBackend) KeepIndefinitely(_ context.Context, key string) (model.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "indefinite "+key)
	v := f.find(key)
	v.Plan = &model.Plan{Key: key, State: model.PlanActive, Indefinite: true, Until: t0.Add(5 * time.Hour)}
	return *v.Plan, nil
}

func (f *fakeBackend) ExtendPlan(_ context.Context, key string, add float64) (model.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("extend %s %g", key, add))
	v := f.find(key)
	v.Plan.Until = v.Plan.Until.Add(time.Duration(add * float64(time.Hour)))
	return *v.Plan, nil
}

func (f *fakeBackend) ResumePlan(_ context.Context, key string) (model.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "resume "+key)
	v := f.find(key)
	v.Plan.State = model.PlanActive
	return *v.Plan, nil
}

func (f *fakeBackend) CancelPlan(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "cancel "+key)
	v := f.find(key)
	if v.Plan == nil {
		return fmt.Errorf("%w: no plan", model.ErrNotFound)
	}
	v.Plan = nil
	return nil
}

func (f *fakeBackend) Totals(context.Context) (model.Totals, error) {
	return model.Totals{Saved: 1.234, Net: 1.2, Pings: 7}, nil
}

func sess(key string, pid int, cwd string, st model.Status) model.SessionView {
	v := model.SessionView{}
	v.Key, v.ID, v.PID, v.Cwd, v.Project, v.Status = key, strings.TrimPrefix(key, "claude:"), pid, cwd, cwd, st
	v.Reach, v.Title = model.ReachSocket, "t-"+v.ID
	v.LastCall, v.StatusSince = t0.Add(-time.Minute), t0.Add(-time.Minute)
	return v
}

// The tree: 1 init → 100 coordinator (claude:boss) → 101 shell → 102 this
// MCP server; 100 → 110 tmux → 111 claude:w1, 112 claude:w2. 200 is a
// session elsewhere.
func newServer(f *fakeBackend) *Server {
	parents := map[int]int{100: 1, 101: 100, 102: 101, 110: 100, 111: 110, 112: 110, 200: 1}
	return &Server{
		Backend: f, Version: "test", PID: 102, Cwd: "/w/boss",
		Parents: func() map[int]int { return parents },
		Now:     func() time.Time { return t0 },
		Poll:    time.Millisecond,
	}
}

func fixture() *fakeBackend {
	return &fakeBackend{maxHours: 36, sessions: []model.SessionView{
		sess("claude:boss", 100, "/w/boss", model.StatusWorking),
		sess("claude:w1", 111, "/w/boss/wt/one", model.StatusIdle),
		sess("claude:w2", 112, "/w/boss/wt/two", model.StatusWorking),
		sess("claude:other", 200, "/elsewhere", model.StatusIdle),
	}}
}

func call(t *testing.T, s *Server, name, args string) (map[string]any, bool) {
	t.Helper()
	res, rerr := s.handle(context.Background(), request{Method: "tools/call", Params: json.RawMessage(fmt.Sprintf(`{"name":%q,"arguments":%s}`, name, args))})
	if rerr != nil {
		t.Fatalf("%s: rpc error %+v", name, rerr)
	}
	m := res.(map[string]any)
	text := m["content"].([]map[string]any)[0]["text"].(string)
	if m["isError"] == true {
		return map[string]any{"error": text}, false
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s: %v in %s", name, err, text)
	}
	return out, true
}

func keys(list any, field string) []string {
	var out []string
	for _, x := range list.([]any) {
		out = append(out, x.(map[string]any)[field].(string))
	}
	return out
}

func TestTargets(t *testing.T) {
	s := newServer(fixture())
	for _, c := range []struct{ args, want string }{
		{`{}`, "claude:boss claude:w1 claude:w2 claude:other"},
		{`{"target":{"self":true}}`, "claude:boss"},
		{`{"target":{"spawnedByMe":true}}`, "claude:w1 claude:w2"},
		{`{"target":{"spawnedByMe":true,"self":true}}`, "claude:boss claude:w1 claude:w2"},
		{`{"target":{"cwdPrefix":"/w/boss/wt"}}`, "claude:w1 claude:w2"},
		{`{"target":{"project":"/w/boss"}}`, "claude:boss claude:w1 claude:w2"},
		{`{"target":{"sessions":["w2","claude:other"]}}`, "claude:w2 claude:other"},
		{`{"target":{"all":true,"titleContains":"T-W"}}`, "claude:w1 claude:w2"},
	} {
		out, ok := call(t, s, "list_sessions", c.args)
		if !ok {
			t.Fatalf("%s: %v", c.args, out["error"])
		}
		if got := strings.Join(keys(out["sessions"], "key"), " "); got != c.want {
			t.Errorf("%s: got %s, want %s", c.args, got, c.want)
		}
	}
	out, _ := call(t, s, "list_sessions", `{}`)
	first := out["sessions"].([]any)[0].(map[string]any)
	if first["self"] != true {
		t.Errorf("the coordinator is not marked self: %v", first)
	}
}

func TestSelfByCwd(t *testing.T) {
	f := fixture()
	s := newServer(f)
	s.Parents = func() map[int]int { return nil } // no ancestry (Windows without it, a sandbox)
	out, ok := call(t, s, "list_sessions", `{"target":{"self":true}}`)
	if !ok || strings.Join(keys(out["sessions"], "key"), " ") != "claude:boss" {
		t.Fatalf("self by cwd: %v", out)
	}
	s.Cwd = "/nowhere"
	if out, ok := call(t, s, "keep_alive", `{"hours":2}`); ok || !strings.Contains(out["error"].(string), "cannot tell which session") {
		t.Fatalf("unknown self: %v", out)
	}
}

func TestKeepAliveSelf(t *testing.T) {
	f := fixture()
	f.cap = 3
	s := newServer(f)
	out, ok := call(t, s, "keep_alive", `{"hours":24}`)
	if !ok {
		t.Fatal(out["error"])
	}
	r := out["results"].([]any)[0].(map[string]any)
	if r["key"] != "claude:boss" || r["ok"] != true || !strings.Contains(r["note"].(string), "earlier than asked") {
		t.Fatalf("result %v", r)
	}
	// Again: the plan exists and ends before the 24 h, so it is extended.
	out, _ = call(t, s, "keep_alive", `{"hours":24}`)
	r = out["results"].([]any)[0].(map[string]any)
	if !strings.Contains(r["note"].(string), "extended") {
		t.Fatalf("second call %v", r)
	}
	// Shorter than what is left: left alone.
	out, _ = call(t, s, "keep_alive", `{"hours":1}`)
	r = out["results"].([]any)[0].(map[string]any)
	if !strings.Contains(r["note"].(string), "already kept warm until") {
		t.Fatalf("third call %v", r)
	}
	want := []string{"start claude:boss 24", "extend claude:boss 21"}
	if strings.Join(f.calls, ",") != strings.Join(want, ",") {
		t.Errorf("calls %v, want %v", f.calls, want)
	}
}

func TestKeepAliveSwarm(t *testing.T) {
	f := fixture()
	f.sessions[2].Reach, f.sessions[2].ReachWhy = model.ReachNone, "no socket"
	f.sessions[1].Plan = &model.Plan{Key: "claude:w1", State: model.PlanPaused, Until: t0.Add(time.Hour)}
	s := newServer(f)
	out, ok := call(t, s, "keep_alive", `{"untilStopped":true,"target":{"spawnedByMe":true,"sessions":["gone"]}}`)
	if !ok {
		t.Fatal(out["error"])
	}
	got := map[string]map[string]any{}
	for _, x := range out["results"].([]any) {
		m := x.(map[string]any)
		got[m["key"].(string)] = m
	}
	if got["claude:w1"]["ok"] != true || !strings.Contains(got["claude:w1"]["note"].(string), "resumed") {
		t.Errorf("w1 %v", got["claude:w1"])
	}
	if got["claude:w2"]["ok"] != true { // the fake does not refuse indefinite plans
		t.Errorf("w2 %v", got["claude:w2"])
	}
	if got["gone"]["error"] == nil {
		t.Errorf("gone %v", got["gone"])
	}
	for _, bad := range []string{`{}`, `{"hours":1,"untilStopped":true}`, `{"hours":-1}`, `{"hours":1,"bogus":1}`} {
		if _, ok := call(t, s, "keep_alive", bad); ok {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestStop(t *testing.T) {
	f := fixture()
	f.sessions[1].Plan = &model.Plan{State: model.PlanActive}
	s := newServer(f)
	out, _ := call(t, s, "stop_keep_alive", `{"target":{"spawnedByMe":true}}`)
	notes := keys(out["results"], "note")
	if strings.Join(notes, ",") != "stopped,had no plan" {
		t.Errorf("notes %v", notes)
	}
}

func TestAgentStatus(t *testing.T) {
	f := fixture()
	f.sessions[1].HarnessStatus = "shell"
	f.sessions[2].LastCall, f.sessions[2].StatusSince = t0.Add(-45*time.Minute), t0.Add(-50*time.Minute)
	f.sessions[3].Status, f.sessions[3].HarnessStatus, f.sessions[3].StatusSince = model.StatusWorking, "waiting", t0.Add(-7*time.Minute)
	f.sessions[3].Plan = &model.Plan{State: model.PlanActive, Blocked: "the session is busy"}
	s := newServer(f)
	out, ok := call(t, s, "agent_status", `{"target":{"all":true}}`)
	if !ok {
		t.Fatal(out["error"])
	}
	if out["summary"] != "1 working, 1 idle, 1 waiting, 1 stuck; 2 need attention" {
		t.Errorf("summary %q", out["summary"])
	}
	agents := out["agents"].([]any)
	order := keys(agents, "key")
	if strings.Join(order[:2], " ") != "claude:other claude:w2" {
		t.Errorf("worst first: %v", order)
	}
	codes := func(a any) string {
		var cs []string
		for _, p := range a.(map[string]any)["problems"].([]any) {
			cs = append(cs, p.(map[string]any)["code"].(string))
		}
		return strings.Join(cs, ",")
	}
	if c := codes(agents[0]); c != "needs_input,plan_blocked" {
		t.Errorf("waiting agent problems %s", c)
	}
	if c := codes(agents[1]); c != "no_progress" {
		t.Errorf("stuck agent problems %s", c)
	}
	for _, a := range agents {
		m := a.(map[string]any)
		if m["key"] == "claude:w1" && m["backgroundCommands"] != true {
			t.Errorf("w1 %v", m)
		}
	}
	// A session asked for by key that is gone is reported exited.
	out, _ = call(t, s, "agent_status", `{"target":{"sessions":["claude:gone"]}}`)
	if out["summary"] != "1 exited; 1 need attention" {
		t.Errorf("exited summary %q", out["summary"])
	}
}

func TestWaitForAgents(t *testing.T) {
	f := fixture()
	s := newServer(f)
	go func() {
		time.Sleep(20 * time.Millisecond)
		f.mu.Lock()
		f.sessions[2].Status = model.StatusIdle
		f.mu.Unlock()
	}()
	out, ok := call(t, s, "wait_for_agents", `{"target":{"spawnedByMe":true},"timeoutSeconds":5}`)
	if !ok {
		t.Fatal(out["error"])
	}
	ch := out["changes"].([]any)
	if len(ch) != 1 || ch[0].(map[string]any)["key"] != "claude:w2" || ch[0].(map[string]any)["to"] != "idle" {
		t.Fatalf("changes %v", ch)
	}
	// all_idle: already true, returns at once.
	out, _ = call(t, s, "wait_for_agents", `{"target":{"spawnedByMe":true},"until":"all_idle"}`)
	if out["reason"] != "all idle" {
		t.Fatalf("all_idle %v", out["reason"])
	}
	// An exit ends an all_idle wait early.
	f.mu.Lock()
	f.sessions[2].Status = model.StatusWorking
	f.mu.Unlock()
	go func() {
		time.Sleep(20 * time.Millisecond)
		f.mu.Lock()
		f.sessions = f.sessions[:2]
		f.mu.Unlock()
	}()
	out, _ = call(t, s, "wait_for_agents", `{"target":{"spawnedByMe":true},"until":"all_idle","timeoutSeconds":5}`)
	if !strings.Contains(out["reason"].(string), "exited") {
		t.Fatalf("exit %v", out["reason"])
	}
	out, _ = call(t, s, "wait_for_agents", `{"target":{"self":true},"timeoutSeconds":1}`)
	if !strings.HasPrefix(out["reason"].(string), "timeout") {
		t.Fatalf("timeout %v", out["reason"])
	}
}

// TestServe runs the stdio transport: initialize, tools/list, a call, an
// unknown method, a notification (no answer) and a cancelled wait.
func TestServe(t *testing.T) {
	s := newServer(fixture())
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"savings","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"nope"}`,
		`{"jsonrpc":"2.0","id":"w","method":"tools/call","params":{"name":"wait_for_agents","arguments":{"timeoutSeconds":60}}}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"w"}}`,
		`not json`,
	}, "\n") + "\n"
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte(in))
		time.Sleep(50 * time.Millisecond) // let the cancel land before EOF
		pw.Close()
	}()
	var out bytes.Buffer
	if err := s.Serve(context.Background(), pr, &out); err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad line %q", line)
		}
		got[fmt.Sprint(m["id"])] = m
	}
	if v := got["1"]["result"].(map[string]any)["protocolVersion"]; v != "2025-06-18" {
		t.Errorf("protocolVersion %v", v)
	}
	if n := len(got["2"]["result"].(map[string]any)["tools"].([]any)); n != 6 {
		t.Errorf("%d tools", n)
	}
	if got["3"]["result"].(map[string]any)["isError"] != nil {
		t.Errorf("savings failed: %v", got["3"])
	}
	if got["4"]["error"].(map[string]any)["code"].(float64) != codeMethodNotFound {
		t.Errorf("unknown method: %v", got["4"])
	}
	if _, ok := got["w"]; ok {
		t.Errorf("a cancelled request was answered")
	}
	if got["<nil>"]["error"] == nil {
		t.Errorf("no parse error answer: %v", got)
	}
	if len(got) != 5 {
		t.Errorf("%d answers: %v", len(got), got)
	}
}
