// Package api serves the local server's HTTP API, its WebSocket event
// stream and the embedded web UI (docs/api.md).
//
// The server is for one local user. Every request passes the guard first
// (guard.go): a loopback Host on our port (against DNS rebinding), Fetch
// Metadata and same-Origin checks (against cross-site requests), the API
// token on every api/ route but api/health, and a JSON content type on
// state changes. The UI page is served with the token in a meta tag and a
// strict Content-Security-Policy.
//
// A server that does not run the scheduler (another server holds the
// scheduler lock, architecture.md rule 11) is built with Options.Upstream:
// it still serves its own UI and applies its own guard, and forwards the
// API to the server that does.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// Name is reported by GET api/health.
const Name = "agent-keepalive"

// maxBody caps JSON request bodies.
const maxBody = 1 << 20

// Options configure the handler.
type Options struct {
	// Service is the scheduler. Required unless Upstream is set.
	Service model.Service
	// Upstream, when set, forwards the API (everything under api/ except
	// api/health and api/quit) to the server that runs the scheduler.
	Upstream Upstream
	// Updater serves api/update; nil reports updates as disabled.
	Updater apitypes.Updater
	// Metrics serves api/metrics; nil reports metrics as unavailable.
	Metrics apitypes.Metrics
	// Info is served by api/info and in the WebSocket hello. Info.Version
	// is also reported by api/health; Info.Supervised refuses api/quit.
	Info apitypes.Info
	// Token is the API token (instance.Token). Required.
	Token string
	// Port is the port we listen on, for the Host check and api/health.
	Port int
	// UI is the built web app (index.html at its root). When it has no
	// index.html a placeholder page is served.
	UI fs.FS
	// Addon, when set, is served under addon/, and its addon.css and
	// addon.js are linked into the UI page (--ui-addon).
	Addon fs.FS
	// FrameAncestors is the CSP frame-ancestors source list of the UI page
	// (--frame-ancestors); "" means DefaultFrameAncestors.
	FrameAncestors string
	// Quit shuts the server down (api/quit). nil refuses api/quit.
	Quit func()
	// Log defaults to slog.Default().
	Log *slog.Logger
	// PingInterval and PongTimeout tune the WebSocket keepalive (30 s, 60 s).
	PingInterval, PongTimeout time.Duration
}

type handler struct {
	opts  Options
	g     guard
	log   *slog.Logger
	mux   *http.ServeMux
	ui    *ui
	proxy http.Handler

	// local carries events the API itself causes and no subscription
	// reports (metrics changes when Metrics has no notifier).
	localMu sync.Mutex
	local   map[chan model.Event]struct{}
}

// New returns the HTTP handler.
func New(opts Options) (http.Handler, error) {
	if opts.Token == "" {
		return nil, errors.New("api: no token")
	}
	if opts.Service == nil && opts.Upstream == nil {
		return nil, errors.New("api: neither a service nor an upstream")
	}
	if opts.PingInterval <= 0 {
		opts.PingInterval = 30 * time.Second
	}
	if opts.PongTimeout <= 0 {
		opts.PongTimeout = 60 * time.Second
	}
	h := &handler{
		opts:  opts,
		g:     guard{port: opts.Port, token: opts.Token},
		log:   opts.Log,
		mux:   http.NewServeMux(),
		local: map[chan model.Event]struct{}{},
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	var err error
	if h.ui, err = newUI(opts.UI, opts.Addon, opts.Token, opts.FrameAncestors); err != nil {
		return nil, err
	}
	route := func(pattern string, fn http.HandlerFunc) {
		method, path, _ := strings.Cut(pattern, " ")
		h.mux.HandleFunc(method+" /api/"+path, fn)
	}
	route("GET health", h.health)
	route("POST quit", h.quit)
	if opts.Upstream != nil {
		h.proxy = newProxy(opts.Upstream, h.log)
		h.mux.Handle("/api/", h.proxy)
	} else {
		route("GET info", h.info)
		route("GET sessions", h.sessions)
		route("GET sessions/{key}", h.session)
		route("POST sessions/{key}/plan", h.startPlan)
		route("PATCH sessions/{key}/plan", h.changePlan)
		route("DELETE sessions/{key}/plan", h.cancelPlan)
		route("POST sessions/{key}/ping", h.ping)
		route("GET sessions/{key}/pings", h.pings)
		route("GET ledger", h.ledger)
		route("GET missed", h.missed)
		route("GET settings", h.settings)
		route("PUT settings", h.updateSettings)
		route("GET metrics", h.metricsStatus)
		route("PUT metrics", h.setMetrics)
		route("GET metrics/preview", h.metricsPreview)
		route("POST metrics/reset-id", h.metricsResetID)
		route("GET startup", h.startup)
		route("GET update", h.updateStatus)
		route("POST update/check", h.updateCheck)
		route("POST update/apply", h.updateApply)
		route("GET ws", h.ws)
		// Unknown API paths and wrong methods: JSON errors, never the UI.
		h.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "no such endpoint")
		})
	}
	h.mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	if opts.Addon != nil {
		h.mux.Handle("/addon/", http.StripPrefix("/addon/", http.HandlerFunc(h.ui.serveAddon)))
	}
	h.mux.Handle("/", h.ui)
	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.g.admit(w, r) {
		return
	}
	h.mux.ServeHTTP(w, r)
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"name": Name, "version": h.opts.Info.Version, "port": h.opts.Port})
}

func (h *handler) info(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.opts.Info)
}

func (h *handler) quit(w http.ResponseWriter, r *http.Request) {
	if h.opts.Info.Supervised {
		writeError(w, http.StatusConflict, "this server is managed by another program, which decides when it stops")
		return
	}
	if h.opts.Quit == nil {
		writeError(w, http.StatusConflict, "this server cannot be stopped from the API")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"quitting": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	// After the answer is out: shutting down waits for this request.
	go h.opts.Quit()
}

func (h *handler) sessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, nonNil(h.opts.Service.Sessions()))
}

// key returns the decoded {key}; ServeMux already unescaped it.
func key(r *http.Request) string { return r.PathValue("key") }

func (h *handler) session(w http.ResponseWriter, r *http.Request) {
	v, ok := h.opts.Service.Session(key(r))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown session "+strconv.Quote(key(r)))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *handler) startPlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hours      *float64 `json:"hours"`
		Indefinite bool     `json:"indefinite"`
	}
	if !decode(w, r, &body) {
		return
	}
	var (
		p   model.Plan
		err error
	)
	switch {
	case body.Indefinite && body.Hours != nil:
		writeError(w, http.StatusBadRequest, "give either hours or indefinite, not both")
		return
	case body.Indefinite:
		p, err = h.opts.Service.KeepIndefinitely(r.Context(), key(r))
	case body.Hours == nil:
		writeError(w, http.StatusBadRequest, "give hours or indefinite")
		return
	default:
		if !positive(w, "hours", *body.Hours) {
			return
		}
		p, err = h.opts.Service.StartPlan(r.Context(), key(r), *body.Hours)
	}
	h.reply(w, r, http.StatusCreated, p, err)
}

func (h *handler) changePlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AddHours *float64 `json:"addHours"`
		Resume   bool     `json:"resume"`
	}
	if !decode(w, r, &body) {
		return
	}
	var (
		p   model.Plan
		err error
	)
	switch {
	case body.Resume && body.AddHours != nil:
		writeError(w, http.StatusBadRequest, "give either addHours or resume, not both")
		return
	case body.Resume:
		p, err = h.opts.Service.ResumePlan(r.Context(), key(r))
	case body.AddHours == nil:
		writeError(w, http.StatusBadRequest, "give addHours or resume")
		return
	default:
		if !positive(w, "addHours", *body.AddHours) {
			return
		}
		p, err = h.opts.Service.ExtendPlan(r.Context(), key(r), *body.AddHours)
	}
	h.reply(w, r, http.StatusOK, p, err)
}

func (h *handler) cancelPlan(w http.ResponseWriter, r *http.Request) {
	if err := h.opts.Service.CancelPlan(r.Context(), key(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) ping(w http.ResponseWriter, r *http.Request) {
	rec, err := h.opts.Service.PingNow(r.Context(), key(r))
	h.reply(w, r, http.StatusOK, rec, err)
}

func (h *handler) pings(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, 1000)
	}
	// Past pings stay readable after the session ended: no session lookup.
	recs, err := h.opts.Service.Pings(key(r), limit)
	h.reply(w, r, http.StatusOK, nonNil(recs), err)
}

func (h *handler) ledger(w http.ResponseWriter, r *http.Request) {
	from, ok := timeParam(w, r, "from", time.Time{})
	if !ok {
		return
	}
	to, ok := timeParam(w, r, "to", time.Time{})
	if !ok {
		return
	}
	if !from.IsZero() && !to.IsZero() && to.Before(from) {
		writeError(w, http.StatusBadRequest, "to is before from")
		return
	}
	entries, totals, err := h.opts.Service.Ledger(from, to)
	h.reply(w, r, http.StatusOK, map[string]any{"entries": nonNil(entries), "totals": totals}, err)
}

func (h *handler) missed(w http.ResponseWriter, r *http.Request) {
	since, ok := timeParam(w, r, "since", time.Now().Round(0).Add(-7*24*time.Hour))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.opts.Service.Missed(since))
}

func (h *handler) settings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.opts.Service.Settings())
}

func (h *handler) updateSettings(w http.ResponseWriter, r *http.Request) {
	// Start from the current settings so a partial body keeps the rest.
	s := h.opts.Service.Settings()
	if !decode(w, r, &s) {
		return
	}
	switch {
	case !(s.MaxHours >= 0.5 && s.MaxHours <= 168):
		writeError(w, http.StatusBadRequest, "maxHours must be between 0.5 and 168")
		return
	case !(s.MarginMin >= 1 && s.MarginMin <= 15):
		writeError(w, http.StatusBadRequest, "marginMin must be between 1 and 15")
		return
	case !(s.ScanSeconds >= 2 && s.ScanSeconds <= 60):
		writeError(w, http.StatusBadRequest, "scanSeconds must be between 2 and 60")
		return
	}
	out, err := h.opts.Service.UpdateSettings(r.Context(), s)
	h.reply(w, r, http.StatusOK, out, err)
}

func (h *handler) metricsStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.metricsNow())
}

func (h *handler) metricsNow() apitypes.MetricsStatus {
	if h.opts.Metrics == nil {
		return apitypes.MetricsStatus{}
	}
	return h.opts.Metrics.Status()
}

func (h *handler) setMetrics(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "give enabled")
		return
	}
	if h.opts.Metrics == nil {
		h.fail(w, r, apitypes.ErrUnavailable)
		return
	}
	if err := h.opts.Metrics.SetEnabled(*body.Enabled); err != nil {
		h.fail(w, r, err)
		return
	}
	st := h.opts.Metrics.Status()
	if _, ok := h.opts.Metrics.(apitypes.MetricsNotifier); !ok {
		h.broadcast(model.Event{Type: "metrics", Data: st})
	}
	writeJSON(w, http.StatusOK, st)
}

// metricsResetID replaces the install id (`agent-keepalive metrics
// reset-id` against a running server, whose reporter holds the id).
func (h *handler) metricsResetID(w http.ResponseWriter, r *http.Request) {
	if h.opts.Metrics == nil {
		h.fail(w, r, apitypes.ErrUnavailable)
		return
	}
	if err := h.opts.Metrics.ResetID(); err != nil {
		h.fail(w, r, err)
		return
	}
	st := h.opts.Metrics.Status()
	if _, ok := h.opts.Metrics.(apitypes.MetricsNotifier); !ok {
		h.broadcast(model.Event{Type: "metrics", Data: st})
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *handler) metricsPreview(w http.ResponseWriter, r *http.Request) {
	if h.opts.Metrics == nil {
		h.fail(w, r, apitypes.ErrUnavailable)
		return
	}
	p, err := h.opts.Metrics.Preview()
	h.reply(w, r, http.StatusOK, p, err)
}

func (h *handler) updateNow() apitypes.UpdateStatus {
	if h.opts.Updater == nil {
		return apitypes.UpdateStatus{Current: h.opts.Info.Version, State: apitypes.UpdateIdle}
	}
	return h.opts.Updater.Status()
}

// starter is a Service that reports its startup (the scheduler does).
type starter interface{ Startup() model.Startup }

// startupNow is the service's startup; one that does not report it is ready.
func (h *handler) startupNow() model.Startup {
	if s, ok := h.opts.Service.(starter); ok {
		return s.Startup()
	}
	return model.Startup{Ready: true}
}

func (h *handler) startup(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.startupNow())
}

func (h *handler) updateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.updateNow())
}

func (h *handler) updateCheck(w http.ResponseWriter, r *http.Request) {
	if h.opts.Updater == nil {
		h.fail(w, r, apitypes.ErrUnavailable)
		return
	}
	// A failed check (network, bad signature) is part of the status, not
	// a failed request.
	if err := h.opts.Updater.Check(r.Context()); errors.Is(err, apitypes.ErrUnavailable) {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.opts.Updater.Status())
}

func (h *handler) updateApply(w http.ResponseWriter, r *http.Request) {
	if h.opts.Updater == nil {
		h.fail(w, r, apitypes.ErrUnavailable)
		return
	}
	// The download outlives this request.
	if err := h.opts.Updater.Apply(context.WithoutCancel(r.Context())); err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, h.opts.Updater.Status())
}

// decode reads a JSON body into dst, refusing unknown fields and trailing
// data. The content type was checked by the guard. An empty body leaves
// dst as it is.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	if dec.More() {
		writeError(w, http.StatusBadRequest, "invalid JSON body: trailing data")
		return false
	}
	return true
}

func positive(w http.ResponseWriter, name string, v float64) bool {
	if v > 0 && !math.IsInf(v, 0) && !math.IsNaN(v) {
		return true
	}
	writeError(w, http.StatusBadRequest, name+" must be a positive number")
	return false
}

func timeParam(w http.ResponseWriter, r *http.Request, name string, def time.Time) (time.Time, bool) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def, true
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		writeError(w, http.StatusBadRequest, name+" must be an RFC 3339 time")
		return time.Time{}, false
	}
	return t, true
}

func (h *handler) reply(w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, status, v)
}

// statusOf maps the shared errors (model, apitypes) to HTTP statuses.
func statusOf(err error) int {
	switch {
	case errors.Is(err, model.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, model.ErrInvalid):
		return http.StatusBadRequest
	case errors.Is(err, apitypes.ErrLocked):
		return http.StatusForbidden
	case errors.Is(err, model.ErrConflict), errors.Is(err, model.ErrUnreachable),
		errors.Is(err, model.ErrSessionChanged), errors.Is(err, apitypes.ErrUnavailable):
		return http.StatusConflict
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

func (h *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := statusOf(err)
	if status >= 500 {
		h.log.Error("api", "method", r.Method, "path", r.URL.Path, "err", err)
	} else {
		h.log.Debug("api", "method", r.Method, "path", r.URL.Path, "status", status, "err", err)
	}
	writeError(w, status, sentence(err.Error()))
}

// sentence capitalises an error message for display.
func sentence(s string) string {
	if s == "" {
		return "Request failed."
	}
	if c := s[0]; c >= 'a' && c <= 'z' {
		s = string(c-'a'+'A') + s[1:]
	}
	return s
}

// nonNil turns a nil slice into an empty one, so it marshals as [].
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode response: "+err.Error())
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "application/json; charset=utf-8")
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
