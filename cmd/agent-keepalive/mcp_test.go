package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/gurupras/agent-keepalive-manager/internal/api"
	"github.com/gurupras/agent-keepalive-manager/internal/instance"
	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// TestMCPBackendFollows checks that the backend answers from the server's
// pushes after the first call, and that a push wakes Changed.
func TestMCPBackendFollows(t *testing.T) {
	dir := t.TempDir()
	tok, err := instance.Token(dir)
	if err != nil {
		t.Fatal(err)
	}
	push := make(chan []model.SessionView, 1)
	var gets atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(instance.Health{Name: instance.Name, Version: "t"})
	})
	view := func(key string) []model.SessionView {
		v := model.SessionView{}
		v.Key = key
		return []model.SessionView{v}
	}
	mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		_ = json.NewEncoder(w).Encode(view("claude:from-get"))
	})
	mux.HandleFunc("GET /api/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(api.TokenHeader) != tok {
			http.Error(w, "token", http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		send := func(vs []model.SessionView) {
			b, _ := json.Marshal(model.Event{Type: "sessions", Data: vs})
			_ = c.Write(r.Context(), websocket.MessageText, b)
		}
		send(view("claude:first-push"))
		for {
			select {
			case <-r.Context().Done():
				return
			case vs := <-push:
				send(vs)
			}
		}
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(mux)
	srv.Listener = ln
	srv.Start()
	defer srv.Close()
	port, _ := strconv.Atoi(srv.URL[len("http://127.0.0.1:"):])
	if err := instance.WritePortFile(dir, port); err != nil {
		t.Fatal(err)
	}

	b := &mcpBackend{dir: dir}
	ctx := context.Background()
	ch := b.Changed()
	vs, err := b.Sessions(ctx)
	if err != nil || len(vs) != 1 || vs[0].Key != "claude:from-get" {
		t.Fatalf("first Sessions = %v, %v", vs, err)
	}
	wake := func(what string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("no wake-up for %s", what)
		}
		ch = b.Changed()
	}
	wake("the first push")
	if vs, _ := b.Sessions(ctx); vs[0].Key != "claude:first-push" {
		t.Fatalf("after the first push: %v", vs[0].Key)
	}
	push <- view("claude:second-push")
	wake("the second push")
	if vs, _ := b.Sessions(ctx); vs[0].Key != "claude:second-push" {
		t.Fatalf("after the second push: %v", vs[0].Key)
	}
	if n := gets.Load(); n != 1 {
		t.Errorf("%d GET api/sessions, want 1: later calls should read the pushed copy", n)
	}
}
