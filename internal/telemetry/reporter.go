package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// Settings is the runtime metrics configuration (internal/config).
type Settings interface {
	// Enabled reports whether reporting is on, whether that choice is
	// fixed, and by whom ("admin" or "profile") when it is.
	Enabled() (on, locked bool, by string)
	// URL is the hub's base URL; "" means this build reports nowhere.
	URL() string
	// Token is the optional bearer token for the hub.
	Token() string
	// SetEnabled persists the user's choice (apitypes.ErrLocked when fixed).
	SetEnabled(on bool) error
	// Experiments reports whether the install takes part in experiments
	// (docs/experiments.md), whether that is fixed, and by whom.
	Experiments() (on, locked bool, by string)
	// SetExperiments persists the user's choice (apitypes.ErrLocked when
	// fixed).
	SetExperiments(on bool) error
}

// maxBackfill is how many days back unsent reports are made and kept; the
// hub refuses older ones.
const maxBackfill = 30

// Options configure a Reporter.
type Options struct {
	DataDir  string
	Build    Build // OS and Arch default to the runtime's
	Stats    model.Stats
	Settings Settings
	Client   *http.Client // default: 30 s timeout
	Log      *slog.Logger
	// SendJitter spreads the send after midnight UTC over [0, SendJitter)
	// (default 15 min; negative: none), so installs do not all report at once.
	SendJitter time.Duration
	// RetryMin and RetryMax bound the backoff between failed sends
	// (defaults 1 min and 6 h).
	RetryMin, RetryMax time.Duration

	now func() time.Time
}

// Reporter builds, queues and sends usage reports. It implements
// apitypes.Metrics and apitypes.MetricsNotifier.
type Reporter struct {
	opts Options
	q    queue

	mu         sync.Mutex
	st         state
	loaded     bool
	installID  string
	epoch      int // bumped whenever reporting turns on or off
	sendCancel context.CancelFunc
	attempts   int
	nextTry    time.Time
	subs       map[int]func(apitypes.MetricsStatus)
	nextSub    int
	notifyMu   sync.Mutex

	poke chan struct{}
}

// New returns a Reporter. It does nothing until Run.
func New(opts Options) *Reporter {
	if opts.Build.OS == "" {
		opts.Build.OS = runtime.GOOS
	}
	if opts.Build.Arch == "" {
		opts.Build.Arch = runtime.GOARCH
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.SendJitter == 0 {
		opts.SendJitter = 15 * time.Minute
	}
	if opts.RetryMin <= 0 {
		opts.RetryMin = time.Minute
	}
	if opts.RetryMax <= 0 {
		opts.RetryMax = 6 * time.Hour
	}
	if opts.now == nil {
		opts.now = func() time.Time { return time.Now().Round(0) }
	}
	r := &Reporter{
		opts: opts,
		q:    queue(filepath.Join(opts.DataDir, queueDir)),
		subs: map[int]func(apitypes.MetricsStatus){},
		poke: make(chan struct{}, 1),
	}
	r.installID = readInstallID(opts.DataDir)
	r.st, r.loaded = loadState(opts.DataDir)
	return r
}

// active reports whether reports are made and sent: a hub is configured
// and reporting is on.
func (r *Reporter) active() bool {
	on, _, _ := r.opts.Settings.Enabled()
	return on && r.opts.Settings.URL() != ""
}

// Status returns the state of reporting.
func (r *Reporter) Status() apitypes.MetricsStatus {
	on, locked, by := r.opts.Settings.Enabled()
	url := r.opts.Settings.URL()
	r.mu.Lock()
	defer r.mu.Unlock()
	st := apitypes.MetricsStatus{
		Available: url != "",
		Enabled:   on,
		Locked:    locked,
		Endpoint:  url,
		InstallID: r.installID,
		LastSent:  r.st.LastSent,
		LastError: r.st.LastError,
	}
	if locked {
		st.LockedBy = by
	}
	st.Experiments, st.ExperimentsLocked, by = r.opts.Settings.Experiments()
	if st.ExperimentsLocked {
		st.ExperimentsLockedBy = by
	}
	if on && url != "" {
		st.Queued = len(r.q.days())
	}
	return st
}

// Subscribe calls fn with the status after every change until cancel.
func (r *Reporter) Subscribe(fn func(apitypes.MetricsStatus)) (cancel func()) {
	r.mu.Lock()
	id := r.nextSub
	r.nextSub++
	r.subs[id] = fn
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.subs, id)
		r.mu.Unlock()
	}
}

func (r *Reporter) notify() {
	r.notifyMu.Lock()
	defer r.notifyMu.Unlock()
	st := r.Status()
	r.mu.Lock()
	fns := make([]func(apitypes.MetricsStatus), 0, len(r.subs))
	for _, fn := range r.subs {
		fns = append(fns, fn)
	}
	r.mu.Unlock()
	for _, fn := range fns {
		fn(st)
	}
}

// SetEnabled turns reporting on or off and persists the choice. Off stops
// a send in progress and deletes the queue before it returns.
func (r *Reporter) SetEnabled(on bool) error {
	cur, locked, _ := r.opts.Settings.Enabled()
	if locked {
		if cur != on {
			return apitypes.ErrLocked
		}
		return nil
	}
	if r.opts.Settings.URL() == "" {
		return fmt.Errorf("%w: this build has no usage hub", apitypes.ErrUnavailable)
	}
	if err := r.opts.Settings.SetEnabled(on); err != nil {
		return err
	}
	r.mu.Lock()
	r.sync(r.opts.now())
	r.mu.Unlock()
	r.wake()
	r.notify()
	return nil
}

// SetExperiments turns taking part in experiments on or off and persists
// the choice. Off stops new episodes at once; episodes closed earlier and
// not yet reported are left out of reports, queued ones included.
func (r *Reporter) SetExperiments(on bool) error {
	cur, locked, _ := r.opts.Settings.Experiments()
	if locked {
		if cur != on {
			return apitypes.ErrLocked
		}
		return nil
	}
	if r.opts.Settings.URL() == "" {
		return fmt.Errorf("%w: this build has no usage hub", apitypes.ErrUnavailable)
	}
	if err := r.opts.Settings.SetExperiments(on); err != nil {
		return err
	}
	r.notify()
	return nil
}

// ResetID replaces the install id with a new random one. Reports already
// queued keep the id they were made with.
func (r *Reporter) ResetID() error {
	r.mu.Lock()
	id, err := writeInstallID(r.opts.DataDir)
	if err == nil {
		r.installID = id
	}
	r.mu.Unlock()
	if err != nil {
		return err
	}
	r.notify()
	return nil
}

// Preview returns the next report exactly as it would be sent: the oldest
// queued one, or else today's so far.
func (r *Reporter) Preview() (any, error) {
	if r.opts.Settings.URL() == "" {
		return nil, fmt.Errorf("%w: this build has no usage hub", apitypes.ErrUnavailable)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active() {
		if days := r.q.days(); len(days) > 0 {
			if b, err := r.q.read(days[0]); err == nil {
				return json.RawMessage(b), nil
			}
		}
	}
	now := r.opts.now()
	return r.build(now.UTC().Truncate(24 * time.Hour))
}

// build makes the report of the UTC day starting at day. Call with r.mu held.
func (r *Reporter) build(day time.Time) (Report, error) {
	ds, err := r.opts.Stats.DayStats(day)
	if err != nil {
		return Report{}, fmt.Errorf("usage of %s: %w", dayOf(day), err)
	}
	ds.Day = dayOf(day)
	return r.buildFrom(ds)
}

func (r *Reporter) buildFrom(ds model.DayStats) (Report, error) {
	if r.installID == "" {
		id, err := writeInstallID(r.opts.DataDir)
		if err != nil {
			return Report{}, fmt.Errorf("install id: %w", err)
		}
		r.installID = id
	}
	if on, _, _ := r.opts.Settings.Experiments(); !on {
		ds.Experiments = nil
	}
	return BuildReport(r.installID, r.opts.Build, ds), nil
}

// sync brings the persisted state in line with whether reporting is
// active, and records today as seen. Turning off cancels a send in
// progress and deletes the queue; turning on starts from today, so days
// spent off are never reported. Call with r.mu held.
func (r *Reporter) sync(now time.Time) {
	today := now.UTC().Truncate(24 * time.Hour)
	yesterday := dayOf(today.AddDate(0, 0, -1))
	if !r.active() {
		if r.st.Active || !r.loaded {
			r.st.Active, r.loaded = false, true
			r.epoch++
			r.save()
		}
		if r.sendCancel != nil {
			r.sendCancel()
			r.sendCancel = nil
		}
		if len(r.q.days()) > 0 {
			if err := r.q.drop(); err != nil {
				r.opts.Log.Warn("metrics: could not delete the report queue", "err", err)
			}
		}
		r.attempts, r.nextTry = 0, time.Time{}
		return
	}
	changed := false
	if !r.st.Active || !r.loaded {
		r.st.Active, r.loaded = true, true
		r.st.Through, r.st.Seen = yesterday, nil
		r.epoch++
		changed = true
	}
	if d := dayOf(today); !slices.Contains(r.st.Seen, d) {
		r.st.Seen = append(r.st.Seen, d)
		changed = true
	}
	// Forget seen days that can no longer be reported.
	oldest := dayOf(today.AddDate(0, 0, -maxBackfill))
	if i := slices.IndexFunc(r.st.Seen, func(d string) bool { return d >= oldest }); i > 0 {
		r.st.Seen = r.st.Seen[i:]
		changed = true
	}
	if changed {
		r.save()
	}
}

func (r *Reporter) save() {
	if err := saveState(r.opts.DataDir, r.st); err != nil {
		r.opts.Log.Warn("metrics: could not save state", "err", err)
	}
}

// catchUp queues the report of every finished day not built yet, up to 30
// days back. A day without activity is reported only if the server ran on
// it. Call with r.mu held, while active.
func (r *Reporter) catchUp(now time.Time) {
	today := now.UTC().Truncate(24 * time.Hour)
	oldest := today.AddDate(0, 0, -maxBackfill)
	from := oldest
	if t, ok := parseDay(r.st.Through); ok && !t.Before(oldest) {
		from = t.AddDate(0, 0, 1)
	}
	built := false
	for d := from; d.Before(today); d = d.AddDate(0, 0, 1) {
		ds, err := r.opts.Stats.DayStats(d)
		if err != nil {
			r.opts.Log.Warn("metrics: could not read usage", "day", dayOf(d), "err", err)
			break // try again on the next pass
		}
		ds.Day = dayOf(d)
		if !empty(ds) || slices.Contains(r.st.Seen, ds.Day) {
			rep, err := r.buildFrom(ds)
			if err == nil {
				var b []byte
				if b, err = json.Marshal(rep); err == nil {
					err = r.q.put(ds.Day, b)
				}
			}
			if err != nil {
				r.opts.Log.Warn("metrics: could not queue a report", "day", ds.Day, "err", err)
				break
			}
			r.opts.Log.Debug("metrics: report queued", "day", ds.Day)
		}
		r.st.Through, built = ds.Day, true
	}
	// Reports the hub would refuse as too old.
	for _, day := range r.q.days() {
		if day < dayOf(oldest) {
			_ = r.q.remove(day) // best effort: a leftover is sent again (the hub keeps one per day) or expires
		}
	}
	if built {
		r.save()
	}
}

// Run makes and sends reports until ctx ends: at start-up for any day not
// sent yet, then after each UTC day ends. Call it once.
func (r *Reporter) Run(ctx context.Context) {
	for {
		wait := r.pass(ctx)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			r.mu.Lock()
			if r.sendCancel != nil {
				r.sendCancel()
			}
			r.mu.Unlock()
			return
		case <-r.poke:
		case <-t.C:
		}
		t.Stop()
	}
}

func (r *Reporter) wake() {
	select {
	case r.poke <- struct{}{}:
	default:
	}
}

// pass syncs, queues finished days and sends what is due; it returns how
// long to wait before the next pass.
func (r *Reporter) pass(ctx context.Context) time.Duration {
	now := r.opts.now()
	r.mu.Lock()
	before := len(r.q.days())
	r.sync(now)
	active := r.active()
	if active {
		r.catchUp(now)
	}
	changed := len(r.q.days()) != before
	r.mu.Unlock()
	if changed {
		r.notify()
	}
	next := nextMidnight(now)
	if r.opts.SendJitter > 0 {
		next = next.Add(rand.N(r.opts.SendJitter))
	}
	if active {
		if retry := r.sendDue(ctx); !retry.IsZero() && retry.Before(next) {
			next = retry
		}
	}
	return max(next.Sub(r.opts.now()), 0)
}

func nextMidnight(t time.Time) time.Time {
	return t.UTC().Truncate(24*time.Hour).AddDate(0, 0, 1)
}

// sendDue sends the queued reports, oldest first, unless a retry is not due
// yet. It returns when to try again, or zero when the queue is empty.
func (r *Reporter) sendDue(parent context.Context) (retryAt time.Time) {
	r.mu.Lock()
	if now := r.opts.now(); now.Before(r.nextTry) {
		at := r.nextTry
		r.mu.Unlock()
		return at
	}
	ctx, cancel := context.WithCancel(parent)
	r.sendCancel = cancel
	epoch := r.epoch
	r.mu.Unlock()
	defer cancel()

	for {
		r.mu.Lock()
		if r.epoch != epoch || !r.active() {
			r.mu.Unlock()
			return time.Time{}
		}
		days := r.q.days()
		if len(days) == 0 {
			r.mu.Unlock()
			return time.Time{}
		}
		day := days[0]
		body, err := r.q.read(day)
		if on, _, _ := r.opts.Settings.Experiments(); err == nil && !on {
			body = withoutExperiments(body) // queued before the opt-out
		}
		r.mu.Unlock()
		if err != nil {
			r.mu.Lock()
			_ = r.q.remove(day) // best effort: a leftover is sent again (the hub keeps one per day) or expires
			r.mu.Unlock()
			continue
		}

		res := r.post(ctx, body)

		r.mu.Lock()
		if r.epoch != epoch || ctx.Err() != nil {
			r.mu.Unlock()
			return time.Time{} // turned off (or shutting down) meanwhile
		}
		switch {
		case res.ok:
			_ = r.q.remove(day) // best effort: a leftover is sent again (the hub keeps one per day) or expires
			r.st.LastSent, r.st.LastError = r.opts.now(), ""
			r.attempts, r.nextTry = 0, time.Time{}
			r.opts.Log.Info("metrics: report sent", "day", day)
		case res.drop:
			_ = r.q.remove(day) // best effort: a leftover is sent again (the hub keeps one per day) or expires
			r.attempts, r.nextTry = 0, time.Time{}
			r.st.LastError = fmt.Sprintf("the hub refused the report for %s: %s", day, res.err)
			r.opts.Log.Warn("metrics: report refused; dropped", "day", day, "err", res.err)
		default:
			r.st.LastError = fmt.Sprintf("could not send the report for %s: %s", day, res.err)
			wait := r.backoff()
			wait = max(wait, res.retryAfter)
			r.nextTry = r.opts.now().Add(wait)
			r.opts.Log.Info("metrics: send failed; will retry", "day", day, "in", wait.Round(time.Second), "err", res.err)
		}
		r.save()
		retry := r.nextTry
		r.mu.Unlock()
		r.notify()
		if !retry.IsZero() {
			return retry
		}
	}
}

// backoff returns the wait before the next attempt: RetryMin doubling up
// to RetryMax, with ±50% jitter. Call with r.mu held.
func (r *Reporter) backoff() time.Duration {
	d := r.opts.RetryMin << min(r.attempts, 20)
	if d <= 0 || d > r.opts.RetryMax {
		d = r.opts.RetryMax
	}
	r.attempts++
	return d/2 + rand.N(d)
}

type postResult struct {
	ok, drop   bool
	err        error
	retryAfter time.Duration
}

// post sends one report. 200 and 202 are done; 429, 5xx and network errors
// are retried; any other answer drops the report.
func (r *Reporter) post(ctx context.Context, body []byte) postResult {
	url := strings.TrimRight(r.opts.Settings.URL(), "/") + "/v1/reports"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return postResult{err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "agent-keepalive/"+r.opts.Build.Version)
	if tok := r.opts.Settings.Token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := r.opts.Client.Do(req)
	if err != nil {
		return postResult{err: err}
	}
	defer res.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	status := fmt.Errorf("HTTP %d", res.StatusCode)
	if m := strings.TrimSpace(string(msg)); m != "" && len(m) <= 200 {
		status = fmt.Errorf("HTTP %d: %s", res.StatusCode, m)
	}
	switch {
	case res.StatusCode == http.StatusOK || res.StatusCode == http.StatusAccepted:
		return postResult{ok: true}
	case res.StatusCode == http.StatusTooManyRequests:
		ra, _ := strconv.Atoi(res.Header.Get("Retry-After"))
		return postResult{err: status, retryAfter: time.Duration(min(max(ra, 0), 6*3600)) * time.Second}
	case res.StatusCode >= 500:
		return postResult{err: status}
	}
	return postResult{drop: true, err: status}
}
