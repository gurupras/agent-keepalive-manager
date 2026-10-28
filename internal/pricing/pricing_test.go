package pricing

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestLookup(t *testing.T) {
	tb := Default()
	cases := map[string]string{
		"claude-opus-5-5":                            "Claude Opus 5.5",
		"us.anthropic.claude-opus-5-5[1m]":           "Claude Opus 5.5",
		"claude-opus-5":                              "Claude Opus 5",
		"claude-opus-5-20260101":                     "Claude Opus 5",
		"claude-sonnet-5-5":                          "Claude Sonnet 5.5",
		"claude-sonnet-5":                            "Claude Sonnet 5",
		"claude-haiku-5-5":                           "Claude Haiku 5.5",
		"claude-fable-5-1":                           "Claude Fable 5.1",
		"claude-fable-5":                             "Claude Fable 5",
		"claude-mythos-5-1":                          "Claude Mythos 5.1",
		"claude-opus-4-8":                            "Claude Opus 4.8",
		"claude-opus-4-1-20250805":                   "Claude Opus 4.1",
		"claude-opus-4-20250514":                     "Claude Opus 4",
		"us.anthropic.claude-sonnet-4-20250514-v1:0": "Claude Sonnet 4",
		"claude-sonnet-4-5-20250929":                 "Claude Sonnet 4.5",
		"claude-haiku-4-5":                           "Claude Haiku 4.5",
		"claude-3-5-haiku-20241022":                  "Claude Haiku 3.5",
		"gpt-6-astra":                                "GPT-6 Astra",
		"gpt-6.1-sol":                                "GPT-6.1 Sol",
		"gpt-6-sol":                                  "GPT-6 Sol",
		"gpt-5.5":                                    "GPT-5.5",
		"gpt-5":                                      "GPT-5",
		"gpt-5-codex":                                "GPT-5",
		"gpt-5-mini":                                 "GPT-5 mini",
		"gpt-5.1-codex-mini":                         "GPT-5.1 Codex mini",
		"openai/gpt-5.2":                             "GPT-5.2",
	}
	for id, want := range cases {
		p, ok := tb.Lookup(id)
		if !ok || p.Name != want {
			t.Errorf("Lookup(%q) = %q, %v; want %q", id, p.Name, ok, want)
		}
	}
	for _, id := range []string{"", "llama-3", "gpt-4o", "claude-unknown-9"} {
		if p, ok := tb.Lookup(id); ok {
			t.Errorf("Lookup(%q) = %q; want unknown", id, p.Name)
		}
	}
}

func TestWriteTiers(t *testing.T) {
	p, _ := Default().Lookup("claude-opus-5-5")
	if p.WritePrice(5*time.Minute) != 5 || p.WritePrice(time.Hour) != 8 {
		t.Fatalf("opus 5.5 write tiers = %v / %v", p.WritePrice(5*time.Minute), p.WritePrice(time.Hour))
	}
	o, _ := Default().Lookup("gpt-5")
	if o.WritePrice(time.Hour) != o.Input || o.WritePrice(5*time.Minute) != o.Input {
		t.Fatalf("openai writes should bill as input")
	}
	// 1M of each bucket on Opus 5.5: 4 + 20 + 0.2 + 5 + 8.
	c := p.Cost(model.Usage{Input: 1e6, Output: 1e6, CacheRead: 1e6, CacheWrite5m: 1e6, CacheWrite1h: 1e6})
	if !near(c, 37.2) {
		t.Fatalf("cost = %v", c)
	}
}

func TestBreakEven(t *testing.T) {
	cases := []struct {
		w, r float64
		n    int
	}{
		{8, 0.2, 39},    // Opus 5.5, 1h tier: 40 - 1
		{5, 0.2, 24},    // Opus 5.5, 5m tier
		{6.25, 0.5, 11}, // 12.5 - 1 = 11.5 -> 11
		{1, 1, 0},
		{1, 0, 0},
	}
	for _, c := range cases {
		if got := BreakEvenPings(c.w, c.r); got != c.n {
			t.Errorf("BreakEvenPings(%v, %v) = %d; want %d", c.w, c.r, got, c.n)
		}
	}
}

func TestCostsOpus55Hour(t *testing.T) {
	c := Default().Costs("claude-opus-5-5", 1_000_000, time.Hour, 5*time.Minute)
	if !c.Known || c.Recache != 8 || c.WritePerM != 8 || c.ReadPerM != 0.2 {
		t.Fatalf("costs = %+v", c)
	}
	// 39 pings x 55 min = 35.75 h
	if !near(c.BreakEvenHours, 35.75) {
		t.Fatalf("break-even = %v", c.BreakEvenHours)
	}
	if time.Duration(c.PingInterval) != 55*time.Minute {
		t.Fatalf("interval = %v", c.PingInterval.Std())
	}
	// 1M read at 0.2 + 5 output tokens at 20/M.
	if !near(c.Ping, 0.2+5*20/1e6) {
		t.Fatalf("ping = %v", c.Ping)
	}
}

func TestCostsFiveMinute(t *testing.T) {
	// 5m TTL, margin 1m: interval 4 min; Opus 5.5 at 5m tier: 24 pings.
	c := Default().Costs("claude-opus-5-5", 100_000, 5*time.Minute, time.Minute)
	if !near(c.BreakEvenHours, 24*4.0/60) {
		t.Fatalf("break-even = %v", c.BreakEvenHours)
	}
	if !near(c.Recache, 0.5) {
		t.Fatalf("recache = %v", c.Recache)
	}
}

func TestCostsUnknown(t *testing.T) {
	c := Default().Costs("mystery-model", 100_000, time.Hour, 5*time.Minute)
	if c.Known || c.Recache != 0 || c.Ping != 0 || c.BreakEvenHours != 0 {
		t.Fatalf("unknown model priced: %+v", c)
	}
	if c.PingInterval.Std() != 55*time.Minute {
		t.Fatalf("interval still expected for unknown models")
	}
}

func TestInterval(t *testing.T) {
	if Interval(time.Hour, 5*time.Minute) != 55*time.Minute {
		t.Fatal("1h")
	}
	if Interval(5*time.Minute, 5*time.Minute) != 150*time.Second {
		t.Fatal("margin as large as the TTL must clamp to half")
	}
}

func TestAvoided(t *testing.T) {
	p, _ := Default().Lookup("claude-opus-5-5")
	if !near(p.Avoided(1_000_000, time.Hour), 7.8) {
		t.Fatalf("avoided = %v", p.Avoided(1_000_000, time.Hour))
	}
}

func TestLoadOverride(t *testing.T) {
	dir := t.TempDir()
	if tb, err := Load(filepath.Join(dir, "missing.json")); err != nil || len(tb.Prices) != len(Default().Prices) {
		t.Fatalf("missing file: %v", err)
	}
	path := filepath.Join(dir, "pricing.json")
	os.WriteFile(path, []byte(`{"prices":[{"name":"Mine","match":"opus-5-5","input":1,"output":2,"cacheRead":0.1,"cacheWrite5m":1.5}]}`), 0o600)
	tb, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := tb.Lookup("claude-opus-5-5"); p.Name != "Mine" {
		t.Fatalf("override not first: %q", p.Name)
	}
	if p, ok := tb.Lookup("claude-sonnet-5-5"); !ok || p.Name != "Claude Sonnet 5.5" {
		t.Fatalf("default lost")
	}
	os.WriteFile(path, []byte(`{"replace":true,"prices":[{"name":"Only","match":"x","input":1,"output":1,"cacheRead":0.1}]}`), 0o600)
	tb, _ = Load(path)
	if _, ok := tb.Lookup("claude-opus-5-5"); ok {
		t.Fatal("replace kept defaults")
	}
	os.WriteFile(path, []byte(`{"prices":[{"name":"Bad","match":"(","input":1}]}`), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("bad regexp accepted")
	}
	os.WriteFile(path, []byte(`{"prices":[{"name":"Neg","match":"x","input":-1}]}`), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("negative price accepted")
	}
}
