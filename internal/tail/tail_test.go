package tail

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

type rec struct {
	mu     sync.Mutex
	lines  []string
	resets int
}

func (r *rec) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resets++
	r.lines = nil
}

func (r *rec) Line(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, string(b))
}

func (r *rec) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.lines)
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, p, s string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func poll(t *testing.T, tl *Tailer) Status {
	t.Helper()
	st, err := tl.Poll()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func eqLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s: lines %q, want %q", what, got, want)
	}
}

// Appending in chunks that split lines anywhere yields the same lines as
// one read.
func TestChunkedAppend(t *testing.T) {
	whole := "alpha\nbeta\n\ngamma {\"x\":1}\r\ndelta\n"
	want := []string{"alpha", "beta", "", "gamma {\"x\":1}\r", "delta"}
	for _, chunk := range []int{1, 2, 3, 7, 100} {
		p := filepath.Join(t.TempDir(), "f.jsonl")
		write(t, p, "")
		r := &rec{}
		tl := New(p, r, Options{})
		for i := 0; i < len(whole); i += chunk {
			appendTo(t, p, whole[i:min(i+chunk, len(whole))])
			poll(t, tl)
		}
		eqLines(t, "chunk", r.got(), want)
		if tl.Offset() != int64(len(whole)) {
			t.Errorf("chunk %d: offset %d, want %d", chunk, tl.Offset(), len(whole))
		}
	}
}

func TestPartialLineKeptUntilComplete(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.jsonl")
	write(t, p, "one\ntw")
	r := &rec{}
	tl := New(p, r, Options{})
	poll(t, tl)
	eqLines(t, "first", r.got(), []string{"one"})
	poll(t, tl) // nothing new
	eqLines(t, "again", r.got(), []string{"one"})
	appendTo(t, p, "o\n")
	poll(t, tl)
	eqLines(t, "completed", r.got(), []string{"one", "two"})
}

func TestTruncateRestarts(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.jsonl")
	write(t, p, "a\nb\nc\n")
	r := &rec{}
	tl := New(p, r, Options{})
	poll(t, tl)
	write(t, p, "x\n") // same file, shorter
	st := poll(t, tl)
	if !st.Reset || r.resets != 1 {
		t.Errorf("status %+v resets %d, want a reset", st, r.resets)
	}
	eqLines(t, "after truncate", r.got(), []string{"x"})
	if st := poll(t, tl); st.Reset {
		t.Error("reset reported twice")
	}
}

// A file replaced by rename with one of exactly the same size is detected
// by identity, not size.
func TestReplaceSameSize(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.jsonl")
	write(t, p, "aaa\nbbb\n")
	r := &rec{}
	tl := New(p, r, Options{})
	poll(t, tl)
	np := filepath.Join(dir, "new")
	write(t, np, "ccc\nddd\n")
	if err := os.Rename(np, p); err != nil {
		t.Fatal(err)
	}
	st := poll(t, tl)
	if !st.Reset {
		t.Errorf("replacement not detected: %+v", st)
	}
	eqLines(t, "after replace", r.got(), []string{"ccc", "ddd"})
}

func TestOversizedLineSkipped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.jsonl")
	r := &rec{}
	tl := New(p, r, Options{MaxLine: 10})
	write(t, p, "ok\n"+strings.Repeat("x", 25)+"\nfine\n")
	st := poll(t, tl)
	eqLines(t, "whole", r.got(), []string{"ok", "fine"})
	if st.Skipped != 26 {
		t.Errorf("skipped %d, want 26", st.Skipped)
	}

	// An oversized line arriving in pieces is dropped once it passes the
	// limit, and the rest of it is skipped up to its newline.
	appendTo(t, p, strings.Repeat("y", 8))
	poll(t, tl)
	appendTo(t, p, strings.Repeat("y", 8))
	poll(t, tl)
	appendTo(t, p, "yy\nafter\n")
	poll(t, tl)
	eqLines(t, "pieces", r.got(), []string{"ok", "fine", "after"})
}

func TestLargeFileReadsTailOnly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.jsonl")
	var b strings.Builder
	for i := range 100 {
		b.WriteString(strings.Repeat(string(rune('a'+i%26)), 9) + "\n") // 10 bytes a line
	}
	write(t, p, b.String())
	r := &rec{}
	tl := New(p, r, Options{MaxInitial: 35})
	st := poll(t, tl)
	// The last 35 bytes start 5 bytes into line 96 ("s..."); that fragment is skipped.
	got := r.got()
	if len(got) != 3 || got[0] != strings.Repeat("t", 9) || got[2] != strings.Repeat("v", 9) {
		t.Errorf("lines %q", got)
	}
	if st.Skipped != 1000-30 {
		t.Errorf("skipped %d, want %d", st.Skipped, 1000-30)
	}
	appendTo(t, p, "next\n")
	poll(t, tl)
	if got := r.got(); got[len(got)-1] != "next" {
		t.Errorf("append after tail read lost: %q", got)
	}
}

func TestLargeFileExactlyAtLineBoundary(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.jsonl")
	write(t, p, "aaaa\nbbbb\ncccc\n")
	r := &rec{}
	tl := New(p, r, Options{MaxInitial: 10})
	poll(t, tl)
	// The window starts exactly at "bbbb"; it cannot know that, so it skips
	// to the first newline. Losing one line of history is acceptable.
	eqLines(t, "boundary", r.got(), []string{"cccc"})
}

func TestBackgroundLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.jsonl")
	var b strings.Builder
	for range 5000 {
		b.WriteString(`{"type":"x"}` + "\n")
	}
	write(t, p, b.String())
	r := &rec{}
	tl := New(p, r, Options{SyncLimit: 1024})
	st := poll(t, tl)
	if !st.Loading {
		t.Fatal("first read of a large file should run in the background")
	}
	tl.Wait()
	st = poll(t, tl)
	if st.Loading {
		t.Fatal("still loading after Wait")
	}
	if n := len(r.got()); n != 5000 {
		t.Errorf("%d lines, want 5000", n)
	}
	appendTo(t, p, "tail\n")
	if st := poll(t, tl); st.Loading {
		t.Error("small appends must be read synchronously")
	}
	if got := r.got(); got[len(got)-1] != "tail" {
		t.Error("append lost")
	}
}

func TestMissingFile(t *testing.T) {
	tl := New(filepath.Join(t.TempDir(), "nope"), &rec{}, Options{})
	if _, err := tl.Poll(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err %v, want not-exist", err)
	}
}

func TestSeenBounded(t *testing.T) {
	s := NewSeen[int](3)
	for i, id := range []string{"a", "b", "c", "d"} {
		s.Put(id, i)
	}
	if s.Len() != 3 {
		t.Errorf("len %d", s.Len())
	}
	if _, ok := s.Get("a"); ok {
		t.Error("oldest id not evicted")
	}
	if v, ok := s.Get("d"); !ok || v != 3 {
		t.Errorf("d = %d, %v", v, ok)
	}
	s.Put("b", 9) // update keeps its slot
	if v, _ := s.Get("b"); v != 9 || s.Len() != 3 {
		t.Error("update failed")
	}
	if s.Add("c") || !s.Add("e") {
		t.Error("Add novelty wrong")
	}
	if _, ok := s.Get("b"); ok {
		t.Error("b should have been evicted by e")
	}
	s.Clear()
	if s.Len() != 0 || !s.Add("a") {
		t.Error("clear failed")
	}
}

func TestActivityLog(t *testing.T) {
	l := NewActivityLog(4)
	for range 3 {
		l.Add(model.Activity{Kind: model.ActHuman})
	}
	if got := l.Since(0); got != nil {
		t.Errorf("unpublished activities visible: %v", got)
	}
	l.Publish()
	got := l.Since(1)
	if len(got) != 2 || got[0].Seq != 2 || got[1].Seq != 3 {
		t.Errorf("Since(1) = %+v", got)
	}
	u := model.Usage{Input: 1}
	seq := l.Add(model.Activity{Kind: model.ActAssistant, Usage: &u})
	u.Input = 99 // Add copies usage
	l.Get(seq).Usage.Output = 7
	l.Publish()
	a := l.Since(3)[0]
	if a.Usage.Input != 1 || a.Usage.Output != 7 {
		t.Errorf("usage %+v", a.Usage)
	}
	a.Usage.Input = 5 // Since copies usage
	if l.Get(seq).Usage.Input != 1 {
		t.Error("Since returned shared usage")
	}
	for range 10 {
		l.Add(model.Activity{Kind: model.ActHuman})
	}
	l.Publish()
	all := l.Since(0)
	if len(all) > 5 || all[len(all)-1].Seq != 14 {
		t.Errorf("trim: %d kept, last %d", len(all), all[len(all)-1].Seq)
	}
	for i := 1; i < len(all); i++ {
		if all[i].Seq != all[i-1].Seq+1 {
			t.Fatal("Seq not consecutive")
		}
	}
	if l.Get(1) != nil {
		t.Error("trimmed activity still addressable")
	}
}
