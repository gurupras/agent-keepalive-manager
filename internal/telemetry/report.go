// Package telemetry builds the daily usage report (docs/metrics.md), keeps
// the ones not sent yet in a queue and sends them to the hub.
//
// A report is pseudonymous: the only identifier is a random install id made
// on this machine. Report content is never logged.
package telemetry

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// Schema is the report schema version.
const Schema = 1

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
		Outcomes: []Outcome{},
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
	return r
}

// empty reports whether a day had no activity at all.
func empty(ds model.DayStats) bool {
	for _, n := range ds.Sessions {
		if n > 0 {
			return false
		}
	}
	return ds.Plans == (model.DayPlans{}) && ds.Pings == (model.DayPings{}) && len(ds.Outcomes) == 0
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
