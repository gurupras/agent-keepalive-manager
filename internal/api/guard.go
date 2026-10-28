package api

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// TokenHeader carries the API token on every request.
const TokenHeader = "X-Keepalive-Token"

// guard applies the request checks of docs/api.md and architecture.md
// (Security) before anything is served. It is shared by the local API and
// by the proxy to another server, so a proxying server protects itself
// exactly like the one it forwards to.
type guard struct {
	port  int
	token string
}

// admit writes an error and returns false when r must be refused.
func (g guard) admit(w http.ResponseWriter, r *http.Request) bool {
	// DNS rebinding: a page on evil.example that resolves to 127.0.0.1
	// reaches us with its own name in Host. Checked first and for every
	// path, because the UI page carries the token.
	if !g.allowedHost(r.Host) {
		writeError(w, http.StatusMisdirectedRequest, "this server only answers to 127.0.0.1 and localhost")
		return false
	}
	api := isAPI(r.URL.Path)
	mutating := isMutating(r.Method)
	ws := api && strings.TrimPrefix(r.URL.Path, "/api/") == "ws"
	if !g.fetchSiteOK(r, api, mutating || ws) {
		writeError(w, http.StatusForbidden, "cross-site request refused")
		return false
	}
	if (mutating || ws) && !g.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin request refused")
		return false
	}
	if !api || r.URL.Path == "/api/health" {
		return true
	}
	if !g.tokenOK(r, ws) {
		writeError(w, http.StatusUnauthorized, "missing or wrong API token")
		return false
	}
	// A JSON content type cannot be sent cross-origin without a CORS
	// preflight, which we never answer, so it also stops "simple"
	// form-style requests.
	if mutating && !isJSON(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	return true
}

// allowedHost accepts loopback names on our own port only.
func (g guard) allowedHost(host string) bool {
	name, port := host, ""
	if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.HasSuffix(host, "]") {
		name, port = host[:i], host[i+1:]
	}
	switch strings.ToLower(name) {
	case "127.0.0.1", "localhost", "[::1]":
	default:
		return false
	}
	if port == "" {
		return g.port == 80
	}
	return port == strconv.Itoa(g.port)
}

// fetchSiteOK checks Fetch Metadata. Sec-Fetch-Site: cross-site is refused,
// except for a top-level navigation to the UI (a link to it from another
// page), whose response the other page cannot read. State changes and the
// WebSocket also refuse same-site, which covers other ports of localhost.
// No header: not a browser, or an old one; the Origin and token checks
// cover those.
func (g guard) fetchSiteOK(r *http.Request, api, strict bool) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	case "same-site":
		return !strict
	case "cross-site":
		return !api && !strict && r.Method == http.MethodGet &&
			r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document"
	}
	return false
}

// sameOrigin is true when there is no Origin (a non-browser client) or the
// Origin is http://<our Host>. "null" (sandboxed frames, file: pages) is
// refused.
func (g guard) sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Scheme == "http" && u.Path == "" && strings.EqualFold(u.Host, r.Host)
}

func (g guard) tokenOK(r *http.Request, ws bool) bool {
	got := r.Header.Get(TokenHeader)
	if got == "" && ws {
		// Browsers cannot set headers on a WebSocket upgrade.
		got = r.URL.Query().Get("token")
	}
	return got != "" && g.token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(g.token)) == 1
}

func isAPI(p string) bool { return p == "/api" || strings.HasPrefix(p, "/api/") }

func isMutating(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

func isJSON(ct string) bool {
	mt, _, _ := strings.Cut(ct, ";")
	return strings.EqualFold(strings.TrimSpace(mt), "application/json")
}
