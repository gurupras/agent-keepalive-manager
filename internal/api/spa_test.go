package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

func TestIndexCarriesTokenAndCSP(t *testing.T) {
	e := newEnv(t, nil)
	for _, p := range []string{"/", "/index.html", "/some/route"} {
		w := e.do("GET", p, "", map[string]string{TokenHeader: ""})
		wantStatus(t, w, 200)
		body := w.Body.String()
		if !strings.Contains(body, `<meta name="ka-token" content="`+testToken+`"></head>`) {
			t.Fatalf("%s: token meta missing: %s", p, body)
		}
		if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s: Cache-Control %q", p, cc)
		}
		csp := w.Header().Get("Content-Security-Policy")
		for _, want := range []string{"script-src 'self'", "style-src 'self'", "connect-src 'self' ws://127.0.0.1:4344 wss://127.0.0.1:4344", "frame-ancestors 'self'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP %q lacks %q", p, csp, want)
			}
		}
	}
}

func TestAssetsCaching(t *testing.T) {
	e := newEnv(t, nil)
	w := e.do("GET", "/assets/index-B9x1k2Qa.js", "", nil)
	wantStatus(t, w, 200)
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("hashed asset: %q", cc)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("content type %q", ct)
	}
	w = e.do("GET", "/favicon.svg", "", nil)
	wantStatus(t, w, 200)
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("unhashed: %q", cc)
	}
	wantStatus(t, e.do("GET", "/assets/missing-AAAAAAAA1.js", "", nil), 404)
	wantStatus(t, e.do("POST", "/", "{}", nil), 405)
	if w := e.do("GET", "/../../etc/passwd", "", nil); strings.Contains(w.Body.String(), "root:") {
		t.Fatal("path traversal")
	}
}

func TestPlaceholderWithoutUI(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.UI = fstest.MapFS{} })
	w := e.do("GET", "/", "", nil)
	wantStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "no web UI") || !strings.Contains(w.Body.String(), `name="ka-token"`) {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), "<script") || strings.Contains(w.Body.String(), "<style") {
		t.Fatal("placeholder must not need inline script or style")
	}
	n := newEnv(t, func(o *Options) { o.UI = nil })
	wantStatus(t, n.do("GET", "/", "", nil), 200)
}

func TestAddon(t *testing.T) {
	addon := fstest.MapFS{
		"addon.css":    {Data: []byte(":root{--ka-bg:#000}")},
		"addon.js":     {Data: []byte("export{}")},
		"img/logo.png": {Data: []byte("png")},
	}
	e := newEnv(t, func(o *Options) { o.Addon = addon })
	w := e.do("GET", "/", "", nil)
	body := w.Body.String()
	css := strings.Index(body, `<link rel="stylesheet" href="./addon/addon.css">`)
	js := strings.Index(body, `<meta name="ka-addon" content=""><script type="module" src="./addon/addon.js"></script>`)
	if css < 0 || js < 0 || css > strings.Index(body, "</head>") {
		t.Fatalf("addon links: %s", body)
	}
	w = e.do("GET", "/addon/addon.css", "", nil)
	wantStatus(t, w, 200)
	if w.Header().Get("Cache-Control") != "no-cache" || !strings.Contains(w.Header().Get("Content-Type"), "css") {
		t.Fatal(w.Header())
	}
	wantStatus(t, e.do("GET", "/addon/img/logo.png", "", nil), 200)
	wantStatus(t, e.do("GET", "/addon/missing.js", "", nil), 404)
	wantStatus(t, e.do("GET", "/addon/", "", nil), 404)

	// Only the files that exist are linked.
	only := newEnv(t, func(o *Options) { o.Addon = fstest.MapFS{"addon.css": {Data: []byte("")}} })
	if b := only.do("GET", "/", "", nil).Body.String(); strings.Contains(b, "addon.js") || strings.Contains(b, "ka-addon") || !strings.Contains(b, "addon.css") {
		t.Fatal(b)
	}
	// Without --ui-addon there is no addon/ route: it falls to the UI.
	plain := newEnv(t, nil)
	wantStatus(t, plain.do("GET", "/addon/addon.js", "", nil), 404)
}

func TestIsHashed(t *testing.T) {
	for name, want := range map[string]bool{
		"assets/index-B9x1k2Qa.js": true, "index-abcdefgh.js": false, "logo.svg": false,
		"chunk.4f3a9b2c.css": true, "favicon.ico": false,
	} {
		if isHashed(name) != want {
			t.Errorf("%s: %v", name, !want)
		}
	}
}

// TestProxy runs a holder server and a second server proxying to it with
// its own token, and checks requests and the WebSocket go through while the
// proxy still applies its own guard.
func TestProxy(t *testing.T) {
	holder, holderHost := wsServer(t, nil)
	holderPort, _ := strconv.Atoi(strings.TrimPrefix(holderHost, "127.0.0.1:"))

	const proxyToken = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	quit := make(chan struct{}, 1)
	h, err := New(Options{
		Upstream: func() (int, string, error) { return holderPort, testToken, nil },
		Info:     apitypes.Info{Name: Name, Version: "1.0.0"}, Token: proxyToken, Port: port, UI: testUI,
		Quit: func() { quit <- struct{}{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = ln
	srv.Start()
	defer srv.Close()
	base := "http://127.0.0.1:" + strconv.Itoa(port)

	do := func(method, path, body, token string, hdr map[string]string) *http.Response {
		t.Helper()
		var r *http.Request
		if body != "" {
			r, _ = http.NewRequest(method, base+path, strings.NewReader(body))
		} else {
			r, _ = http.NewRequest(method, base+path, nil)
		}
		if token != "" {
			r.Header.Set(TokenHeader, token)
		}
		if isMutating(method) {
			r.Header.Set("Content-Type", "application/json")
		}
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if r := do("GET", "/api/sessions", "", proxyToken, nil); r.StatusCode != 200 {
		t.Fatalf("proxied GET: %d", r.StatusCode)
	}
	if r := do("POST", sess(keyA, "/ping"), "", proxyToken, map[string]string{"Origin": base, "Sec-Fetch-Site": "same-origin"}); r.StatusCode != 200 {
		t.Fatalf("proxied POST: %d", r.StatusCode)
	}
	// The holder's token is not ours: our guard refuses it.
	if r := do("GET", "/api/sessions", "", testToken, nil); r.StatusCode != 401 {
		t.Fatalf("holder token accepted by proxy: %d", r.StatusCode)
	}
	if r := do("POST", sess(keyA, "/ping"), "", proxyToken, map[string]string{"Origin": "http://evil.example"}); r.StatusCode != 403 {
		t.Fatalf("foreign origin through proxy: %d", r.StatusCode)
	}
	// quit stops this server, not the holder.
	if r := do("POST", "/api/quit", "", proxyToken, nil); r.StatusCode != 202 {
		t.Fatalf("quit: %d", r.StatusCode)
	}
	select {
	case <-quit:
	case <-time.After(time.Second):
		t.Fatal("proxy quit not called")
	}
	select {
	case <-holder.quit:
		t.Fatal("holder quit through the proxy")
	default:
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := dialWS(ctx, "127.0.0.1:"+strconv.Itoa(port), proxyToken, base)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if ev := readEvent(t, ctx, c); ev.Type != "hello" {
		t.Fatalf("%+v", ev)
	}
	readEvent(t, ctx, c)
	holder.svc.publish(model.Event{Type: "plan", Data: model.Plan{ID: 1}})
	if ev := readEvent(t, ctx, c); ev.Type != "plan" {
		t.Fatalf("%+v", ev)
	}
	c.Close(websocket.StatusNormalClosure, "")

	// The holder is gone: 502, not a hang.
	down, _ := New(Options{
		Upstream: func() (int, string, error) { return 1, testToken, nil },
		Token:    proxyToken, Port: testPort,
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/sessions", nil)
	r.Host = "127.0.0.1:4344"
	r.Header.Set(TokenHeader, proxyToken)
	down.ServeHTTP(w, r)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("down holder: %d", w.Code)
	}
}
