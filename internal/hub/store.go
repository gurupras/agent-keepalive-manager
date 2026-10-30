package hub

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx"
	_ "modernc.org/sqlite"             // registers "sqlite"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
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
	// hub_kv holds the per-day network salts; new_installs the installs
	// each network added on a day, as a salted hash (Admission).
	`CREATE TABLE hub_kv (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	CREATE TABLE new_installs (
		day        TEXT NOT NULL,
		network    TEXT NOT NULL,
		install_id TEXT NOT NULL,
		PRIMARY KEY (day, network, install_id)
	);`,
	// experiment_sums holds schema 2's experiments, one row per entry.
	`CREATE TABLE experiment_sums (
		install_id     TEXT NOT NULL,
		day            TEXT NOT NULL,
		experiment     TEXT NOT NULL,
		arm            TEXT NOT NULL,
		harness        TEXT NOT NULL,
		model          TEXT NOT NULL,
		episodes       BIGINT NOT NULL,
		returned       BIGINT NOT NULL,
		warm           BIGINT NOT NULL,
		pings          BIGINT NOT NULL,
		ping_calls     BIGINT NOT NULL,
		ping_cost      DOUBLE PRECISION NOT NULL,
		rewrite        DOUBLE PRECISION NOT NULL,
		cost           DOUBLE PRECISION NOT NULL,
		cost_sq        DOUBLE PRECISION NOT NULL,
		context_tokens BIGINT NOT NULL,
		quiet_minutes  BIGINT NOT NULL,
		PRIMARY KEY (install_id, day, experiment, arm, harness, model)
	);
	CREATE INDEX experiment_sums_day ON experiment_sums (day);`,
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

// Admission limits how many installs never seen before one network may
// add in a UTC day, so one machine cannot flood the totals with fresh
// install ids. The network is kept only as an HMAC under a random salt
// that is replaced every day, so stored rows cannot be tied to an address.
type Admission struct {
	// Network is the sender's network (NetworkOf); empty skips the limit.
	Network string
	// Limit is the most new installs per network per day; 0 is no limit.
	Limit int
}

// ErrNetworkFull is Upsert refusing a new install under Admission.
var ErrNetworkFull = errors.New("too many new installs from this network today")

const saltKeyPrefix = "network-salt:"

// NetworkOf is the /24 of an IPv4 address or the /64 of an IPv6 one, or
// the input itself when it is not an address.
func NetworkOf(addr string) string {
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return addr
	}
	ip = ip.Unmap()
	bits := 64
	if ip.Is4() {
		bits = 24
	}
	p, _ := ip.Prefix(bits)
	return p.String()
}

// admit records r's install as new from a.Network today, or returns
// ErrNetworkFull when the network has added a.Limit installs already. An
// install with a stored report is not new.
func (s *Store) admit(ctx context.Context, tx *sql.Tx, r *Report, a Admission, received time.Time) error {
	if a.Limit <= 0 || a.Network == "" {
		return nil
	}
	var one int
	switch err := tx.QueryRowContext(ctx, s.q(`SELECT 1 FROM reports WHERE install_id = ? LIMIT 1`),
		r.InstallID).Scan(&one); {
	case err == nil:
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	today := received.UTC().Format(time.DateOnly)
	salt, err := s.daySalt(ctx, tx, today)
	if err != nil {
		return err
	}
	m := hmac.New(sha256.New, []byte(salt))
	m.Write([]byte(a.Network))
	network := hex.EncodeToString(m.Sum(nil))
	var n int
	if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM new_installs WHERE day = ? AND network = ?`),
		today, network).Scan(&n); err != nil {
		return err
	}
	if n >= a.Limit {
		return ErrNetworkFull
	}
	_, err = tx.ExecContext(ctx, s.q(`INSERT INTO new_installs (day, network, install_id) VALUES (?, ?, ?)
		ON CONFLICT (day, network, install_id) DO NOTHING`), today, network, r.InstallID)
	return err
}

// daySalt returns the salt for day, creating it on the day's first use and
// then deleting every earlier salt and new_installs row.
func (s *Store) daySalt(ctx context.Context, tx *sql.Tx, day string) (string, error) {
	key := saltKeyPrefix + day
	var salt string
	err := tx.QueryRowContext(ctx, s.q(`SELECT value FROM hub_kv WHERE key = ?`), key).Scan(&salt)
	if err == nil {
		return salt, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	b := make([]byte, 32)
	rand.Read(b)
	// Another hub on the same database may have won the race: read back.
	if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO hub_kv (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO NOTHING`), key, hex.EncodeToString(b)); err != nil {
		return "", err
	}
	if err := tx.QueryRowContext(ctx, s.q(`SELECT value FROM hub_kv WHERE key = ?`), key).Scan(&salt); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM hub_kv WHERE key LIKE ? AND key < ?`),
		saltKeyPrefix+"%", key); err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, s.q(`DELETE FROM new_installs WHERE day < ?`), day)
	return salt, err
}

// Upsert stores a validated report. A report for an (installId, day) already
// stored replaces it; created is false then. A new install is subject to a,
// and refused with ErrNetworkFull.
func (s *Store) Upsert(ctx context.Context, r *Report, received time.Time, a Admission) (created bool, err error) {
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var one int
		switch err := tx.QueryRowContext(ctx, s.q(`SELECT 1 FROM reports WHERE install_id = ? AND day = ?`),
			r.InstallID, r.Day).Scan(&one); {
		case errors.Is(err, sql.ErrNoRows):
			created = true
		case err != nil:
			return err
		}
		if created {
			if err := s.admit(ctx, tx, r, a, received); err != nil {
				return err
			}
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
		for _, table := range []string{"report_sessions", "outcomes", "experiment_sums"} {
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
		for _, e := range r.Experiments {
			if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO experiment_sums (
				install_id, day, experiment, arm, harness, model, episodes, returned, warm, pings,
				ping_calls, ping_cost, rewrite, cost, cost_sq, context_tokens, quiet_minutes)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
				r.InstallID, r.Day, e.Experiment, e.Arm, string(e.Harness), e.Model, e.Episodes, e.Returned, e.Warm,
				e.Pings, e.PingCalls, e.PingCostUSD, e.RewriteUSD, e.CostUSD, e.CostSqUSD,
				e.ContextTokens, e.QuietMinutes); err != nil {
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
		for _, table := range []string{"outcomes", "report_sessions", "experiment_sums"} {
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
	// Pending is the installs in the range left out for not having
	// reported on MinDays days yet.
	Pending int64 `json:"pending"`
	// MedianNet is the median of each install's net over the range.
	MedianNet float64 `json:"medianNet"`
	// TrimmedNet is Net without the 1% of installs (rounded down) furthest
	// from zero; Trimmed is how many were left out.
	TrimmedNet float64 `json:"trimmedNet"`
	Trimmed    int64   `json:"trimmed"`
	// TopShare is the largest install's share of the summed |net|, 0–1.
	TopShare float64 `json:"topShare"`
	// EstablishedDays is the hub's EstablishedDays setting, for the
	// dashboard to describe the filter.
	EstablishedDays int `json:"establishedDays"`
}

// Range is an inclusive range of UTC days, as YYYY-MM-DD. With MinDays
// above 1, only installs that have reported on at least MinDays days (in
// any range) count.
type Range struct {
	From, To string
	MinDays  int
}

// established is a filter on col keeping rg's established installs, to
// append to a WHERE clause, with its argument.
func (rg Range) established(col string) (string, []any) {
	if rg.MinDays <= 1 {
		return "", nil
	}
	return ` AND ` + col + ` IN (SELECT install_id FROM reports GROUP BY install_id HAVING COUNT(*) >= ?)`,
		[]any{rg.MinDays}
}

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
	rf, rargs := rg.established("install_id")
	err := s.db.QueryRowContext(ctx, s.q(`SELECT
		COUNT(DISTINCT install_id),
		COUNT(DISTINCT CASE WHEN day >= ? THEN install_id END),
		COALESCE(SUM(pings_cost), 0),
		CAST(COALESCE(SUM(pings_sent), 0) AS BIGINT),
		CAST(COALESCE(SUM(pings_cold), 0) AS BIGINT)
		FROM reports WHERE day >= ? AND day <= ?`+rf), append([]any{activeFrom, rg.From, rg.To}, rargs...)...).
		Scan(&sum.Installs, &sum.ActiveInstalls, &sum.PingCost, &sum.Pings, &sum.Cold)
	if err != nil {
		return sum, err
	}
	if rf != "" {
		var all int64
		if err := s.db.QueryRowContext(ctx, s.q(`SELECT COUNT(DISTINCT install_id) FROM reports
			WHERE day >= ? AND day <= ?`), rg.From, rg.To).Scan(&all); err != nil {
			return sum, err
		}
		sum.Pending = all - sum.Installs
	}
	of, oargs := rg.established("o.install_id")
	err = s.db.QueryRowContext(ctx, s.q(`SELECT `+sqlSaved+`, `+sqlWasted+`, `+sqlNet+`, `+sqlReturns+`
		FROM outcomes o WHERE o.day >= ? AND o.day <= ?`+of), append([]any{rg.From, rg.To}, oargs...)...).
		Scan(&sum.Saved, &sum.Wasted, &sum.Net, &sum.Returns)
	if err != nil {
		return sum, err
	}
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT `+sqlNet+` FROM outcomes o
		WHERE o.day >= ? AND o.day <= ?`+of+` GROUP BY o.install_id`), append([]any{rg.From, rg.To}, oargs...)...)
	if err != nil {
		return sum, err
	}
	defer rows.Close()
	var nets []float64
	for rows.Next() {
		var n float64
		if err := rows.Scan(&n); err != nil {
			return sum, err
		}
		nets = append(nets, n)
	}
	if err := rows.Err(); err != nil {
		return sum, err
	}
	// Installs that reported but closed no outcome have a net of zero.
	for int64(len(nets)) < sum.Installs {
		nets = append(nets, 0)
	}
	sum.MedianNet, sum.TrimmedNet, sum.Trimmed, sum.TopShare = robust(nets)
	return sum, nil
}

// robust returns the median of nets, their sum without the 1% furthest from
// zero (and how many that is), and the largest one's share of the sum of
// their magnitudes.
func robust(nets []float64) (median, trimmed float64, cut int64, top float64) {
	n := len(nets)
	if n == 0 {
		return 0, 0, 0, 0
	}
	sorted := append([]float64(nil), nets...)
	sort.Float64s(sorted)
	if n%2 == 1 {
		median = sorted[n/2]
	} else {
		median = (sorted[n/2-1] + sorted[n/2]) / 2
	}
	sort.Slice(sorted, func(i, j int) bool { return math.Abs(sorted[i]) > math.Abs(sorted[j]) })
	cut = int64(n / 100)
	var abs float64
	for i, v := range sorted {
		abs += math.Abs(v)
		if int64(i) >= cut {
			trimmed += v
		}
	}
	if abs > 0 {
		top = math.Abs(sorted[0]) / abs
	}
	return median, trimmed, cut, top
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
	of, args := rg.established("o.install_id")
	rf, _ := rg.established("install_id")
	var query string
	switch metric {
	case "net":
		query = `SELECT o.day, ` + sqlNet + ` FROM outcomes o WHERE o.day >= ? AND o.day <= ?` + of + ` GROUP BY o.day`
	case "saved":
		query = `SELECT o.day, ` + sqlSaved + ` FROM outcomes o WHERE o.day >= ? AND o.day <= ?` + of + ` GROUP BY o.day`
	case "wasted":
		query = `SELECT o.day, ` + sqlWasted + ` FROM outcomes o WHERE o.day >= ? AND o.day <= ?` + of + ` GROUP BY o.day`
	case "installs":
		query = `SELECT day, CAST(COUNT(DISTINCT install_id) AS DOUBLE PRECISION) FROM reports
			WHERE day >= ? AND day <= ?` + rf + ` GROUP BY day`
	case "pings":
		query = `SELECT day, CAST(COALESCE(SUM(pings_sent), 0) AS DOUBLE PRECISION) FROM reports
			WHERE day >= ? AND day <= ?` + rf + ` GROUP BY day`
	default:
		return nil, fmt.Errorf("unknown metric %q", metric)
	}
	rows, err := s.db.QueryContext(ctx, s.q(query), append([]any{rg.From, rg.To}, args...)...)
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
	of, fargs := rg.established("o.install_id")
	rf, _ := rg.established("r.install_id")
	var query string
	if by == "model" || by == "harness" {
		// Pings here are those attributed to the closed outcomes.
		query = `SELECT o.` + col + `, COUNT(DISTINCT o.install_id), ` + sqlSaved + `, ` + sqlWasted + `, ` + sqlNet + `,
			CAST(COALESCE(SUM(o.pings), 0) AS BIGINT)
			FROM outcomes o WHERE o.day >= ? AND o.day <= ?` + of + ` GROUP BY o.` + col
	} else {
		// Pings come from the reports; money from their outcomes.
		query = `SELECT r.` + col + `, COUNT(DISTINCT r.install_id),
			COALESCE(SUM(m.saved), 0), COALESCE(SUM(m.wasted), 0), COALESCE(SUM(m.net), 0),
			CAST(COALESCE(SUM(r.pings_sent), 0) AS BIGINT)
			FROM reports r LEFT JOIN (
				SELECT o.install_id, o.day, ` + sqlSaved + ` AS saved, ` + sqlWasted + ` AS wasted, ` + sqlNet + ` AS net
				FROM outcomes o WHERE o.day >= ? AND o.day <= ? GROUP BY o.install_id, o.day
			) m ON m.install_id = r.install_id AND m.day = r.day
			WHERE r.day >= ? AND r.day <= ?` + rf + ` GROUP BY r.` + col
	}
	args := []any{rg.From, rg.To}
	if by != "model" && by != "harness" {
		args = append(args, rg.From, rg.To)
	}
	args = append(args, fargs...)
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

// ExperimentArm is one arm of an experiment over a range of days. Costs are
// per episode; lower is better.
type ExperimentArm struct {
	Arm       string `json:"arm"`
	Installs  int64  `json:"installs"`
	Episodes  int64  `json:"episodes"`
	Returned  int64  `json:"returned"`
	Warm      int64  `json:"warm"`
	Pings     int64  `json:"pings"`
	PingCalls int64  `json:"pingCalls"`
	// MeanCostUSD is the mean episode cost and SECostUSD its standard error.
	MeanCostUSD      float64 `json:"meanCostUsd"`
	SECostUSD        float64 `json:"seCostUsd"`
	WarmRate         float64 `json:"warmRate"` // Warm / Returned
	PingsPerEpisode  float64 `json:"pingsPerEpisode"`
	MeanQuietMinutes float64 `json:"meanQuietMinutes"`
}

// ExperimentDiff is the ping arm's mean episode cost minus the hold arm's,
// with its 95% confidence interval. Significant when the interval excludes 0.
type ExperimentDiff struct {
	MeanCostUSD float64 `json:"meanCostUsd"`
	CILowUSD    float64 `json:"ciLowUsd"`
	CIHighUSD   float64 `json:"ciHighUsd"`
	Significant bool    `json:"significant"`
}

// Experiment is one experiment's arms (hold first) and their difference,
// nil while either arm has fewer than 2 episodes.
type Experiment struct {
	Experiment string          `json:"experiment"`
	Arms       []ExperimentArm `json:"arms"`
	Diff       *ExperimentDiff `json:"diff"`
}

// z95 is the normal quantile of a two-sided 95% interval.
const z95 = 1.96

// Experiments returns every experiment with episodes in rg, by id.
func (s *Store) Experiments(ctx context.Context, rg Range) ([]Experiment, error) {
	ef, args := rg.established("e.install_id")
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT e.experiment, e.arm, COUNT(DISTINCT e.install_id),
		CAST(SUM(e.episodes) AS BIGINT), CAST(SUM(e.returned) AS BIGINT), CAST(SUM(e.warm) AS BIGINT),
		CAST(SUM(e.pings) AS BIGINT), CAST(SUM(e.ping_calls) AS BIGINT),
		SUM(e.cost), SUM(e.cost_sq), CAST(SUM(e.quiet_minutes) AS BIGINT)
		FROM experiment_sums e WHERE e.day >= ? AND e.day <= ?`+ef+`
		GROUP BY e.experiment, e.arm ORDER BY e.experiment, e.arm`), append([]any{rg.From, rg.To}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Experiment{}
	for rows.Next() {
		var (
			id           string
			a            ExperimentArm
			cost, costSq float64
			quiet        int64
		)
		if err := rows.Scan(&id, &a.Arm, &a.Installs, &a.Episodes, &a.Returned, &a.Warm, &a.Pings, &a.PingCalls,
			&cost, &costSq, &quiet); err != nil {
			return nil, err
		}
		a.MeanCostUSD, a.SECostUSD = meanSE(a.Episodes, cost, costSq)
		if a.Returned > 0 {
			a.WarmRate = float64(a.Warm) / float64(a.Returned)
		}
		if a.Episodes > 0 {
			a.PingsPerEpisode = float64(a.Pings) / float64(a.Episodes)
			a.MeanQuietMinutes = float64(quiet) / float64(a.Episodes)
		}
		if len(out) == 0 || out[len(out)-1].Experiment != id {
			out = append(out, Experiment{Experiment: id})
		}
		e := &out[len(out)-1]
		e.Arms = append(e.Arms, a) // "hold" sorts before "ping"
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		e := &out[i]
		if len(e.Arms) == 2 && e.Arms[0].Arm == model.ArmHold && e.Arms[1].Arm == model.ArmPing {
			e.Diff = experimentDiff(e.Arms[0], e.Arms[1])
		}
	}
	return out, nil
}

// meanSE is the mean of n episode costs and its standard error, from their
// sum and sum of squares. The sample variance is clamped at 0 against
// rounding; with fewer than 2 episodes the error is 0.
func meanSE(n int64, sum, sumSq float64) (mean, se float64) {
	if n < 1 {
		return 0, 0
	}
	mean = sum / float64(n)
	if n < 2 {
		return mean, 0
	}
	variance := max((sumSq-sum*sum/float64(n))/float64(n-1), 0)
	return mean, math.Sqrt(variance / float64(n))
}

// experimentDiff compares the arms' mean episode costs with Welch's
// interval, ping minus hold, or returns nil when either arm has fewer than
// 2 episodes. Episodes are treated as independent, though those of one
// install are correlated (its sessions, models and habits): the interval is
// narrower than it should be when a few installs supply most episodes.
func experimentDiff(hold, ping ExperimentArm) *ExperimentDiff {
	if hold.Episodes < 2 || ping.Episodes < 2 {
		return nil
	}
	d := ping.MeanCostUSD - hold.MeanCostUSD
	half := z95 * math.Sqrt(hold.SECostUSD*hold.SECostUSD+ping.SECostUSD*ping.SECostUSD)
	return &ExperimentDiff{
		MeanCostUSD: d,
		CILowUSD:    d - half,
		CIHighUSD:   d + half,
		Significant: d-half > 0 || d+half < 0,
	}
}
