package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"sync"

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
		Parents: proc.Parents,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return s.Serve(ctx, stdin, stdout)
}

// mcpBackend is mcp.Backend over the running server's API. It finds (or
// starts) the server on first use and again after a call fails, so an
// update restarting the server on another port does not break the session.
type mcpBackend struct {
	dir  string
	port int

	mu sync.Mutex
	c  *client
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
	var out []model.SessionView
	err := b.call(ctx, "GET", "api/sessions", nil, &out)
	return out, err
}

func (b *mcpBackend) StartPlan(ctx context.Context, key string, hours float64) (model.Plan, error) {
	var p model.Plan
	err := b.call(ctx, "POST", planPath(key), map[string]any{"hours": hours}, &p)
	return p, err
}

func (b *mcpBackend) KeepIndefinitely(ctx context.Context, key string) (model.Plan, error) {
	var p model.Plan
	err := b.call(ctx, "POST", planPath(key), map[string]any{"indefinite": true}, &p)
	return p, err
}

func (b *mcpBackend) ExtendPlan(ctx context.Context, key string, addHours float64) (model.Plan, error) {
	var p model.Plan
	err := b.call(ctx, "PATCH", planPath(key), map[string]any{"addHours": addHours}, &p)
	return p, err
}

func (b *mcpBackend) ResumePlan(ctx context.Context, key string) (model.Plan, error) {
	var p model.Plan
	err := b.call(ctx, "PATCH", planPath(key), map[string]any{"resume": true}, &p)
	return p, err
}

func (b *mcpBackend) CancelPlan(ctx context.Context, key string) error {
	return b.call(ctx, "DELETE", planPath(key), nil, nil)
}

func (b *mcpBackend) Totals(ctx context.Context) (model.Totals, error) {
	var out struct {
		Totals model.Totals `json:"totals"`
	}
	err := b.call(ctx, "GET", "api/ledger", nil, &out)
	return out.Totals, err
}

var _ mcp.Backend = (*mcpBackend)(nil)
