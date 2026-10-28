package claude

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
	"github.com/gurupras/agent-keepalive-manager/internal/proc"
)

// DefaultPeerName is the from-name pings carry; the Source uses it to tell
// our prompts apart from human input.
const DefaultPeerName = "keepalive"

// IOTimeout bounds connecting to and writing a session's socket.
const IOTimeout = 2 * time.Second

const tag = "cross-session-message"

// DelivererOptions configure a Deliverer.
type DelivererOptions struct {
	// ConfigDir is the Claude Code config dir whose registry is re-checked
	// before each write (default DefaultConfigDir()).
	ConfigDir string
	Name      string // from-name, [A-Za-z0-9_-]+ (default "keepalive")
	Log       *slog.Logger
	// SinkDir holds the reply-sink socket (default: a per-user directory
	// under os.TempDir()).
	SinkDir string
	// StartTime reads a pid's start token (default proc.StartTime).
	StartTime func(pid int) string
}

// Deliverer implements model.Deliverer over Claude Code's per-session
// messaging socket (peer protocol 1): one JSON line per message, no reply.
//
// The protocol names a "from" socket the receiver may answer to. The
// Deliverer owns one such reply sink and discards whatever arrives on it.
//
// The socket is a unix-domain socket on every OS, Windows included (which
// supports AF_UNIX since Windows 10 1803). A registry path in the
// `\\.\pipe\` namespace is written to as a named pipe instead.
type Deliverer struct {
	name      string
	log       *slog.Logger
	sinkDir   string
	regDir    string
	startTime func(int) string

	mu    sync.Mutex
	sink  string // path of the reply sink ("" until first Ping)
	ln    net.Listener
	owned bool // we created ln (otherwise another instance owns sink)
}

var _ model.Deliverer = (*Deliverer)(nil)

var nameRe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// NewDeliverer returns a Deliverer; see DelivererOptions for defaults.
// Characters not allowed in a peer name are replaced by '-'.
func NewDeliverer(opts DelivererOptions) *Deliverer {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.ConfigDir == "" {
		opts.ConfigDir = DefaultConfigDir()
	}
	name := strings.Trim(nameRe.ReplaceAllString(opts.Name, "-"), "-")
	if name == "" {
		name = DefaultPeerName
	}
	if opts.SinkDir == "" {
		opts.SinkDir = defaultSinkDir()
	}
	if opts.StartTime == nil {
		opts.StartTime = proc.StartTime
	}
	return &Deliverer{
		name:      name,
		log:       opts.Log.With("harness", "claude"),
		sinkDir:   opts.SinkDir,
		regDir:    filepath.Join(opts.ConfigDir, "sessions"),
		startTime: opts.StartTime,
	}
}

func (d *Deliverer) Harness() model.Harness { return model.Claude }

// Name is the sanitized from-name.
func (d *Deliverer) Name() string { return d.name }

// Ping sends text (already formatted with model.FormatPing) to the
// session's messaging socket. Immediately before connecting it re-reads
// the session's registry record: if the pid now runs another session, or
// is another process, it returns model.ErrSessionChanged. pingID is only
// logged: the marker inside text carries it.
func (d *Deliverer) Ping(ctx context.Context, s model.Session, pingID, text string) error {
	if s.Harness != "" && s.Harness != model.Claude {
		return fmt.Errorf("claude: session %s is not a Claude session", s.Key)
	}
	sock, err := d.recheck(s)
	if err != nil {
		return err
	}
	from, err := d.replySink()
	if err != nil {
		return fmt.Errorf("claude: reply sink: %w", err)
	}
	line, err := encodeMessage(newUUID(), "uds:"+from, d.name, text)
	if err != nil {
		return fmt.Errorf("claude: encode message: %w", err)
	}

	dctx, cancel := context.WithTimeout(ctx, IOTimeout)
	defer cancel()
	conn, err := dialSocket(dctx, sock)
	if err != nil {
		return fmt.Errorf("claude: connect to %s (pid %d): %w", sock, s.PID, err)
	}
	if err := writeAll(dctx, conn, line); err != nil {
		return fmt.Errorf("claude: write to %s (pid %d): %w", sock, s.PID, err)
	}
	d.log.Debug("ping delivered", "key", s.Key, "pid", s.PID, "pingId", pingID)
	return nil
}

// recheck re-reads <pid>.json and returns the socket to write to.
func (d *Deliverer) recheck(s model.Session) (string, error) {
	if s.PID <= 0 || s.ID == "" {
		return "", fmt.Errorf("claude: session %q has no pid or id", s.Key)
	}
	e, err := readEntry(d.regDir, s.PID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// The CLI rewrites the record in place; a read can catch it half written.
		time.Sleep(50 * time.Millisecond)
		e, err = readEntry(d.regDir, s.PID)
	}
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", model.ErrSessionChanged // the process exited, or no longer registers
	case err != nil:
		return "", fmt.Errorf("claude: registry: %w", err)
	case e.SessionID != s.ID:
		return "", model.ErrSessionChanged
	}
	if s.ProcStart != "" && d.startTime(s.PID) != s.ProcStart {
		return "", model.ErrSessionChanged // the pid now belongs to another process
	}
	sock := e.MessagingSocketPath
	if sock == "" {
		sock = s.SocketPath
	}
	if sock == "" {
		return "", fmt.Errorf("claude: session %s has no messaging socket: %w", s.Key, model.ErrUnreachable)
	}
	return sock, nil
}

// writeAll writes b and closes conn, giving up when ctx ends.
func writeAll(ctx context.Context, conn io.WriteCloser, b []byte) error {
	if c, ok := conn.(net.Conn); ok {
		if dl, ok := ctx.Deadline(); ok {
			_ = c.SetWriteDeadline(dl)
		}
		_, err := c.Write(b)
		if uc, ok := c.(*net.UnixConn); ok && err == nil {
			_ = uc.CloseWrite()
		}
		return errors.Join(err, c.Close())
	}
	// No deadline support (a named pipe opened as a file): write in the
	// background and abandon it on timeout, closing to unblock it.
	done := make(chan error, 1)
	go func() {
		_, err := conn.Write(b)
		done <- err
	}()
	select {
	case err := <-done:
		return errors.Join(err, conn.Close())
	case <-ctx.Done():
		conn.Close()
		return ctx.Err()
	}
}

type wireMessage struct {
	MsgV     int    `json:"msgV"`
	MsgID    string `json:"msg_id"`
	Type     string `json:"type"`
	Priority string `json:"priority"`
	From     string `json:"from"`
	Message  struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
}

// encodeMessage builds the newline-terminated wire line.
func encodeMessage(msgID, from, name, text string) ([]byte, error) {
	body := strings.ReplaceAll(text, "</"+tag, `<\/`+tag)
	m := wireMessage{MsgV: 1, MsgID: msgID, Type: "user", Priority: "next", From: from}
	m.Message.Role = "user"
	m.Message.Content = "<" + tag + ` from="` + escapeAttr(from) + `" from-name="` + escapeAttr(name) +
		`" from-mode="prompting">` + "\n" + body + "\n</" + tag + ">"
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func escapeAttr(v string) string {
	return strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;").Replace(v)
}

// replySink returns the path of the reply socket, creating it on first use.
// If another live instance already listens there, its socket is reused.
func (d *Deliverer) replySink() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sink != "" {
		return d.sink, nil
	}
	if err := os.MkdirAll(d.sinkDir, 0o700); err != nil {
		return "", err
	}
	fi, err := os.Lstat(d.sinkDir)
	if err != nil {
		return "", err
	}
	if err := checkPrivateDir(d.sinkDir, fi); err != nil {
		return "", err
	}
	path := filepath.Join(d.sinkDir, "reply.sock")
	if socketExists(path) {
		if c, err := net.DialTimeout("unix", path, IOTimeout); err == nil {
			c.Close()
			d.sink = path // owned by another running instance
			return path, nil
		}
		_ = os.Remove(path) // stale
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return "", err
	}
	if err := restrictSocket(path); err != nil {
		ln.Close()
		return "", err
	}
	d.sink, d.ln, d.owned = path, ln, true
	go discard(ln, d.log)
	return path, nil
}

// discard accepts connections and drops whatever they send.
func discard(ln net.Listener, log *slog.Logger) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				log.Debug("reply sink accept", "err", err)
			}
			return
		}
		go func() {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(30 * time.Second))
			n, _ := io.Copy(io.Discard, io.LimitReader(c, 1<<20))
			log.Debug("reply sink: discarded message", "bytes", n)
		}()
	}
}

// Close stops the reply sink and removes its socket file.
func (d *Deliverer) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var err error
	if d.owned && d.ln != nil {
		err = d.ln.Close()
		_ = os.Remove(d.sink)
	}
	d.sink, d.ln, d.owned = "", nil, false
	return err
}

// newUUID returns a random RFC 4122 version 4 UUID.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
