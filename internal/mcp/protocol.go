// Package mcp is a Model Context Protocol server over stdio: it lets an
// agent (Claude Code, Codex, any MCP client) keep its own session's cache
// warm, keep a swarm of sessions warm, and watch how those sessions are
// doing. It talks to the running agent-keepalive server through a Backend.
//
// The transport is newline-delimited JSON-RPC 2.0 on stdin and stdout, as
// the MCP stdio transport specifies. Only tools are offered.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
)

// protocolVersions are the MCP revisions this server speaks, newest first.
var protocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Serve answers requests from r on w until r ends or ctx is done. Requests
// run concurrently, so a long wait_for_agents does not hold up the others;
// notifications/cancelled stops the request it names.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu       sync.Mutex // guards w and running
		running  = map[string]context.CancelFunc{}
		wg       sync.WaitGroup
		enc      = json.NewEncoder(w)
		writeErr error
	)
	send := func(v any) {
		mu.Lock()
		defer mu.Unlock()
		if err := enc.Encode(v); err != nil && writeErr == nil {
			writeErr = err
			cancel()
		}
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			send(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParse, "parse error: " + err.Error()}})
			continue
		}
		if req.Method == "" {
			if len(req.ID) > 0 {
				send(response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{codeInvalidRequest, "missing method"}})
			}
			continue // a response to something we never sent
		}
		if len(req.ID) == 0 {
			if req.Method == "notifications/cancelled" {
				var p struct {
					RequestID json.RawMessage `json:"requestId"`
				}
				if json.Unmarshal(req.Params, &p) == nil {
					mu.Lock()
					if c, ok := running[string(p.RequestID)]; ok {
						c()
					}
					mu.Unlock()
				}
			}
			continue // other notifications need no answer
		}
		rctx, rcancel := context.WithCancel(ctx)
		id := string(req.ID)
		mu.Lock()
		running[id] = rcancel
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, rerr := s.handle(rctx, req)
			mu.Lock()
			delete(running, id)
			mu.Unlock()
			cancelled := rctx.Err() != nil && ctx.Err() == nil
			rcancel()
			if cancelled {
				return // the client gave up on it and expects no answer
			}
			resp := response{JSONRPC: "2.0", ID: req.ID}
			if rerr != nil {
				resp.Error = rerr
			} else {
				resp.Result = result
			}
			send(resp)
		}()
	}
	wg.Wait()
	if writeErr != nil {
		return writeErr
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *Server) handle(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := protocolVersions[0]
		if slices.Contains(protocolVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "agent-keepalive", "title": "agent-keepalive", "version": s.Version},
			"instructions":    instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": toolList()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{codeInvalidParams, "bad params: " + err.Error()}
		}
		t, ok := tools[p.Name]
		if !ok {
			return nil, &rpcError{codeInvalidParams, fmt.Sprintf("unknown tool %q", p.Name)}
		}
		args := p.Arguments
		if len(args) == 0 || string(args) == "null" {
			args = json.RawMessage("{}")
		}
		out, err := t.run(s, ctx, args)
		if err != nil {
			return toolError(err.Error()), nil
		}
		return toolResult(out), nil
	}
	return nil, &rpcError{codeMethodNotFound, "method not found: " + req.Method}
}

// toolResult carries out as JSON text (what models read) and as
// structuredContent (what programs read).
func toolResult(out any) map[string]any {
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return toolError(err.Error())
	}
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(b)}},
		"structuredContent": out,
	}
}

func toolError(msg string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": msg}},
		"isError": true,
	}
}

const instructions = `agent-keepalive keeps coding-agent sessions' prompt caches warm while they sit idle, so the next prompt reads the cache instead of paying to write the whole context again. It can also report how sessions are doing.

- "Keep this alive for N hours" / "until I stop it": call keep_alive with hours or untilStopped; with no target it acts on the session that runs this tool.
- Coordinating several agents: pick them with target (spawnedByMe, sessions, project, cwdPrefix, titleContains), keep them warm with keep_alive, check on them with agent_status, and block on wait_for_agents until one finishes, gets stuck or exits instead of polling.
- stop_keep_alive ends plans; savings reports what keeping warm has saved.`
