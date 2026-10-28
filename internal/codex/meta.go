package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/tail"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// threadMeta is what the state database knows about a thread. Every field
// is optional: the schema differs between Codex versions.
type threadMeta struct {
	RolloutPath      string
	Cwd              string
	Title            string
	Name             string
	FirstUserMessage string
	Model            string
	CreatedAt        time.Time
	Subagent         bool // agent_role / agent_nickname set
}

// stateDBName is the Codex thread index under CODEX_HOME.
const stateDBName = "state_5.sqlite"

// sqliteDSN builds a modernc.org/sqlite DSN for path. busy_timeout makes
// a write by the live CLI wait for us (and us for it) instead of failing.
func sqliteDSN(path, mode string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	if vol := filepath.VolumeName(path); vol != "" {
		u.Path = "/" + u.Path // file:///C:/...
	}
	q := url.Values{}
	q.Set("mode", mode)
	q.Add("_pragma", "busy_timeout(2000)")
	u.RawQuery = q.Encode()
	return u.String()
}

// wantedThreadCols are the threads columns we read when present.
var wantedThreadCols = []string{
	"id", "rollout_path", "cwd", "title", "name", "first_user_message",
	"model", "created_at_ms", "created_at", "agent_role", "agent_nickname",
}

// readThreadMeta looks up ids in <home>/state_5.sqlite, opened read-only.
// A missing database or table yields an empty map and no error.
func readThreadMeta(ctx context.Context, home string, ids []string) (map[string]threadMeta, error) {
	out := map[string]threadMeta{}
	path := filepath.Join(home, stateDBName)
	if _, err := os.Stat(path); err != nil || len(ids) == 0 {
		return out, nil
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, "ro"))
	if err != nil {
		return out, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	cols, err := tableColumns(ctx, db, "threads")
	if err != nil || !cols["id"] {
		return out, err
	}
	var sel []string
	for _, c := range wantedThreadCols {
		if cols[c] {
			sel = append(sel, c)
		}
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	// Column names come from the fixed allow-list above, never from input.
	q := "SELECT " + strings.Join(sel, ",") + " FROM threads WHERE id IN (" + ph + ")"
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		vals := make([]sql.NullString, len(sel))
		ptrs := make([]any, len(sel))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return out, err
		}
		var id string
		var m threadMeta
		for i, c := range sel {
			v := vals[i].String
			switch c {
			case "id":
				id = strings.ToLower(v)
			case "rollout_path":
				m.RolloutPath = v
			case "cwd":
				m.Cwd = v
			case "title":
				m.Title = v
			case "name":
				m.Name = v
			case "first_user_message":
				m.FirstUserMessage = v
			case "model":
				m.Model = v
			case "created_at_ms":
				if ms, ok := parseInt(v); ok && ms > 0 {
					m.CreatedAt = time.UnixMilli(ms)
				}
			case "created_at":
				if s, ok := parseInt(v); ok && s > 0 && m.CreatedAt.IsZero() {
					m.CreatedAt = time.Unix(s, 0)
				}
			case "agent_role", "agent_nickname":
				if v != "" {
					m.Subagent = true
				}
			}
		}
		out[id] = m
	}
	return out, rows.Err()
}

func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		cols[n] = true
	}
	return cols, rows.Err()
}

func parseInt(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}

// nameIndex follows <home>/session_index.jsonl, an append-only log of
// {"id","thread_name","updated_at"} where the last entry for an id wins.
type nameIndex struct {
	tl    *tail.Tailer
	mu    sync.Mutex
	names map[string]string
}

func newNameIndex(home string) *nameIndex {
	n := &nameIndex{names: map[string]string{}}
	n.tl = tail.New(filepath.Join(home, "session_index.jsonl"), n, tail.Options{})
	return n
}

func (n *nameIndex) Reset() {
	n.mu.Lock()
	defer n.mu.Unlock()
	clear(n.names)
}

func (n *nameIndex) Line(b []byte) {
	var e struct {
		ID   string `json:"id"`
		Name string `json:"thread_name"`
	}
	if json.Unmarshal(b, &e) == nil && e.ID != "" {
		n.mu.Lock()
		n.names[strings.ToLower(e.ID)] = e.Name
		n.mu.Unlock()
	}
}

func (n *nameIndex) name(id string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.names[id]
}
