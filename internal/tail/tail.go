// Package tail reads JSONL transcripts incrementally. One Tailer follows one
// file: it remembers how far it has read, the file's size and identity, and
// a partial last line, so each Poll hands over only the complete lines
// appended since the previous one.
//
// The rules (docs/architecture.md, "Tailing rules"):
//
//   - A file that was replaced (different identity) or shrank is read again
//     from the start; the Handler's Reset is called first.
//   - A last line without its newline is kept until it is complete. A line
//     longer than MaxLine is skipped, never buffered further.
//   - On first sight of a file larger than MaxInitial only its last
//     MaxInitial bytes are read, starting after the first newline in them.
//   - When more than SyncLimit bytes are waiting (typically the first read of
//     a large transcript) the read happens on a background goroutine and Poll
//     returns at once with Loading set. The caller reports the session as
//     not ready until a later Poll returns Loading false, so one huge
//     transcript never delays the scan of other sessions.
//
// Identity is os.SameFile over the FileInfo of the open file: device and
// inode on unix, volume serial and file index on Windows (File.Stat reads
// them from the handle, so the comparison is about the file that was read,
// not whatever the path names now). A file rewritten in place to exactly
// the same size keeps its identity and is not detected.
package tail

import (
	"bytes"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// Defaults for Options.
const (
	DefaultMaxLine    = 8 << 20
	DefaultMaxInitial = 64 << 20
	DefaultSyncLimit  = 4 << 20
)

// Handler receives what a Tailer reads. Calls never overlap, but they may
// come from a background goroutine, so a Handler that is also read from
// elsewhere guards its own state.
type Handler interface {
	// Reset is called before the file is read again from another offset than
	// where the last read stopped: the file was replaced or shrank. It is not
	// called on the very first read.
	Reset()
	// Line is called for each complete line, without its trailing newline
	// (a trailing "\r" is kept). b is only valid during the call.
	Line(b []byte)
}

// Options tune a Tailer. Zero values select the defaults.
type Options struct {
	MaxLine    int   // longest line kept; longer ones are skipped
	MaxInitial int64 // on first sight read at most this many trailing bytes
	SyncLimit  int64 // more pending bytes than this are read in the background
}

// Status is the result of a Poll.
type Status struct {
	// Loading is set while a background read is in progress. The Handler
	// has seen only part of the file.
	Loading bool
	// Reset is set when the file was replaced or shrank since the previous
	// Poll (Handler.Reset was called).
	Reset bool
	// Skipped counts bytes not handed to the Handler since the previous
	// Poll: the head of a large file and oversized lines.
	Skipped int64
}

// Tailer follows one file. It is safe for concurrent use.
type Tailer struct {
	path string
	h    Handler
	opt  Options

	loading atomic.Bool
	bg      sync.WaitGroup

	mu       sync.Mutex // held for the whole of a read
	ident    os.FileInfo
	off      int64  // bytes consumed, including the buffered partial line
	partial  []byte // incomplete last line
	skipping bool   // discarding up to the next newline
	reset    bool   // a reset happened that the caller has not been told about
	skipped  int64
	err      error // from a background read, reported by the next Poll
}

// New returns a Tailer for path that feeds h.
func New(path string, h Handler, opt Options) *Tailer {
	if opt.MaxLine <= 0 {
		opt.MaxLine = DefaultMaxLine
	}
	if opt.MaxInitial <= 0 {
		opt.MaxInitial = DefaultMaxInitial
	}
	if opt.SyncLimit <= 0 {
		opt.SyncLimit = DefaultSyncLimit
	}
	return &Tailer{path: path, h: h, opt: opt}
}

// Path is the file being followed.
func (t *Tailer) Path() string { return t.path }

// Offset is how many bytes of the file have been consumed.
func (t *Tailer) Offset() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.off
}

// Loading reports whether a background read is in progress.
func (t *Tailer) Loading() bool { return t.loading.Load() }

// Poll reads what was appended since the last call. It never blocks on a
// background read: while one runs it returns Loading at once. The error of
// a failed background read is returned by the next Poll. A missing file is
// returned as an error satisfying errors.Is(err, fs.ErrNotExist).
func (t *Tailer) Poll() (Status, error) {
	if t.loading.Load() {
		return Status{Loading: true}, nil
	}
	t.mu.Lock()
	if err := t.err; err != nil {
		t.err = nil
		st := t.takeStatus()
		t.mu.Unlock()
		return st, err
	}
	f, fi, err := t.open()
	if err != nil {
		t.mu.Unlock()
		return Status{}, err
	}
	start := t.prepare(fi)
	if fi.Size()-start > t.opt.SyncLimit {
		t.loading.Store(true)
		t.bg.Add(1)
		t.mu.Unlock()
		go func() {
			defer t.bg.Done()
			t.mu.Lock()
			defer t.mu.Unlock()
			defer t.loading.Store(false)
			defer f.Close()
			t.err = t.read(f, start)
		}()
		return Status{Loading: true}, nil
	}
	defer t.mu.Unlock()
	defer f.Close()
	err = t.read(f, start)
	return t.takeStatus(), err
}

// Wait blocks until no background read is in progress. Tests use it.
func (t *Tailer) Wait() { t.bg.Wait() }

func (t *Tailer) takeStatus() Status {
	st := Status{Reset: t.reset, Skipped: t.skipped}
	t.reset, t.skipped = false, 0
	return st
}

func (t *Tailer) open() (*os.File, os.FileInfo, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, errors.New("tail: " + t.path + " is not a regular file")
	}
	return f, fi, nil
}

// prepare decides where reading starts, resetting on replacement or
// shrinkage and capping the first read of a large file. Called with mu held.
func (t *Tailer) prepare(fi os.FileInfo) int64 {
	if t.ident != nil && (!os.SameFile(t.ident, fi) || fi.Size() < t.off) {
		t.ident, t.off, t.partial, t.skipping = nil, 0, nil, false
		t.reset = true
		t.h.Reset()
	}
	if t.ident == nil {
		t.ident = fi
		t.off = 0
		if fi.Size() > t.opt.MaxInitial {
			t.off = fi.Size() - t.opt.MaxInitial
			t.skipped += t.off
			t.skipping = true // the first bytes are the tail of a line
		}
	}
	return t.off
}

// read consumes the file from off to EOF. Called with mu held.
func (t *Tailer) read(f *os.File, off int64) error {
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, 256<<10)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			t.off += int64(n)
			t.feed(buf[:n])
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// feed splits b into lines, joining the buffered partial line.
func (t *Tailer) feed(b []byte) {
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			t.hold(b)
			return
		}
		seg := b[:i]
		b = b[i+1:]
		switch {
		case t.skipping:
			t.skipped += int64(len(seg)) + 1
			t.skipping = false
		case len(t.partial) > 0:
			if len(t.partial)+len(seg) > t.opt.MaxLine {
				t.skipped += int64(len(t.partial)+len(seg)) + 1
			} else {
				t.h.Line(append(t.partial, seg...))
			}
			t.partial = t.partial[:0]
			if cap(t.partial) > 1<<20 {
				t.partial = nil // do not pin a huge buffer
			}
		case len(seg) > t.opt.MaxLine:
			t.skipped += int64(len(seg)) + 1
		default:
			t.h.Line(seg)
		}
	}
}

// hold buffers an incomplete line, giving up on it once it exceeds MaxLine.
func (t *Tailer) hold(b []byte) {
	if t.skipping {
		t.skipped += int64(len(b))
		return
	}
	if len(t.partial)+len(b) > t.opt.MaxLine {
		t.skipped += int64(len(t.partial) + len(b))
		t.partial = nil
		t.skipping = true
		return
	}
	t.partial = append(t.partial, b...)
}
