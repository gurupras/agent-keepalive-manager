package api

import (
	"context"
	"encoding/json"
	"time"

	"net/http"

	"github.com/coder/websocket"

	"github.com/gurupras/agent-keepalive-manager/internal/apitypes"
	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// wsBuffer is how many events a slow client may fall behind before it
// loses some; the next scan's "sessions" snapshot repairs its view.
const wsBuffer = 64

// writeTimeout bounds one frame write, so a stuck client cannot hold a
// goroutine forever.
const writeTimeout = 10 * time.Second

// ws upgrades api/ws and streams events: hello, a sessions snapshot, then
// everything the scheduler, the updater and usage reporting publish.
func (h *handler) ws(w http.ResponseWriter, r *http.Request) {
	// The guard checked Origin and the token. The library's own Origin
	// check (Origin host == Host) stays on as a second line.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept wrote the error
	}
	defer c.CloseNow()

	events := make(chan model.Event, wsBuffer)
	offer := func(ev model.Event) {
		select {
		case events <- ev:
		default: // a slow client loses events rather than blocking anyone
		}
	}
	svcEvents, cancelSvc := h.opts.Service.Subscribe()
	defer cancelSvc()
	if h.opts.Updater != nil {
		cancel := h.opts.Updater.Subscribe(func(s apitypes.UpdateStatus) { offer(model.Event{Type: "update", Data: s}) })
		defer cancel()
	}
	if n, ok := h.opts.Metrics.(apitypes.MetricsNotifier); ok {
		cancel := n.Subscribe(func(s apitypes.MetricsStatus) { offer(model.Event{Type: "metrics", Data: s}) })
		defer cancel()
	}
	local := h.subscribeLocal()
	defer h.unsubscribeLocal(local)

	// Clients send nothing but pongs; CloseRead handles control frames and
	// cancels ctx when the client goes away.
	ctx := c.CloseRead(r.Context())

	send := func(ev model.Event) bool {
		b, err := json.Marshal(ev)
		if err != nil {
			h.log.Warn("ws: marshal", "type", ev.Type, "err", err)
			return true
		}
		wctx, cancel := context.WithTimeout(ctx, writeTimeout)
		defer cancel()
		return c.Write(wctx, websocket.MessageText, b) == nil
	}
	if !send(model.Event{Type: "hello", Data: map[string]any{"version": h.opts.Info.Version, "info": h.opts.Info, "startup": h.startupNow()}}) ||
		!send(model.Event{Type: "sessions", Data: nonNil(h.opts.Service.Sessions())}) {
		return
	}

	pingErr := make(chan error, 1)
	go func() { pingErr <- h.keepalive(ctx, c) }()

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-pingErr:
			h.log.Debug("ws: client stopped answering pings", "err", err)
			c.Close(websocket.StatusPolicyViolation, "no pong")
			return
		case ev, ok := <-svcEvents:
			if !ok {
				c.Close(websocket.StatusGoingAway, "server stopping")
				return
			}
			if !send(ev) {
				return
			}
		case ev := <-events:
			if !send(ev) {
				return
			}
		case ev := <-local:
			if !send(ev) {
				return
			}
		}
	}
}

// keepalive pings every PingInterval and returns when a pong does not
// arrive within PongTimeout, or when ctx ends.
func (h *handler) keepalive(ctx context.Context, c *websocket.Conn) error {
	t := time.NewTicker(h.opts.PingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		pctx, cancel := context.WithTimeout(ctx, h.opts.PongTimeout)
		err := c.Ping(pctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}
}

func (h *handler) subscribeLocal() chan model.Event {
	ch := make(chan model.Event, wsBuffer)
	h.localMu.Lock()
	h.local[ch] = struct{}{}
	h.localMu.Unlock()
	return ch
}

func (h *handler) unsubscribeLocal(ch chan model.Event) {
	h.localMu.Lock()
	delete(h.local, ch)
	h.localMu.Unlock()
}

// broadcast sends an event the API itself caused to every WebSocket.
func (h *handler) broadcast(ev model.Event) {
	h.localMu.Lock()
	defer h.localMu.Unlock()
	for ch := range h.local {
		select {
		case ch <- ev:
		default:
		}
	}
}
