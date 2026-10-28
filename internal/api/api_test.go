package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

const (
	testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testPort  = 4344
	keyA      = "claude:aaaa"
)

var testUI = fstest.MapFS{
	"index.html":               {Data: []byte("<!doctype html><html><head><title>t</title></head><body></body></html>")},
	"assets/index-B9x1k2Qa.js": {Data: []byte("console.log(1)")},
	"favicon.svg":              {Data: []byte("<svg/>")},
}

type env struct {
	h    http.Handler
	svc  *fakeService
	met  *fakeMetrics
	upd  *fakeUpdater
	quit chan struct{}
}

func newEnv(t *testing.T, mod func(*Options)) *env {
	t.Helper()
	e := &env{
		svc:  newFakeService(keyA, "claude:changed"),
		met:  &fakeMetrics{st: apitypes.MetricsStatus{Available: true, Enabled: true}},
		upd:  &fakeUpdater{st: apitypes.UpdateStatus{Enabled: true, Current: "1.0.0", State: apitypes.UpdateIdle, Action: apitypes.ActionApply}},
		quit: make(chan struct{}, 1),
	}
	opts := Options{
		Service: e.svc, Metrics: e.met, Updater: e.upd,
		Info:  apitypes.Info{Name: Name, Version: "1.0.0"},
		Token: testToken, Port: testPort, UI: testUI,
		Quit: func() { e.quit <- struct{}{} },
	}
	if mod != nil {
		mod(&opts)
	}
	h, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	e.h = h
	return e
}

// req builds an authorised same-origin request; hdr overrides headers
// ("" deletes one). A "Host" entry sets the Host.
func req(method, target, body string, hdr map[string]string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.Host = "127.0.0.1:" + strconv.Itoa(testPort)
	r.Header.Set(TokenHeader, testToken)
	if isMutating(method) {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		switch {
		case k == "Host":
			r.Host = v
		case v == "":
			r.Header.Del(k)
		default:
			r.Header.Set(k, v)
		}
	}
	return r
}

func (e *env) do(method, target, body string, hdr map[string]string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req(method, target, body, hdr))
	return w
}

func sess(key, suffix string) string { return "/api/sessions/" + url.PathEscape(key) + suffix }

func wantStatus(t *testing.T, w *httptest.ResponseRecorder, code int) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("status %d, want %d; body %s", w.Code, code, w.Body.String())
	}
}

func decodeBody[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return v
}

func TestHealthNeedsNoToken(t *testing.T) {
	e := newEnv(t, nil)
	w := e.do("GET", "/api/health", "", map[string]string{TokenHeader: ""})
	wantStatus(t, w, 200)
	h := decodeBody[map[string]any](t, w)
	if h["name"] != Name || h["version"] != "1.0.0" || h["port"] != float64(testPort) {
		t.Fatalf("%v", h)
	}
}

func TestInfoAndSessions(t *testing.T) {
	e := newEnv(t, nil)
	wantStatus(t, e.do("GET", "/api/info", "", nil), 200)
	w := e.do("GET", "/api/sessions", "", nil)
	wantStatus(t, w, 200)
	if got := decodeBody[[]model.SessionView](t, w); len(got) != 2 {
		t.Fatalf("%v", got)
	}
	wantStatus(t, e.do("GET", sess(keyA, ""), "", nil), 200)
	wantStatus(t, e.do("GET", sess("claude:nope", ""), "", nil), 404)
	// The encoded key form of docs/api.md.
	wantStatus(t, e.do("GET", "/api/sessions/claude%3Aaaaa", "", nil), 200)
}

func TestEmptyListsAreArrays(t *testing.T) {
	e := newEnv(t, nil)
	e.svc.sessions = map[string]model.SessionView{}
	for _, p := range []string{"/api/sessions", sess(keyA, "/pings")} {
		w := e.do("GET", p, "", nil)
		if strings.TrimSpace(w.Body.String()) != "[]" {
			t.Errorf("%s: %s", p, w.Body.String())
		}
	}
	w := e.do("GET", "/api/ledger", "", nil)
	if !strings.Contains(w.Body.String(), `"entries":[]`) {
		t.Errorf("ledger: %s", w.Body.String())
	}
}

func TestPlanRoutes(t *testing.T) {
	e := newEnv(t, nil)
	wantStatus(t, e.do("POST", sess(keyA, "/plan"), `{}`, nil), 400)
	wantStatus(t, e.do("POST", sess(keyA, "/plan"), `{"hours":-1}`, nil), 400)
	wantStatus(t, e.do("POST", sess(keyA, "/plan"), `{"hours":2,"indefinite":true}`, nil), 400)
	wantStatus(t, e.do("POST", sess(keyA, "/plan"), `{"hours":2,"bogus":1}`, nil), 400)
	w := e.do("POST", sess(keyA, "/plan"), `{"hours":2}`, nil)
	wantStatus(t, w, 201)
	if p := decodeBody[model.Plan](t, w); p.Hours != 2 {
		t.Fatalf("%+v", p)
	}
	wantStatus(t, e.do("POST", sess(keyA, "/plan"), `{"indefinite":true}`, nil), 409)
	wantStatus(t, e.do("POST", sess("claude:nope", "/plan"), `{"hours":1}`, nil), 404)

	w = e.do("PATCH", sess(keyA, "/plan"), `{"addHours":1.5}`, nil)
	wantStatus(t, w, 200)
	if p := decodeBody[model.Plan](t, w); p.Hours != 3.5 {
		t.Fatalf("%+v", p)
	}
	wantStatus(t, e.do("PATCH", sess(keyA, "/plan"), `{"resume":true}`, nil), 200)
	wantStatus(t, e.do("PATCH", sess(keyA, "/plan"), `{}`, nil), 400)
	wantStatus(t, e.do("PATCH", sess(keyA, "/plan"), `{"resume":true,"addHours":1}`, nil), 400)

	w = e.do("DELETE", sess(keyA, "/plan"), "", nil)
	wantStatus(t, w, 204)
}

func TestUnreachableIs409WithReason(t *testing.T) {
	e := newEnv(t, nil)
	e.svc.sessions["codex:x"] = model.SessionView{Session: model.Session{Key: "codex:x", Reach: model.ReachNone, ReachWhy: "no codex binary"}}
	w := e.do("POST", sess("codex:x", "/plan"), `{"hours":1}`, nil)
	wantStatus(t, w, 409)
	if !strings.Contains(w.Body.String(), "no codex binary") {
		t.Fatal(w.Body.String())
	}
}

func TestPingRoutes(t *testing.T) {
	e := newEnv(t, nil)
	wantStatus(t, e.do("POST", sess(keyA, "/ping"), "", nil), 200)
	wantStatus(t, e.do("POST", sess("claude:changed", "/ping"), "", nil), 409)
	wantStatus(t, e.do("GET", sess(keyA, "/pings?limit=10"), "", nil), 200)
	wantStatus(t, e.do("GET", sess(keyA, "/pings?limit=x"), "", nil), 400)
}

func TestLedgerAndMissedParams(t *testing.T) {
	e := newEnv(t, nil)
	wantStatus(t, e.do("GET", "/api/ledger?from=2026-10-01T00:00:00Z", "", nil), 200)
	if e.svc.lastFrom.IsZero() {
		t.Fatal("from not passed")
	}
	wantStatus(t, e.do("GET", "/api/ledger?from=yesterday", "", nil), 400)
	wantStatus(t, e.do("GET", "/api/ledger?from=2026-10-02T00:00:00Z&to=2026-10-01T00:00:00Z", "", nil), 400)

	wantStatus(t, e.do("GET", "/api/missed", "", nil), 200)
	if d := time.Since(e.svc.since); d < 7*24*time.Hour-time.Minute || d > 7*24*time.Hour+time.Minute {
		t.Fatalf("default since %v ago", d)
	}
	wantStatus(t, e.do("GET", "/api/missed?since=bad", "", nil), 400)
}

func TestSettingsValidation(t *testing.T) {
	e := newEnv(t, nil)
	for _, body := range []string{`{"maxHours":0.4}`, `{"maxHours":169}`, `{"marginMin":0.5}`, `{"marginMin":16}`, `{"scanSeconds":1}`, `{"scanSeconds":61}`, `{"pingText":"x"}`} {
		wantStatus(t, e.do("PUT", "/api/settings", body, nil), 400)
	}
	w := e.do("PUT", "/api/settings", `{"maxHours":12}`, nil)
	wantStatus(t, w, 200)
	s := decodeBody[model.Settings](t, w)
	if s.MaxHours != 12 || s.MarginMin != 5 {
		t.Fatalf("partial update lost fields: %+v", s)
	}
}

func TestMetricsRoutes(t *testing.T) {
	e := newEnv(t, nil)
	wantStatus(t, e.do("GET", "/api/metrics", "", nil), 200)
	w := e.do("PUT", "/api/metrics", `{"enabled":false}`, nil)
	wantStatus(t, w, 200)
	if decodeBody[apitypes.MetricsStatus](t, w).Enabled {
		t.Fatal("not disabled")
	}
	wantStatus(t, e.do("PUT", "/api/metrics", `{}`, nil), 400)
	e.met.st.Locked = true
	wantStatus(t, e.do("PUT", "/api/metrics", `{"enabled":true}`, nil), 403)
	wantStatus(t, e.do("GET", "/api/metrics/preview", "", nil), 200)
	wantStatus(t, e.do("POST", "/api/metrics/reset-id", "", nil), 200)
	if e.met.resets != 1 {
		t.Fatal("ResetID not called")
	}

	none := newEnv(t, func(o *Options) { o.Metrics = nil })
	w = none.do("GET", "/api/metrics", "", nil)
	wantStatus(t, w, 200)
	if decodeBody[apitypes.MetricsStatus](t, w).Available {
		t.Fatal("available without metrics")
	}
	wantStatus(t, none.do("PUT", "/api/metrics", `{"enabled":true}`, nil), 409)
	wantStatus(t, none.do("GET", "/api/metrics/preview", "", nil), 409)
}

func TestUpdateRoutes(t *testing.T) {
	e := newEnv(t, nil)
	wantStatus(t, e.do("GET", "/api/update", "", nil), 200)
	w := e.do("POST", "/api/update/check", "", nil)
	wantStatus(t, w, 200)
	if decodeBody[apitypes.UpdateStatus](t, w).State != apitypes.UpdateAvailable {
		t.Fatal(w.Body.String())
	}
	wantStatus(t, e.do("POST", "/api/update/apply", "", nil), 202)
	e.upd.st.Action = apitypes.ActionCommand
	wantStatus(t, e.do("POST", "/api/update/apply", "", nil), 409)

	none := newEnv(t, func(o *Options) { o.Updater = nil })
	w = none.do("GET", "/api/update", "", nil)
	wantStatus(t, w, 200)
	if st := decodeBody[apitypes.UpdateStatus](t, w); st.Enabled || st.Current != "1.0.0" {
		t.Fatalf("%+v", st)
	}
	wantStatus(t, none.do("POST", "/api/update/check", "", nil), 409)
}

func TestQuit(t *testing.T) {
	e := newEnv(t, nil)
	wantStatus(t, e.do("POST", "/api/quit", "", nil), 202)
	select {
	case <-e.quit:
	case <-time.After(time.Second):
		t.Fatal("quit not called")
	}
	sup := newEnv(t, func(o *Options) { o.Info.Supervised = true })
	wantStatus(t, sup.do("POST", "/api/quit", "", nil), 409)
	select {
	case <-sup.quit:
		t.Fatal("supervised server quit")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	e := newEnv(t, nil)
	w := e.do("GET", "/api/nope", "", nil)
	wantStatus(t, w, 404)
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatal(w.Header())
	}
	// A wrong method on a known path is also a JSON error.
	if w := e.do("PUT", "/api/sessions", `{}`, nil); w.Code < 400 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
}

func TestErrorMapping(t *testing.T) {
	cases := map[error]int{
		model.ErrNotFound: 404, model.ErrInvalid: 400, model.ErrConflict: 409,
		model.ErrUnreachable: 409, model.ErrSessionChanged: 409, apitypes.ErrLocked: 403,
		apitypes.ErrUnavailable: 409, context.Canceled: 503, io.EOF: 500,
	}
	for err, want := range cases {
		if got := statusOf(err); got != want {
			t.Errorf("%v: %d, want %d", err, got, want)
		}
	}
}

// wsServer runs the handler on a real loopback listener whose port is the
// one the guard expects.
func wsServer(t *testing.T, mod func(*Options)) (*env, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	e := newEnv(t, func(o *Options) {
		o.Port = port
		if mod != nil {
			mod(o)
		}
	})
	srv := httptest.NewUnstartedServer(e.h)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return e, "127.0.0.1:" + strconv.Itoa(port)
}

func dialWS(ctx context.Context, host, token, origin string) (*websocket.Conn, *http.Response, error) {
	hdr := http.Header{}
	if origin != "" {
		hdr.Set("Origin", origin)
	}
	return websocket.Dial(ctx, "ws://"+host+"/api/ws?token="+token, &websocket.DialOptions{HTTPHeader: hdr})
}

func readEvent(t *testing.T, ctx context.Context, c *websocket.Conn) model.Event {
	t.Helper()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ev model.Event
	if err := json.Unmarshal(b, &ev); err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestWebSocket(t *testing.T) {
	e, host := wsServer(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := dialWS(ctx, host, testToken, "http://"+host)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if ev := readEvent(t, ctx, c); ev.Type != "hello" {
		t.Fatalf("first frame %+v", ev)
	}
	if ev := readEvent(t, ctx, c); ev.Type != "sessions" {
		t.Fatalf("second frame %+v", ev)
	}
	e.svc.publish(model.Event{Type: "plan", Data: model.Plan{ID: 7}})
	if ev := readEvent(t, ctx, c); ev.Type != "plan" {
		t.Fatalf("%+v", ev)
	}
	e.upd.Check(ctx)
	if ev := readEvent(t, ctx, c); ev.Type != "update" {
		t.Fatalf("%+v", ev)
	}
	// A metrics change made through the API reaches the socket.
	r, _ := http.NewRequest("PUT", "http://"+host+"/api/metrics", strings.NewReader(`{"enabled":false}`))
	r.Header.Set(TokenHeader, testToken)
	r.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(err, resp)
	}
	resp.Body.Close()
	if ev := readEvent(t, ctx, c); ev.Type != "metrics" {
		t.Fatalf("%+v", ev)
	}
	c.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(2 * time.Second)
	for e.svc.subscribers() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if e.svc.subscribers() != 0 {
		t.Fatal("subscription leaked after close")
	}
}

func TestWebSocketPingTimeout(t *testing.T) {
	_, host := wsServer(t, func(o *Options) {
		o.PingInterval = 20 * time.Millisecond
		o.PongTimeout = 50 * time.Millisecond
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := dialWS(ctx, host, testToken, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	// Reading answers pings, so the server keeps the socket open well past
	// several ping intervals.
	readEvent(t, ctx, c)
	readEvent(t, ctx, c)
	rctx, rcancel := context.WithTimeout(ctx, 300*time.Millisecond)
	_, _, err = c.Read(rctx)
	rcancel()
	if websocket.CloseStatus(err) != -1 {
		t.Fatalf("server closed a responsive client: %v", err)
	}
	// c.Read failing with a timeout closes the conn on our side; a client
	// that never reads never answers pings, so the server must hang up.
	c2, _, err := dialWS(ctx, host, testToken, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()
	time.Sleep(400 * time.Millisecond) // no reads: pings go unanswered
	var closed bool
	for range 5 {
		if _, _, err := c2.Read(ctx); err != nil {
			closed = true
			break
		}
	}
	if !closed {
		t.Fatal("server kept a silent client")
	}
}
