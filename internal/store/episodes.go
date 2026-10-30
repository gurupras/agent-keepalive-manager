package store

import (
	"context"
	"sort"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// ---- experiment episodes ----

// Episode is one quiet stretch of a session taking part in an experiment
// (internal/experiment).
type Episode struct {
	ID         int64
	Experiment string
	Arm        string
	Cause      model.QuietCause
	PlanID     int64
	Key        string
	Harness    model.Harness
	Model      string
	// QuietFrom is the last real API call before the stretch; with PlanID
	// and Experiment it identifies the stretch.
	QuietFrom   time.Time
	StartedAt   time.Time // when the episode opened
	ContextSize int64
	// Recache is what re-writing ContextSize tokens costs: what a cold
	// return would pay.
	Recache float64
	EndedAt time.Time // zero while open
	EndedBy string    // model.Episode*
	Warm    bool
	// Rewrite is the cache writes of the returning call; for an episode that
	// ended without one, Recache if the cache had expired by then, else 0.
	Rewrite float64
}

// InsertEpisode opens an episode and returns it with its ID. inserted is
// false if the stretch already had one for the experiment.
func (s *Store) InsertEpisode(ctx context.Context, e Episode) (Episode, bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO episodes (experiment, arm, cause, plan_id, key, harness, model,
		quiet_from, started_at, context_size, recache) VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(plan_id, quiet_from, experiment) DO NOTHING`,
		e.Experiment, e.Arm, e.Cause, e.PlanID, e.Key, e.Harness, e.Model, ms(e.QuietFrom), ms(e.StartedAt),
		e.ContextSize, e.Recache)
	if err != nil {
		return e, false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return e, false, err
	}
	e.ID, err = res.LastInsertId()
	return e, err == nil, err
}

// CloseEpisode records how an open episode ended.
func (s *Store) CloseEpisode(ctx context.Context, e Episode) error {
	_, err := s.db.ExecContext(ctx, `UPDATE episodes SET ended_at=?, ended_by=?, warm=?, rewrite=?
		WHERE id=? AND ended_at=0`, ms(e.EndedAt), e.EndedBy, b2i(e.Warm), e.Rewrite, e.ID)
	return err
}

// CloseOpenEpisodes closes every open episode as interrupted: their state
// did not survive a restart.
func (s *Store) CloseOpenEpisodes(ctx context.Context, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE episodes SET ended_at=?, ended_by=? WHERE ended_at=0`,
		ms(at), model.EpisodeInterrupted)
	return err
}

// TreatmentSpend returns the treatment pings sent in [from, to) and what
// they cost.
func (s *Store) TreatmentSpend(ctx context.Context, from, to time.Time) (pings int, cost float64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(cost), 0) FROM pings
		WHERE episode_id != 0 AND state != ? AND sent_at >= ? AND sent_at < ?`,
		model.PingFailed, ms(from), ms(to)).Scan(&pings, &cost)
	return pings, cost, err
}

// TreatmentTotals sums the closed treatment episodes of an experiment that
// count: how many, what they cost (pings plus cache writes on return) and
// what re-writing the cache every time would have cost.
func (s *Store) TreatmentTotals(ctx context.Context, experiment string) (n int, cost, recache float64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(e.rewrite + COALESCE(
			(SELECT SUM(p.cost) FROM pings p WHERE p.episode_id = e.id), 0)), 0), COALESCE(SUM(e.recache), 0)
		FROM episodes e WHERE e.experiment=? AND e.arm=? AND e.ended_at != 0 AND e.ended_by != ?`,
		experiment, model.ArmPing, model.EpisodeInterrupted).Scan(&n, &cost, &recache)
	return n, cost, recache, err
}

// experimentDay sums the episodes closed in [lo, hi) (unix ms) by
// experiment, arm, harness and model. Interrupted episodes are left out.
func (s *Store) experimentDay(ctx context.Context, lo, hi int64) ([]model.ExperimentSum, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.experiment, e.arm, e.harness, e.model, e.ended_by, e.warm,
		e.rewrite, e.context_size, (e.ended_at - e.quiet_from) / 60000,
		COALESCE(SUM(p.id IS NOT NULL AND p.state != ?), 0), COALESCE(SUM(p.calls), 0), COALESCE(SUM(p.cost), 0)
		FROM episodes e LEFT JOIN pings p ON p.episode_id = e.id
		WHERE e.ended_at >= ? AND e.ended_at < ? AND e.ended_by != ?
		GROUP BY e.id`, model.PingFailed, lo, hi, model.EpisodeInterrupted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type group struct{ exp, arm, h, m string }
	sums := map[group]*model.ExperimentSum{}
	for rows.Next() {
		var g group
		var endedBy string
		var warm bool
		var rewrite, pingCost float64
		var ctxSize, quiet int64
		var pings, calls int
		if err := rows.Scan(&g.exp, &g.arm, &g.h, &g.m, &endedBy, &warm, &rewrite, &ctxSize, &quiet,
			&pings, &calls, &pingCost); err != nil {
			return nil, err
		}
		o := sums[g]
		if o == nil {
			o = &model.ExperimentSum{Experiment: g.exp, Arm: g.arm, Harness: model.Harness(g.h), Model: g.m}
			sums[g] = o
		}
		cost := pingCost + rewrite
		o.Episodes++
		if endedBy == model.EpisodeReturned {
			o.Returned++
			if warm {
				o.Warm++
			}
		}
		o.Pings += pings
		o.PingCalls += calls
		o.PingCostUSD += pingCost
		o.RewriteUSD += rewrite
		o.CostUSD += cost
		o.CostSqUSD += cost * cost
		o.ContextTokens += ctxSize
		o.QuietMinutes += quiet
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]model.ExperimentSum, 0, len(sums))
	for _, o := range sums {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Experiment != b.Experiment {
			return a.Experiment < b.Experiment
		}
		if a.Arm != b.Arm {
			return a.Arm < b.Arm
		}
		if a.Harness != b.Harness {
			return a.Harness < b.Harness
		}
		return a.Model < b.Model
	})
	return out, nil
}
