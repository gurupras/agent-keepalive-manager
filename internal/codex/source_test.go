package codex

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/proc"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

const (
	tMain  = "01a17900-0000-7000-8000-000000000001"
	tSub   = "01a17900-0000-7000-8000-000000000003"
	tOther = "01a17900-0000-7000-8000-000000000005"
)

// fakeEnv is a CODEX_HOME with rollouts and a fake process table.
type fakeEnv struct {
	home  string
	bin   string
	sys   *System
	procs []proc.Proc
	open  map[int][]string
	cwd   map[int]string
	files bool // HasOpenFiles
}

func rolloutPath(home, id string) string {
	return filepath.Join(home, "sessions", "2026", "10", "26", "rollout-2026-10-26T10-00-00-"+id+".jsonl")
}

func copyFixture(t *testing.T, name, dst string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	must(t, err)
	must(t, os.MkdirAll(filepath.Dir(dst), 0o700))
	must(t, os.WriteFile(dst, b, 0o600))
}

func setup(t *testing.T) *fakeEnv {
	t.Helper()
	e := &fakeEnv{home: t.TempDir(), open: map[int][]string{}, cwd: map[int]string{}, files: true}
	copyFixture(t, "basic.jsonl", rolloutPath(e.home, tMain))
	copyFixture(t, "subagent.jsonl", rolloutPath(e.home, tSub))
	copyFixture(t, "reset.jsonl", rolloutPath(e.home, tOther))
	e.bin = filepath.Join(e.home, "bin", "codex")
	must(t, os.MkdirAll(filepath.Dir(e.bin), 0o700))
	must(t, os.WriteFile(e.bin, []byte("#!/bin/sh\n"), 0o700))
	e.procs = []proc.Proc{
		{PID: 10, Exe: e.bin, Args: []string{"codex"}, Start: "1000"},
		{PID: 11, Exe: e.bin, Args: []string{"codex", "exec", "x"}, Start: "1001"},  // non-interactive
		{PID: 12, Exe: e.bin, Args: []string{"codex-linux-sandbox"}, Start: "1002"}, // helper
		{PID: 13, Exe: e.bin, Args: []string{"codex"}, Start: "1003"},               // no thread yet
	}
	e.open[10] = []string{
		rolloutPath(e.home, tMain),
		rolloutPath(e.home, tSub), // a sub-agent's rollout is never the session
		filepath.Join(e.home, "thread-writer-locks", tMain+".lock"),
		"/somewhere/else/sessions/rollout-x-" + tOther + ".jsonl", // another home
	}
	e.sys = &System{
		List: func(names ...string) []proc.Proc { return e.procs },
		Env:  func(int, string) (string, bool) { return "", false },
		OpenFiles: func(pid int) []string {
			return e.open[pid]
		},
		HasOpenFiles: func() bool { return e.files },
		Cwd:          func(pid int) string { return e.cwd[pid] },
		StartTime: func(pid int) string {
			for _, p := range e.procs {
				if p.PID == pid {
					return p.Start
				}
			}
			return ""
		},
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
		GOOS:     "linux",
	}
	return e
}

func (e *fakeEnv) opts() Options {
	return Options{Home: e.home, Log: quiet, Sys: e.sys}
}

func scan(t *testing.T, src *Source) []model.Session {
	t.Helper()
	ss, err := src.Scan(context.Background())
	must(t, err)
	return ss
}

func TestScanOpenFiles(t *testing.T) {
	e := setup(t)
	src := NewSource(e.opts())
	ss := scan(t, src)
	if len(ss) != 1 {
		t.Fatalf("sessions %+v, want only the main thread", ss)
	}
	s := ss[0]
	eq(t, "key", s.Key, "codex:"+tMain)
	eq(t, "pid", s.PID, 10)
	eq(t, "procStart", s.ProcStart, "1000")
	eq(t, "reach", s.Reach, model.ReachQueue)
	eq(t, "bin", s.CodexBin, e.bin)
	eq(t, "home", s.CodexHome, e.home)
	eq(t, "status", s.Status, model.StatusWorking)
	eq(t, "statusSince", s.StatusSince, ts("2026-10-26T10:40:00Z"))
	eq(t, "ttl", s.CacheTTL, model.Duration(CacheTTL))
	eq(t, "title", s.Title, "Fix the failing test in parser.go")
	eq(t, "cwd", s.Cwd, "/work/proj")
	eq(t, "startedAt", s.StartedAt, ts("2026-10-26T10:00:00Z"))
	eq(t, "lastCall", s.LastCall, ts("2026-10-26T10:25:00.020Z"))
	eq(t, "context", s.ContextTokens, int64(10120))

	if a := src.Activity(s.Key, 4); len(a) != 2 || a[0].Seq != 5 {
		t.Errorf("Activity(after 4) = %+v", a)
	}
	if src.Activity("claude:"+tMain, 0) != nil {
		t.Error("foreign key answered")
	}
}

func TestSessionIndexTitle(t *testing.T) {
	e := setup(t)
	must(t, os.WriteFile(filepath.Join(e.home, "session_index.jsonl"),
		[]byte(`{"id":"`+tMain+`","thread_name":"old"}`+"\n"+`{"id":"`+tMain+`","thread_name":"parser fix"}`+"\n"), 0o600))
	eq(t, "title", scan(t, NewSource(e.opts()))[0].Title, "parser fix")
}

// Without an open-file table the thread is the newest rollout written since
// the process started whose cwd matches; each rollout goes to one process.
func TestScanFallback(t *testing.T) {
	e := setup(t)
	e.files = false
	e.sys.GOOS = "windows"
	start := time.Now().Add(-time.Minute)
	tok := func(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }
	e.procs = []proc.Proc{
		{PID: 20, Exe: e.bin + ".exe", Args: []string{"codex.exe"}, Start: tok(start)},
		{PID: 21, Exe: e.bin, Args: []string{"codex"}, Start: tok(start.Add(10 * time.Second))},
	}
	e.cwd[21] = "/work/proj"
	old := time.Now().Add(-time.Hour)
	must(t, os.Chtimes(rolloutPath(e.home, tOther), old, old)) // written before either process started
	now := time.Now()
	must(t, os.Chtimes(rolloutPath(e.home, tMain), now, now))
	must(t, os.Chtimes(rolloutPath(e.home, tSub), now, now))

	ss := scan(t, NewSource(e.opts()))
	if len(ss) != 1 || ss[0].PID != 21 || ss[0].ID != tMain {
		t.Fatalf("sessions %+v, want pid 21 on the main thread (pid 20 has nothing left)", ss)
	}

	// The older rollout becomes the newest one written: it is now pid 21's.
	must(t, os.Chtimes(rolloutPath(e.home, tOther), now.Add(time.Second), now.Add(time.Second)))
	src := NewSource(e.opts())
	ss = scan(t, src)
	if len(ss) != 2 {
		t.Fatalf("sessions %+v", ss)
	}
	got := map[int]string{}
	for _, s := range ss {
		got[s.PID] = s.ID
	}
	eq(t, "pid 21", got[21], tOther)
	eq(t, "pid 20", got[20], tMain)
	if !src.Owns(21, tok(start.Add(10*time.Second)), e.home, tOther) || src.Owns(21, "", e.home, tMain) {
		t.Error("Owns disagrees with Scan")
	}
}

func TestThreadFiles(t *testing.T) {
	home := `C:\Users\me\.codex`
	files := []string{
		`C:\Users\me\.codex\sessions\2026\10\26\rollout-2026-10-26T10-00-00-` + strings.ToUpper(tMain) + `.jsonl`,
		`c:\users\me\.codex\thread-writer-locks\` + tOther + `.lock`,
		`D:\x\sessions\rollout-a-` + tSub + `.jsonl`,
	}
	got := threadFiles("windows", home, files)
	if len(got) != 2 || got[tMain] != files[0] || got[tOther] != "" {
		t.Errorf("threadFiles = %v", got)
	}
}

func TestPing(t *testing.T) {
	e := setup(t)
	src := NewSource(e.opts())
	s := scan(t, src)[0]
	text := model.FormatPing("p7")

	tests := []struct {
		name    string
		mutate  func(*model.Session)
		out     string
		runErr  error
		wantErr error
		errHas  string
	}{
		{name: "ok", out: "Queued message 019a for thread " + tMain + ".\n"},
		{name: "pid reused", mutate: func(s *model.Session) { s.ProcStart = "1" }, wantErr: model.ErrSessionChanged},
		{name: "process gone", mutate: func(s *model.Session) { s.PID = 999 }, wantErr: model.ErrSessionChanged},
		{name: "thread no longer open", mutate: func(s *model.Session) { s.ID = tOther }, wantErr: model.ErrSessionChanged},
		{name: "no binary", mutate: func(s *model.Session) { s.CodexBin = "" }, wantErr: model.ErrUnreachable},
		{name: "cli failure", out: "Error: thread not found", runErr: errors.New("exit status 1"), errHas: "thread not found"},
		{name: "odd output", out: "something else", errHas: "unexpected output"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ss := s
			if tc.mutate != nil {
				tc.mutate(&ss)
			}
			d := NewDeliverer(e.opts(), src)
			var gotBin string
			var gotArgs, gotEnv []string
			d.run = func(_ context.Context, bin string, env []string, args ...string) ([]byte, error) {
				gotBin, gotArgs, gotEnv = bin, args, env
				return []byte(tc.out), tc.runErr
			}
			err := d.Ping(context.Background(), ss, "p7", text)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err %v, want %v", err, tc.wantErr)
				}
				return
			case tc.errHas != "":
				if err == nil || !strings.Contains(err.Error(), tc.errHas) {
					t.Fatalf("err %v, want %q", err, tc.errHas)
				}
				return
			case err != nil:
				t.Fatal(err)
			}
			eq(t, "bin", gotBin, e.bin)
			eq(t, "args", strings.Join(gotArgs, "|"), "queue|--thread|"+tMain+"|--message|"+text)
			eq(t, "env home", gotEnv[len(gotEnv)-1], "CODEX_HOME="+e.home)
		})
	}
}

func createQueue(t *testing.T, home string, rows [][4]any) {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(home, queueDBName), "rwc"))
	must(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE queued_items (id TEXT PRIMARY KEY NOT NULL, thread_id TEXT NOT NULL,
		payload_json TEXT NOT NULL, queue_order INTEGER NOT NULL, created_at_ms INTEGER NOT NULL, updated_at_ms INTEGER NOT NULL)`)
	must(t, err)
	for i, r := range rows {
		_, err := db.Exec(`INSERT INTO queued_items VALUES (?,?,?,?,?,?)`, r[0], r[1], r[2], i, r[3], r[3])
		must(t, err)
	}
}

func remaining(t *testing.T, home string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(home, queueDBName), "ro"))
	must(t, err)
	defer db.Close()
	rows, err := db.Query(`SELECT id FROM queued_items ORDER BY id`)
	must(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		must(t, rows.Scan(&id))
		out = append(out, id)
	}
	return out
}

func TestSweep(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	ms := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	payload := func(text string) string {
		return `{"UserInput":{"content":[{"type":"text","text":"` + text + `"}]}}`
	}
	ping := payload(model.FormatPing("p1"))
	createQueue(t, home, [][4]any{
		{"a-live-fresh", tMain, ping, ms(time.Minute)},
		{"b-live-old", tMain, ping, ms(time.Hour)},
		{"c-dead-thread", tOther, ping, ms(time.Minute)},
		{"d-human-dead", tOther, payload("please continue"), ms(time.Hour)},
		{"e-quoted", tOther, payload("what is [keepalive"), ms(time.Hour)},
	})
	d := NewDeliverer(Options{Home: home, Log: quiet, Now: func() time.Time { return now }}, nil)
	live := []model.Session{{Harness: model.Codex, ID: strings.ToUpper(tMain), CodexHome: home}}
	n, err := d.Sweep(context.Background(), live, 10*time.Minute)
	must(t, err)
	eq(t, "removed", n, 2)
	eq(t, "left", strings.Join(remaining(t, home), ","), "a-live-fresh,d-human-dead,e-quoted")
}

func TestSweepWithoutQueue(t *testing.T) {
	home := t.TempDir()
	d := NewDeliverer(Options{Home: home, Log: quiet}, nil)
	n, err := d.Sweep(context.Background(), nil, time.Minute)
	if n != 0 || err != nil {
		t.Errorf("no database: %d, %v", n, err)
	}
	// A database without the table is left alone.
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(home, queueDBName), "rwc"))
	must(t, err)
	_, err = db.Exec(`CREATE TABLE other (x)`)
	must(t, err)
	db.Close()
	n, err = d.Sweep(context.Background(), nil, time.Minute)
	if n != 0 || err != nil {
		t.Errorf("no table: %d, %v", n, err)
	}
}
