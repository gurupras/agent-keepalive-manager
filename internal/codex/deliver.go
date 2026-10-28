package codex

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// QueueTimeout bounds one `codex queue` invocation.
const QueueTimeout = 20 * time.Second

// queueDBName is the database behind `codex queue`.
//
// Schema (codex-cli 0.15x):
//
//	queued_items(id TEXT PK, thread_id TEXT, payload_json TEXT,
//	             queue_order INTEGER, created_at_ms INTEGER, updated_at_ms INTEGER)
//	UNIQUE INDEX (thread_id, queue_order)
//
// Triggers on insert/update/delete bump queued_thread_revisions, which the
// live TUI watches, so deleting a row is noticed like any other change.
// payload_json holds the message text. The TUI deletes a row when it
// drains it into a turn.
const queueDBName = "queue_1.sqlite"

// Deliverer implements model.Deliverer and model.Withdrawer for Codex via
// `codex queue`, which enqueues a user message that the live TUI picks up
// as a separate turn (it waits for a busy turn to finish and never touches
// the composer).
type Deliverer struct {
	opts Options
	sys  *System
	src  *Source
	// run executes the queue command; replaceable in tests.
	run func(ctx context.Context, bin string, env []string, args ...string) ([]byte, error)
}

var (
	_ model.Deliverer  = (*Deliverer)(nil)
	_ model.Withdrawer = (*Deliverer)(nil)
)

// NewDeliverer returns a Codex deliverer. src, if not nil, is asked whether
// a process still runs a thread on systems without an open-file table;
// without it only the process itself is checked there.
func NewDeliverer(opts Options, src *Source) *Deliverer {
	opts = opts.withDefaults()
	return &Deliverer{opts: opts, sys: opts.Sys, src: src, run: runCmd}
}

func (*Deliverer) Harness() model.Harness { return model.Codex }

// runCmd runs bin directly (no shell) with stdin closed.
func runCmd(ctx context.Context, bin string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	return out.Bytes(), err
}

func (d *Deliverer) home(s model.Session) string {
	if s.CodexHome != "" {
		return s.CodexHome
	}
	return d.opts.Home
}

// owns re-checks rule 5: s.PID is still the process it was and still runs
// thread s.ID.
func (d *Deliverer) owns(s model.Session) bool {
	st := d.sys.StartTime(s.PID)
	if st == "" || (s.ProcStart != "" && st != s.ProcStart) {
		return false
	}
	if d.sys.HasOpenFiles() {
		_, ok := threadFiles(d.sys.GOOS, d.home(s), d.sys.OpenFiles(s.PID))[strings.ToLower(s.ID)]
		return ok
	}
	if d.src != nil {
		return d.src.Owns(s.PID, s.ProcStart, d.home(s), s.ID)
	}
	return true
}

// Ping queues text (which already carries the marker) for the session's
// thread. If the process exited, was replaced, or moved to another thread
// it returns model.ErrSessionChanged: a message queued then would fire on
// a later resume of the thread instead.
func (d *Deliverer) Ping(ctx context.Context, s model.Session, pingID, text string) error {
	if s.Harness != model.Codex || s.ID == "" {
		return fmt.Errorf("codex: not a codex session: %q", s.Key)
	}
	if s.CodexBin == "" {
		return fmt.Errorf("codex: no codex executable for %s: %w", s.Key, model.ErrUnreachable)
	}
	if !d.owns(s) {
		return model.ErrSessionChanged
	}
	ctx, cancel := context.WithTimeout(ctx, QueueTimeout)
	defer cancel()
	env := append(os.Environ(), "CODEX_HOME="+d.home(s))
	out, err := d.run(ctx, s.CodexBin, env, "queue", "--thread", s.ID, "--message", text)
	res := strings.TrimSpace(string(out))
	if err != nil {
		return fmt.Errorf("codex queue: %w: %s", err, truncate(res, 300))
	}
	if !strings.HasPrefix(res, "Queued message") && !strings.Contains(res, "\nQueued message") {
		return fmt.Errorf("codex queue: unexpected output: %s", truncate(res, 300))
	}
	d.opts.Log.Debug("codex: ping queued", "key", s.Key, "pingId", pingID)
	return nil
}

// Sweep deletes queued keepalive messages that will never be consumed by
// the session they were meant for: every row whose text carries a
// keepalive marker and whose thread is not one of live, or that was queued
// more than maxAge ago. It looks in the queue database of every CODEX_HOME
// in live plus the default one. A missing database, table or column is
// not an error; nothing is touched then.
func (d *Deliverer) Sweep(ctx context.Context, live []model.Session, maxAge time.Duration) (int, error) {
	homes := []string{d.opts.Home}
	liveIDs := map[string]bool{}
	for _, s := range live {
		if s.Harness != model.Codex && s.Harness != "" {
			continue
		}
		liveIDs[strings.ToLower(s.ID)] = true
		if h := s.CodexHome; h != "" && !containsPath(d.sys.GOOS, homes, h) {
			homes = append(homes, h)
		}
	}
	cutoff := d.opts.Now().Add(-maxAge).UnixMilli()
	total := 0
	var errs []error
	for _, h := range homes {
		n, err := d.sweepHome(ctx, h, liveIDs, cutoff)
		total += n
		if err != nil {
			errs = append(errs, err)
		}
	}
	return total, errors.Join(errs...)
}

func containsPath(goos string, list []string, p string) bool {
	for _, q := range list {
		if samePath(goos, p, q) {
			return true
		}
	}
	return false
}

func (d *Deliverer) sweepHome(ctx context.Context, home string, live map[string]bool, cutoff int64) (int, error) {
	path := filepath.Join(home, queueDBName)
	if _, err := os.Stat(path); err != nil {
		return 0, nil
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, "rw"))
	if err != nil {
		return 0, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	cols, err := tableColumns(ctx, db, "queued_items")
	if err != nil {
		return 0, fmt.Errorf("codex: %s: %w", path, err)
	}
	for _, c := range []string{"id", "thread_id", "payload_json", "created_at_ms"} {
		if !cols[c] {
			if len(cols) > 0 {
				d.opts.Log.Warn("codex: queue table has an unknown layout; not sweeping", "db", path, "missing", c)
			}
			return 0, nil
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, thread_id, payload_json, created_at_ms FROM queued_items WHERE instr(payload_json, ?) > 0`, markerPrefix)
	if err != nil {
		return 0, err
	}
	var doomed []string
	for rows.Next() {
		var id, thread, payload string
		var created int64
		if err := rows.Scan(&id, &thread, &payload, &created); err != nil {
			rows.Close()
			return 0, err
		}
		if _, ok := model.ParsePing(payload); !ok {
			continue
		}
		if !live[strings.ToLower(thread)] || created < cutoff {
			doomed = append(doomed, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range doomed {
		res, err := tx.ExecContext(ctx, `DELETE FROM queued_items WHERE id = ?`, id)
		if err != nil {
			return 0, err
		}
		k, _ := res.RowsAffected()
		n += int(k)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if n > 0 {
		d.opts.Log.Info("codex: withdrew queued keepalive pings", "db", path, "rows", n)
	}
	return n, nil
}
