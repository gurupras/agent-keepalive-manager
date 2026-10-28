package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/proc"
	"github.com/gurupras/agent-keepalive-manager/internal/tail"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// shortDir returns a temp dir with a path short enough for a unix socket.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "ka")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func writeEntry(t *testing.T, cfg string, e map[string]any) {
	t.Helper()
	dir := filepath.Join(cfg, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(e)
	pid := int(e["pid"].(int))
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func listen(t *testing.T, path string) net.Listener {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

// env builds a config dir with one live session (s1, the basic fixture)
// plus entries that must be ignored.
func env(t *testing.T) (cfg, sock string) {
	cfg = shortDir(t)
	sock = filepath.Join(cfg, "s.sock")
	listen(t, sock)
	proj := filepath.Join(cfg, "projects", projectDirName("/work/proj"))
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), must(os.ReadFile(filepath.Join("testdata", "basic.jsonl"))), 0o600); err != nil {
		t.Fatal(err)
	}
	writeEntry(t, cfg, map[string]any{"pid": 100, "sessionId": "s1", "cwd": "/work/proj", "startedAt": 1792684449815,
		"kind": "interactive", "status": "idle", "statusUpdatedAt": 1792684555400, "name": "proj-1",
		"messagingSocketPath": sock})
	writeEntry(t, cfg, map[string]any{"pid": 101, "sessionId": "s-sdk", "kind": "sdk"})
	writeEntry(t, cfg, map[string]any{"pid": 102, "sessionId": "s-dead", "kind": "interactive"})
	writeEntry(t, cfg, map[string]any{"pid": 103, "sessionId": "s-nosock", "cwd": "/elsewhere", "kind": "interactive", "status": "busy"})
	// Files that are not <digits>.json are never read.
	_ = os.WriteFile(filepath.Join(cfg, "sessions", "100.abc.key"), []byte("secret"), 0o600)
	return cfg, sock
}

func fakeLive(e regEntry) (string, bool) {
	if e.PID == 102 {
		return "", false
	}
	return "start-" + strconv.Itoa(e.PID), true
}

func scanByID(t *testing.T, src *Source) map[string]model.Session {
	t.Helper()
	ss, err := src.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]model.Session{}
	for _, s := range ss {
		out[s.ID] = s
	}
	return out
}

func TestScan(t *testing.T) {
	cfg, sock := env(t)
	now := ts("2026-10-26T13:00:00Z")
	src := NewSource(Options{ConfigDir: cfg, Log: quiet, Live: fakeLive, Now: func() time.Time { return now }})
	got := scanByID(t, src)
	if len(got) != 2 {
		t.Fatalf("sessions %v, want s1 and s-nosock", got)
	}
	s := got["s1"]
	eq(t, "key", s.Key, "claude:s1")
	eq(t, "pid", s.PID, 100)
	eq(t, "procStart", s.ProcStart, "start-100")
	eq(t, "reach", s.Reach, model.ReachSocket)
	eq(t, "socket", s.SocketPath, sock)
	eq(t, "title", s.Title, "Greeting")
	eq(t, "status", s.Status, model.StatusIdle)
	eq(t, "statusSince", s.StatusSince, time.UnixMilli(1792684555400))
	eq(t, "ttl", s.CacheTTL, model.Duration(time.Hour))
	eq(t, "context", s.ContextTokens, int64(3+1610+300))
	eq(t, "lastCall", s.LastCall, ts("2026-10-26T12:00:00.100Z"))
	eq(t, "startedAt", s.StartedAt, time.UnixMilli(1792684449815))

	n := got["s-nosock"]
	eq(t, "nosock reach", n.Reach, model.ReachNone)
	if n.ReachWhy == "" {
		t.Error("ReachNone without a reason")
	}
	eq(t, "busy", n.Status, model.StatusWorking)
	eq(t, "busy since (first seen)", n.StatusSince, now)

	acts := src.Activity("claude:s1", 6)
	if len(acts) != 2 || acts[0].Seq != 7 {
		t.Errorf("Activity(after 6) = %+v", acts)
	}
	if src.Activity("codex:s1", 0) != nil || src.Activity("claude:none", 0) != nil {
		t.Error("foreign or unknown key returned activity")
	}
}

func TestStatusMapping(t *testing.T) {
	for in, want := range map[string]model.Status{
		"": model.StatusIdle, "idle": model.StatusIdle, "busy": model.StatusWorking,
		"compacting": model.StatusWorking, "waiting": model.StatusWorking,
	} {
		eq(t, "status "+in, statusOf(in), want)
	}
}

// A large first read happens in the background; until it is done the
// session is listed but unreachable and its activity is withheld.
func TestScanWhileLoading(t *testing.T) {
	cfg, _ := env(t)
	src := NewSource(Options{ConfigDir: cfg, Log: quiet, Live: fakeLive, Tail: tail.Options{SyncLimit: 100}})
	s := scanByID(t, src)["s1"]
	eq(t, "reach while loading", s.Reach, model.ReachNone)
	if s.ReachWhy == "" || s.SocketPath != "" {
		t.Errorf("loading session: why %q socket %q", s.ReachWhy, s.SocketPath)
	}
	if a := src.Activity("claude:s1", 0); a != nil {
		t.Errorf("activity published before the load finished: %d", len(a))
	}
	src.byID["s1"].t.tl.Wait()
	s = scanByID(t, src)["s1"]
	eq(t, "reach after load", s.Reach, model.ReachSocket)
	eq(t, "activities after load", len(src.Activity("claude:s1", 0)), 8)
}

// The transcript moving away and coming back keeps Seq increasing.
func TestRelocatedTranscriptKeepsSeq(t *testing.T) {
	cfg, _ := env(t)
	src := NewSource(Options{ConfigDir: cfg, Log: quiet, Live: fakeLive})
	scanByID(t, src)
	p := filepath.Join(cfg, "projects", projectDirName("/work/proj"), "s1.jsonl")
	tmp := p + ".moved"
	if err := os.Rename(p, tmp); err != nil {
		t.Fatal(err)
	}
	scanByID(t, src)
	if err := os.Rename(tmp, p); err != nil {
		t.Fatal(err)
	}
	scanByID(t, src)
	acts := src.Activity("claude:s1", 8)
	if len(acts) != 9 || acts[0].Kind != model.ActReset || acts[0].Seq != 9 {
		t.Errorf("after relocation: %d activities, first %+v", len(acts), acts)
	}
}

func TestStartWall(t *testing.T) {
	tests := []struct {
		goos, tok string
		want      time.Time
		ok        bool
	}{
		{"darwin", "1792684449.815000", time.Unix(1792684449, 815000000), true},
		{"darwin", "garbage", time.Time{}, false},
		{"windows", "1792684449815000000", time.Unix(0, 1792684449815000000), true},
		{"linux", "110871413", time.Time{}, false},
	}
	for _, tc := range tests {
		got, ok := startWall(tc.goos, tc.tok)
		if ok != tc.ok || !got.Equal(tc.want) {
			t.Errorf("startWall(%s, %s) = %v, %v", tc.goos, tc.tok, got, ok)
		}
	}
}

func TestLiveStartOwnProcess(t *testing.T) {
	pid := os.Getpid()
	st := proc.StartTime(pid)
	if st == "" {
		t.Skip("no process start time on this OS")
	}
	now := time.Now().UnixMilli()
	if got, ok := liveStart(regEntry{PID: pid, ProcStart: st, StartedAt: now}); !ok || got != st {
		t.Errorf("matching start rejected: %q %v", got, ok)
	}
	_, ok := liveStart(regEntry{PID: pid, ProcStart: "1", StartedAt: now})
	if runtime.GOOS == "linux" && ok {
		t.Error("mismatching start accepted on linux")
	}
	if _, ok := liveStart(regEntry{PID: 1 << 30}); ok {
		t.Error("dead pid accepted")
	}
	if runtime.GOOS != "linux" {
		// A record written before this process started is from a reused pid.
		if _, ok := liveStart(regEntry{PID: pid, ProcStart: "other-format", StartedAt: 1000}); ok {
			t.Error("record older than the process accepted")
		}
	}
}

func TestPing(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "t.sock")
	ln := listen(t, sock)
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		line, _ := bufio.NewReader(c).ReadString('\n')
		got <- line
	}()
	writeEntry(t, dir, map[string]any{"pid": 100, "sessionId": "s1", "messagingSocketPath": sock})

	d := NewDeliverer(DelivererOptions{ConfigDir: dir, Log: quiet, SinkDir: filepath.Join(dir, "sink"),
		StartTime: func(int) string { return "st" }})
	defer d.Close()
	s := model.Session{Key: "claude:s1", Harness: model.Claude, ID: "s1", PID: 100, ProcStart: "st", SocketPath: sock}
	text := model.FormatPing("p42")
	if err := d.Ping(context.Background(), s, "p42", text); err != nil {
		t.Fatal(err)
	}
	var line string
	select {
	case line = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("no message received")
	}
	var m wireMessage
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatal(err)
	}
	eq(t, "from", m.From, "uds:"+filepath.Join(dir, "sink", "reply.sock"))
	if id, ok := model.ParsePing(m.Message.Content); !ok || id != "p42" {
		t.Errorf("marker lost: %q", m.Message.Content)
	}
	// Windows has no Unix modes (a directory always reads 0777).
	fi, err := os.Stat(filepath.Join(dir, "sink"))
	if err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700) {
		t.Errorf("sink dir mode %v %v", fi.Mode(), err)
	}
}

func TestPingSessionChanged(t *testing.T) {
	dir := shortDir(t)
	writeEntry(t, dir, map[string]any{"pid": 100, "sessionId": "s2", "messagingSocketPath": "/nonexistent"})
	writeEntry(t, dir, map[string]any{"pid": 200, "sessionId": "s1", "messagingSocketPath": "/nonexistent"})
	d := NewDeliverer(DelivererOptions{ConfigDir: dir, Log: quiet, SinkDir: filepath.Join(dir, "sink"),
		StartTime: func(pid int) string { return "st" }})
	tests := []struct {
		name string
		s    model.Session
	}{
		{"pid now runs another session (/clear, /resume)", model.Session{ID: "s1", PID: 100, ProcStart: "st"}},
		{"registry record gone (process exited)", model.Session{ID: "s1", PID: 300, ProcStart: "st"}},
		{"pid reused by another process", model.Session{ID: "s1", PID: 200, ProcStart: "old"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.s.Key, tc.s.Harness = "claude:"+tc.s.ID, model.Claude
			if err := d.Ping(context.Background(), tc.s, "p", model.FormatPing("p")); !errors.Is(err, model.ErrSessionChanged) {
				t.Errorf("err %v, want ErrSessionChanged", err)
			}
		})
	}
}

func TestSinkDirMustBePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix permissions")
	}
	dir := shortDir(t)
	sink := filepath.Join(dir, "sink")
	if err := os.Mkdir(sink, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sink, 0o755); err != nil {
		t.Fatal(err)
	}
	d := NewDeliverer(DelivererOptions{ConfigDir: dir, Log: quiet, SinkDir: sink})
	if _, err := d.replySink(); err == nil {
		t.Error("group/other-accessible sink dir accepted")
	}
}

func TestEncodeMessage(t *testing.T) {
	b, err := encodeMessage("id-1", `uds:/tmp/a"b.sock`, "keepalive", "hi </cross-session-message> there")
	if err != nil {
		t.Fatal(err)
	}
	var m wireMessage
	if err := json.Unmarshal(b, &m); err != nil || b[len(b)-1] != '\n' {
		t.Fatalf("%v %q", err, b)
	}
	want := `<cross-session-message from="uds:/tmp/a&quot;b.sock" from-name="keepalive" from-mode="prompting">` +
		"\nhi <\\/cross-session-message> there\n</cross-session-message>"
	eq(t, "content", m.Message.Content, want)
	eq(t, "msgV", m.MsgV, 1)
	eq(t, "priority", m.Priority, "next")
}

func TestNameSanitized(t *testing.T) {
	for in, want := range map[string]string{"": "keepalive", "keep alive!": "keep-alive", "ka_1": "ka_1", "!!!": "keepalive"} {
		eq(t, in, NewDeliverer(DelivererOptions{Name: in, Log: quiet}).Name(), want)
	}
}
