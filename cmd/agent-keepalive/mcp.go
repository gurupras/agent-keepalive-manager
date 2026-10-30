package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/gurupras/agent-keepalive-manager/internal/api"
	"github.com/gurupras/agent-keepalive-manager/internal/instance"
	"github.com/gurupras/agent-keepalive-manager/internal/mcp"
	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/proc"
)

// cmdMCP serves the Model Context Protocol on stdin and stdout, for an
// agent to run as a tool server. It starts a background server on first use
// if none is running.
func cmdMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := newFlags("mcp")
	fs.SetOutput(stderr)
	dataDir := dataDirFlag(fs)
	port := fs.Int("port", 0, "port for a server this starts (default: 4344 or the next free one)")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "mcp: unexpected argument %q\n", fs.Arg(0))
		return errUsage
	}
	dir, err := dataDir()
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	s := &mcp.Server{
		Backend: &mcpBackend{dir: dir, port: *port},
		Version: version,
		PID:     os.Getpid(),
		Cwd:     cwd,
		Parent:  proc.Parent,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return s.Serve(ctx, stdin, stdout)
}

// mcpBackend is mcp.Backend over the running server's API. It finds (or
// starts) the server on first use and again after a call fails, so an
// update restarting the server on another port does not break the session.
//
// After the first call it follows the server's WebSocket, which pushes the
// sessions whenever a scan changes them, and answers Sessions from that
// copy: a tool call reads memory, and wait_for_agents wakes on the push.
// While the socket is down it falls back to GET api/sessions.
type mcpBackend struct {
	dir  string
	port int

	mu        sync.Mutex
	c         *client
	views     []model.SessionView
	live      bool // views follow the server's pushes
	following bool
	changed   chan struct{} // closed at the next change
}

func (b *mcpBackend) Changed() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.changed == nil {
		b.changed = make(chan struct{})
	}
	return b.changed
}

// notify wakes Changed waiters. It requires b.mu.
func (b *mcpBackend) notify() {
	if b.changed != nil {
		close(b.changed)
	}
	b.changed = make(chan struct{})
}

// stale marks the copy out of date after a change this process made, until
// the server pushes the sessions again.
func (b *mcpBackend) stale() {
	b.mu.Lock()
	b.live = false
	b.mu.Unlock()
}

// follow keeps a WebSocket to the server open for the life of the process.
// The socket is passive: it does not count as use, so a server started in
// the background can still stop once idle (serve --idle-exit).
// It never starts a server: if the user stopped it, it stays stopped until
// a tool call needs it.
func (b *mcpBackend) follow() {
	ctx := context.Background()
	wait := time.Second
	for {
		began := time.Now()
		b.subscribe(ctx)
		b.mu.Lock()
		b.live = false
		b.notify()
		b.mu.Unlock()
		if time.Since(began) > time.Minute {
			wait = time.Second
		}
		time.Sleep(wait)
		wait = min(2*wait, 15*time.Second)
	}
}

func (b *mcpBackend) subscribe(ctx context.Context) {
	r, ok := instance.FindRunning(ctx, b.dir)
	if !ok {
		return
	}
	tok, err := instance.ReadToken(b.dir)
	if err != nil {
		return
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	c, _, err := websocket.Dial(dctx, "ws"+strings.TrimPrefix(r.URL, "http")+"api/ws?passive=1",
		&websocket.DialOptions{HTTPHeader: http.Header{api.TokenHeader: {tok}}})
	cancel()
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(64 << 20)
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var ev struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(data, &ev) != nil || ev.Type != "sessions" {
			continue
		}
		var views []model.SessionView
		if json.Unmarshal(ev.Data, &views) != nil {
			continue
		}
		b.mu.Lock()
		b.views, b.live = views, true
		b.notify()
		b.mu.Unlock()
	}
}

func (b *mcpBackend) client(ctx context.Context) (*client, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.c != nil {
		return b.c, nil
	}
	r, err := ensureServer(ctx, b.dir, b.port)
	if err != nil {
		return nil, err
	}
	tok, err := instance.ReadToken(b.dir)
	if err != nil {
		return nil, fmt.Errorf("API token: %w", err)
	}
	b.c = &client{base: r.URL, token: tok}
	return b.c, nil
}

// call runs do against the server, once more with a fresh lookup when the
// server could not be reached.
func (b *mcpBackend) call(ctx context.Context, method, path string, body, out any) error {
	c, err := b.client(ctx)
	if err != nil {
		return err
	}
	err = c.do(method, path, body, out)
	if _, isAPI := err.(*apiError); err == nil || isAPI {
		return err
	}
	b.mu.Lock()
	b.c = nil
	b.mu.Unlock()
	if c, err = b.client(ctx); err != nil {
		return err
	}
	return c.do(method, path, body, out)
}

func planPath(key string) string { return "api/sessions/" + url.PathEscape(key) + "/plan" }

func (b *mcpBackend) Sessions(ctx context.Context) ([]model.SessionView, error) {
	b.mu.Lock()
	if b.live {
		out := slices.Clone(b.views)
		b.mu.Unlock()
		return out, nil
	}
	b.mu.Unlock()
	var out []model.SessionView
	if err := b.call(ctx, "GET", "api/sessions", nil, &out); err != nil {
		return nil, err
	}
	b.mu.Lock()
	if !b.following {
		b.following = true
		go b.follow()
	}
	b.mu.Unlock()
	return out, nil
}

func (b *mcpBackend) StartPlan(ctx context.Context, key string, hours float64) (model.Plan, error) {
	defer b.stale()
	var p model.Plan
	err := b.call(ctx, "POST", planPath(key), map[string]any{"hours": hours}, &p)
	return p, err
}

func (b *mcpBackend) KeepIndefinitely(ctx context.Context, key string) (model.Plan, error) {
	defer b.stale()
	var p model.Plan
	err := b.call(ctx, "POST", planPath(key), map[string]any{"indefinite": true}, &p)
	return p, err
}

func (b *mcpBackend) ExtendPlan(ctx context.Context, key string, addHours float64) (model.Plan, error) {
	defer b.stale()
	var p model.Plan
	err := b.call(ctx, "PATCH", planPath(key), map[string]any{"addHours": addHours}, &p)
	return p, err
}

func (b *mcpBackend) ResumePlan(ctx context.Context, key string) (model.Plan, error) {
	defer b.stale()
	var p model.Plan
	err := b.call(ctx, "PATCH", planPath(key), map[string]any{"resume": true}, &p)
	return p, err
}

func (b *mcpBackend) CancelPlan(ctx context.Context, key string) error {
	defer b.stale()
	return b.call(ctx, "DELETE", planPath(key), nil, nil)
}

func (b *mcpBackend) Totals(ctx context.Context) (model.Totals, error) {
	var out struct {
		Totals model.Totals `json:"totals"`
	}
	err := b.call(ctx, "GET", "api/ledger", nil, &out)
	return out.Totals, err
}

var (
	_ mcp.Backend  = (*mcpBackend)(nil)
	_ mcp.Notifier = (*mcpBackend)(nil)
)
