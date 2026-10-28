//go:build live

package codex

// Live check against the real CODEX_HOME: it scans and logs what it finds
// and never queues anything.
//
//	go test -tags live -run TestLive -v ./internal/codex/

import (
	"context"
	"testing"
	"time"
)

func TestLive(t *testing.T) {
	src := NewSource(Options{})
	start := time.Now()
	ss, err := src.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("scan: %d sessions in %v", len(ss), time.Since(start))
	for _, s := range ss {
		t.Logf("%s pid=%d status=%s reach=%s %q ctx=%d bin=%s acts=%d",
			s.Key, s.PID, s.Status, s.Reach, s.Title, s.ContextTokens, s.CodexBin, len(src.Activity(s.Key, 0)))
	}
}
