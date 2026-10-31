// Package telemetry builds the daily usage report (docs/metrics.md), keeps
// the ones not sent yet in a queue and sends them to the hub.
//
// A report is pseudonymous: the only identifier is a random install id made
// on this machine. Report content is never logged.
package telemetry

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// Schema is the report schema version. Schema 2 added Experiments and
// schema 3 PingContext. A report is sent as the lowest schema that holds
// what it has (schema 1 without either), so that older hubs take it.
const Schema = 3

// Report is one install's usage for one UTC day: the body POSTed to the
// hub. The field order is the documented one.
type Report struct {
	Schema      int            `json:"schema"`
	InstallID   string         `json:"installId"`
	Day         string         `json:"day"`
	Version     string         `json:"version"`
	OS          string         `json:"os"`
	Arch        string         `json:"arch"`
	Profile     string         `json:"profile"`
	InstallKind string         `json:"installKind"`
	Sessions    map[string]int `json:"sessions"`
	Plans       Plans          `json:"plans"`
	Pings       Pings          `json:"pings"`
	Outcomes    []Outcome      `json:"outcomes"`
	// Experiments sums the experiment episodes closed on the day; absent
	// when there are none (docs/experiments.md).
	Experiments []model.ExperimentSum `json:"experiments,omitempty"`
	// PingContext counts the day's pings by the context size they were sent
	// at, in buckets; absent, before schema 3, when there are none.
	PingContext []PingContext `json:"pingContext,omitempty"`
}

// PingContext counts the pings sent on the day to sessions of one harness
// and model with a context of FromTokens to FromTokens+50k tokens (1M: 1M
// or more).
type PingContext struct {
	Harness    string `json:"harness"`
	Model      string `json:"model"`
	FromTokens int64  `json:"fromTokens"`
	Pings      int    `json:"pings"`
}

// Plans counts the plans started on the day.
type Plans struct {
	Started    int     `json:"started"`
	Indefinite int     `json:"indefinite"`
	Hours      float64 `json:"hours"`
}

// Pings counts the pings sent on the day, by result.
type Pings struct {
	Sent     int     `json:"sent"`
	Verified int     `json:"verified"`
	Cold     int     `json:"cold"`
	Failed   int     `json:"failed"`
	CostUSD  float64 `json:"costUsd"`
}

// Outcome sums the ledger entries closed on the day with the same harness,
// model, outcome and reason.
type Outcome struct {
	Harness       string  `json:"harness"`
	Model         string  `json:"model"`
	Outcome       string  `json:"outcome"`
	EndedBy       string  `json:"endedBy"`
	Count         int     `json:"count"`
	Pings         int     `json:"pings"`
	PingCostUSD   float64 `json:"pingCostUsd"`
	AvoidedUSD    float64 `json:"avoidedUsd"`
	NetUSD        float64 `json:"netUsd"`
	ContextTokens int64   `json:"contextTokens"`
	IdleMinutes   int64   `json:"idleMinutes"`
}

// Build describes the build a report comes from.
type Build struct {
	Version, OS, Arch, Profile, InstallKind string
}

// BuildReport makes the report for one day of stats. Every string is
// sanitised to what the hub accepts and every number made finite and
// non-negative (netUsd may be negative).
func BuildReport(installID string, b Build, ds model.DayStats) Report {
	r := Report{
		Schema:      Schema,
		InstallID:   Sanitize(installID),
		Day:         Sanitize(ds.Day),
		Version:     Sanitize(b.Version),
		OS:          Sanitize(b.OS),
		Arch:        Sanitize(b.Arch),
		Profile:     Sanitize(b.Profile),
		InstallKind: Sanitize(b.InstallKind),
		Sessions:    map[string]int{},
		Plans: Plans{
			Started:    count(ds.Plans.Started),
			Indefinite: count(ds.Plans.Indefinite),
			Hours:      amount(ds.Plans.Hours),
		},
		Pings: Pings{
			Sent:     count(ds.Pings.Sent),
			Verified: count(ds.Pings.Verified),
			Cold:     count(ds.Pings.Cold),
			Failed:   count(ds.Pings.Failed),
			CostUSD:  amount(ds.Pings.CostUSD),
		},
		Outcomes:    []Outcome{},
		Experiments: []model.ExperimentSum{},
	}
	for h, n := range ds.Sessions {
		if n > 0 {
			r.Sessions[Sanitize(string(h))] += n
		}
	}
	type key struct{ h, m, o, e string }
	idx := map[key]int{}
	for _, o := range ds.Outcomes {
		k := key{Sanitize(string(o.Harness)), Sanitize(o.Model), Sanitize(string(o.Outcome)), Sanitize(o.EndedBy)}
		i, ok := idx[k]
		if !ok {
			i = len(r.Outcomes)
			idx[k] = i
			r.Outcomes = append(r.Outcomes, Outcome{Harness: k.h, Model: k.m, Outcome: k.o, EndedBy: k.e})
		}
		s := &r.Outcomes[i]
		s.Count += count(o.Count)
		s.Pings += count(o.Pings)
		s.PingCostUSD += amount(o.PingCostUSD)
		s.AvoidedUSD += amount(o.AvoidedUSD)
		s.NetUSD += finite(o.NetUSD)
		s.ContextTokens += max(o.ContextTokens, 0)
		s.IdleMinutes += max(o.IdleMinutes, 0)
	}
	for i := range r.Outcomes {
		s := &r.Outcomes[i]
		s.PingCostUSD, s.AvoidedUSD, s.NetUSD = round(s.PingCostUSD), round(s.AvoidedUSD), round(s.NetUSD)
	}
	sort.SliceStable(r.Outcomes, func(i, j int) bool {
		a, b := r.Outcomes[i], r.Outcomes[j]
		if a.Harness != b.Harness {
			return a.Harness < b.Harness
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		if a.Outcome != b.Outcome {
			return a.Outcome < b.Outcome
		}
		return a.EndedBy < b.EndedBy
	})
	r.Experiments = experiments(ds.Experiments)
	r.PingContext = pingContext(ds.PingContext)
	r.setSchema()
	return r
}

// pingContext sanitises ping context counts, merging those that sanitise
// to the same group.
func pingContext(in []model.PingContextSum) []PingContext {
	type key struct {
		h, m string
		from int64
	}
	idx := map[key]int{}
	var out []PingContext
	for _, c := range in {
		if c.Pings <= 0 {
			continue
		}
		k := key{Sanitize(string(c.Harness)), Sanitize(c.Model), model.PingContextBucketOf(c.FromTokens)}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, PingContext{Harness: k.h, Model: k.m, FromTokens: k.from})
		}
		out[i].Pings += c.Pings
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Harness != b.Harness {
			return a.Harness < b.Harness
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.FromTokens < b.FromTokens
	})
	return out
}

// setSchema gives r the lowest schema that holds its content, leaving out
// the optional fields it has none of.
func (r *Report) setSchema() {
	if len(r.Experiments) == 0 {
		r.Experiments = nil
	}
	if len(r.PingContext) == 0 {
		r.PingContext = nil
	}
	switch {
	case r.PingContext != nil:
		r.Schema = 3
	case r.Experiments != nil:
		r.Schema = 2
	default:
		r.Schema = 1
	}
}

// experiments sanitises experiment sums, merging those that sanitise to
// the same group.
// withoutExperiments returns a queued report body without its experiments,
// or body as it is when it has none (or cannot be read).
func withoutExperiments(body []byte) []byte {
	return rewrite(body, func(r *Report) bool {
		had := len(r.Experiments) > 0
		r.Experiments = nil
		return had
	})
}

// withoutPingContext returns a report body without its ping context
// counts, for a hub from before schema 3, or body as it is when it has
// none (or cannot be read).
func withoutPingContext(body []byte) []byte {
	return rewrite(body, func(r *Report) bool {
		had := len(r.PingContext) > 0
		r.PingContext = nil
		return had
	})
}

// rewrite applies change to a report body and re-encodes it at the schema
// its content then needs; body is returned as it is when change reports
// that nothing changed or the body cannot be read.
func rewrite(body []byte, change func(*Report) bool) []byte {
	var r Report
	if err := json.Unmarshal(body, &r); err != nil || !change(&r) {
		return body
	}
	r.setSchema()
	b, err := json.Marshal(r)
	if err != nil {
		return body
	}
	return b
}

func experiments(in []model.ExperimentSum) []model.ExperimentSum {
	type key struct{ x, a, h, m string }
	idx := map[key]int{}
	out := []model.ExperimentSum{}
	for _, e := range in {
		k := key{Sanitize(e.Experiment), Sanitize(e.Arm), Sanitize(string(e.Harness)), Sanitize(e.Model)}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, model.ExperimentSum{Experiment: k.x, Arm: k.a, Harness: model.Harness(k.h), Model: k.m})
		}
		s := &out[i]
		s.Episodes += count(e.Episodes)
		s.Returned += count(e.Returned)
		s.Warm += count(e.Warm)
		s.Pings += count(e.Pings)
		s.PingCalls += count(e.PingCalls)
		s.PingCostUSD += finite(max(e.PingCostUSD, 0))
		s.RewriteUSD += finite(max(e.RewriteUSD, 0))
		s.CostSqUSD += finite(max(e.CostSqUSD, 0))
		s.ContextTokens += max(e.ContextTokens, 0)
		s.QuietMinutes += max(e.QuietMinutes, 0)
	}
	for i := range out {
		s := &out[i]
		s.PingCostUSD, s.RewriteUSD = round(s.PingCostUSD), round(s.RewriteUSD)
		s.CostUSD = round(s.PingCostUSD + s.RewriteUSD)
		s.CostSqUSD = round(s.CostSqUSD)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Experiment != b.Experiment {
			return a.Experiment < b.Experiment
		}
		if a.Arm != b.Arm {
			return a.Arm < b.Arm
		}
		if a.Harness != b.Harness {
			return a.Harness < b.Harness
		}
		return a.Model < b.Model
	})
	return out
}

// empty reports whether a day had no activity at all.
func empty(ds model.DayStats) bool {
	for _, n := range ds.Sessions {
		if n > 0 {
			return false
		}
	}
	return ds.Plans == (model.DayPlans{}) && ds.Pings == (model.DayPings{}) && len(ds.Outcomes) == 0 &&
		len(ds.Experiments) == 0 && len(ds.PingContext) == 0
}

// MaxLen is the longest string the hub accepts.
const MaxLen = 64

// Sanitize makes s acceptable to the hub: at most 64 characters of
// [A-Za-z0-9._:/-]. Other characters become "_"; an empty result is
// "unknown".
func Sanitize(s string) string {
	var b strings.Builder
	for _, c := range s {
		if b.Len() >= MaxLen {
			break
		}
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == ':', c == '/', c == '-':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

func count(n int) int { return max(n, 0) }

func finite(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

func amount(f float64) float64 { return round(max(finite(f), 0)) }

// round keeps 6 decimals, enough for fractions of a cent.
func round(f float64) float64 { return math.Round(f*1e6) / 1e6 }

// dayLayout is a UTC day as reports name it.
const dayLayout = "2006-01-02"

func dayOf(t time.Time) string { return t.UTC().Format(dayLayout) }

func parseDay(s string) (time.Time, bool) {
	t, err := time.Parse(dayLayout, s)
	return t, err == nil
}
