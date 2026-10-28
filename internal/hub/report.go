// Package hub is agent-keepalive-hub: it receives usage reports from many
// installs, stores them, and serves the aggregate dashboard (docs/metrics.md).
package hub

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// SchemaVersion is the only report schema this hub accepts.
const SchemaVersion = 1

const (
	// MaxReportBytes bounds the body of one report.
	MaxReportBytes = 64 << 10
	// MaxReportAgeDays is how far back a report's day may be.
	MaxReportAgeDays = 30
	maxStringLen     = 64
	maxSessionKeys   = 64
	maxOutcomes      = 1000
	// Bounds on counts and amounts keep the database's sums far from
	// overflow; no real install comes near them.
	maxCount  = 1_000_000_000_000
	maxAmount = 1_000_000_000.0
)

// Report is one install's usage on one UTC day, exactly as sent.
type Report struct {
	Schema      int                   `json:"schema"`
	InstallID   string                `json:"installId"`
	Day         string                `json:"day"`
	Version     string                `json:"version"`
	OS          string                `json:"os"`
	Arch        string                `json:"arch"`
	Profile     string                `json:"profile"`
	InstallKind string                `json:"installKind"`
	Sessions    map[model.Harness]int `json:"sessions"`
	Plans       model.DayPlans        `json:"plans"`
	Pings       model.DayPings        `json:"pings"`
	Outcomes    []model.OutcomeSum    `json:"outcomes"`
}

// ValidationError is a report that is refused with 400.
type ValidationError struct{ msg string }

func (e *ValidationError) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return &ValidationError{msg: fmt.Sprintf(format, args...)}
}

var (
	safeString = regexp.MustCompile(`^[A-Za-z0-9._:/-]*$`)
	uuidRe     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// DecodeReport parses and validates one report. now decides the day window.
// The install id is normalised to lower case.
func DecodeReport(body []byte, now time.Time) (*Report, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var r Report
	if err := dec.Decode(&r); err != nil {
		return nil, invalid("malformed report: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, invalid("malformed report: data after the report")
	}
	r.InstallID = strings.ToLower(r.InstallID)
	if err := r.Validate(now); err != nil {
		return nil, err
	}
	return &r, nil
}

// Validate applies every rule of docs/metrics.md to a decoded report.
func (r *Report) Validate(now time.Time) error {
	if r.Schema != SchemaVersion {
		return invalid("schema: must be %d", SchemaVersion)
	}
	if !uuidRe.MatchString(r.InstallID) {
		return invalid("installId: must be a UUID")
	}
	if err := checkDay(r.Day, now); err != nil {
		return err
	}
	for _, f := range []struct {
		name, v  string
		required bool
	}{
		{"version", r.Version, true},
		{"os", r.OS, true},
		{"arch", r.Arch, true},
		{"profile", r.Profile, false},
		{"installKind", r.InstallKind, false},
	} {
		if err := checkString(f.name, f.v, f.required); err != nil {
			return err
		}
	}

	if len(r.Sessions) > maxSessionKeys {
		return invalid("sessions: more than %d harnesses", maxSessionKeys)
	}
	for h, n := range r.Sessions {
		if err := checkString("sessions key", string(h), true); err != nil {
			return err
		}
		if err := checkCount("sessions."+string(h), int64(n)); err != nil {
			return err
		}
	}

	if err := checkCount("plans.started", int64(r.Plans.Started)); err != nil {
		return err
	}
	if err := checkCount("plans.indefinite", int64(r.Plans.Indefinite)); err != nil {
		return err
	}
	if err := checkAmount("plans.hours", r.Plans.Hours, false); err != nil {
		return err
	}

	for name, n := range map[string]int{
		"pings.sent": r.Pings.Sent, "pings.verified": r.Pings.Verified,
		"pings.cold": r.Pings.Cold, "pings.failed": r.Pings.Failed,
	} {
		if err := checkCount(name, int64(n)); err != nil {
			return err
		}
	}
	if err := checkAmount("pings.costUsd", r.Pings.CostUSD, false); err != nil {
		return err
	}

	if len(r.Outcomes) > maxOutcomes {
		return invalid("outcomes: more than %d entries", maxOutcomes)
	}
	for i, o := range r.Outcomes {
		p := fmt.Sprintf("outcomes[%d].", i)
		if err := checkString(p+"harness", string(o.Harness), true); err != nil {
			return err
		}
		if err := checkString(p+"model", o.Model, false); err != nil {
			return err
		}
		if o.Outcome != model.OutcomeSaved && o.Outcome != model.OutcomeWasted {
			return invalid("%soutcome: must be %q or %q", p, model.OutcomeSaved, model.OutcomeWasted)
		}
		if err := checkString(p+"endedBy", o.EndedBy, true); err != nil {
			return err
		}
		for name, n := range map[string]int64{
			"count": int64(o.Count), "pings": int64(o.Pings),
			"contextTokens": o.ContextTokens, "idleMinutes": o.IdleMinutes,
		} {
			if err := checkCount(p+name, n); err != nil {
				return err
			}
		}
		if err := checkAmount(p+"pingCostUsd", o.PingCostUSD, false); err != nil {
			return err
		}
		if err := checkAmount(p+"avoidedUsd", o.AvoidedUSD, false); err != nil {
			return err
		}
		if err := checkAmount(p+"netUsd", o.NetUSD, true); err != nil {
			return err
		}
	}
	return r.checkPlausible()
}

// Plausibility bounds: the most any real install can report for one day,
// with wide headroom, so that only an impossible report is refused (and a
// refused report is dropped by the sender, not retried).
const (
	// maxPlanHours is the app's largest "Longest plan" setting.
	maxPlanHours = 168
	// maxPingsPerSession is one ping a minute, the shortest interval.
	maxPingsPerSession = 24 * 60
	// maxEntryContext is one ledger entry's context, in tokens; the largest
	// context windows today are a tenth of it.
	maxEntryContext = 10_000_000
	// maxWritePerMTok is a cache-write price, USD per 1M tokens, above any
	// list price.
	maxWritePerMTok = 100.0
	// maxPingCost is one ping, USD: a cache read of maxEntryContext at a
	// price above any list read price.
	maxPingCost = 25.0
	// netTolerance absorbs the sender's rounding of each sum to 1e-6.
	netTolerance = 0.01
)

var versionRe = regexp.MustCompile(`^(dev|[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?)$`)

// checkPlausible refuses a report whose numbers contradict each other or
// exceed what one install can do in a day (docs/metrics.md, The hub).
func (r *Report) checkPlausible() error {
	if !versionRe.MatchString(r.Version) {
		return invalid("version: must be a release version (1.2.3, 1.2.3-rc.1) or dev")
	}
	p := r.Plans
	if p.Indefinite > p.Started {
		return invalid("plans.indefinite: more than plans.started")
	}
	if p.Hours > float64(p.Started)*maxPlanHours {
		return invalid("plans.hours: more than %d per plan started", maxPlanHours)
	}
	g := r.Pings
	switch {
	case g.Verified > g.Sent:
		return invalid("pings.verified: more than pings.sent")
	case g.Cold > g.Sent:
		return invalid("pings.cold: more than pings.sent")
	case g.Failed > g.Sent-g.Verified:
		return invalid("pings.failed: more than the pings sent and not verified")
	}
	sessions := 0
	for _, n := range r.Sessions {
		sessions += n
	}
	if int64(g.Sent) > int64(max(sessions, 1))*maxPingsPerSession {
		return invalid("pings.sent: more than %d per session seen", maxPingsPerSession)
	}
	if g.CostUSD > float64(g.Sent)*maxPingCost {
		return invalid("pings.costUsd: more than $%v per ping", maxPingCost)
	}
	for i, o := range r.Outcomes {
		pre := fmt.Sprintf("outcomes[%d].", i)
		switch {
		case o.Count < 1:
			return invalid("%scount: must be at least 1", pre)
		case o.ContextTokens > int64(o.Count)*maxEntryContext:
			return invalid("%scontextTokens: more than %d per entry", pre, maxEntryContext)
		case o.AvoidedUSD > float64(o.ContextTokens)*maxWritePerMTok/1e6+netTolerance:
			return invalid("%savoidedUsd: more than re-writing its context could cost", pre)
		case o.PingCostUSD > float64(o.Pings)*maxPingCost+netTolerance:
			return invalid("%spingCostUsd: more than $%v per ping", pre, maxPingCost)
		case math.Abs(o.NetUSD-(o.AvoidedUSD-o.PingCostUSD)) > netTolerance:
			return invalid("%snetUsd: must be avoidedUsd minus pingCostUsd", pre)
		}
	}
	return nil
}

// AvoidedUSD is the total re-cache cost the report says was avoided.
func (r *Report) AvoidedUSD() float64 {
	var t float64
	for _, o := range r.Outcomes {
		t += o.AvoidedUSD
	}
	return t
}

func checkDay(day string, now time.Time) error {
	d, err := time.Parse(time.DateOnly, day)
	if err != nil || d.Format(time.DateOnly) != day {
		return invalid("day: must be YYYY-MM-DD")
	}
	today := utcDay(now)
	if d.After(today) {
		return invalid("day: in the future")
	}
	if d.Before(today.AddDate(0, 0, -MaxReportAgeDays)) {
		return invalid("day: more than %d days old", MaxReportAgeDays)
	}
	return nil
}

func checkString(name, v string, required bool) error {
	if required && v == "" {
		return invalid("%s: required", name)
	}
	if len(v) > maxStringLen {
		return invalid("%s: longer than %d characters", name, maxStringLen)
	}
	if !safeString.MatchString(v) {
		return invalid("%s: only A-Z a-z 0-9 . _ : / - are allowed", name)
	}
	return nil
}

func checkCount(name string, n int64) error {
	if n < 0 {
		return invalid("%s: must not be negative", name)
	}
	if n > maxCount {
		return invalid("%s: too large", name)
	}
	return nil
}

func checkAmount(name string, v float64, signed bool) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return invalid("%s: must be finite", name)
	}
	if !signed && v < 0 {
		return invalid("%s: must not be negative", name)
	}
	if math.Abs(v) > maxAmount {
		return invalid("%s: too large", name)
	}
	return nil
}

// utcDay is the start of the UTC day that contains t.
func utcDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
