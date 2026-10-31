package api

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

// Addon file names (--ui-addon): linked into the page when present.
const (
	AddonStyle  = "addon.css" // after the app's stylesheet
	AddonScript = "addon.js"  // as a module, after the app
)

// placeholder is served when the binary was built without the web UI
// (web/dist has no index.html). No inline script or style: the CSP applies.
const placeholder = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>agent-keepalive</title>
</head>
<body>
<h1>agent-keepalive</h1>
<p>This build has no web UI. Build it with <code>make web</code> (or <code>npm run build</code> in <code>web/</code>) and rebuild the program.</p>
<p>The API is served under <a href="./api/health">api/</a>.</p>
</body>
</html>
`

// ui serves the single-page app: index.html with the token and the add-on
// links injected, everything else as files.
type ui struct {
	frames string // CSP frame-ancestors sources
	files  fs.FS
	addon  fs.FS
	index  []byte // rendered once: the token and add-on do not change
}

func newUI(files, addon fs.FS, token, frameAncestors string) (*ui, error) {
	if frameAncestors == "" {
		frameAncestors = DefaultFrameAncestors
	}
	if err := ValidFrameAncestors(frameAncestors); err != nil {
		return nil, fmt.Errorf("api: frame-ancestors: %w", err)
	}
	u := &ui{files: files, addon: addon, frames: strings.Join(strings.Fields(frameAncestors), " ")}
	src := []byte(placeholder)
	if files != nil {
		if b, err := fs.ReadFile(files, "index.html"); err == nil {
			src = b
		}
	}
	var tags strings.Builder
	tags.WriteString(`<meta name="ka-token" content="` + html.EscapeString(token) + `">`)
	if addon != nil {
		if st, err := fs.Stat(addon, AddonStyle); err == nil && !st.IsDir() {
			tags.WriteString(`<link rel="stylesheet" href="./addon/` + AddonStyle + `">`)
		}
		if st, err := fs.Stat(addon, AddonScript); err == nil && !st.IsDir() {
			// The meta tag tells the app to import the add-on and call its
			// setup (docs/api.md, "Add-on API").
			tags.WriteString(`<meta name="ka-addon" content="">`)
			tags.WriteString(`<script type="module" src="./addon/` + AddonScript + `"></script>`)
		}
	}
	u.index = injectHead(src, tags.String())
	return u, nil
}

// injectHead puts tags just before </head> (so add-on styles come after
// the app's), or at the start when the page has no head.
func injectHead(page []byte, tags string) []byte {
	i := bytes.Index(bytes.ToLower(page), []byte("</head>"))
	if i < 0 {
		return append([]byte(tags), page...)
	}
	out := make([]byte, 0, len(page)+len(tags))
	out = append(out, page[:i]...)
	out = append(out, tags...)
	return append(out, page[i:]...)
}

func (u *ui) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" || name == "index.html" {
		u.serveIndex(w, r)
		return
	}
	if !fs.ValidPath(name) {
		http.NotFound(w, r)
		return
	}
	if u.files != nil && u.serveFile(w, r, u.files, name, false) {
		return
	}
	// The app uses hash routing, but a path without an extension still
	// gets the page; a missing asset is a 404 so stale bundles never
	// receive HTML as script.
	if path.Ext(name) == "" {
		u.serveIndex(w, r)
		return
	}
	http.NotFound(w, r)
}

func (u *ui) serveIndex(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	u.setPageHeaders(hd, r.Host)
	hd.Set("Content-Type", "text/html; charset=utf-8")
	// The page carries the token: revalidate always and keep it out of
	// shared and disk caches.
	hd.Set("Cache-Control", "no-store")
	hd.Set("Content-Length", strconv.Itoa(len(u.index)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(u.index)
	}
}

// serveAddon serves the add-on directory; its files change whenever the
// host app changes them, so they are never cached unvalidated.
func (u *ui) serveAddon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if !fs.ValidPath(name) || name == "." || !u.serveFile(w, r, u.addon, name, true) {
		http.NotFound(w, r)
	}
}

// csp is the page's Content-Security-Policy. No inline script or style;
// the WebSocket goes to our own host (some browsers do not count ws: as
// 'self').
func csp(host, frames string) string {
	return "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; " +
		"connect-src 'self' ws://" + host + " wss://" + host + "; object-src 'none'; base-uri 'self'; " +
		"form-action 'self'; frame-ancestors " + frames
}

// setPageHeaders sets the headers of an HTML page. X-Frame-Options is
// only for browsers without CSP frame-ancestors; it cannot express a list
// of origins, so it is sent only with the default 'self'.
func (u *ui) setPageHeaders(hd http.Header, host string) {
	hd.Set("Content-Security-Policy", csp(host, u.frames))
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Referrer-Policy", "no-referrer")
	if u.frames == DefaultFrameAncestors {
		hd.Set("X-Frame-Options", "SAMEORIGIN")
	}
}

// serveFile writes a regular file from fsys, reporting false if there is
// none. revalidate forces no-cache; otherwise content-hashed files (Vite's
// assets/) are immutable.
func (u *ui) serveFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string, revalidate bool) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		return false
	}
	hd := w.Header()
	switch {
	case revalidate:
		hd.Set("Cache-Control", "no-cache")
	case strings.HasPrefix(name, "assets/") || isHashed(name):
		hd.Set("Cache-Control", "public, max-age=31536000, immutable")
	default:
		hd.Set("Cache-Control", "no-cache")
	}
	if path.Ext(name) == ".html" {
		u.setPageHeaders(hd, r.Host)
	} else {
		hd.Set("X-Content-Type-Options", "nosniff")
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		b, err := io.ReadAll(f)
		if err != nil {
			return false
		}
		rs = bytes.NewReader(b)
	}
	mod := st.ModTime()
	if mod.Equal(time.Unix(0, 0)) {
		mod = time.Time{}
	}
	http.ServeContent(w, r, name, mod, rs)
	return true
}

// isHashed reports whether name looks like content-hashed bundler output
// ("index-B9x_k2Qa.js"): such files never change.
func isHashed(name string) bool {
	base := path.Base(name)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	i := strings.LastIndexAny(stem, "-.")
	if i < 0 || ext == "" {
		return false
	}
	hash := stem[i+1:]
	if len(hash) < 8 {
		return false
	}
	var digit bool
	for _, c := range hash {
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		default:
			return false
		}
	}
	return digit || strings.ToLower(hash) != hash && strings.ToUpper(hash) != hash
}
