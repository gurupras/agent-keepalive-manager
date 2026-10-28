package api

import (
	"strings"
	"testing"
)

func TestValidFrameAncestors(t *testing.T) {
	good := []string{
		"'self'", "'none'", "https:", "http://127.0.0.1:3000", "https://host.example",
		"https://*.example.com", "*.localhost", "http://localhost:*", "'self' https://a.example:8443/panel/",
		"http://[::1]:8080", "*", "  'self'   https://a.example  ",
	}
	for _, v := range good {
		if err := ValidFrameAncestors(v); err != nil {
			t.Errorf("%q refused: %v", v, err)
		}
	}
	bad := []string{
		"", "   ", "'self'; script-src *", "https://a.example,https://b.example", "'unsafe-inline'",
		"'none' 'self'", "https://a.example\"", `https://a\b`, "https://exa mple;x", "http://host:99999x",
		"https://-bad.example", "'self' 'nonce-abc'",
	}
	for _, v := range bad {
		if err := ValidFrameAncestors(v); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
}

func TestFrameAncestorsHeaders(t *testing.T) {
	def := newEnv(t, nil)
	w := def.do("GET", "/", "", nil)
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'") || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("default: %v", w.Header())
	}

	e := newEnv(t, func(o *Options) { o.FrameAncestors = "'self'   https://host.example:8443\n" })
	w = e.do("GET", "/", "", nil)
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.HasSuffix(csp, "frame-ancestors 'self' https://host.example:8443") {
		t.Fatalf("CSP %q", csp)
	}
	if w.Header().Get("X-Frame-Options") != "" {
		t.Fatal("X-Frame-Options must be dropped for a custom list")
	}
	if _, err := New(Options{Service: newFakeService(), Token: testToken, Port: testPort, FrameAncestors: "'self'; script-src *"}); err == nil {
		t.Fatal("invalid frame-ancestors accepted by New")
	}
}
