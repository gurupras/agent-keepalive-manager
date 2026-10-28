package hub

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

// Dashboard modes.
const (
	DashboardPublic = "public" // anyone can read the aggregates
	DashboardToken  = "token"  // reading needs the dashboard token
	DashboardOff    = "off"    // no dashboard and no read API
)

// Config configures the hub. Field meanings follow the flags in
// docs/metrics.md.
type Config struct {
	Listen         string
	DataDir        string
	DatabaseURL    string
	BasePath       string
	IngestToken    string
	Dashboard      string
	DashboardToken string
	TrustProxy     bool
	RetentionDays  int
	Version        string
	Logger         *slog.Logger
}

var basePathRe = regexp.MustCompile(`^(/[A-Za-z0-9._~-]+)*$`)

// NormalizeBasePath turns "/", "" or "/hub/" into the prefix the routes
// use: "" or "/hub".
func NormalizeBasePath(p string) (string, error) {
	p = strings.TrimRight(strings.TrimSpace(p), "/")
	if p != "" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !basePathRe.MatchString(p) {
		return "", fmt.Errorf("base path %q: only A-Z a-z 0-9 . _ ~ - and / are allowed", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return "", fmt.Errorf("base path %q: no . or .. segments", p)
		}
	}
	return p, nil
}

func (c *Config) validate() error {
	var err error
	if c.BasePath, err = NormalizeBasePath(c.BasePath); err != nil {
		return err
	}
	switch c.Dashboard {
	case "":
		c.Dashboard = DashboardPublic
	case DashboardPublic, DashboardOff:
	case DashboardToken:
		if c.DashboardToken == "" {
			return errors.New("dashboard mode token needs a dashboard token (HUB_DASHBOARD_TOKEN)")
		}
	default:
		return fmt.Errorf("dashboard mode %q: must be public, token or off", c.Dashboard)
	}
	if c.RetentionDays < 0 {
		return errors.New("retention days must not be negative")
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	return nil
}

// Rate limits per sender.
const (
	reportsPerHour = 60
	loginsPerHour  = 60
	maxLoginBytes  = 4 << 10
	sessionCookie  = "agent_keepalive_hub_session"
	sessionMaxAge  = 30 * 24 * time.Hour
	maxRangeDays   = 1000
	defaultDays    = 30
)

// Server is the hub's HTTP side.
type Server struct {
	cfg     Config
	store   *Store
	ui      fs.FS
	reports *rateLimiter
	logins  *rateLimiter
	now     func() time.Time
	log     *slog.Logger
}

// NewServer serves store under cfg. ui is the dashboard's files, with
// index.html at its root.
func NewServer(cfg Config, store *Store, ui fs.FS) (*Server, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Server{
		cfg:     cfg,
		store:   store,
		ui:      ui,
		reports: newRateLimiter(reportsPerHour, time.Hour),
		logins:  newRateLimiter(loginsPerHour, time.Hour),
		now:     func() time.Time { return time.Now().Round(0) },
		log:     cfg.Logger,
	}, nil
}

// Handler returns every route under the base path.
func (s *Server) Handler() http.Handler {
	b := s.cfg.BasePath
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+b+"/v1/reports", s.handleReport)
	mux.HandleFunc("GET "+b+"/healthz", s.handleHealth)
	if s.cfg.Dashboard != DashboardOff {
		mux.HandleFunc("GET "+b+"/v1/summary", s.dashboardAPI(s.handleSummary))
		mux.HandleFunc("GET "+b+"/v1/series", s.dashboardAPI(s.handleSeries))
		mux.HandleFunc("GET "+b+"/v1/breakdown", s.dashboardAPI(s.handleBreakdown))
		mux.HandleFunc("POST "+b+"/v1/login", s.handleLogin)
		mux.HandleFunc("POST "+b+"/v1/logout", s.handleLogout)
		mux.HandleFunc("GET "+b+"/v1/session", s.handleSession)
		mux.HandleFunc("GET "+b+"/{file...}", s.handleUI)
		if b != "" {
			mux.HandleFunc("GET "+b, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, b+"/", http.StatusMovedPermanently)
			})
		}
	}
	return s.recoverer(s.headers(mux))
}

// apiCSP is for every response that is not the dashboard page.
const (
	apiCSP       = "default-src 'none'; frame-ancestors 'none'"
	dashboardCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
		"font-src 'self'; connect-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'none'; " +
		"frame-ancestors 'self'"
)

func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
		h.Set("Content-Security-Policy", apiCSP)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.log.Error("handler panic", "path", r.URL.Path, "panic", fmt.Sprint(v))
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// sender identifies who sent a request, for rate limiting only. With
// --trust-proxy it is the last X-Forwarded-For entry, the address the
// nearest proxy saw; earlier entries can be forged by the client.
func (s *Server) sender(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
			parts := strings.Split(xff[len(xff)-1], ",")
			if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
				return last
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) secure(r *http.Request) bool {
	return r.TLS != nil || (s.cfg.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
}

// tokenEqual compares secrets in constant time, length included.
func tokenEqual(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

func bearer(r *http.Request) (string, bool) {
	const prefix = "bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	if !s.reports.allow(s.sender(r), s.now()) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	if s.cfg.IngestToken != "" {
		tok, ok := bearer(r)
		if !ok || !tokenEqual(tok, s.cfg.IngestToken) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="agent-keepalive-hub"`)
			writeError(w, http.StatusUnauthorized, "bad token")
			return
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxReportBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("report larger than %d bytes", MaxReportBytes))
			return
		}
		writeError(w, http.StatusBadRequest, "could not read report")
		return
	}
	now := s.now()
	rep, err := DecodeReport(body, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.store.Upsert(r.Context(), rep, now)
	if err != nil {
		s.log.Error("store report", "err", err)
		writeError(w, http.StatusInternalServerError, "could not store report")
		return
	}
	if created {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "stored"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.store.Ping(r.Context()); err != nil {
		s.log.Warn("health: database unreachable", "err", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, "database unreachable\n")
		return
	}
	io.WriteString(w, "ok\n")
}

// sessionValue is the login cookie's value: derived from the dashboard
// token, so it never reveals it and changing the token signs everyone out.
func (s *Server) sessionValue() string {
	m := hmac.New(sha256.New, []byte(s.cfg.DashboardToken))
	m.Write([]byte("agent-keepalive-hub dashboard session v1"))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) dashboardAuthorized(r *http.Request) bool {
	if s.cfg.Dashboard == DashboardPublic {
		return true
	}
	if tok, ok := bearer(r); ok {
		return tokenEqual(tok, s.cfg.DashboardToken)
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		return tokenEqual(c.Value, s.sessionValue())
	}
	return false
}

func (s *Server) dashboardAPI(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.dashboardAuthorized(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="agent-keepalive-hub"`)
			writeError(w, http.StatusUnauthorized, "dashboard token required")
			return
		}
		h(w, r)
	}
}

func (s *Server) cookiePath() string { return s.cfg.BasePath + "/" }

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Dashboard != DashboardToken {
		writeError(w, http.StatusNotFound, "this dashboard has no login")
		return
	}
	if !s.logins.allow(s.sender(r), s.now()) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	if !isJSON(r) {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "body must be {\"token\": \"…\"}")
		return
	}
	if !tokenEqual(req.Token, s.cfg.DashboardToken) {
		writeError(w, http.StatusUnauthorized, "wrong token")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.sessionValue(),
		Path:     s.cookiePath(),
		MaxAge:   int(sessionMaxAge / time.Second),
		HttpOnly: true,
		Secure:   s.secure(r),
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// handleSession tells the dashboard the mode and whether this request may
// read, so it can offer sign-out after a reload.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":          s.cfg.Dashboard,
		"authenticated": s.dashboardAuthorized(r),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     s.cookiePath(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure(r),
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func isJSON(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(ct), "application/json")
}

// parseRange reads from and to (YYYY-MM-DD, inclusive). Missing ones
// default to the last 30 days ending today (UTC).
func (s *Server) parseRange(r *http.Request) (Range, error) {
	today := utcDay(s.now())
	to := today
	if v := r.URL.Query().Get("to"); v != "" {
		d, err := time.Parse(time.DateOnly, v)
		if err != nil {
			return Range{}, errors.New("to: must be YYYY-MM-DD")
		}
		to = d
	}
	from := to.AddDate(0, 0, -(defaultDays - 1))
	if v := r.URL.Query().Get("from"); v != "" {
		d, err := time.Parse(time.DateOnly, v)
		if err != nil {
			return Range{}, errors.New("from: must be YYYY-MM-DD")
		}
		from = d
	}
	if from.After(to) {
		return Range{}, errors.New("from is after to")
	}
	if to.Sub(from) > maxRangeDays*24*time.Hour {
		return Range{}, fmt.Errorf("range longer than %d days", maxRangeDays)
	}
	return Range{From: from.Format(time.DateOnly), To: to.Format(time.DateOnly)}, nil
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	rg, err := s.parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sum, err := s.store.Summary(r.Context(), rg)
	if err != nil {
		s.log.Error("summary", "err", err)
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	rg, err := s.parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	metric := r.URL.Query().Get("metric")
	if metric == "" {
		metric = "net"
	}
	known := false
	for _, m := range SeriesMetrics {
		known = known || m == metric
	}
	if !known {
		writeError(w, http.StatusBadRequest, "metric: must be one of "+strings.Join(SeriesMetrics, ", "))
		return
	}
	points, err := s.store.Series(r.Context(), rg, metric)
	if err != nil {
		s.log.Error("series", "err", err)
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, points)
}

func (s *Server) handleBreakdown(w http.ResponseWriter, r *http.Request) {
	rg, err := s.parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	by := r.URL.Query().Get("by")
	if _, ok := BreakdownDimensions[by]; !ok {
		writeError(w, http.StatusBadRequest, "by: must be one of model, harness, version, os, installKind, profile")
		return
	}
	rows, err := s.store.Breakdown(r.Context(), rg, by)
	if err != nil {
		s.log.Error("breakdown", "err", err)
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// handleUI serves the dashboard's files. Hashed assets are cached for good;
// the page itself is revalidated every time.
func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if name == "" {
		name = "index.html"
	}
	name = path.Clean(name)
	if !fs.ValidPath(name) {
		http.NotFound(w, r)
		return
	}
	f, err := s.ui.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if name == "index.html" {
		w.Header().Set("Content-Security-Policy", dashboardCSP)
		w.Header().Set("Cache-Control", "no-cache")
	} else if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	http.ServeContent(w, r, name, st.ModTime(), rs)
}
