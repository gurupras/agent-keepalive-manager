//go:build live

package claude

// Live check against the real ~/.claude (or $CLAUDE_CONFIG_DIR): it scans
// and logs what it finds. It pings only when $KA_LIVE_CWD names the cwd of
// a throwaway session you started yourself:
//
//	KA_LIVE_CWD=/tmp/scratch go test -tags live -run TestLive -v ./internal/claude/

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

func TestLive(t *testing.T) {
	src := NewSource(Options{})
	ctx := context.Background()
	start := time.Now()
	ss, err := src.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("first scan: %d sessions in %v", len(ss), time.Since(start))
	time.Sleep(2 * time.Second)
	start = time.Now()
	if ss, err = src.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("second scan: %v", time.Since(start))
	for _, s := range ss {
		t.Logf("%s pid=%d status=%s/%s quiet=%s reach=%s %q ctx=%d ttl=%v lastCall=%v acts=%d",
			s.Key, s.PID, s.Status, s.HarnessStatus, s.Quiet, s.Reach, s.Title, s.ContextTokens, s.CacheTTL.Std(), s.LastCall, len(src.Activity(s.Key, 0)))
	}
	cwd := os.Getenv("KA_LIVE_CWD")
	if cwd == "" {
		return
	}
	d := NewDeliverer(DelivererOptions{})
	defer d.Close()
	for _, s := range ss {
		if s.Cwd == cwd {
			if err := d.Ping(ctx, s, "live1", model.FormatPing("live1")); err != nil {
				t.Fatal(err)
			}
			t.Logf("pinged %s", s.Key)
			return
		}
	}
	t.Fatalf("no session in %s", cwd)
}
