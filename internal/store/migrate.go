package store

import (
	"context"
	"fmt"
	"time"
)

// migration upgrades the schema to Version. Versions are applied in order,
// each in its own transaction, and recorded in schema_migrations; a
// released migration is never edited, only followed by a new one.
type migration struct {
	Version int
	SQL     string
}

var migrations = []migration{
	{1, `
CREATE TABLE plans (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	key             TEXT    NOT NULL,
	pid             INTEGER NOT NULL DEFAULT 0,
	proc_start      TEXT    NOT NULL DEFAULT '',
	hours           REAL    NOT NULL,
	requested_hours REAL    NOT NULL DEFAULT 0,
	started_at      INTEGER NOT NULL,
	until           INTEGER NOT NULL,
	state           TEXT    NOT NULL,
	indefinite      INTEGER NOT NULL DEFAULT 0,
	ended_at        INTEGER NOT NULL DEFAULT 0,
	next_ping       INTEGER NOT NULL DEFAULT 0,
	pings           INTEGER NOT NULL DEFAULT 0,
	ping_cost       REAL    NOT NULL DEFAULT 0,
	reason          TEXT    NOT NULL DEFAULT '',
	last_error      TEXT    NOT NULL DEFAULT '',
	cold_streak     INTEGER NOT NULL DEFAULT 0,
	wait_warm       INTEGER NOT NULL DEFAULT 0,
	last_seen       INTEGER NOT NULL DEFAULT 0,
	base_from       INTEGER NOT NULL DEFAULT 0,
	base_context    INTEGER NOT NULL DEFAULT 0,
	base_model      TEXT    NOT NULL DEFAULT '',
	last_ping_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX plans_state ON plans(state);
CREATE INDEX plans_started ON plans(started_at);

CREATE TABLE pings (
	id       TEXT PRIMARY KEY,
	plan_id  INTEGER NOT NULL DEFAULT 0,
	key      TEXT    NOT NULL,
	sent_at  INTEGER NOT NULL,
	state    TEXT    NOT NULL,
	calls    INTEGER NOT NULL DEFAULT 0,
	usage    TEXT    NOT NULL DEFAULT '{}',
	cost     REAL    NOT NULL DEFAULT 0,
	cold     INTEGER NOT NULL DEFAULT 0,
	error    TEXT    NOT NULL DEFAULT '',
	seg_from INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX pings_key ON pings(key, sent_at);
CREATE INDEX pings_seg ON pings(plan_id, seg_from);
CREATE INDEX pings_state ON pings(state);
CREATE INDEX pings_sent ON pings(sent_at);

CREATE TABLE segments (
	plan_id      INTEGER NOT NULL,
	key          TEXT    NOT NULL,
	harness      TEXT    NOT NULL DEFAULT '',
	model        TEXT    NOT NULL DEFAULT '',
	title        TEXT    NOT NULL DEFAULT '',
	project      TEXT    NOT NULL DEFAULT '',
	idle_from    INTEGER NOT NULL,
	context_size INTEGER NOT NULL DEFAULT 0,
	opened_at    INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (plan_id, idle_from)
);

CREATE TABLE ledger (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	plan_id      INTEGER NOT NULL,
	key          TEXT    NOT NULL,
	harness      TEXT    NOT NULL DEFAULT '',
	title        TEXT    NOT NULL DEFAULT '',
	project      TEXT    NOT NULL DEFAULT '',
	model        TEXT    NOT NULL DEFAULT '',
	idle_from    INTEGER NOT NULL,
	ended_at     INTEGER NOT NULL,
	ended_by     TEXT    NOT NULL,
	outcome      TEXT    NOT NULL,
	pings        INTEGER NOT NULL DEFAULT 0,
	ping_cost    REAL    NOT NULL DEFAULT 0,
	avoided      REAL    NOT NULL DEFAULT 0,
	net          REAL    NOT NULL DEFAULT 0,
	context_size INTEGER NOT NULL DEFAULT 0,
	UNIQUE (plan_id, idle_from, ended_by)
);
CREATE INDEX ledger_ended ON ledger(ended_at);
CREATE INDEX ledger_key ON ledger(key);

CREATE TABLE settings (id INTEGER PRIMARY KEY CHECK (id = 1), json TEXT NOT NULL);

CREATE TABLE kv (k TEXT PRIMARY KEY, v TEXT NOT NULL);

CREATE TABLE sightings (
	day     TEXT NOT NULL,
	harness TEXT NOT NULL,
	key     TEXT NOT NULL,
	PRIMARY KEY (day, key)
);
`},
}

// SchemaVersion returns the highest applied migration.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}

func (s *Store) migrate(ctx context.Context) error { return s.migrateTo(ctx, migrations) }

// migrateTo applies ms on one dedicated connection, in one transaction.
// Switching the journal to WAL needs the database to itself, so it is done
// once here first (the mode is stored in the file) rather than per pooled
// connection, where it would wait out busy_timeout behind another
// connection's transaction. In WAL mode with synchronous=NORMAL a commit
// does not fsync, which keeps opening fast on slow disks.
func (s *Store) migrateTo(ctx context.Context, ms []migration) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if s.file {
		if _, err := conn.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
			return err
		}
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	var cur int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&cur); err != nil {
		return err
	}
	if n := len(ms); n > 0 && cur > ms[n-1].Version {
		return fmt.Errorf("database schema v%d is newer than this program (v%d)", cur, ms[n-1].Version)
	}
	for _, m := range ms {
		if m.Version <= cur {
			continue
		}
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			return fmt.Errorf("v%d: %w", m.Version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
			m.Version, time.Now().UnixMilli()); err != nil {
			return fmt.Errorf("v%d: %w", m.Version, err)
		}
	}
	return tx.Commit()
}
