package claude

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/tail"
)

// transcript is the incrementally classified state of one
// <sessionId>.jsonl. It is the tail.Handler of the file's Tailer; all
// fields are guarded by mu because a background read may be feeding it
// while a scan or Activity reads it.
type transcript struct {
	mu   sync.Mutex
	tl   *tail.Tailer
	path string

	peerName   string        // from-name our deliverer uses
	defaultTTL time.Duration // TTL assumed for writes without a 5m/1h breakdown

	log  *tail.ActivityLog
	seen *tail.Seen[int64] // message.id -> Seq of its API call

	cur summary // what has been read so far
	pub summary // what the last Scan published

	// Classification state.
	sessionID  string    // the sessionId of the file's first record that has one
	trigger    time.Time // latest user/attachment record since the last call
	prevCallAt time.Time // At of the previous API call
	lastCall   int64     // Seq of the latest API call
	inPing     bool      // the latest prompt was a keepalive ping
	pingID     string
}

// summary is the per-session state a Scan reports.
type summary struct {
	totals      model.Usage
	lastUsage   model.Usage
	lastModel   string
	lastCall    time.Time // anchor (request time) of the latest call
	lastHuman   time.Time
	lastAny     time.Time
	customTitle string
	aiTitle     string
	// awaySummary is Claude Code's latest recap of the session, written
	// for a user coming back to it.
	awaySummary string
	toolPending bool // the latest call stopped for tool_use and no turn_duration followed
	// lastRequest is when the main agent last sent an API request (its
	// api-request record, written by Claude Code 2.1.300 and later);
	// lastResponse when it last recorded a response, errors included.
	lastRequest  time.Time
	lastResponse time.Time
	ttl          time.Duration // tier of the most recent cache write (0 = none seen)
}

// newTranscript returns the state for path, adding to log (which a caller
// may carry over from an earlier transcript of the same session).
func newTranscript(path, peerName string, defaultTTL time.Duration, log *tail.ActivityLog, opt tail.Options) *transcript {
	t := &transcript{
		path:       path,
		peerName:   peerName,
		defaultTTL: defaultTTL,
		log:        log,
		seen:       tail.NewSeen[int64](0),
		lastCall:   -1,
	}
	t.tl = tail.New(path, t, opt)
	return t
}

// poll reads what was appended. While a background read is in progress it
// returns loading and the published state is left as it was.
func (t *transcript) poll() (loading bool, err error) {
	st, err := t.tl.Poll()
	if st.Loading {
		return true, err
	}
	t.mu.Lock()
	t.log.Publish()
	t.pub = t.cur
	t.mu.Unlock()
	return false, err
}

// requestTimeout bounds how long a request without a response is believed
// to be in flight; a crash can leave one unanswered.
const requestTimeout = 30 * time.Minute

// calling reports whether a request of the main agent is in flight. It is
// only known when the transcript has api-request records (see tracksRequests).
func (p summary) calling(now time.Time) bool {
	return p.lastRequest.After(p.lastResponse) && now.Sub(p.lastRequest) < requestTimeout
}

// tracksRequests reports whether the transcript records the main agent's
// API requests, which telling a quiet session from a working one needs.
func (p summary) tracksRequests() bool { return !p.lastRequest.IsZero() }

// quietSince is when the main agent last sent or received an API call.
func (p summary) quietSince() time.Time {
	if p.lastRequest.After(p.lastResponse) {
		return p.lastRequest
	}
	return p.lastResponse
}

func (t *transcript) published() summary {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pub
}

func (t *transcript) activity(after int64) []model.Activity {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.log.Since(after)
}

// Reset is called by the tailer when the file was replaced or shrank. The
// history already handed out stays valid (Seq keeps counting); an ActReset
// tells consumers that what follows is a re-read.
func (t *transcript) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cur = summary{}
	t.seen.Clear()
	t.sessionID, t.trigger, t.prevCallAt = "", time.Time{}, time.Time{}
	t.lastCall, t.inPing, t.pingID = -1, false, ""
	t.log.Add(model.Activity{At: time.Now().Round(0), Kind: model.ActReset})
}

// Structural byte patterns. Inside JSON strings quotes are escaped, so these
// only match real keys; they let most lines be skipped without decoding.
var (
	patAssistant  = []byte(`"type":"assistant"`)
	patUser       = []byte(`"type":"user"`)
	patAttachment = []byte(`"type":"attachment"`)
	patSystem     = []byte(`"type":"system"`)
	patCustom     = []byte(`"type":"custom-title"`)
	patAITitle    = []byte(`"type":"ai-title"`)
	patSidechain  = []byte(`"isSidechain":true`)
	patTurnDur    = []byte(`"subtype":"turn_duration"`)
	patRequest    = []byte(`"type":"api-request"`)
	patCompact    = []byte(`"subtype":"compact_boundary"`)
	patAway       = []byte(`"subtype":"away_summary"`)
)

type rawRecord struct {
	Type             string `json:"type"`
	Subtype          string `json:"subtype"`
	Timestamp        string `json:"timestamp"`
	SessionID        string `json:"sessionId"`
	IsSidechain      bool   `json:"isSidechain"`
	IsMeta           bool   `json:"isMeta"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	CustomTitle      string `json:"customTitle"`
	AITitle          string `json:"aiTitle"`
	// Content is a system record's text (a message's is Message.Content).
	Content json.RawMessage `json:"content"`
	Origin  *struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		Body string `json:"body"`
	} `json:"origin"`
	Message *struct {
		ID         string          `json:"id"`
		Model      string          `json:"model"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
		Usage      *rawUsage       `json:"usage"`
	} `json:"message"`
}

type rawUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheCreation            *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

// usage maps the API usage onto model.Usage. Older builds report only the
// write total; it is booked at defaultTTL's tier.
func (u rawUsage) usage(defaultTTL time.Duration) model.Usage {
	out := model.Usage{Input: u.InputTokens, Output: u.OutputTokens, CacheRead: u.CacheReadInputTokens}
	if u.CacheCreation != nil {
		out.CacheWrite5m, out.CacheWrite1h = u.CacheCreation.Ephemeral5m, u.CacheCreation.Ephemeral1h
	}
	// No breakdown, or one that is empty while the total is not: trust the total.
	if out.CacheWrite5m+out.CacheWrite1h == 0 && u.CacheCreationInputTokens > 0 {
		if defaultTTL > 5*time.Minute {
			out.CacheWrite1h = u.CacheCreationInputTokens
		} else {
			out.CacheWrite5m = u.CacheCreationInputTokens
		}
	}
	return out
}

// Line classifies one transcript record.
func (t *transcript) Line(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || bytes.Contains(line, patSidechain) {
		return
	}
	switch {
	case bytes.Contains(line, patAssistant),
		bytes.Contains(line, patUser),
		bytes.Contains(line, patAttachment),
		bytes.Contains(line, patCustom),
		bytes.Contains(line, patAITitle),
		bytes.Contains(line, patRequest):
	case bytes.Contains(line, patSystem):
		if !bytes.Contains(line, patTurnDur) && !bytes.Contains(line, patCompact) && !bytes.Contains(line, patAway) {
			return
		}
	default:
		return
	}
	var r rawRecord
	if err := json.Unmarshal(line, &r); err != nil || r.IsSidechain {
		return
	}
	at := parseTime(r.Timestamp)

	t.mu.Lock()
	defer t.mu.Unlock()
	if at.After(t.cur.lastAny) {
		t.cur.lastAny = at
	}
	if r.SessionID != "" && (r.Type == "user" || r.Type == "assistant") {
		switch {
		case t.sessionID == "":
			t.sessionID = r.SessionID
		case r.SessionID != t.sessionID:
			// The file goes on under another session: a resume or a new
			// session start written into it.
			t.sessionID = r.SessionID
			t.resetHere(at)
		}
	}
	switch r.Type {
	case "assistant":
		t.onAssistant(&r, at)
	case "user":
		t.onUser(&r, at)
	case "attachment":
		t.noteTrigger(at)
	case "api-request":
		if at.After(t.cur.lastRequest) {
			t.cur.lastRequest = at
		}
	case "system":
		switch r.Subtype {
		case "turn_duration":
			t.cur.toolPending = false
		case "compact_boundary":
			t.resetHere(at)
		case "away_summary":
			var text string
			if json.Unmarshal(r.Content, &text) == nil {
				text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "(disable recaps in /config)"))
				if text != "" {
					t.cur.awaySummary = text
				}
			}
		}
	case "custom-title":
		if r.CustomTitle != "" {
			t.cur.customTitle = r.CustomTitle
		}
	case "ai-title":
		if r.AITitle != "" {
			t.cur.aiTitle = r.AITitle
		}
	}
}

// resetHere records that the conversation's context was replaced
// (compaction, /clear, another session continuing the file). What comes
// next writes a new cache prefix.
func (t *transcript) resetHere(at time.Time) {
	t.inPing, t.pingID = false, ""
	t.cur.toolPending = false
	t.log.Add(model.Activity{At: at, Kind: model.ActReset})
}

func (t *transcript) noteTrigger(at time.Time) {
	if at.After(t.trigger) {
		t.trigger = at
	}
}

func (t *transcript) onAssistant(r *rawRecord, at time.Time) {
	m := r.Message
	if m != nil && at.After(t.cur.lastResponse) {
		t.cur.lastResponse = at
	}
	if m == nil || m.Model == "<synthetic>" || m.Usage == nil {
		return
	}
	u := m.Usage.usage(t.defaultTTL)
	t.cur.toolPending = m.StopReason == "tool_use"
	if m.Model != "" {
		t.cur.lastModel = m.Model
	}
	t.noteWrite(u)
	if m.ID != "" {
		if seq, ok := t.seen.Get(m.ID); ok {
			// One response split over several records: count it once,
			// keeping the latest (most complete) usage.
			if a := t.log.Get(seq); a != nil && a.Usage != nil {
				t.cur.totals = sub(t.cur.totals, *a.Usage).Add(u)
				*a.Usage = u
				a.Working = t.cur.toolPending
				if seq == t.lastCall {
					t.cur.lastUsage = u
				}
			}
			if at.After(t.prevCallAt) {
				t.prevCallAt = at
			}
			return
		}
	}
	sent := t.trigger
	if sent.IsZero() || sent.Before(t.prevCallAt) {
		// No prompt or tool result since the previous call (a continuation):
		// the request cannot have left before that response was recorded.
		sent = t.prevCallAt
	}
	if !at.IsZero() && sent.After(at) {
		sent = at
	}
	kind, pingID := model.ActAssistant, ""
	if t.inPing {
		kind, pingID = model.ActPingReply, t.pingID
	}
	t.cur.totals = t.cur.totals.Add(u)
	t.cur.lastUsage = u
	a := model.Activity{At: at, Sent: sent, Kind: kind, Model: m.Model, Usage: &u, PingID: pingID, Working: t.cur.toolPending}
	if anchor := a.Anchor(); anchor.After(t.cur.lastCall) {
		t.cur.lastCall = anchor
	}
	t.lastCall = t.log.Add(a)
	if m.ID != "" {
		t.seen.Put(m.ID, t.lastCall)
	}
	t.trigger = time.Time{}
	if at.After(t.prevCallAt) {
		t.prevCallAt = at
	}
}

// noteWrite remembers the TTL tier of the most recent cache write.
func (t *transcript) noteWrite(u model.Usage) {
	switch {
	case u.CacheWrite1h > 0:
		t.cur.ttl = time.Hour
	case u.CacheWrite5m > 0:
		t.cur.ttl = 5 * time.Minute
	}
}

// Slash commands that start the conversation over.
var resetCommands = []string{"<command-name>/clear</command-name>", "<command-name>/compact</command-name>"}

func (t *transcript) onUser(r *rawRecord, at time.Time) {
	// Every user record (prompt, tool result, injected context) is part of
	// the next request, so the latest one is when that request left.
	t.noteTrigger(at)
	text, isToolResult := userText(r)
	if isToolResult || r.IsCompactSummary {
		return
	}
	for _, c := range resetCommands {
		if strings.Contains(text, c) {
			t.resetHere(at)
			return
		}
	}
	if id, ok := pingOf(r, text, t.peerName); ok {
		t.inPing, t.pingID = true, id
		t.log.Add(model.Activity{At: at, Kind: model.ActPing, PingID: id})
		return
	}
	switch {
	case r.Origin != nil:
		// Any other origin (another peer, a task notification, ...) is a new
		// prompt; replies to it are ordinary calls.
		t.inPing, t.pingID = false, ""
		if r.Origin.Kind == "human" && !r.IsMeta {
			t.human(at)
		}
	case r.IsMeta:
		// Injected context (caveats, skill bodies): not a new prompt.
	default:
		t.inPing, t.pingID = false, ""
		if text != "" && !strings.HasPrefix(strings.TrimSpace(text), "<") {
			t.human(at) // builds without origin: plain typed text
		}
	}
}

func (t *transcript) human(at time.Time) {
	if at.After(t.cur.lastHuman) {
		t.cur.lastHuman = at
	}
	t.log.Add(model.Activity{At: at, Kind: model.ActHuman})
}

// markerPrefix starts both "[keepalive]" and "[keepalive id=...]".
const markerPrefix = "[keepalive"

// pingOf reports whether a user record is one of our keepalive prompts. A
// peer message from our peer name always is. Otherwise the marker must lead
// the prompt (or the peer body), so a human merely quoting it is not a ping.
func pingOf(r *rawRecord, text, peerName string) (string, bool) {
	if o := r.Origin; o != nil && o.Kind == "peer" {
		id, ok := model.ParsePing(o.Body)
		if !ok {
			id, ok = model.ParsePing(text)
		}
		if o.Name == peerName {
			return id, true
		}
		if ok && strings.HasPrefix(strings.TrimSpace(o.Body), markerPrefix) {
			return id, true
		}
		return "", false
	}
	if r.Origin != nil && r.Origin.Kind != "human" {
		return "", false
	}
	s := strings.TrimSpace(text)
	if !strings.HasPrefix(s, markerPrefix) {
		return "", false
	}
	return model.ParsePing(s)
}

// userText extracts the prompt text of a user record and whether its
// content is a tool_result list.
func userText(r *rawRecord) (text string, toolResult bool) {
	if r.Message == nil || len(r.Message.Content) == 0 {
		return "", false
	}
	c := r.Message.Content
	if c[0] == '"' {
		var s string
		_ = json.Unmarshal(c, &s)
		return s, false
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(c, &parts) != nil {
		return "", false
	}
	var b strings.Builder
	for _, p := range parts {
		switch p.Type {
		case "tool_result":
			return "", true
		case "text":
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(p.Text)
		}
	}
	return b.String(), false
}

func sub(a, b model.Usage) model.Usage {
	return model.Usage{
		Input:        a.Input - b.Input,
		Output:       a.Output - b.Output,
		CacheRead:    a.CacheRead - b.CacheRead,
		CacheWrite5m: a.CacheWrite5m - b.CacheWrite5m,
		CacheWrite1h: a.CacheWrite1h - b.CacheWrite1h,
	}
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
