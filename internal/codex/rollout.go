package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/tail"
)

// rollout is the incrementally classified state of one rollout JSONL file.
// Each line is {"timestamp": RFC3339, "type": ..., "payload": {...}}. It is
// the tail.Handler of the file's Tailer; fields are guarded by mu because a
// background read may feed it while a scan reads it.
type rollout struct {
	mu   sync.Mutex
	tl   *tail.Tailer
	path string

	log  *tail.ActivityLog
	seen *tail.Seen[struct{}] // response ids of token_usage_record

	cur summary // what has been read so far
	pub summary // what the last poll published

	// Classification state.
	metaID         string    // id of the first session_meta: the file's own thread
	metaStamp      time.Time // that record's line timestamp
	replaying      bool      // inside a fork's copy of its parent's history
	inPing         bool
	pingID         string
	trigger        time.Time // latest task start, user message or tool output since the last call
	prevCallAt     time.Time
	sawUsageRecord bool  // token_usage_record seen: ignore token_count
	lastTotal      int64 // token_count fallback: last total_tokens
}

// summary is the per-thread state a Scan reports.
type summary struct {
	metaCwd       string
	metaStart     time.Time
	subagent      bool
	firstHuman    string
	model         string
	busy          bool
	busySince     time.Time // when busy last changed
	lastCall      time.Time // anchor of the latest call
	lastHuman     time.Time
	lastEvent     time.Time
	contextTokens int64
	totals        model.Usage
}

func newRollout(path string, log *tail.ActivityLog, opt tail.Options) *rollout {
	r := &rollout{path: path, log: log, seen: tail.NewSeen[struct{}](0)}
	r.tl = tail.New(path, r, opt)
	return r
}

// poll reads what was appended; see transcript.poll in the claude package.
func (r *rollout) poll() (loading bool, err error) {
	st, err := r.tl.Poll()
	if st.Loading {
		return true, err
	}
	r.mu.Lock()
	r.log.Publish()
	r.pub = r.cur
	r.mu.Unlock()
	return false, err
}

func (r *rollout) published() summary {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pub
}

func (r *rollout) activity(after int64) []model.Activity {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.log.Since(after)
}

// Reset is called by the tailer when the file was replaced or shrank.
func (r *rollout) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cur = summary{}
	r.seen.Clear()
	r.metaID, r.metaStamp, r.replaying = "", time.Time{}, false
	r.inPing, r.pingID = false, ""
	r.trigger, r.prevCallAt = time.Time{}, time.Time{}
	r.sawUsageRecord, r.lastTotal = false, 0
	r.log.Add(model.Activity{At: time.Now().Round(0), Kind: model.ActReset})
}

type line struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMeta struct {
	ID        string          `json:"id"`
	Cwd       string          `json:"cwd"`
	Timestamp string          `json:"timestamp"`
	Source    json.RawMessage `json:"source"`
}

type typed struct {
	Type string `json:"type"`
}

type message struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type rawUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

// toUsage maps Codex usage onto model.Usage. Codex reports input_tokens
// inclusive of cached and freshly written tokens, and has a single
// cache-write tier, recorded as CacheWrite5m.
func (u rawUsage) toUsage() model.Usage {
	in := u.InputTokens - u.CachedInputTokens - u.CacheWriteInputTokens
	return model.Usage{
		Input:        max(in, 0),
		Output:       u.OutputTokens,
		CacheRead:    u.CachedInputTokens,
		CacheWrite5m: u.CacheWriteInputTokens,
	}
}

type usageRecord struct {
	ResponseID string    `json:"response_id"`
	Usage      *rawUsage `json:"usage"`
}

type tokenCount struct {
	Info *struct {
		Total *rawUsage `json:"total_token_usage"`
		Last  *rawUsage `json:"last_token_usage"`
	} `json:"info"`
}

// injectedPrefixes mark user-role items that Codex adds itself.
var injectedPrefixes = []string{
	"# AGENTS.md instructions",
	"<environment_context",
	"<user_instructions",
	"<permissions instructions",
	"<skills_instructions",
	"<collaboration_mode",
	"<turn_aborted",
	"<developer_instructions",
}

func isInjected(text string) bool {
	t := strings.TrimSpace(text)
	for _, p := range injectedPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// replayWindow: a fork copies its parent's history with (almost) the
// fork's own timestamp; the first record well after it is new.
const replayWindow = 2 * time.Second

// Line classifies one rollout record.
func (r *rollout) Line(b []byte) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return
	}
	var l line
	if json.Unmarshal(b, &l) != nil {
		return
	}
	at, _ := time.Parse(time.RFC3339Nano, l.Timestamp)

	r.mu.Lock()
	defer r.mu.Unlock()
	if at.After(r.cur.lastEvent) {
		r.cur.lastEvent = at
	}
	if r.replaying && at.Sub(r.metaStamp) > replayWindow {
		r.replaying = false
	}
	switch l.Type {
	case "session_meta":
		r.sessionMeta(at, l.Payload)
	case "compacted":
		if !r.replaying {
			r.resetHere(at) // the history was replaced by a summary
		}
	case "turn_context":
		var tc struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(l.Payload, &tc) == nil && tc.Model != "" {
			r.cur.model = tc.Model
		}
	case "event_msg":
		r.event(at, l.Payload)
	case "response_item":
		if r.replaying {
			return
		}
		var m message
		if json.Unmarshal(l.Payload, &m) != nil {
			return
		}
		switch {
		case m.Type == "message" && m.Role == "user":
			r.userInput(at, m)
		case strings.HasSuffix(m.Type, "_output"): // function_call_output, custom_tool_call_output
			r.noteTrigger(at)
		}
	case "token_usage_record":
		if r.replaying {
			return
		}
		var rec usageRecord
		if json.Unmarshal(l.Payload, &rec) != nil || rec.Usage == nil {
			return
		}
		r.sawUsageRecord = true
		if rec.ResponseID != "" && !r.seen.Add(rec.ResponseID) {
			return
		}
		r.call(at, *rec.Usage)
	}
}

// sessionMeta handles the thread header. The first one names the file's
// thread. A fork follows it with its parent's header and a copy of the
// parent's history, which is skipped. The file's own header again means
// the thread was resumed into this file: a new start for the cache.
func (r *rollout) sessionMeta(at time.Time, payload json.RawMessage) {
	var m sessionMeta
	if json.Unmarshal(payload, &m) != nil {
		return
	}
	id := strings.ToLower(m.ID)
	if r.metaID == "" {
		r.metaID, r.metaStamp = id, at
		r.cur.metaCwd = m.Cwd
		r.cur.metaStart, _ = time.Parse(time.RFC3339Nano, m.Timestamp)
		if r.cur.metaStart.IsZero() {
			r.cur.metaStart = at
		}
		// Interactive threads have a string source ("cli", "vscode", ...);
		// sub-agent threads carry an object describing their parent.
		src := bytes.TrimSpace(m.Source)
		r.cur.subagent = len(src) > 0 && src[0] == '{'
		return
	}
	if id != r.metaID {
		if !r.metaStamp.IsZero() && at.Sub(r.metaStamp) <= replayWindow {
			r.replaying = true
		}
		return
	}
	r.replaying = false
	r.resetHere(at)
}

func (r *rollout) resetHere(at time.Time) {
	r.inPing, r.pingID = false, ""
	r.log.Add(model.Activity{At: at, Kind: model.ActReset})
}

func (r *rollout) event(at time.Time, payload json.RawMessage) {
	var t typed
	if json.Unmarshal(payload, &t) != nil {
		return
	}
	switch t.Type {
	case "task_started":
		r.setBusy(true, at)
		r.noteTrigger(at)
	case "task_complete", "turn_aborted":
		r.setBusy(false, at)
		// A ping's reply calls all belong to the ping's turn.
		r.inPing, r.pingID = false, ""
		if a := r.log.Last(); a != nil && a.Usage != nil {
			a.Working = false // the turn's last call
		}
	case "token_count":
		if !r.sawUsageRecord && !r.replaying {
			r.tokenCount(at, payload)
		}
	}
}

func (r *rollout) setBusy(b bool, at time.Time) {
	if r.cur.busy != b || r.cur.busySince.IsZero() {
		r.cur.busy, r.cur.busySince = b, at
	}
}

func (r *rollout) noteTrigger(at time.Time) {
	if at.After(r.trigger) {
		r.trigger = at
	}
}

// markerPrefix starts both "[keepalive]" and "[keepalive id=...]".
const markerPrefix = "[keepalive"

func (r *rollout) userInput(at time.Time, m message) {
	var parts []string
	for _, c := range m.Content {
		if (c.Type == "input_text" || c.Type == "text") && !isInjected(c.Text) {
			parts = append(parts, c.Text)
		}
	}
	if len(parts) == 0 {
		return
	}
	r.noteTrigger(at)
	text := strings.Join(parts, "\n")
	// The marker must lead the prompt: a human merely quoting it is human.
	if id, ok := model.ParsePing(text); ok && strings.HasPrefix(strings.TrimSpace(text), markerPrefix) {
		r.inPing, r.pingID = true, id
		r.log.Add(model.Activity{At: at, Kind: model.ActPing, Model: r.cur.model, PingID: id})
		return
	}
	r.inPing, r.pingID = false, ""
	if at.After(r.cur.lastHuman) {
		r.cur.lastHuman = at
	}
	if r.cur.firstHuman == "" {
		r.cur.firstHuman = strings.TrimSpace(text)
	}
	r.log.Add(model.Activity{At: at, Kind: model.ActHuman, Model: r.cur.model})
}

// tokenCount is the fallback for rollouts written before token_usage_record
// existed: count last_token_usage whenever the running total grows.
func (r *rollout) tokenCount(at time.Time, payload json.RawMessage) {
	var tc tokenCount
	if json.Unmarshal(payload, &tc) != nil || tc.Info == nil || tc.Info.Last == nil || tc.Info.Total == nil {
		return
	}
	if tc.Info.Total.TotalTokens <= r.lastTotal {
		return
	}
	r.lastTotal = tc.Info.Total.TotalTokens
	r.call(at, *tc.Info.Last)
}

func (r *rollout) call(at time.Time, u rawUsage) {
	usage := u.toUsage()
	r.cur.totals = r.cur.totals.Add(usage)
	r.cur.contextTokens = u.InputTokens
	sent := r.trigger
	if sent.IsZero() || sent.Before(r.prevCallAt) {
		sent = r.prevCallAt // a follow-up request inside the turn
	}
	if !at.IsZero() && sent.After(at) {
		sent = at
	}
	kind, id := model.ActAssistant, ""
	if r.inPing {
		kind, id = model.ActPingReply, r.pingID
	}
	// Mid-turn until task_complete says this was the last call.
	a := model.Activity{At: at, Sent: sent, Kind: kind, Model: r.cur.model, Usage: &usage, PingID: id, Working: r.cur.busy}
	if anchor := a.Anchor(); anchor.After(r.cur.lastCall) {
		r.cur.lastCall = anchor
	}
	r.log.Add(a)
	r.trigger = time.Time{}
	if at.After(r.prevCallAt) {
		r.prevCallAt = at
	}
}

// metaHeader is the first record of a rollout, read without tailing it.
type metaHeader struct {
	ID       string
	Cwd      string
	Start    time.Time
	Subagent bool
}

// readHeader reads the session_meta that opens a rollout. The line holds
// the base instructions, so it is read up to 4 MiB.
func readHeader(path string) (metaHeader, bool) {
	f, err := os.Open(path)
	if err != nil {
		return metaHeader{}, false
	}
	defer f.Close()
	br := bufio.NewReaderSize(io.LimitReader(f, 4<<20), 64<<10)
	b, err := br.ReadBytes('\n')
	if err != nil && len(b) == 0 {
		return metaHeader{}, false
	}
	var l line
	if json.Unmarshal(bytes.TrimSpace(b), &l) != nil || l.Type != "session_meta" {
		return metaHeader{}, false
	}
	var m sessionMeta
	if json.Unmarshal(l.Payload, &m) != nil {
		return metaHeader{}, false
	}
	h := metaHeader{ID: strings.ToLower(m.ID), Cwd: m.Cwd}
	h.Start, _ = time.Parse(time.RFC3339Nano, m.Timestamp)
	if h.Start.IsZero() {
		h.Start, _ = time.Parse(time.RFC3339Nano, l.Timestamp)
	}
	src := bytes.TrimSpace(m.Source)
	h.Subagent = len(src) > 0 && src[0] == '{'
	return h, true
}
