// Package store persists the scheduler's state in SQLite (modernc.org/sqlite,
// pure Go): plans, pings, open ledger segments, the ledger itself, settings,
// a small key/value table (learned cache TTLs and the like) and per-day
// session sightings for usage reports.
//
// The ledger must survive crashes and restarts without double counting, so
// open idle segments are rows, closing one is a single transaction (ledger
// entry + plan + segment), and ledger entries are unique on
// (plan_id, idle_from, ended_by): booking the same segment twice is a no-op.
// A ping whose cost becomes known after its segment was booked corrects that
// entry in the same transaction that records the cost.
//
// Times are stored as unix milliseconds; the zero time is stored as 0.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// Store is a handle to the database. It is safe for concurrent use.
type Store struct {
	db   *sql.DB
	file bool // on disk (WAL applies), not ":memory:"
}

// Open opens (creating if needed) the database at path and applies any
// pending migrations. ":memory:" opens a private in-memory database.
func Open(path string) (*Store, error) {
	var dsn string
	if path == ":memory:" {
		dsn = ":memory:"
	} else {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		dsn = "file:" + path
	}
	// Immediate transactions take the write lock at BEGIN, so two writers
	// wait on busy_timeout instead of failing on a read-to-write upgrade.
	// Per-connection pragmas go in the DSN so every pooled connection gets
	// them; journal_mode is persistent and is set once by migrate.
	dsn += "?_txlock=immediate&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		// Every connection would otherwise get its own empty database.
		db.SetMaxOpenConns(1)
	}
	s := &Store{db: db, file: path != ":memory:"}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate %s: %w", path, err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// ---- helpers ----

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

type scanner interface{ Scan(...any) error }

func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- plans ----

// Plan is a persisted plan plus the scheduler bookkeeping a restart needs.
type Plan struct {
	model.Plan
	// ProcStart is the start token of PID when the plan began, so a reused
	// pid is not mistaken for the plan's process.
	ProcStart string
	// RequestedHours is what the user asked for (before the break-even
	// cap); usage reports sum it.
	RequestedHours float64
	// ColdStreak counts consecutive cold pings (two pause the plan).
	ColdStreak int
	// WaitWarm: the cache expired while the plan could not ping; nothing is
	// sent until real activity warms it again.
	WaitWarm bool
	// LastSeen is the At of the last transcript activity the plan applied.
	// After a restart, activity up to it is not applied again.
	LastSeen time.Time
	// BaseFrom, BaseContext and BaseModel describe the last real API call:
	// when it refreshed the cache, its context size and model. The next
	// idle segment starts there.
	BaseFrom    time.Time
	BaseContext int64
	BaseModel   string
	// LastPingAt is when the newest delivered ping of the plan was sent.
	LastPingAt time.Time
}

const planCols = `id, key, pid, proc_start, hours, requested_hours, started_at, until, state, indefinite,
	ended_at, next_ping, pings, ping_cost, reason, last_error, cold_streak, wait_warm, last_seen,
	base_from, base_context, base_model, last_ping_at`

func scanPlan(sc scanner) (Plan, error) {
	var p Plan
	var started, until, ended, next, seen, base, lastPing int64
	var indef, wait int
	err := sc.Scan(&p.ID, &p.Key, &p.PID, &p.ProcStart, &p.Hours, &p.RequestedHours, &started, &until, &p.State,
		&indef, &ended, &next, &p.Pings, &p.PingCost, &p.Reason, &p.LastError, &p.ColdStreak, &wait, &seen,
		&base, &p.BaseContext, &p.BaseModel, &lastPing)
	p.StartedAt, p.Until, p.EndedAt, p.NextPing = fromMS(started), fromMS(until), fromMS(ended), fromMS(next)
	p.LastSeen, p.BaseFrom, p.LastPingAt = fromMS(seen), fromMS(base), fromMS(lastPing)
	p.Indefinite, p.WaitWarm = indef != 0, wait != 0
	return p, err
}

func planArgs(p Plan) []any {
	return []any{p.Key, p.PID, p.ProcStart, p.Hours, p.RequestedHours, ms(p.StartedAt), ms(p.Until), p.State,
		b2i(p.Indefinite), ms(p.EndedAt), ms(p.NextPing), p.Pings, p.PingCost, p.Reason, p.LastError,
		p.ColdStreak, b2i(p.WaitWarm), ms(p.LastSeen), ms(p.BaseFrom), p.BaseContext, p.BaseModel, ms(p.LastPingAt)}
}

const planSet = `key=?, pid=?, proc_start=?, hours=?, requested_hours=?, started_at=?, until=?, state=?,
	indefinite=?, ended_at=?, next_ping=?, pings=?, ping_cost=?, reason=?, last_error=?, cold_streak=?,
	wait_warm=?, last_seen=?, base_from=?, base_context=?, base_model=?, last_ping_at=?`

// InsertPlan stores a new plan and returns it with its ID set.
func (s *Store) InsertPlan(ctx context.Context, p Plan) (Plan, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO plans (key, pid, proc_start, hours, requested_hours, started_at,
		until, state, indefinite, ended_at, next_ping, pings, ping_cost, reason, last_error, cold_streak, wait_warm,
		last_seen, base_from, base_context, base_model, last_ping_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, planArgs(p)...)
	if err != nil {
		return p, err
	}
	p.ID, err = res.LastInsertId()
	return p, err
}

func updatePlan(ctx context.Context, ex interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, p Plan) error {
	res, err := ex.ExecContext(ctx, `UPDATE plans SET `+planSet+` WHERE id=?`, append(planArgs(p), p.ID)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdatePlan overwrites plan p.ID.
func (s *Store) UpdatePlan(ctx context.Context, p Plan) error { return updatePlan(ctx, s.db, p) }

// GetPlan returns one plan.
func (s *Store) GetPlan(ctx context.Context, id int64) (Plan, error) {
	p, err := scanPlan(s.db.QueryRowContext(ctx, `SELECT `+planCols+` FROM plans WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// OpenPlans returns the plans still attached to their session (active or
// paused), oldest first.
func (s *Store) OpenPlans(ctx context.Context) ([]Plan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+planCols+` FROM plans WHERE state IN (?, ?) ORDER BY id`,
		model.PlanActive, model.PlanPaused)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- segments ----

// Segment is an open idle segment of a plan: the gap since the last real
// activity in which pings were sent. Its pings carry SegFrom = IdleFrom.
type Segment struct {
	PlanID      int64
	Key         string
	Harness     model.Harness
	Model       string
	Title       string
	Project     string
	IdleFrom    time.Time // when the last real call refreshed the cache
	ContextSize int64     // context of that call: what a re-cache would write
	OpenedAt    time.Time // when the first ping of the segment was decided
}

const segCols = `plan_id, key, harness, model, title, project, idle_from, context_size, opened_at`

func insertSegment(ctx context.Context, tx *sql.Tx, g Segment) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO segments (`+segCols+`) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(plan_id, idle_from) DO NOTHING`,
		g.PlanID, g.Key, g.Harness, g.Model, g.Title, g.Project, ms(g.IdleFrom), g.ContextSize, ms(g.OpenedAt))
	return err
}

// OpenSegment stores a segment (no-op if it exists).
func (s *Store) OpenSegment(ctx context.Context, g Segment) error {
	return s.tx(ctx, func(tx *sql.Tx) error { return insertSegment(ctx, tx, g) })
}

// Segments returns every open segment, oldest first.
func (s *Store) Segments(ctx context.Context) ([]Segment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+segCols+` FROM segments ORDER BY plan_id, idle_from`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Segment
	for rows.Next() {
		var g Segment
		var idle, opened int64
		if err := rows.Scan(&g.PlanID, &g.Key, &g.Harness, &g.Model, &g.Title, &g.Project, &idle, &g.ContextSize, &opened); err != nil {
			return nil, err
		}
		g.IdleFrom, g.OpenedAt = fromMS(idle), fromMS(opened)
		out = append(out, g)
	}
	return out, rows.Err()
}

// CloseSegment books the segment (planID, e.IdleFrom) in one transaction:
// it counts the segment's pings and their cost from the pings table, sets
// e.Pings, e.PingCost and e.Net (= e.Avoided − cost), inserts the ledger
// entry unless the same (plan, idle_from, ended_by) is already booked,
// writes plan (if not nil), deletes the segment and opens next (if not nil).
// It returns the entry as stored and whether it was newly inserted.
func (s *Store) CloseSegment(ctx context.Context, e model.LedgerEntry, plan *Plan, next *Segment) (model.LedgerEntry, bool, error) {
	inserted := false
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(cost), 0) FROM pings
			WHERE plan_id=? AND seg_from=? AND state NOT IN (?, ?)`,
			e.PlanID, ms(e.IdleFrom), model.PingFailed, model.PingWithdrawn).Scan(&e.Pings, &e.PingCost); err != nil {
			return err
		}
		if e.Outcome != model.OutcomeSaved {
			e.Avoided = 0
		}
		e.Net = e.Avoided - e.PingCost
		if e.Pings == 0 {
			// Every ping of the segment failed or was withdrawn: nothing
			// reached the session, so there is nothing to book.
			return finishSegment(ctx, tx, e, plan, next)
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO ledger (plan_id, key, harness, title, project, model, idle_from,
			ended_at, ended_by, outcome, pings, ping_cost, avoided, net, context_size)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(plan_id, idle_from, ended_by) DO NOTHING`,
			e.PlanID, e.Key, e.Harness, e.Title, e.Project, e.Model, ms(e.IdleFrom), ms(e.EndedAt), e.EndedBy,
			e.Outcome, e.Pings, e.PingCost, e.Avoided, e.Net, e.ContextSize)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted = true
			e.ID, _ = res.LastInsertId()
		} else if err := tx.QueryRowContext(ctx, `SELECT id FROM ledger WHERE plan_id=? AND idle_from=? AND ended_by=?`,
			e.PlanID, ms(e.IdleFrom), e.EndedBy).Scan(&e.ID); err != nil {
			return err
		}
		return finishSegment(ctx, tx, e, plan, next)
	})
	return e, inserted, err
}

func finishSegment(ctx context.Context, tx *sql.Tx, e model.LedgerEntry, plan *Plan, next *Segment) error {
	if plan != nil {
		if err := updatePlan(ctx, tx, *plan); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM segments WHERE plan_id=? AND idle_from=?`,
		e.PlanID, ms(e.IdleFrom)); err != nil {
		return err
	}
	if next != nil {
		return insertSegment(ctx, tx, *next)
	}
	return nil
}

// ---- pings ----

// Ping is a persisted ping: the record plus the idle segment it belongs to.
type Ping struct {
	model.PingRecord
	// SegFrom is the IdleFrom of the segment the ping was sent in; zero for
	// a ping outside any plan.
	SegFrom time.Time
	// EpisodeID is the experiment episode whose treatment sent the ping;
	// zero for every other ping.
	EpisodeID int64
}

const pingCols = `id, plan_id, key, sent_at, state, calls, usage, cost, cold, error, seg_from, episode_id`

func scanPing(sc scanner) (Ping, error) {
	var p Ping
	var sent, seg int64
	var cold int
	var usage string
	if err := sc.Scan(&p.ID, &p.PlanID, &p.Key, &sent, &p.State, &p.Calls, &usage, &p.Cost, &cold, &p.Error, &seg,
		&p.EpisodeID); err != nil {
		return p, err
	}
	p.SentAt, p.SegFrom, p.Cold, p.Experiment = fromMS(sent), fromMS(seg), cold != 0, p.EpisodeID != 0
	if err := json.Unmarshal([]byte(usage), &p.Usage); err != nil {
		return p, fmt.Errorf("ping %s usage: %w", p.ID, err)
	}
	return p, nil
}

// InsertPing records a new ping and, when seg is not nil, opens its
// segment in the same transaction.
func (s *Store) InsertPing(ctx context.Context, p Ping, seg *Segment) error {
	u, _ := json.Marshal(p.Usage)
	return s.tx(ctx, func(tx *sql.Tx) error {
		if seg != nil {
			if err := insertSegment(ctx, tx, *seg); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO pings (`+pingCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			p.ID, p.PlanID, p.Key, ms(p.SentAt), p.State, p.Calls, string(u), p.Cost, b2i(p.Cold), p.Error, ms(p.SegFrom),
			p.EpisodeID)
		return err
	})
}

// UpdatePing overwrites ping p.ID. When its cost changed since it was last
// stored, the difference is applied to its plan and, if its segment was
// already booked, to that ledger entry, in the same transaction: a late
// reply corrects the ledger instead of being lost. It returns the ledger
// entries it corrected.
func (s *Store) UpdatePing(ctx context.Context, p Ping) ([]model.LedgerEntry, error) {
	u, _ := json.Marshal(p.Usage)
	var fixed []model.LedgerEntry
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var old float64
		if err := tx.QueryRowContext(ctx, `SELECT cost FROM pings WHERE id=?`, p.ID).Scan(&old); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE pings SET plan_id=?, key=?, sent_at=?, state=?, calls=?, usage=?,
			cost=?, cold=?, error=?, seg_from=?, episode_id=? WHERE id=?`,
			p.PlanID, p.Key, ms(p.SentAt), p.State, p.Calls, string(u), p.Cost, b2i(p.Cold), p.Error, ms(p.SegFrom),
			p.EpisodeID, p.ID); err != nil {
			return err
		}
		delta := p.Cost - old
		if delta == 0 || p.PlanID == 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE plans SET ping_cost = ping_cost + ? WHERE id=?`, delta, p.PlanID); err != nil {
			return err
		}
		if p.SegFrom.IsZero() {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ledger SET ping_cost = ping_cost + ?, net = net - ?
			WHERE plan_id=? AND idle_from=?`, delta, delta, p.PlanID, ms(p.SegFrom)); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+ledgerCols+` FROM ledger WHERE plan_id=? AND idle_from=?`,
			p.PlanID, ms(p.SegFrom))
		if err != nil {
			return err
		}
		fixed, err = scanLedgerRows(rows)
		return err
	})
	return fixed, err
}

func (s *Store) queryPings(ctx context.Context, q string, args ...any) ([]Ping, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ping
	for rows.Next() {
		p, err := scanPing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPing returns one ping.
func (s *Store) GetPing(ctx context.Context, id string) (Ping, error) {
	ps, err := s.queryPings(ctx, `SELECT `+pingCols+` FROM pings WHERE id=?`, id)
	if err != nil {
		return Ping{}, err
	}
	if len(ps) == 0 {
		return Ping{}, ErrNotFound
	}
	return ps[0], nil
}

// PingsByKey returns the newest pings of a session, newest first.
// limit <= 0 means no limit.
func (s *Store) PingsByKey(ctx context.Context, key string, limit int) ([]Ping, error) {
	if limit <= 0 {
		limit = -1
	}
	return s.queryPings(ctx, `SELECT `+pingCols+` FROM pings WHERE key=? ORDER BY sent_at DESC, rowid DESC LIMIT ?`, key, limit)
}

// UnsettledPings returns the pings whose outcome may still change: not yet
// seen or verified (sending, delivered, seen), plus verified ones sent at or
// after recent, whose reply may still be taking more calls. Oldest first.
func (s *Store) UnsettledPings(ctx context.Context, recent time.Time) ([]Ping, error) {
	return s.queryPings(ctx, `SELECT `+pingCols+` FROM pings
		WHERE state IN (?, ?, ?) OR (state = ? AND sent_at >= ?) ORDER BY sent_at, rowid`,
		model.PingSending, model.PingDelivered, model.PingSeen, model.PingVerified, ms(recent))
}

// ---- ledger ----

const ledgerCols = `id, plan_id, key, harness, title, project, model, idle_from, ended_at, ended_by, outcome,
	pings, ping_cost, avoided, net, context_size`

func scanLedgerRows(rows *sql.Rows) ([]model.LedgerEntry, error) {
	defer rows.Close()
	var out []model.LedgerEntry
	for rows.Next() {
		var e model.LedgerEntry
		var idle, ended int64
		if err := rows.Scan(&e.ID, &e.PlanID, &e.Key, &e.Harness, &e.Title, &e.Project, &e.Model, &idle, &ended,
			&e.EndedBy, &e.Outcome, &e.Pings, &e.PingCost, &e.Avoided, &e.Net, &e.ContextSize); err != nil {
			return nil, err
		}
		e.IdleFrom, e.EndedAt = fromMS(idle), fromMS(ended)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Ledger returns the entries with from <= EndedAt < to, oldest first. A
// zero from or to leaves that side open.
func (s *Store) Ledger(ctx context.Context, from, to time.Time) ([]model.LedgerEntry, error) {
	hi := int64(1<<63 - 1)
	if !to.IsZero() {
		hi = to.UnixMilli()
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+ledgerCols+` FROM ledger WHERE ended_at >= ? AND ended_at < ?
		ORDER BY ended_at, id`, ms(from), hi)
	if err != nil {
		return nil, err
	}
	return scanLedgerRows(rows)
}

// SumTotals aggregates ledger entries: Saved is the net of saved entries,
// Wasted the (positive) loss of wasted ones.
func SumTotals(es []model.LedgerEntry) model.Totals {
	var t model.Totals
	for _, e := range es {
		add(&t, e.Outcome, 1, e.Net, e.PingCost, e.Pings)
	}
	return t
}

func add(t *model.Totals, oc model.Outcome, n int, net, cost float64, pings int) {
	t.Net += net
	t.PingCost += cost
	t.Pings += pings
	if oc == model.OutcomeSaved {
		t.Saved += net
		t.Returns += n
	} else {
		t.Wasted -= net
		t.Expired += n
	}
}

// TotalsByKey aggregates the whole ledger per session key.
func (s *Store) TotalsByKey(ctx context.Context) (map[string]model.Totals, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, outcome, COUNT(*), SUM(net), SUM(ping_cost), SUM(pings)
		FROM ledger GROUP BY key, outcome`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.Totals{}
	for rows.Next() {
		var key string
		var oc model.Outcome
		var n, pings int
		var net, cost float64
		if err := rows.Scan(&key, &oc, &n, &net, &cost, &pings); err != nil {
			return nil, err
		}
		t := out[key]
		add(&t, oc, n, net, cost, pings)
		out[key] = t
	}
	return out, rows.Err()
}

// ---- settings and kv ----

// LoadSettings returns the stored settings, or model.DefaultSettings() if
// none were saved. Fields missing from the stored JSON keep their defaults.
func (s *Store) LoadSettings(ctx context.Context) (model.Settings, error) {
	st := model.DefaultSettings()
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT json FROM settings WHERE id=1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return model.DefaultSettings(), fmt.Errorf("store: settings: %w", err)
	}
	return st, nil
}

// SaveSettings persists settings.
func (s *Store) SaveSettings(ctx context.Context, st model.Settings) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings(id, json) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET json=excluded.json`, string(b))
	return err
}

// Get returns the value stored under k.
func (s *Store) Get(ctx context.Context, k string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT v FROM kv WHERE k=?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// Set stores v under k.
func (s *Store) Set(ctx context.Context, k, v string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO kv(k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

// Prefixed returns every key/value whose key starts with prefix.
func (s *Store) Prefixed(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT k, v FROM kv WHERE substr(k, 1, ?) = ?`, len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// ---- usage statistics ----

// Day returns the UTC day containing t as YYYY-MM-DD.
func Day(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// RecordSighting notes that session key of harness h was running on day
// (see Day). Repeated sightings on one day are kept once.
func (s *Store) RecordSighting(ctx context.Context, day string, h model.Harness, key string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sightings(day, harness, key) VALUES (?,?,?)
		ON CONFLICT(day, key) DO NOTHING`, day, h, key)
	return err
}

// DayStats returns the activity of the UTC day that contains day: distinct
// sessions per harness, plans started, pings sent (by result) and ledger
// entries closed, grouped by harness, model, outcome and reason.
//
// Pings.Sent counts every ping attempted that day, failed ones included;
// Verified counts those whose reply was read (cold ones too), Cold those
// whose reply re-wrote the cache, Failed those that could not be delivered.
func (s *Store) DayStats(ctx context.Context, day time.Time) (model.DayStats, error) {
	d := day.UTC()
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	lo, hi := start.UnixMilli(), start.AddDate(0, 0, 1).UnixMilli()
	out := model.DayStats{Day: start.Format(time.DateOnly), Sessions: map[model.Harness]int{}, Outcomes: []model.OutcomeSum{},
		Experiments: []model.ExperimentSum{}}

	rows, err := s.db.QueryContext(ctx, `SELECT harness, COUNT(*) FROM sightings WHERE day=? GROUP BY harness`, out.Day)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var h model.Harness
		var n int
		if err := rows.Scan(&h, &n); err != nil {
			rows.Close()
			return out, err
		}
		out.Sessions[h] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(indefinite), 0), COALESCE(SUM(requested_hours), 0)
		FROM plans WHERE started_at >= ? AND started_at < ?`, lo, hi).
		Scan(&out.Plans.Started, &out.Plans.Indefinite, &out.Plans.Hours); err != nil {
		return out, err
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(state = ?), 0), COALESCE(SUM(cold), 0), COALESCE(SUM(state = ?), 0), COALESCE(SUM(cost), 0)
		FROM pings WHERE sent_at >= ? AND sent_at < ?`, model.PingVerified, model.PingFailed, lo, hi).
		Scan(&out.Pings.Sent, &out.Pings.Verified, &out.Pings.Cold, &out.Pings.Failed, &out.Pings.CostUSD); err != nil {
		return out, err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT harness, model, outcome, ended_by, COUNT(*), SUM(pings), SUM(ping_cost),
		SUM(avoided), SUM(net), SUM(context_size), SUM((ended_at - idle_from) / 60000)
		FROM ledger WHERE ended_at >= ? AND ended_at < ?
		GROUP BY harness, model, outcome, ended_by ORDER BY harness, model, outcome, ended_by`, lo, hi)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var o model.OutcomeSum
		if err := rows.Scan(&o.Harness, &o.Model, &o.Outcome, &o.EndedBy, &o.Count, &o.Pings, &o.PingCostUSD,
			&o.AvoidedUSD, &o.NetUSD, &o.ContextTokens, &o.IdleMinutes); err != nil {
			return out, err
		}
		out.Outcomes = append(out.Outcomes, o)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.Experiments, err = s.experimentDay(ctx, lo, hi)
	return out, err
}
