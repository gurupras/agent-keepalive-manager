package claude

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// subagents tells whether any sub-agent of a session is still running. Claude
// Code writes each sub-agent's transcript to
// <projects>/<dir>/<sessionId>/subagents/agent-<id>.jsonl, next to a
// .meta.json. Neither says plainly that the agent finished (the meta's
// "finished" is only written for foreground agents), so the state is read
// from the end of the transcript:
//
//   - running: its last call stopped for a tool (however long the tool
//     takes), a tool result or prompt waits for a call, or a request is in
//     flight;
//   - done: its last call ended its turn, it handed its report back
//     (SubagentHandback), or the meta says the user stopped it.
//
// A finished agent can be resumed, so files that changed are read again.
// Agents that were cut off (the process crashed or was killed) look running
// forever; only files written since the session's process started count.
type subagents struct {
	dir   string
	files map[string]subFile
}

// subFile caches one transcript's state by size and modification time.
type subFile struct {
	size    int64
	mod     time.Time
	running bool
}

func newSubagents(transcriptPath string) *subagents {
	return &subagents{
		dir:   filepath.Join(strings.TrimSuffix(transcriptPath, ".jsonl"), "subagents"),
		files: map[string]subFile{},
	}
}

// running reports whether a sub-agent written at or after since is running.
func (s *subagents) running(since time.Time) bool {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return false
	}
	seen := make(map[string]bool, len(ents))
	any := false
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		fi, err := e.Info()
		if err != nil || fi.ModTime().Before(since) {
			continue
		}
		seen[name] = true
		f, ok := s.files[name]
		if !ok || f.size != fi.Size() || !f.mod.Equal(fi.ModTime()) {
			path := filepath.Join(s.dir, name)
			f = subFile{size: fi.Size(), mod: fi.ModTime(), running: subagentRunning(path) && !stoppedByUser(path)}
			s.files[name] = f
		}
		if f.running {
			any = true
		}
	}
	for name := range s.files {
		if !seen[name] {
			delete(s.files, name)
		}
	}
	return any
}

// Reading a sub-agent's tail: start with the last tailChunk bytes and grow
// fourfold until a decisive record is found, up to tailMax.
const (
	tailChunk = 64 << 10
	tailMax   = 4 << 20
)

// subRecord is the part of a sub-agent transcript record the state needs.
type subRecord struct {
	Type    string `json:"type"`
	Message *struct {
		Model      string          `json:"model"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// handback is the tool a sub-agent calls to deliver its report; its result
// is the last record of an agent that finished that way.
const handback = "SubagentHandback"

// subagentRunning reads the end of a sub-agent transcript.
func subagentRunning(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	size := fi.Size()
	for n := int64(tailChunk); ; n *= 4 {
		start := max(size-n, 0)
		buf := make([]byte, size-start)
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return false
		}
		lines := bytes.Split(buf, []byte("\n"))
		if start > 0 {
			lines = lines[1:] // the first line may be cut
		}
		if running, ok := decide(lines); ok {
			return running
		}
		if start == 0 || n >= tailMax {
			return false
		}
	}
}

// decide walks records from the end. The latest request, response or user
// record decides; ok is false when lines hold none.
func decide(lines [][]byte) (running, ok bool) {
	// A tool result decides only together with the call it answers, which
	// comes before it.
	afterResult := false
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		switch {
		case bytes.Contains(l, patRequest):
			if !afterResult {
				return true, true // a request without a response yet
			}
		case bytes.Contains(l, patAssistant), bytes.Contains(l, patUser):
		default:
			continue
		}
		var r subRecord
		if json.Unmarshal(l, &r) != nil || r.Message == nil {
			continue
		}
		var blocks []contentBlock
		_ = json.Unmarshal(r.Message.Content, &blocks)
		switch r.Type {
		case "user":
			if afterResult {
				continue
			}
			if hasType(blocks, "tool_result") {
				afterResult = true
				continue
			}
			return true, true // a prompt waiting for a call
		case "assistant":
			calls := toolCalls(blocks)
			if afterResult {
				// The result answered this call: a handback ended the agent,
				// anything else is fed to the next request.
				return !contains(calls, handback), true
			}
			switch {
			case r.Message.Model == "<synthetic>":
				return false, true // an error or interruption ended the turn
			case len(calls) > 0:
				return !contains(calls, handback), true
			case r.Message.StopReason == "" || r.Message.StopReason == "tool_use":
				continue // a partial record of a response still being written
			default:
				return false, true
			}
		}
	}
	return false, false
}

func hasType(bs []contentBlock, t string) bool {
	for _, b := range bs {
		if b.Type == t {
			return true
		}
	}
	return false
}

func toolCalls(bs []contentBlock) []string {
	var out []string
	for _, b := range bs {
		if b.Type == "tool_use" {
			out = append(out, b.Name)
		}
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// stoppedByUser reads the sub-agent's .meta.json.
func stoppedByUser(transcript string) bool {
	b, err := os.ReadFile(strings.TrimSuffix(transcript, ".jsonl") + ".meta.json")
	if err != nil {
		return false
	}
	var m struct {
		StoppedByUser bool `json:"stoppedByUser"`
	}
	return json.Unmarshal(b, &m) == nil && m.StoppedByUser
}
