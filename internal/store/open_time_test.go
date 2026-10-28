package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fsyncLatency measures one fsync in dir.
func fsyncLatency(t *testing.T, dir string) time.Duration {
	f, err := os.Create(filepath.Join(dir, "probe"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Write([]byte("x"))
	start := time.Now()
	f.Sync()
	return time.Since(start)
}

// Opening a fresh database must not wait on locks: migrations run on one
// connection and WAL is switched on once. What remains is a few fsyncs, so
// the bound is only meaningful on a disk with ordinary fsync latency.
func TestOpenFreshIsFast(t *testing.T) {
	dir := t.TempDir()
	if d := fsyncLatency(t, dir); d > 50*time.Millisecond {
		t.Skipf("fsync takes %v on this disk; Open's time would measure the disk, not the store", d)
	}
	start := time.Now()
	s, err := Open(filepath.Join(dir, "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	s.Close()
	if took > 500*time.Millisecond {
		t.Fatalf("Open on a fresh database took %v", took)
	}
	// Reopening an existing database is no slower.
	start = time.Now()
	s, err = Open(filepath.Join(dir, "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	took = time.Since(start)
	s.Close()
	if took > 500*time.Millisecond {
		t.Fatalf("reopen took %v", took)
	}
}

// Every pooled connection sees WAL and the DSN pragmas.
func TestOpenPragmasOnEveryConnection(t *testing.T) {
	s := open(t)
	s.db.SetMaxOpenConns(4)
	for i := 0; i < 4; i++ {
		c, err := s.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		var mode string
		var timeout, fk int
		c.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode)
		c.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&timeout)
		c.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk)
		if mode != "wal" || timeout != 5000 || fk != 1 {
			t.Fatalf("conn %d: journal_mode=%s busy_timeout=%d foreign_keys=%d", i, mode, timeout, fk)
		}
	}
}
