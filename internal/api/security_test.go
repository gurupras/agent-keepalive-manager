package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTokenRequired(t *testing.T) {
	e := newEnv(t, nil)
	routes := []struct{ method, path, body string }{
		{"GET", "/api/info", ""},
		{"GET", "/api/sessions", ""},
		{"GET", sess(keyA, ""), ""},
		{"POST", sess(keyA, "/plan"), `{"hours":1}`},
		{"PATCH", sess(keyA, "/plan"), `{"addHours":1}`},
		{"DELETE", sess(keyA, "/plan"), ""},
		{"POST", sess(keyA, "/ping"), ""},
		{"GET", sess(keyA, "/pings"), ""},
		{"GET", "/api/ledger", ""},
		{"GET", "/api/missed", ""},
		{"GET", "/api/settings", ""},
		{"PUT", "/api/settings", `{}`},
		{"GET", "/api/metrics", ""},
		{"PUT", "/api/metrics", `{"enabled":true}`},
		{"GET", "/api/metrics/preview", ""},
		{"POST", "/api/metrics/reset-id", ""},
		{"GET", "/api/update", ""},
		{"POST", "/api/update/check", ""},
		{"POST", "/api/update/apply", ""},
		{"POST", "/api/quit", ""},
		{"GET", "/api/ws", ""},
		{"GET", "/api/nope", ""},
	}
	for _, rt := range routes {
		for name, tok := range map[string]string{"missing": "", "wrong": strings.Repeat("0", 64), "prefix": testToken[:32]} {
			w := e.do(rt.method, rt.path, rt.body, map[string]string{TokenHeader: tok})
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s %s with %s token: %d", rt.method, rt.path, name, w.Code)
			}
		}
	}
	if len(e.svc.calls) != 0 {
		t.Fatalf("service called without a token: %v", e.svc.calls)
	}
	select {
	case <-e.quit:
		t.Fatal("quit without a token")
	default:
	}
}

func TestQueryTokenOnlyForWebSocket(t *testing.T) {
	e := newEnv(t, nil)
	w := e.do("GET", "/api/sessions?token="+testToken, "", map[string]string{TokenHeader: ""})
	wantStatus(t, w, http.StatusUnauthorized)
}

func TestDNSRebindingHostRefused(t *testing.T) {
	e := newEnv(t, nil)
	for _, host := range []string{"evil.example:4344", "evil.example", "127.0.0.1.evil.example:4344", "127.0.0.1:9999", "127.0.0.1", "localhost.:4344", "0.0.0.0:4344"} {
		for _, path := range []string{"/", "/index.html", "/api/health", "/api/sessions"} {
			w := e.do("GET", path, "", map[string]string{"Host": host})
			if w.Code != http.StatusMisdirectedRequest {
				t.Errorf("Host %q %s: %d", host, path, w.Code)
			}
			if strings.Contains(w.Body.String(), testToken) {
				t.Errorf("Host %q %s leaked the token", host, path)
			}
		}
	}
	for _, host := range []string{"127.0.0.1:4344", "localhost:4344", "LOCALHOST:4344", "[::1]:4344"} {
		wantStatus(t, e.do("GET", "/api/health", "", map[string]string{"Host": host}), 200)
	}
}

func TestCrossSiteRefused(t *testing.T) {
	e := newEnv(t, nil)
	cases := []struct {
		name         string
		method, path string
		body         string
		hdr          map[string]string
	}{
		{"cross-site POST", "POST", sess(keyA, "/ping"), "", map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{"cross-site GET api", "GET", "/api/sessions", "", map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{"cross-site subresource of the page", "GET", "/", "", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "script"}},
		{"cross-site iframe", "GET", "/", "", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"}},
		{"same-site POST (other localhost port)", "POST", sess(keyA, "/ping"), "", map[string]string{"Sec-Fetch-Site": "same-site"}},
		{"foreign Origin POST", "POST", sess(keyA, "/plan"), `{"hours":1}`, map[string]string{"Origin": "http://evil.example"}},
		{"other port Origin", "PUT", "/api/settings", `{}`, map[string]string{"Origin": "http://127.0.0.1:5173"}},
		{"null Origin", "POST", "/api/quit", "", map[string]string{"Origin": "null"}},
		{"https Origin", "POST", "/api/quit", "", map[string]string{"Origin": "https://127.0.0.1:4344"}},
		{"form POST", "POST", sess(keyA, "/ping"), "", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}},
		{"text/plain POST", "POST", sess(keyA, "/plan"), `{"hours":1}`, map[string]string{"Content-Type": "text/plain"}},
		{"no content type DELETE", "DELETE", sess(keyA, "/plan"), "", map[string]string{"Content-Type": ""}},
	}
	for _, c := range cases {
		w := e.do(c.method, c.path, c.body, c.hdr)
		if w.Code < 400 {
			t.Errorf("%s: accepted with %d", c.name, w.Code)
		}
	}
	if len(e.svc.calls) != 0 {
		t.Fatalf("service reached: %v", e.svc.calls)
	}
	// Allowed: a top-level navigation from a link elsewhere, and the UI's
	// own same-origin requests.
	wantStatus(t, e.do("GET", "/", "", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}), 200)
	wantStatus(t, e.do("POST", sess(keyA, "/ping"), "", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://127.0.0.1:4344"}), 200)
	wantStatus(t, e.do("POST", sess(keyA, "/ping"), "", map[string]string{"Origin": "http://localhost:4344", "Host": "localhost:4344"}), 200)
}

func TestPreflightNotAnswered(t *testing.T) {
	e := newEnv(t, nil)
	w := e.do("OPTIONS", sess(keyA, "/plan"), "", map[string]string{
		TokenHeader: "", "Origin": "http://evil.example", "Access-Control-Request-Method": "POST",
	})
	if w.Code < 400 || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("preflight: %d %v", w.Code, w.Header())
	}
}

func TestWebSocketOriginAndToken(t *testing.T) {
	_, host := wsServer(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cases := []struct {
		name, token, origin string
		want                int
	}{
		{"no token", "", "", 401},
		{"wrong token", strings.Repeat("a", 64), "", 401},
		{"foreign origin", testToken, "http://evil.example", 403},
		{"other port origin", testToken, "http://127.0.0.1:1", 403},
	}
	for _, c := range cases {
		conn, resp, err := dialWS(ctx, host, c.token, c.origin)
		if err == nil {
			conn.CloseNow()
			t.Errorf("%s: upgraded", c.name)
			continue
		}
		if resp == nil || resp.StatusCode != c.want {
			t.Errorf("%s: %v %v", c.name, resp, err)
		}
	}
	// Cross-site per Fetch Metadata, even with the right origin and token.
	hdr := http.Header{"Origin": {"http://" + host}, "Sec-Fetch-Site": {"cross-site"}}
	r, _ := http.NewRequestWithContext(ctx, "GET", "http://"+host+"/api/ws?token="+testToken, nil)
	r.Header = hdr
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("cross-site upgrade: %d", resp.StatusCode)
	}
}
