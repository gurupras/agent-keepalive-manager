package hub

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx"
	_ "modernc.org/sqlite"             // registers "sqlite"
)

// Store keeps reports in SQLite or Postgres. Every statement is written once,
// with ? placeholders, in SQL that both accept.
type Store struct {
	db       *sql.DB
	postgres bool
	// SQLite has one writer; serialising writes here avoids lock-upgrade
	// failures between concurrent transactions.
	writeMu sync.Mutex
}

// OpenStore opens Postgres when databaseURL is set, otherwise SQLite at
// <dataDir>/hub.db, and brings the schema up to date.
func OpenStore(ctx context.Context, dataDir, databaseURL string) (*Store, error) {
	s := &Store{}
	var err error
	if databaseURL != "" {
		if !strings.HasPrefix(databaseURL, "postgres://") && !strings.HasPrefix(databaseURL, "postgresql://") {
			return nil, errors.New("database url must start with postgres:// or postgresql://")
		}
		s.postgres = true
		s.db, err = sql.Open("pgx", databaseURL)
		if err != nil {
			return nil, fmt.Errorf("open postgres: %w", err)
		}
		s.db.SetMaxOpenConns(10)
	} else {
		if err := os.MkdirAll(dataDir, 0o700); err != nil {
			return nil, fmt.Errorf("data dir: %w", err)
		}
		dsn := "file:" + filepath.Join(dataDir, "hub.db") +
			"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
		s.db, err = sql.Open("sqlite", dsn)
		if err != nil {
			return nil, fmt.Errorf("open sqlite: %w", err)
		}
		s.db.SetMaxOpenConns(4)
	}
	if err := s.db.PingContext(ctx); err != nil {
		s.db.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}
	if err := s.migrate(ctx); err != nil {
		s.db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// migrations are applied in order, each once. Types are chosen to mean the
// same in both databases: TEXT, BIGINT (SQLite INTEGER affinity) and
// DOUBLE PRECISION (SQLite REAL affinity). Days are 'YYYY-MM-DD' text, which
// sorts as dates.
var migrations = []string{
	`CREATE TABLE reports (
		install_id       TEXT NOT NULL,
		day              TEXT NOT NULL,
		schema_version   BIGINT NOT NULL,
		version          TEXT NOT NULL,
		os               TEXT NOT NULL,
		arch             TEXT NOT NULL,
		profile          TEXT NOT NULL,
		install_kind     TEXT NOT NULL,
		plans_started    BIGINT NOT NULL,
		plans_indefinite BIGINT NOT NULL,
		plans_hours      DOUBLE PRECISION NOT NULL,
		pings_sent       BIGINT NOT NULL,
		pings_verified   BIGINT NOT NULL,
		pings_cold       BIGINT NOT NULL,
		pings_failed     BIGINT NOT NULL,
		pings_cost       DOUBLE PRECISION NOT NULL,
		received_at      BIGINT NOT NULL,
		PRIMARY KEY (install_id, day)
	);
	CREATE INDEX reports_day ON reports (day);
	CREATE TABLE report_sessions (
		install_id TEXT NOT NULL,
		day        TEXT NOT NULL,
		harness    TEXT NOT NULL,
		sessions   BIGINT NOT NULL,
		PRIMARY KEY (install_id, day, harness)
	);
	CREATE INDEX report_sessions_day ON report_sessions (day);
	CREATE TABLE outcomes (
		install_id     TEXT NOT NULL,
		day            TEXT NOT NULL,
		idx            BIGINT NOT NULL,
		harness        TEXT NOT NULL,
		model          TEXT NOT NULL,
		outcome        TEXT NOT NULL,
		ended_by       TEXT NOT NULL,
		entries        BIGINT NOT NULL,
		pings          BIGINT NOT NULL,
		ping_cost      DOUBLE PRECISION NOT NULL,
		avoided        DOUBLE PRECISION NOT NULL,
		net            DOUBLE PRECISION NOT NULL,
		context_tokens BIGINT NOT NULL,
		idle_minutes   BIGINT NOT NULL,
		PRIMARY KEY (install_id, day, idx)
	);
	CREATE INDEX outcomes_day ON outcomes (day);`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version BIGINT PRIMARY KEY)`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	var current int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	for v := current + 1; v <= len(migrations); v++ {
		err := s.inTx(ctx, func(tx *sql.Tx) error {
			for _, stmt := range strings.Split(migrations[v-1], ";") {
				if strings.TrimSpace(stmt) == "" {
					continue
				}
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return err
				}
			}
			_, err := tx.ExecContext(ctx, s.q(`INSERT INTO schema_migrations (version) VALUES (?)`), v)
			return err
		})
		if err != nil {
			return fmt.Errorf("migrate to %d: %w", v, err)
		}
	}
	return nil
}

// q rewrites ? placeholders as $1, $2, … for Postgres. No statement here
// has a ? inside a string literal.
func (s *Store) q(query string) string {
	if !s.postgres {
		return query
	}
	var b strings.Builder
	n := 0
	for _, c := range query {
		if c == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Upsert stores a validated report. A report for an (installId, day) already
// stored replaces it; created is false then.
func (s *Store) Upsert(ctx context.Context, r *Report, received time.Time) (created bool, err error) {
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var one int
		switch err := tx.QueryRowContext(ctx, s.q(`SELECT 1 FROM reports WHERE install_id = ? AND day = ?`),
			r.InstallID, r.Day).Scan(&one); {
		case errors.Is(err, sql.ErrNoRows):
			created = true
		case err != nil:
			return err
		}
		_, err := tx.ExecContext(ctx, s.q(`INSERT INTO reports (
			install_id, day, schema_version, version, os, arch, profile, install_kind,
			plans_started, plans_indefinite, plans_hours,
			pings_sent, pings_verified, pings_cold, pings_failed, pings_cost, received_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (install_id, day) DO UPDATE SET
			schema_version = excluded.schema_version, version = excluded.version,
			os = excluded.os, arch = excluded.arch, profile = excluded.profile,
			install_kind = excluded.install_kind, plans_started = excluded.plans_started,
			plans_indefinite = excluded.plans_indefinite, plans_hours = excluded.plans_hours,
			pings_sent = excluded.pings_sent, pings_verified = excluded.pings_verified,
			pings_cold = excluded.pings_cold, pings_failed = excluded.pings_failed,
			pings_cost = excluded.pings_cost, received_at = excluded.received_at`),
			r.InstallID, r.Day, r.Schema, r.Version, r.OS, r.Arch, r.Profile, r.InstallKind,
			r.Plans.Started, r.Plans.Indefinite, r.Plans.Hours,
			r.Pings.Sent, r.Pings.Verified, r.Pings.Cold, r.Pings.Failed, r.Pings.CostUSD,
			received.Unix())
		if err != nil {
			return err
		}
		for _, table := range []string{"report_sessions", "outcomes"} {
			if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM `+table+` WHERE install_id = ? AND day = ?`),
				r.InstallID, r.Day); err != nil {
				return err
			}
		}
		for h, n := range r.Sessions {
			if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO report_sessions (install_id, day, harness, sessions)
				VALUES (?, ?, ?, ?)`), r.InstallID, r.Day, string(h), n); err != nil {
				return err
			}
		}
		for i, o := range r.Outcomes {
			if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO outcomes (
				install_id, day, idx, harness, model, outcome, ended_by, entries, pings,
				ping_cost, avoided, net, context_tokens, idle_minutes)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
				r.InstallID, r.Day, i, string(o.Harness), o.Model, string(o.Outcome), o.EndedBy,
				o.Count, o.Pings, o.PingCostUSD, o.AvoidedUSD, o.NetUSD, o.ContextTokens, o.IdleMinutes); err != nil {
				return err
			}
		}
		return nil
	})
	return created, err
}

// Purge deletes every report for a day before cutoff (YYYY-MM-DD) and
// returns how many reports went.
func (s *Store) Purge(ctx context.Context, cutoff string) (int64, error) {
	var n int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"outcomes", "report_sessions"} {
			if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM `+table+` WHERE day < ?`), cutoff); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, s.q(`DELETE FROM reports WHERE day < ?`), cutoff)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}

// Summary is the totals over a range of days.
type Summary struct {
	Installs       int64   `json:"installs"`
	ActiveInstalls int64   `json:"activeInstalls"`
	Saved          float64 `json:"saved"`
	Wasted         float64 `json:"wasted"`
	Net            float64 `json:"net"`
	PingCost       float64 `json:"pingCost"`
	Pings          int64   `json:"pings"`
	Cold           int64   `json:"cold"`
	Returns        int64   `json:"returns"`
}

// Range is an inclusive range of UTC days, as YYYY-MM-DD.
type Range struct{ From, To string }

// activeWindowDays is how recent a report must be for its install to count
// as active: the last 7 days of the range.
const activeWindowDays = 7

// Money columns, shared by every query: saved is the net of saved outcomes,
// wasted the ping cost of wasted ones, net the sum of both signs.
const (
	sqlSaved   = `COALESCE(SUM(CASE WHEN o.outcome = 'saved' THEN o.net ELSE 0 END), 0)`
	sqlWasted  = `COALESCE(SUM(CASE WHEN o.outcome = 'wasted' THEN o.ping_cost ELSE 0 END), 0)`
	sqlNet     = `COALESCE(SUM(o.net), 0)`
	sqlReturns = `CAST(COALESCE(SUM(CASE WHEN o.outcome = 'saved' THEN o.entries ELSE 0 END), 0) AS BIGINT)`
)

// Summary returns the totals over rg.
func (s *Store) Summary(ctx context.Context, rg Range) (Summary, error) {
	var sum Summary
	to, _ := time.Parse(time.DateOnly, rg.To)
	activeFrom := to.AddDate(0, 0, -(activeWindowDays - 1)).Format(time.DateOnly)
	if activeFrom < rg.From {
		activeFrom = rg.From
	}
	err := s.db.QueryRowContext(ctx, s.q(`SELECT
		COUNT(DISTINCT install_id),
		COUNT(DISTINCT CASE WHEN day >= ? THEN install_id END),
		COALESCE(SUM(pings_cost), 0),
		CAST(COALESCE(SUM(pings_sent), 0) AS BIGINT),
		CAST(COALESCE(SUM(pings_cold), 0) AS BIGINT)
		FROM reports WHERE day >= ? AND day <= ?`), activeFrom, rg.From, rg.To).
		Scan(&sum.Installs, &sum.ActiveInstalls, &sum.PingCost, &sum.Pings, &sum.Cold)
	if err != nil {
		return sum, err
	}
	err = s.db.QueryRowContext(ctx, s.q(`SELECT `+sqlSaved+`, `+sqlWasted+`, `+sqlNet+`, `+sqlReturns+`
		FROM outcomes o WHERE o.day >= ? AND o.day <= ?`), rg.From, rg.To).
		Scan(&sum.Saved, &sum.Wasted, &sum.Net, &sum.Returns)
	return sum, err
}

// Point is one day of a series.
type Point struct {
	Day   string  `json:"day"`
	Value float64 `json:"value"`
}

// SeriesMetrics are the metrics Series accepts.
var SeriesMetrics = []string{"net", "saved", "wasted", "installs", "pings"}

// Series returns one value per day of rg, zero on days without reports.
func (s *Store) Series(ctx context.Context, rg Range, metric string) ([]Point, error) {
	var query string
	switch metric {
	case "net":
		query = `SELECT o.day, ` + sqlNet + ` FROM outcomes o WHERE o.day >= ? AND o.day <= ? GROUP BY o.day`
	case "saved":
		query = `SELECT o.day, ` + sqlSaved + ` FROM outcomes o WHERE o.day >= ? AND o.day <= ? GROUP BY o.day`
	case "wasted":
		query = `SELECT o.day, ` + sqlWasted + ` FROM outcomes o WHERE o.day >= ? AND o.day <= ? GROUP BY o.day`
	case "installs":
		query = `SELECT day, CAST(COUNT(DISTINCT install_id) AS DOUBLE PRECISION) FROM reports
			WHERE day >= ? AND day <= ? GROUP BY day`
	case "pings":
		query = `SELECT day, CAST(COALESCE(SUM(pings_sent), 0) AS DOUBLE PRECISION) FROM reports
			WHERE day >= ? AND day <= ? GROUP BY day`
	default:
		return nil, fmt.Errorf("unknown metric %q", metric)
	}
	rows, err := s.db.QueryContext(ctx, s.q(query), rg.From, rg.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[string]float64{}
	for rows.Next() {
		var day string
		var v float64
		if err := rows.Scan(&day, &v); err != nil {
			return nil, err
		}
		byDay[day] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	from, _ := time.Parse(time.DateOnly, rg.From)
	to, _ := time.Parse(time.DateOnly, rg.To)
	points := []Point{}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		day := d.Format(time.DateOnly)
		points = append(points, Point{Day: day, Value: byDay[day]})
	}
	return points, nil
}

// BreakdownRow is the totals for one value of a dimension.
type BreakdownRow struct {
	Key      string  `json:"key"`
	Installs int64   `json:"installs"`
	Saved    float64 `json:"saved"`
	Wasted   float64 `json:"wasted"`
	Net      float64 `json:"net"`
	Pings    int64   `json:"pings"`
}

// BreakdownDimensions maps each accepted `by` to its column. model and
// harness are per outcome; the others are per report.
var BreakdownDimensions = map[string]string{
	"model":       "model",
	"harness":     "harness",
	"version":     "version",
	"os":          "os",
	"installKind": "install_kind",
	"profile":     "profile",
}

// maxBreakdownRows bounds a breakdown; rows are sorted by net, largest first.
const maxBreakdownRows = 200

// Breakdown returns the totals over rg per value of dimension by.
func (s *Store) Breakdown(ctx context.Context, rg Range, by string) ([]BreakdownRow, error) {
	col, ok := BreakdownDimensions[by]
	if !ok {
		return nil, fmt.Errorf("unknown dimension %q", by)
	}
	var query string
	if by == "model" || by == "harness" {
		// Pings here are those attributed to the closed outcomes.
		query = `SELECT o.` + col + `, COUNT(DISTINCT o.install_id), ` + sqlSaved + `, ` + sqlWasted + `, ` + sqlNet + `,
			CAST(COALESCE(SUM(o.pings), 0) AS BIGINT)
			FROM outcomes o WHERE o.day >= ? AND o.day <= ? GROUP BY o.` + col
	} else {
		// Pings come from the reports; money from their outcomes.
		query = `SELECT r.` + col + `, COUNT(DISTINCT r.install_id),
			COALESCE(SUM(m.saved), 0), COALESCE(SUM(m.wasted), 0), COALESCE(SUM(m.net), 0),
			CAST(COALESCE(SUM(r.pings_sent), 0) AS BIGINT)
			FROM reports r LEFT JOIN (
				SELECT o.install_id, o.day, ` + sqlSaved + ` AS saved, ` + sqlWasted + ` AS wasted, ` + sqlNet + ` AS net
				FROM outcomes o WHERE o.day >= ? AND o.day <= ? GROUP BY o.install_id, o.day
			) m ON m.install_id = r.install_id AND m.day = r.day
			WHERE r.day >= ? AND r.day <= ? GROUP BY r.` + col
	}
	args := []any{rg.From, rg.To}
	if by != "model" && by != "harness" {
		args = append(args, rg.From, rg.To)
	}
	rows, err := s.db.QueryContext(ctx, s.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BreakdownRow{}
	for rows.Next() {
		var b BreakdownRow
		if err := rows.Scan(&b.Key, &b.Installs, &b.Saved, &b.Wasted, &b.Net, &b.Pings); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Net != out[j].Net {
			return out[i].Net > out[j].Net
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > maxBreakdownRows {
		out = out[:maxBreakdownRows]
	}
	return out, nil
}
