package hub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

var testUI = fstest.MapFS{
	"index.html":       {Data: []byte("<!doctype html><title>hub</title>")},
	"assets/app-1.js":  {Data: []byte("console.log(1)")},
	"assets/app-1.css": {Data: []byte("body{}")},
}

func newTestServer(t *testing.T, cfg Config) (*Server, http.Handler) {
	t.Helper()
	srv, err := NewServer(cfg, newSQLiteStore(t), testUI)
	if err != nil {
		t.Fatal(err)
	}
	srv.now = func() time.Time { return testNow }
	return srv, srv.Handler()
}

type reqOpt func(*http.Request)

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }
func withRemote(addr string) reqOpt { return func(r *http.Request) { r.RemoteAddr = addr } }

func do(h http.Handler, method, target string, body []byte, opts ...reqOpt) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(req)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestIngestStatuses(t *testing.T) {
	_, h := newTestServer(t, Config{})
	body := mustJSON(validReport())
	if w := do(h, "POST", "/v1/reports", body); w.Code != http.StatusAccepted {
		t.Fatalf("first: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/v1/reports", body); w.Code != http.StatusOK {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body)
	}
	bad := validReport()
	bad["hostname"] = "x"
	w := do(h, "POST", "/v1/reports", mustJSON(bad))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "hostname") {
		t.Fatalf("unknown field: %d %s", w.Code, w.Body)
	}
	big := bytes.Repeat([]byte(" "), MaxReportBytes+1)
	copy(big, body)
	if w := do(h, "POST", "/v1/reports", big); w.Code != http.StatusBadRequest {
		t.Fatalf("oversized: %d", w.Code)
	}
	// Exactly at the limit is fine (trailing whitespace is not data).
	ok := bytes.Repeat([]byte(" "), MaxReportBytes)
	r2 := validReport()
	r2["day"] = "2026-10-25"
	copy(ok, mustJSON(r2))
	if w := do(h, "POST", "/v1/reports", ok); w.Code != http.StatusAccepted {
		t.Fatalf("at limit: %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/v1/reports", nil); w.Code == http.StatusAccepted || w.Code == http.StatusOK {
		t.Fatalf("GET reports: %d", w.Code)
	}
}

func TestIngestToken(t *testing.T) {
	_, h := newTestServer(t, Config{IngestToken: "s3cret"})
	body := mustJSON(validReport())
	for _, auth := range []string{"", "Bearer wrong", "Basic s3cret", "Bearer s3cret2", "Bearer"} {
		var opts []reqOpt
		if auth != "" {
			opts = append(opts, withHeader("Authorization", auth))
		}
		if w := do(h, "POST", "/v1/reports", body, opts...); w.Code != http.StatusUnauthorized {
			t.Fatalf("auth %q: %d", auth, w.Code)
		}
	}
	if w := do(h, "POST", "/v1/reports", body, withHeader("Authorization", "Bearer s3cret")); w.Code != http.StatusAccepted {
		t.Fatalf("good token: %d %s", w.Code, w.Body)
	}
}

func TestRateLimit(t *testing.T) {
	srv, h := newTestServer(t, Config{})
	body := []byte(`{}`) // invalid reports count too
	for i := 0; i < reportsPerHour; i++ {
		if w := do(h, "POST", "/v1/reports", body, withRemote("192.0.2.1:1234")); w.Code != http.StatusBadRequest {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	w := do(h, "POST", "/v1/reports", body, withRemote("192.0.2.1:999"))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("over limit: %d", w.Code)
	}
	if w := do(h, "POST", "/v1/reports", body, withRemote("192.0.2.2:1234")); w.Code != http.StatusBadRequest {
		t.Fatalf("other sender: %d", w.Code)
	}
	// Without --trust-proxy, X-Forwarded-For does not let a sender escape.
	if w := do(h, "POST", "/v1/reports", body, withRemote("192.0.2.1:1"), withHeader("X-Forwarded-For", "203.0.113.9")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("forged XFF: %d", w.Code)
	}
	// One minute refills one report.
	srv.now = func() time.Time { return testNow.Add(time.Minute) }
	if w := do(h, "POST", "/v1/reports", body, withRemote("192.0.2.1:1")); w.Code != http.StatusBadRequest {
		t.Fatalf("after refill: %d", w.Code)
	}
	if w := do(h, "POST", "/v1/reports", body, withRemote("192.0.2.1:1")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second after refill: %d", w.Code)
	}
	// The limiter holds no addresses.
	for k := range srv.reports.buckets {
		if bytes.Contains(k[:], []byte("192.0.2")) {
			t.Fatal("address stored in limiter")
		}
	}
}

func TestRateLimitTrustProxy(t *testing.T) {
	_, h := newTestServer(t, Config{TrustProxy: true})
	body := []byte(`{}`)
	for i := 0; i < reportsPerHour; i++ {
		// Same proxy address, client spoofs the first hop; the last entry counts.
		xff := fmt.Sprintf("10.9.9.%d, 198.51.100.7", i)
		if w := do(h, "POST", "/v1/reports", body, withRemote("10.0.0.1:80"), withHeader("X-Forwarded-For", xff)); w.Code != http.StatusBadRequest {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	if w := do(h, "POST", "/v1/reports", body, withRemote("10.0.0.1:80"), withHeader("X-Forwarded-For", "1.1.1.1, 198.51.100.7")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("over limit: %d", w.Code)
	}
	if w := do(h, "POST", "/v1/reports", body, withRemote("10.0.0.1:80"), withHeader("X-Forwarded-For", "198.51.100.8")); w.Code != http.StatusBadRequest {
		t.Fatalf("other client behind proxy: %d", w.Code)
	}
}

func TestDashboardModes(t *testing.T) {
	read := []string{"/v1/summary", "/v1/series?metric=pings", "/v1/breakdown?by=os", "/v1/experiments"}

	t.Run("public", func(t *testing.T) {
		_, h := newTestServer(t, Config{})
		for _, p := range read {
			if w := do(h, "GET", p, nil); w.Code != http.StatusOK {
				t.Fatalf("%s: %d %s", p, w.Code, w.Body)
			}
		}
		if w := do(h, "GET", "/", nil); w.Code != http.StatusOK {
			t.Fatalf("dashboard: %d", w.Code)
		}
		if w := do(h, "GET", "/v1/session", nil); strings.TrimSpace(w.Body.String()) != `{"authenticated":true,"mode":"public"}` {
			t.Fatalf("session: %s", w.Body)
		}
		if w := do(h, "POST", "/v1/login", []byte(`{"token":"x"}`)); w.Code != http.StatusNotFound {
			t.Fatalf("login in public mode: %d", w.Code)
		}
	})

	t.Run("off", func(t *testing.T) {
		_, h := newTestServer(t, Config{Dashboard: DashboardOff})
		for _, p := range append(read, "/", "/assets/app-1.js", "/v1/session") {
			if w := do(h, "GET", p, nil); w.Code != http.StatusNotFound {
				t.Fatalf("%s: %d", p, w.Code)
			}
		}
		if w := do(h, "POST", "/v1/reports", mustJSON(validReport())); w.Code != http.StatusAccepted {
			t.Fatalf("ingest with dashboard off: %d", w.Code)
		}
		if w := do(h, "GET", "/healthz", nil); w.Code != http.StatusOK {
			t.Fatalf("healthz: %d", w.Code)
		}
	})

	t.Run("token", func(t *testing.T) {
		if _, err := NewServer(Config{Dashboard: DashboardToken}, nil, testUI); err == nil {
			t.Fatal("token mode without a token accepted")
		}
		_, h := newTestServer(t, Config{Dashboard: DashboardToken, DashboardToken: "dash"})
		for _, p := range read {
			if w := do(h, "GET", p, nil); w.Code != http.StatusUnauthorized {
				t.Fatalf("%s without token: %d", p, w.Code)
			}
			if w := do(h, "GET", p, nil, withHeader("Authorization", "Bearer nope")); w.Code != http.StatusUnauthorized {
				t.Fatalf("%s bad token: %d", p, w.Code)
			}
			if w := do(h, "GET", p, nil, withHeader("Authorization", "Bearer dash")); w.Code != http.StatusOK {
				t.Fatalf("%s bearer: %d", p, w.Code)
			}
		}
		// The page itself loads so it can ask for the token.
		if w := do(h, "GET", "/", nil); w.Code != http.StatusOK {
			t.Fatalf("dashboard page: %d", w.Code)
		}
		if w := do(h, "POST", "/v1/login", []byte(`{"token":"nope"}`)); w.Code != http.StatusUnauthorized {
			t.Fatalf("bad login: %d", w.Code)
		}
		if w := do(h, "POST", "/v1/login", []byte(`{"token":"dash","x":1}`)); w.Code != http.StatusBadRequest {
			t.Fatalf("login unknown field: %d", w.Code)
		}
		req := httptest.NewRequest("POST", "/v1/login", strings.NewReader(`{"token":"dash"}`))
		req.Header.Set("Content-Type", "text/plain")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("login text/plain: %d", w.Code)
		}

		w = do(h, "POST", "/v1/login", []byte(`{"token":"dash"}`))
		if w.Code != http.StatusNoContent {
			t.Fatalf("login: %d %s", w.Code, w.Body)
		}
		cookies := w.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("cookies: %v", cookies)
		}
		c := cookies[0]
		if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Secure || c.Path != "/" ||
			strings.Contains(c.Value, "dash") {
			t.Fatalf("cookie: %+v", c)
		}
		if w := do(h, "GET", "/v1/summary", nil, withHeader("Cookie", c.Name+"="+c.Value)); w.Code != http.StatusOK {
			t.Fatalf("with cookie: %d", w.Code)
		}
		if w := do(h, "GET", "/v1/summary", nil, withHeader("Cookie", c.Name+"=forged")); w.Code != http.StatusUnauthorized {
			t.Fatalf("forged cookie: %d", w.Code)
		}
		for _, tc := range []struct {
			cookie string
			want   string
		}{{"", `{"authenticated":false,"mode":"token"}`}, {c.Name + "=" + c.Value, `{"authenticated":true,"mode":"token"}`}} {
			var opts []reqOpt
			if tc.cookie != "" {
				opts = append(opts, withHeader("Cookie", tc.cookie))
			}
			if w := do(h, "GET", "/v1/session", nil, opts...); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != tc.want {
				t.Fatalf("session (cookie %v): %d %s", tc.cookie != "", w.Code, w.Body)
			}
		}
		w = do(h, "POST", "/v1/logout", nil)
		if w.Code != http.StatusNoContent || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge >= 0 {
			t.Fatalf("logout: %d %v", w.Code, w.Result().Cookies())
		}
	})

	t.Run("secure cookie", func(t *testing.T) {
		for _, tc := range []struct {
			trust  bool
			secure bool
		}{{false, false}, {true, true}} {
			_, h := newTestServer(t, Config{Dashboard: DashboardToken, DashboardToken: "dash", TrustProxy: tc.trust})
			w := do(h, "POST", "/v1/login", []byte(`{"token":"dash"}`), withHeader("X-Forwarded-Proto", "https"))
			if c := w.Result().Cookies(); len(c) != 1 || c[0].Secure != tc.secure {
				t.Fatalf("trust=%v: %v", tc.trust, c)
			}
		}
		_, h := newTestServer(t, Config{Dashboard: DashboardToken, DashboardToken: "dash"})
		req := httptest.NewRequest("POST", "https://hub.example/v1/login", strings.NewReader(`{"token":"dash"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if c := w.Result().Cookies(); len(c) != 1 || !c[0].Secure {
			t.Fatalf("tls: %v", c)
		}
	})
}

func TestBasePath(t *testing.T) {
	_, h := newTestServer(t, Config{BasePath: "/hub/", Dashboard: DashboardToken, DashboardToken: "d"})
	if w := do(h, "POST", "/hub/v1/reports", mustJSON(validReport())); w.Code != http.StatusAccepted {
		t.Fatalf("ingest: %d %s", w.Code, w.Body)
	}
	for _, p := range []string{"/v1/reports", "/healthz", "/", "/v1/summary"} {
		method := "GET"
		if p == "/v1/reports" {
			method = "POST"
		}
		if w := do(h, method, p, mustJSON(validReport())); w.Code != http.StatusNotFound {
			t.Fatalf("%s outside base: %d", p, w.Code)
		}
	}
	if w := do(h, "GET", "/hub/healthz", nil); w.Code != http.StatusOK || w.Body.String() != "ok\n" {
		t.Fatalf("healthz: %d %q", w.Code, w.Body)
	}
	if w := do(h, "GET", "/hub", nil); w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/hub/" {
		t.Fatalf("redirect: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := do(h, "GET", "/hub/", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<title>hub") {
		t.Fatalf("dashboard: %d", w.Code)
	}
	if w := do(h, "GET", "/hub/assets/app-1.js", nil); w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %s", w.Code, w.Header().Get("Cache-Control"))
	}
	w := do(h, "POST", "/hub/v1/login", []byte(`{"token":"d"}`))
	if c := w.Result().Cookies(); len(c) != 1 || c[0].Path != "/hub/" {
		t.Fatalf("cookie path: %v", c)
	}
	if w := do(h, "GET", "/hub/v1/summary?from=2026-10-01&to=2026-10-27", nil, withHeader("Authorization", "Bearer d")); w.Code != http.StatusOK {
		t.Fatalf("summary: %d", w.Code)
	}

	for _, bad := range []string{"/a b", "/../x", "/a//b", "/a?b"} {
		if _, err := NormalizeBasePath(bad); err == nil {
			t.Errorf("base path %q accepted", bad)
		}
	}
	for in, want := range map[string]string{"": "", "/": "", "hub": "/hub", "/a/b/": "/a/b"} {
		if got, err := NormalizeBasePath(in); err != nil || got != want {
			t.Errorf("NormalizeBasePath(%q) = %q, %v", in, got, err)
		}
	}
}

func TestQueryAPI(t *testing.T) {
	_, h := newTestServer(t, Config{})
	rep := validReport()
	if w := do(h, "POST", "/v1/reports", mustJSON(rep)); w.Code != http.StatusAccepted {
		t.Fatal(w.Code)
	}
	w := do(h, "GET", "/v1/summary?from=2026-10-20&to=2026-10-27", nil)
	var sum Summary
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil || sum.Installs != 1 || sum.Pings != 17 || !near(sum.Net, 7.39) {
		t.Fatalf("summary: %s", w.Body)
	}
	// The JSON keys are the documented ones.
	var raw map[string]any
	json.Unmarshal(w.Body.Bytes(), &raw)
	for _, k := range []string{"installs", "activeInstalls", "saved", "wasted", "net", "pingCost", "pings", "cold", "returns",
		"pending", "medianNet", "trimmedNet", "trimmed", "topShare", "establishedDays"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("summary lacks %s", k)
		}
	}
	w = do(h, "GET", "/v1/series?from=2026-10-25&to=2026-10-27&metric=net", nil)
	var pts []Point
	if err := json.Unmarshal(w.Body.Bytes(), &pts); err != nil || len(pts) != 3 || !near(pts[1].Value, 7.39) {
		t.Fatalf("series: %s", w.Body)
	}
	// Default range: the last 30 days.
	w = do(h, "GET", "/v1/series?metric=installs", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &pts); err != nil || len(pts) != 30 || pts[29].Day != "2026-10-27" {
		t.Fatalf("default series: %s", w.Body)
	}
	w = do(h, "GET", "/v1/breakdown?by=model&from=2026-10-01&to=2026-10-27", nil)
	var rows []BreakdownRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].Key != "claude-opus-4-1" {
		t.Fatalf("breakdown: %s", w.Body)
	}
	w = do(h, "GET", "/v1/breakdown?by=os&from=2026-01-01&to=2026-01-02", nil)
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty breakdown: %s", w.Body)
	}
	for _, p := range []string{
		"/v1/summary?from=2026-13-01",
		"/v1/summary?from=2026-10-27&to=2026-10-01",
		"/v1/summary?from=2020-01-01&to=2026-10-01",
		"/v1/series?metric=bogus",
		"/v1/breakdown?by=hostname",
		"/v1/breakdown",
		"/v1/summary?installs=some",
	} {
		if w := do(h, "GET", p, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

func TestExperimentsAPI(t *testing.T) {
	_, h := newTestServer(t, Config{})
	w := do(h, "GET", "/v1/experiments?from=2026-10-01&to=2026-10-27", nil)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"experiments":[]}` {
		t.Fatalf("empty: %d %s", w.Code, w.Body)
	}
	// Schema 1 and schema 2 reports are both stored.
	if w := do(h, "POST", "/v1/reports", mustJSON(validReport())); w.Code != http.StatusAccepted {
		t.Fatalf("schema 1: %d %s", w.Code, w.Body)
	}
	r2 := validReport2()
	r2["day"] = "2026-10-25"
	if w := do(h, "POST", "/v1/reports", mustJSON(r2)); w.Code != http.StatusAccepted {
		t.Fatalf("schema 2: %d %s", w.Code, w.Body)
	}
	bad := validReport2()
	exp0(bad)["costUsd"] = 9.0
	if w := do(h, "POST", "/v1/reports", mustJSON(bad)); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "experiments[0].costUsd") {
		t.Fatalf("bad experiment: %d %s", w.Code, w.Body)
	}

	w = do(h, "GET", "/v1/experiments?from=2026-10-01&to=2026-10-27", nil)
	var body struct {
		Experiments []Experiment `json:"experiments"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Experiments) != 1 {
		t.Fatalf("experiments: %s", w.Body)
	}
	e := body.Experiments[0]
	if e.Experiment != "busy-ping-subagents-1" || len(e.Arms) != 2 || e.Arms[0].Arm != "hold" || e.Arms[1].Arm != "ping" ||
		e.Arms[1].Episodes != 3 || !near(e.Arms[1].WarmRate, 2.0/3) || !near(e.Arms[0].MeanCostUSD, 1.05) || e.Diff == nil {
		t.Fatalf("experiment: %+v", e)
	}
	// The JSON keys are the documented ones.
	var raw struct {
		Experiments []map[string]json.RawMessage `json:"experiments"`
	}
	json.Unmarshal(w.Body.Bytes(), &raw)
	for _, k := range []string{"experiment", "arms", "diff"} {
		if _, ok := raw.Experiments[0][k]; !ok {
			t.Errorf("experiment lacks %s", k)
		}
	}
	var arms []map[string]any
	json.Unmarshal(raw.Experiments[0]["arms"], &arms)
	for _, k := range []string{"arm", "installs", "episodes", "returned", "warm", "pings", "pingCalls",
		"meanCostUsd", "seCostUsd", "warmRate", "pingsPerEpisode", "meanQuietMinutes"} {
		if _, ok := arms[0][k]; !ok {
			t.Errorf("arm lacks %s", k)
		}
	}
	var diff map[string]any
	json.Unmarshal(raw.Experiments[0]["diff"], &diff)
	for _, k := range []string{"meanCostUsd", "ciLowUsd", "ciHighUsd", "significant"} {
		if _, ok := diff[k]; !ok {
			t.Errorf("diff lacks %s", k)
		}
	}

	// A range without the schema 2 day has none.
	w = do(h, "GET", "/v1/experiments?from=2026-10-26&to=2026-10-27", nil)
	if strings.TrimSpace(w.Body.String()) != `{"experiments":[]}` {
		t.Fatalf("other range: %s", w.Body)
	}
	for _, p := range []string{"/v1/experiments?from=2026-13-01", "/v1/experiments?installs=some"} {
		if w := do(h, "GET", p, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	_, h := newTestServer(t, Config{})
	w := do(h, "GET", "/", nil)
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("dashboard CSP: %q", csp)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("headers: %v", w.Header())
	}
	w = do(h, "GET", "/v1/summary", nil)
	if w.Header().Get("Content-Security-Policy") != apiCSP || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("api headers: %v", w.Header())
	}
	if w := do(h, "GET", "/assets/", nil); w.Code != http.StatusNotFound {
		t.Fatalf("directory listing: %d", w.Code)
	}
	if w := do(h, "GET", "/missing.js", nil); w.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", w.Code)
	}
}

func TestHealthURL(t *testing.T) {
	for _, tc := range []struct{ listen, base, want string }{
		{":8080", "/", "http://127.0.0.1:8080/healthz"},
		{"0.0.0.0:9000", "/hub", "http://127.0.0.1:9000/hub/healthz"},
		{"[::]:8080", "", "http://127.0.0.1:8080/healthz"},
		{"127.0.0.2:81", "/", "http://127.0.0.2:81/healthz"},
	} {
		got, err := HealthURL(tc.listen, tc.base)
		if err != nil || got != tc.want {
			t.Errorf("HealthURL(%q, %q) = %q, %v", tc.listen, tc.base, got, err)
		}
	}
}

func TestRedactingWriter(t *testing.T) {
	var buf bytes.Buffer
	w := &redactingWriter{log: slogTo(&buf)}
	fmt.Fprintf(w, "http: TLS handshake error from 203.0.113.5:51234: EOF\n")
	fmt.Fprintf(w, "http: TLS handshake error from [2001:db8::1]:443: EOF\n")
	if s := buf.String(); strings.Contains(s, "203.0.113") || strings.Contains(s, "2001:db8") {
		t.Fatalf("address logged: %s", s)
	}
}

func slogTo(w io.Writer) *slog.Logger { return slog.New(slog.NewTextHandler(w, nil)) }

func TestIngestNewInstallsPerNetwork(t *testing.T) {
	_, h := newTestServer(t, Config{NewInstallsPerNetwork: 1})
	first, second := validReport(), validReport()
	second["installId"] = "bbbbbbbb-0000-4000-8000-000000000002"
	if w := do(h, "POST", "/v1/reports", mustJSON(first), withRemote("203.0.113.7:4000")); w.Code != http.StatusAccepted {
		t.Fatalf("first: %d %s", w.Code, w.Body)
	}
	// Same /24, new install: refused until the next UTC midnight.
	w := do(h, "POST", "/v1/reports", mustJSON(second), withRemote("203.0.113.200:4000"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second: %d %s", w.Code, w.Body)
	}
	next := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	if got, want := w.Header().Get("Retry-After"), strconv.Itoa(int(next.Sub(testNow).Seconds())); got != want {
		t.Fatalf("Retry-After %s, want %s", got, want)
	}
	// The known install still reports from there.
	first["day"] = "2026-10-25"
	if w := do(h, "POST", "/v1/reports", mustJSON(first), withRemote("203.0.113.9:4000")); w.Code != http.StatusAccepted {
		t.Fatalf("known install: %d %s", w.Code, w.Body)
	}
	// Another network is not affected.
	if w := do(h, "POST", "/v1/reports", mustJSON(second), withRemote("198.51.100.1:4000")); w.Code != http.StatusAccepted {
		t.Fatalf("other network: %d %s", w.Code, w.Body)
	}
}

func TestIngestMaxDailyUSD(t *testing.T) {
	_, h := newTestServer(t, Config{MaxDailyUSD: 5})
	w := do(h, "POST", "/v1/reports", mustJSON(validReport()))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "cap") {
		t.Fatalf("over the cap: %d %s", w.Code, w.Body)
	}
	_, h = newTestServer(t, Config{MaxDailyUSD: 7.8})
	if w := do(h, "POST", "/v1/reports", mustJSON(validReport())); w.Code != http.StatusAccepted {
		t.Fatalf("at the cap: %d %s", w.Code, w.Body)
	}
}

func TestQueryEstablished(t *testing.T) {
	_, h := newTestServer(t, Config{EstablishedDays: 2})
	if w := do(h, "POST", "/v1/reports", mustJSON(validReport())); w.Code != http.StatusAccepted {
		t.Fatal(w.Code)
	}
	var sum Summary
	w := do(h, "GET", "/v1/summary?from=2026-10-20&to=2026-10-27", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil || sum.Installs != 0 || sum.Pending != 1 || sum.EstablishedDays != 2 {
		t.Fatalf("established: %s", w.Body)
	}
	w = do(h, "GET", "/v1/summary?from=2026-10-20&to=2026-10-27&installs=all", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil || sum.Installs != 1 || sum.Pending != 0 || !near(sum.Net, 7.39) {
		t.Fatalf("all: %s", w.Body)
	}
	r2 := validReport()
	r2["day"] = "2026-10-25"
	if w := do(h, "POST", "/v1/reports", mustJSON(r2)); w.Code != http.StatusAccepted {
		t.Fatal(w.Code)
	}
	w = do(h, "GET", "/v1/summary?from=2026-10-20&to=2026-10-27", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil || sum.Installs != 1 || sum.Pending != 0 {
		t.Fatalf("after two days: %s", w.Body)
	}
}
