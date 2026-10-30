package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/experiment"
	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/pricing"
	"github.com/gurupras/agent-keepalive-manager/internal/store"
)

// Experiments (internal/experiment, docs/experiments.md).
//
// A plan's session that the harness reports as working while its main agent
// makes no calls (Session.Quiet) opens an episode of the experiment for that
// cause. The hold arm changes nothing; the ping arm pings the session as if
// it were idle. The episode closes when the main agent makes its next real
// call (returned), when the session stops working or stays loud without one
// (left), or when the plan ends (ended).

const (
	saltKey       = "exp:salt"
	stoppedPrefix = "exp:stopped:"
	// leaveIdle and leaveLoud: how long a session may be idle, or working
	// without being quiet, before its episode closes as left. The first
	// leaves time for the returning call's usage to settle.
	leaveIdle = time.Minute
	leaveLoud = ReturnWait
)

// episodeRun is a plan's open episode.
type episodeRun struct {
	store.Episode
	pings     int       // treatment pings sent
	maxPings  int       // break-even pings of the model; no more are sent
	loudSince time.Time // when the session stopped being quiet; zero while quiet
}

// restoreExperiments loads the salt and stopped experiments, and closes the
// episodes a restart cut off.
func (s *Service) restoreExperiments(ctx context.Context) error {
	st := s.cfg.Store
	salt, ok, err := st.Get(ctx, saltKey)
	if err != nil {
		return fmt.Errorf("scheduler: load experiment salt: %w", err)
	}
	if !ok {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		salt = hex.EncodeToString(b)
		if err := st.Set(ctx, saltKey, salt); err != nil {
			return fmt.Errorf("scheduler: store experiment salt: %w", err)
		}
	}
	s.expSalt = salt
	stopped, err := st.Prefixed(ctx, stoppedPrefix)
	if err != nil {
		return fmt.Errorf("scheduler: load stopped experiments: %w", err)
	}
	for k := range stopped {
		s.expStopped[strings.TrimPrefix(k, stoppedPrefix)] = true
	}
	return st.CloseOpenEpisodes(ctx, s.now())
}

// enrolled reports whether this install takes part in experiments.
func (s *Service) enrolled() bool { return s.cfg.Experiments != nil && s.cfg.Experiments() }

// budgetLeft reports whether today's treatment budget has room. Requires
// s.mu.
func (s *Service) budgetLeft(ctx context.Context, now time.Time) bool {
	if day := store.Day(now); day != s.expDay {
		d := now.UTC()
		start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
		n, cost, err := s.cfg.Store.TreatmentSpend(context.WithoutCancel(ctx), start, start.AddDate(0, 0, 1))
		if err != nil {
			s.log.Warn("scheduler: reading treatment spend failed", "err", err)
			return false
		}
		eps, err := s.cfg.Store.EpisodesStarted(context.WithoutCancel(ctx), start, start.AddDate(0, 0, 1))
		if err != nil {
			s.log.Warn("scheduler: counting experiment episodes failed", "err", err)
			return false
		}
		s.expDay, s.expPings, s.expCost, s.expEpisodes = day, n, cost, eps
	}
	return s.expPings < experiment.DailyPings && s.expCost < experiment.DailyCostUSD &&
		s.expEpisodes < experiment.DailyEpisodes
}

// reserve is what a treatment ping of ep is expected to cost: a cache read
// of its context. A cold reply costs more, a re-write the agent's return
// would otherwise have paid, and is counted once priced. Requires s.mu.
func (s *Service) reserve(ep *episodeRun) float64 {
	price, ok := s.cfg.Pricing.Lookup(ep.Model)
	if !ok {
		return 0
	}
	return float64(ep.ContextSize) * price.Read / 1e6
}

// experimentStep opens, follows and closes the plan's episode, and reports
// whether the plan should ping its working session as if it were idle (the
// ping arm). working is the session's state before that. Requires s.mu.
func (s *Service) experimentStep(ctx context.Context, now time.Time, pr *planRun, sess model.Session, working bool, ttl time.Duration) bool {
	if ep := pr.ep; ep != nil {
		switch {
		case !s.enrolled():
			s.closeEpisode(ctx, pr, now, model.EpisodeInterrupted, false, 0)
		case working && sess.Quiet != "":
			ep.loudSince = time.Time{}
		default:
			if ep.loudSince.IsZero() {
				ep.loudSince = now
			}
			limit := leaveLoud
			if !working {
				limit = leaveIdle
			}
			if now.Sub(ep.loudSince) >= limit {
				// Whether the cache held when the stretch ended decides what
				// the next return would pay.
				warm := !sess.LastCall.IsZero() && ep.loudSince.Before(sess.LastCall.Add(ttl))
				var rewrite float64
				if !warm {
					rewrite = ep.Recache
				}
				s.closeEpisode(ctx, pr, now, model.EpisodeLeft, warm, rewrite)
			}
		}
	}
	if pr.ep == nil && working && sess.Quiet != "" {
		s.openEpisode(ctx, now, pr, sess, ttl)
	}
	ep := pr.ep
	return ep != nil && ep.Arm == model.ArmPing && pr.State == model.PlanActive && working && sess.Quiet != "" &&
		ep.pings < ep.maxPings && !s.expStopped[ep.Experiment] && s.budgetLeft(ctx, now) &&
		s.expCost+s.reserve(ep) <= experiment.DailyCostUSD
}

// openEpisode opens an episode for the session's quiet stretch if one may
// open. Requires s.mu.
func (s *Service) openEpisode(ctx context.Context, now time.Time, pr *planRun, sess model.Session, ttl time.Duration) {
	from := sess.LastCall
	if pr.State != model.PlanActive || pr.WaitWarm || pr.pending != nil || from.IsZero() ||
		from.Equal(pr.epDone) || !now.Before(from.Add(ttl)) || s.undeliverable(pr, sess) != "" || !s.enrolled() {
		return
	}
	exp, ok := experiment.ForCause(sess.Quiet)
	if !ok || s.expStopped[exp.ID] || !s.budgetLeft(ctx, now) {
		return
	}
	e := store.Episode{
		Experiment: exp.ID, Cause: sess.Quiet, PlanID: pr.ID, Key: pr.Key, Harness: sess.Harness,
		Model: sess.Model, QuietFrom: from, StartedAt: now, ContextSize: pr.BaseContext,
	}
	if pr.BaseModel != "" {
		e.Model = pr.BaseModel
	}
	if e.ContextSize == 0 {
		e.ContextSize = sess.ContextTokens
	}
	price, ok := s.cfg.Pricing.Lookup(e.Model)
	if !ok || e.ContextSize == 0 {
		return // an episode that cannot be priced tells nothing
	}
	w := price.WritePrice(ttl)
	e.Recache = float64(e.ContextSize) * w / 1e6
	e.Arm = experiment.Arm(s.expSalt, pr.Key, from, exp.ID)
	e, inserted, err := s.cfg.Store.InsertEpisode(context.WithoutCancel(ctx), e)
	if err != nil {
		s.log.Warn("scheduler: opening experiment episode failed", "key", pr.Key, "err", err)
		return
	}
	if !inserted {
		pr.epDone = from // the stretch had its episode already
		return
	}
	s.expEpisodes++ // budgetLeft has just set expDay to today
	pr.ep = &episodeRun{Episode: e, maxPings: pricing.BreakEvenPings(w, price.Read)}
	s.log.Info("scheduler: experiment episode opened", "key", pr.Key, "experiment", e.Experiment, "arm", e.Arm)
}

// returnEpisode closes the plan's episode on a real API call of the main
// agent made after the stretch began. Requires s.mu.
func (s *Service) returnEpisode(ctx context.Context, pr *planRun, call model.Activity) {
	ep := pr.ep
	if ep == nil || call.Kind != model.ActAssistant || call.Usage == nil || !call.Anchor().After(ep.QuietFrom) {
		return
	}
	warm := float64(call.Usage.CacheRead) >= HitFraction*float64(ep.ContextSize)
	var rewrite float64
	if price, ok := s.cfg.Pricing.Lookup(ep.Model); ok {
		rewrite = price.Cost(model.Usage{CacheWrite5m: call.Usage.CacheWrite5m, CacheWrite1h: call.Usage.CacheWrite1h})
	}
	s.closeEpisode(ctx, pr, call.At, model.EpisodeReturned, warm, rewrite)
}

// closeEpisode records how the plan's episode ended and, after a treatment
// episode, applies the stop rule. Requires s.mu.
func (s *Service) closeEpisode(ctx context.Context, pr *planRun, at time.Time, by string, warm bool, rewrite float64) {
	ep := pr.ep
	if ep == nil {
		return
	}
	pr.ep, pr.epDone = nil, ep.QuietFrom
	ep.EndedAt, ep.EndedBy, ep.Warm, ep.Rewrite = at, by, warm, rewrite
	ctx = context.WithoutCancel(ctx)
	if err := s.cfg.Store.CloseEpisode(ctx, ep.Episode); err != nil {
		s.log.Warn("scheduler: closing experiment episode failed", "key", pr.Key, "err", err)
		return
	}
	s.log.Info("scheduler: experiment episode closed", "key", pr.Key, "experiment", ep.Experiment, "arm", ep.Arm,
		"by", by, "warm", warm, "pings", ep.pings)
	if ep.Arm != model.ArmPing || by == model.EpisodeInterrupted || s.expStopped[ep.Experiment] {
		return
	}
	n, cost, recache, err := s.cfg.Store.TreatmentTotals(ctx, ep.Experiment)
	if err != nil {
		s.log.Warn("scheduler: reading treatment totals failed", "err", err)
		return
	}
	if n < experiment.StopAfter || cost <= recache {
		return
	}
	s.expStopped[ep.Experiment] = true
	why := fmt.Sprintf("%d treatment episodes cost $%.4f, more than re-caching each ($%.4f)", n, cost, recache)
	if err := s.cfg.Store.Set(ctx, stoppedPrefix+ep.Experiment, why); err != nil {
		s.log.Warn("scheduler: storing stopped experiment failed", "err", err)
	}
	s.log.Info("scheduler: experiment stopped on this install", "experiment", ep.Experiment, "why", why)
}

// experimentView is the plan's open episode for the UI. Requires s.mu.
func experimentView(pr *planRun) *model.ExperimentView {
	if pr == nil || pr.ep == nil {
		return nil
	}
	ep := pr.ep
	return &model.ExperimentView{ID: ep.Experiment, Arm: ep.Arm, Cause: ep.Cause, Since: ep.StartedAt, Pings: ep.pings}
}
