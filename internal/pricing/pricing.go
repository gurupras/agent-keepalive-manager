// Package pricing maps model ids to list prices and derives the keepalive
// economics of a session: what a ping costs, what a re-cache costs, and how
// long keeping a cache warm stays cheaper than letting it expire.
//
// Prices are list prices in USD per million tokens. A model that is not in
// the table is still shown by the UI but is never priced: its Costs have
// Known=false and every amount is zero, so nothing is booked as saved or
// wasted on a guess.
package pricing

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// Price is the list price of one model family, USD per 1M tokens.
//
// Write5m and Write1h are the cache-write prices of the two TTL tiers. A
// provider with a single tier and no write surcharge (OpenAI) leaves both
// zero, and writes bill as input. Write1h zero with Write5m set means only
// the 5-minute tier exists.
type Price struct {
	Name    string  `json:"name"`
	Match   string  `json:"match"` // regexp matched against the normalized model id
	Input   float64 `json:"input"`
	Output  float64 `json:"output"`
	Read    float64 `json:"cacheRead"`
	Write5m float64 `json:"cacheWrite5m,omitempty"`
	Write1h float64 `json:"cacheWrite1h,omitempty"`

	re *regexp.Regexp
}

// Table is an ordered list of prices; the first match wins, so more
// specific patterns come first.
type Table struct {
	Prices []Price `json:"prices"`
}

// PingOutputTokens is the typical reply size of a keepalive ping ("pong"
// plus framing), used for the estimate in Costs before a real reply is
// priced.
const PingOutputTokens = 5

// claude builds a Claude entry from the input price: the provider's cache
// multipliers are 1.25x input for a 5-minute write and 2x for a 1-hour
// write; the read price is given because it varies per model.
func claude(name, match string, input, output, read float64) Price {
	return Price{Name: name, Match: match, Input: input, Output: output, Read: read,
		Write5m: input * 1.25, Write1h: input * 2}
}

// openai builds an OpenAI entry: cached input is discounted, writes carry
// no surcharge.
func openai(name, match string, input, output, read float64) Price {
	return Price{Name: name, Match: match, Input: input, Output: output, Read: read}
}

// Default is the built-in list-price table.
func Default() *Table {
	t := &Table{Prices: []Price{
		// Claude, newest first. A bare family pattern ("opus-5") must not
		// swallow a point release ("opus-5-5"), hence the guards.
		claude("Claude Fable 5.1", `fable-5-1`, 10, 50, 0.25),
		claude("Claude Mythos 5.1", `mythos-5-1`, 10, 50, 0.25),
		claude("Claude Fable 5", `fable-5($|-\d{8}|[^-0-9])`, 10, 50, 1.00),
		claude("Claude Mythos 5", `mythos-5($|-\d{8}|[^-0-9])`, 10, 50, 1.00),
		claude("Claude Opus 5.5", `opus-5-5`, 4, 20, 0.20),
		claude("Claude Opus 5", `opus-5($|-\d{8}|[^-0-9])`, 5, 25, 0.50),
		claude("Claude Sonnet 5.5", `sonnet-5-5`, 2, 10, 0.20),
		claude("Claude Sonnet 5", `sonnet-5($|-\d{8}|[^-0-9])`, 2, 10, 0.20),
		claude("Claude Haiku 5.5", `haiku-5-5`, 0.10, 0.50, 0.01),
		claude("Claude Opus 4.8", `opus-4-8`, 5, 25, 0.50),
		claude("Claude Opus 4.7", `opus-4-7`, 5, 25, 0.50),
		claude("Claude Opus 4.6", `opus-4-6`, 5, 25, 0.50),
		claude("Claude Opus 4.5", `opus-4-5`, 5, 25, 0.50),
		claude("Claude Opus 4.1", `opus-4-1`, 15, 75, 1.50),
		claude("Claude Opus 4", `opus-4($|-\d{8}|[^-0-9])`, 15, 75, 1.50),
		claude("Claude Sonnet 4.6", `sonnet-4-6`, 3, 15, 0.30),
		claude("Claude Sonnet 4.5", `sonnet-4-5`, 3, 15, 0.30),
		claude("Claude Sonnet 4", `sonnet-4($|-\d{8}|[^-0-9])`, 3, 15, 0.30),
		claude("Claude Haiku 4.5", `haiku-4-5`, 1, 5, 0.10),
		claude("Claude Sonnet 3.7", `3-7-sonnet`, 3, 15, 0.30),
		claude("Claude Haiku 3.5", `3-5-haiku`, 0.80, 4, 0.08),

		// OpenAI (Codex CLI). Point releases and tiers first.
		openai("GPT-6.1 Sol", `gpt-6\.1-sol`, 2.2, 11, 0.05),
		openai("GPT-6 Astra", `gpt-6-astra`, 11, 55, 1),
		openai("GPT-6 Sol", `gpt-6-sol`, 2.2, 11, 0.2),
		openai("GPT-6 Luna", `gpt-6-luna`, 0.11, 0.55, 0.01),
		openai("GPT-5.6 Sol", `gpt-5\.6-sol`, 4.4, 22, 0.4),
		openai("GPT-5.6 Terra", `gpt-5\.6-terra`, 2.2, 13.2, 0.2),
		openai("GPT-5.6 Luna", `gpt-5\.6-luna`, 0.22, 1.32, 0.02),
		openai("GPT-5.5", `gpt-5\.5`, 5.5, 33, 0.5),
		openai("GPT-5.4", `gpt-5\.4`, 2.75, 16.5, 0.125),
		openai("GPT-5.2", `gpt-5\.2`, 1.75, 14, 0.175),
		openai("GPT-5.1 Codex mini", `gpt-5\.1-codex-mini`, 0.25, 2, 0.025),
		openai("GPT-5.1", `gpt-5\.1`, 1.25, 10, 0.125),
		openai("GPT-5 mini", `gpt-5-mini`, 0.25, 2, 0.025),
		openai("GPT-5 nano", `gpt-5-nano`, 0.05, 0.40, 0.005),
		openai("GPT-5", `gpt-5($|-codex|-\d{4})`, 1.25, 10, 0.125),
		openai("GPT-OSS-120b", `gpt-oss-120b`, 0.15, 0.60, 0.0075),
		openai("GPT-OSS-20b", `gpt-oss-20b`, 0.07, 0.15, 0.003),
	}}
	if err := t.compile(); err != nil {
		panic(err) // the built-in table is a programming error if it fails
	}
	return t
}

// File is the shape of pricing.json. Its prices are tried before the
// built-in table, so a user can correct one model or add a new one without
// copying the whole table. Replace drops the built-in table entirely.
type File struct {
	Prices  []Price `json:"prices"`
	Replace bool    `json:"replace,omitempty"`
}

// Load returns the built-in table overridden by the pricing.json at path.
// A missing file is not an error.
func Load(path string) (*Table, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("pricing: %s: %w", path, err)
	}
	t := &Table{Prices: f.Prices}
	if !f.Replace {
		t.Prices = append(t.Prices, Default().Prices...)
	}
	if err := t.compile(); err != nil {
		return nil, fmt.Errorf("pricing: %s: %w", path, err)
	}
	return t, nil
}

func (t *Table) compile() error {
	for i := range t.Prices {
		p := &t.Prices[i]
		if p.Match == "" {
			return fmt.Errorf("price %q has no match pattern", p.Name)
		}
		for _, v := range []float64{p.Input, p.Output, p.Read, p.Write5m, p.Write1h} {
			if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("price %q has an invalid amount", p.Name)
			}
		}
		re, err := regexp.Compile(p.Match)
		if err != nil {
			return fmt.Errorf("price %q: %w", p.Name, err)
		}
		p.re = re
	}
	return nil
}

// normalize strips provider prefixes and suffixes so one pattern matches
// every spelling: "us.anthropic.claude-opus-5-5[1m]" -> "claude-opus-5-5".
func normalize(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if i := strings.IndexByte(id, '['); i >= 0 {
		id = id[:i]
	}
	for _, p := range []string{"us.", "eu.", "apac.", "global.", "anthropic.", "anthropic/", "openai/", "azure/"} {
		id = strings.TrimPrefix(id, p)
	}
	return id
}

// Lookup returns the price of a model id.
func (t *Table) Lookup(modelID string) (Price, bool) {
	if t == nil || modelID == "" {
		return Price{}, false
	}
	n := normalize(modelID)
	for _, p := range t.Prices {
		if p.re != nil && p.re.MatchString(n) {
			return p, true
		}
	}
	return Price{}, false
}

// WritePrice is the cache-write price at a TTL tier: anything longer than
// five minutes is the 1-hour tier where the provider has one.
func (p Price) WritePrice(ttl time.Duration) float64 {
	if ttl > 5*time.Minute && p.Write1h > 0 {
		return p.Write1h
	}
	if p.Write5m > 0 {
		return p.Write5m
	}
	return p.Input
}

// Cost prices one usage record, each write at its own tier.
func (p Price) Cost(u model.Usage) float64 {
	return (float64(u.Input)*p.Input +
		float64(u.Output)*p.Output +
		float64(u.CacheRead)*p.Read +
		float64(u.CacheWrite5m)*p.WritePrice(5*time.Minute) +
		float64(u.CacheWrite1h)*p.WritePrice(time.Hour)) / 1e6
}

// Interval is how often a plan pings: margin before the TTL. A margin that
// would leave less than half the TTL is clamped to half, so a large margin
// on a short TTL cannot turn into a ping storm.
func Interval(ttl, margin time.Duration) time.Duration {
	iv := ttl - margin
	if iv < ttl/2 {
		iv = ttl / 2
	}
	return iv
}

// BreakEvenPings is how many pings cost less than one re-cache. Keeping the
// cache warm through n pings costs n reads plus the read on return; letting
// it expire costs one write. n+1 reads = one write gives n = w/r − 1, and
// only whole pings count: floor(w/r − 1). Zero when reads are free or
// writes are not dearer than reads.
func BreakEvenPings(write, read float64) int {
	if read <= 0 || write <= read {
		return 0
	}
	// A small epsilon so exact ratios (w/r = 40) are not lost to rounding.
	return int(math.Floor(write/read - 1 + 1e-9))
}

// Costs derives the keepalive economics of a session from its model,
// context size and cache TTL.
func (t *Table) Costs(modelID string, contextTokens int64, ttl, margin time.Duration) model.Costs {
	iv := Interval(ttl, margin)
	c := model.Costs{PingInterval: model.Duration(iv)}
	p, ok := t.Lookup(modelID)
	if !ok {
		return c
	}
	c.Known = true
	w := p.WritePrice(ttl)
	c.ReadPerM, c.WritePerM, c.OutputPerM = p.Read, w, p.Output
	ctx := float64(contextTokens)
	c.Recache = ctx * w / 1e6
	c.Ping = (ctx*p.Read + PingOutputTokens*p.Output) / 1e6
	c.BreakEvenHours = float64(BreakEvenPings(w, p.Read)) * iv.Hours()
	return c
}

// Avoided is what a cache hit of cacheRead tokens saved over re-writing
// them at the TTL tier: the write it avoided minus the read it paid.
func (p Price) Avoided(cacheRead int64, ttl time.Duration) float64 {
	return float64(cacheRead) * (p.WritePrice(ttl) - p.Read) / 1e6
}
