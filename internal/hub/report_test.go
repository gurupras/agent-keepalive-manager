package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 27, 15, 4, 5, 0, time.UTC)

const testInstall = "8d3c6f0e-2b7a-4f5e-9c1d-3a4b5c6d7e8f"

// validReport is the example from docs/metrics.md, as a generic value so
// each case can change one thing.
func validReport() map[string]any {
	return map[string]any{
		"schema":      1,
		"installId":   testInstall,
		"day":         "2026-10-26",
		"version":     "1.4.0",
		"os":          "linux",
		"arch":        "amd64",
		"profile":     "public",
		"installKind": "deb",
		"sessions":    map[string]any{"claude": 4, "codex": 1},
		"plans":       map[string]any{"started": 3, "indefinite": 1, "hours": 21.5},
		"pings":       map[string]any{"sent": 17, "verified": 16, "cold": 1, "failed": 0, "costUsd": 0.84},
		"outcomes": []any{map[string]any{
			"harness": "claude", "model": "claude-opus-4-1", "outcome": "saved", "endedBy": "returned",
			"count": 2, "pings": 9, "pingCostUsd": 0.41, "avoidedUsd": 7.80, "netUsd": 7.39,
			"contextTokens": 310000, "idleMinutes": 410,
		}},
	}
}

func outcome0(r map[string]any) map[string]any { return r["outcomes"].([]any)[0].(map[string]any) }

// validReport2 is the schema 2 example from docs/metrics.md: validReport
// plus experiments, a ping arm first and a hold arm second.
func validReport2() map[string]any {
	r := validReport()
	r["schema"] = 2
	r["experiments"] = []any{
		map[string]any{
			"experiment": "busy-ping-subagents-1", "arm": "ping", "harness": "claude", "model": "claude-opus-4-1",
			"episodes": 3, "returned": 3, "warm": 2, "pings": 5, "pingCalls": 5,
			"pingCostUsd": 0.35, "rewriteUsd": 1.20, "costUsd": 1.55, "costSqUsd": 1.61,
			"contextTokens": 540000, "quietMinutes": 96,
		},
		map[string]any{
			"experiment": "busy-ping-subagents-1", "arm": "hold", "harness": "claude", "model": "claude-opus-4-1",
			"episodes": 2, "returned": 2, "warm": 0, "pings": 0, "pingCalls": 0,
			"pingCostUsd": 0, "rewriteUsd": 2.10, "costUsd": 2.10, "costSqUsd": 2.25,
			"contextTokens": 380000, "quietMinutes": 140,
		},
	}
	return r
}

func exp0(r map[string]any) map[string]any { return r["experiments"].([]any)[0].(map[string]any) }
func exp1(r map[string]any) map[string]any { return r["experiments"].([]any)[1].(map[string]any) }

func encode(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeReportValid(t *testing.T) {
	r, err := DecodeReport(encode(t, validReport()), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if r.InstallID != testInstall || r.Day != "2026-10-26" || r.Pings.Sent != 17 ||
		len(r.Outcomes) != 1 || r.Outcomes[0].NetUSD != 7.39 || r.Sessions["claude"] != 4 {
		t.Fatalf("decoded %+v", r)
	}
}

func TestDecodeReportSchema2(t *testing.T) {
	r, err := DecodeReport(encode(t, validReport2()), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if r.Schema != 2 || len(r.Experiments) != 2 || r.Experiments[0].Arm != "ping" ||
		r.Experiments[0].CostSqUSD != 1.61 || r.Experiments[1].QuietMinutes != 140 || r.Outcomes[0].NetUSD != 7.39 {
		t.Fatalf("decoded %+v", r)
	}
	// A schema 1 report decodes with no experiments.
	r, err = DecodeReport(encode(t, validReport()), testNow)
	if err != nil || r.Experiments != nil {
		t.Fatalf("schema 1: %v %+v", err, r)
	}
}

// TestDecodeReportExperimentRules changes one thing in validReport2 per
// case; a refusal must name the field.
func TestDecodeReportExperimentRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
		field  string // in the refusal
		ok     bool
	}{
		// schema
		{name: "schema 1 with experiments", mutate: func(r map[string]any) { r["schema"] = 1 }, field: "experiments"},
		{name: "schema 1 with empty experiments", mutate: func(r map[string]any) { r["schema"] = 1; r["experiments"] = []any{} }, field: "experiments"},
		{name: "schema 2 without experiments", mutate: func(r map[string]any) { delete(r, "experiments") }, field: "experiments"},
		{name: "schema 2 with null experiments", mutate: func(r map[string]any) { r["experiments"] = nil }, field: "experiments"},
		{name: "schema 2 with empty experiments", mutate: func(r map[string]any) { r["experiments"] = []any{} }, ok: true},
		{name: "schema 3", mutate: func(r map[string]any) { r["schema"] = 3 }, field: "schema"},
		// structure and strings
		{name: "unknown field", mutate: func(r map[string]any) { exp0(r)["session"] = "x" }, field: "session"},
		{name: "experiment missing", mutate: func(r map[string]any) { delete(exp0(r), "experiment") }, field: "experiments[0].experiment"},
		{name: "experiment bad", mutate: func(r map[string]any) { exp0(r)["experiment"] = "busy ping" }, field: "experiments[0].experiment"},
		{name: "experiment long", mutate: func(r map[string]any) { exp0(r)["experiment"] = strings.Repeat("e", 65) }, field: "experiments[0].experiment"},
		{name: "arm missing", mutate: func(r map[string]any) { delete(exp0(r), "arm") }, field: "experiments[0].arm"},
		{name: "arm unknown", mutate: func(r map[string]any) { exp0(r)["arm"] = "control" }, field: "experiments[0].arm"},
		{name: "harness missing", mutate: func(r map[string]any) { delete(exp0(r), "harness") }, field: "experiments[0].harness"},
		{name: "harness bad", mutate: func(r map[string]any) { exp0(r)["harness"] = "a b" }, field: "experiments[0].harness"},
		{name: "model empty", mutate: func(r map[string]any) { exp0(r)["model"] = "" }, ok: true},
		{name: "model bad", mutate: func(r map[string]any) { exp0(r)["model"] = "<b>" }, field: "experiments[0].model"},
		{name: "duplicate key", mutate: func(r map[string]any) { exp1(r)["arm"] = "ping" }, field: "experiments[1]: same"},
		{name: "same key other model", mutate: func(r map[string]any) {
			e := exp1(r)
			for k, v := range exp0(r) {
				e[k] = v
			}
			e["model"] = "claude-sonnet-4-5"
		}, ok: true},
		// counts and amounts
		{name: "episodes negative", mutate: func(r map[string]any) { exp0(r)["episodes"] = -1 }, field: "experiments[0].episodes"},
		{name: "pings negative", mutate: func(r map[string]any) { exp0(r)["pings"] = -1 }, field: "experiments[0].pings"},
		{name: "quietMinutes negative", mutate: func(r map[string]any) { exp0(r)["quietMinutes"] = -1 }, field: "experiments[0].quietMinutes"},
		{name: "contextTokens too large", mutate: func(r map[string]any) { exp0(r)["contextTokens"] = 2e12 }, field: "experiments[0].contextTokens"},
		{name: "rewriteUsd negative", mutate: func(r map[string]any) { exp0(r)["rewriteUsd"] = -0.5 }, field: "experiments[0].rewriteUsd"},
		{name: "costSqUsd negative", mutate: func(r map[string]any) { exp0(r)["costSqUsd"] = -1 }, field: "experiments[0].costSqUsd"},
		{name: "costUsd too large", mutate: func(r map[string]any) { exp0(r)["costUsd"] = 1e10 }, field: "experiments[0].costUsd"},
		{name: "too many entries", mutate: func(r map[string]any) {
			list := make([]any, 1001)
			for i := range list {
				e := map[string]any{}
				for k, v := range exp0(r) {
					e[k] = v
				}
				e["experiment"] = fmt.Sprintf("x-%d", i)
				list[i] = e
			}
			r["experiments"] = list
		}, field: "experiments: more than 1000"},
		// plausibility
		{name: "episodes zero", mutate: func(r map[string]any) { exp1(r)["episodes"] = 0 }, field: "experiments[1].episodes"},
		{name: "returned over episodes", mutate: func(r map[string]any) { exp0(r)["returned"] = 4 }, field: "experiments[0].returned"},
		{name: "warm over returned", mutate: func(r map[string]any) { exp0(r)["returned"], exp0(r)["warm"] = 1, 2 }, field: "experiments[0].warm"},
		{name: "none returned", mutate: func(r map[string]any) { exp0(r)["returned"], exp0(r)["warm"] = 0, 0 }, ok: true},
		{name: "hold with pings", mutate: func(r map[string]any) { exp1(r)["pings"] = 1 }, field: "experiments[1].pings"},
		{name: "hold with ping cost", mutate: func(r map[string]any) {
			e := exp1(r)
			e["pingCostUsd"], e["rewriteUsd"] = 0.1, 2.0
		}, field: "experiments[1].pings"},
		{name: "hold with ping calls", mutate: func(r map[string]any) { exp1(r)["pingCalls"] = 1 }, field: "experiments[1].pingCalls"},
		{name: "ping unanswered", mutate: func(r map[string]any) { exp0(r)["pingCalls"] = 0 }, ok: true},
		{name: "ping calls at cap", mutate: func(r map[string]any) { exp0(r)["pingCalls"] = 250 }, ok: true},
		{name: "ping calls over cap", mutate: func(r map[string]any) { exp0(r)["pingCalls"] = 251 }, field: "experiments[0].pingCalls"},
		{name: "context over cap", mutate: func(r map[string]any) { exp0(r)["contextTokens"] = 30_000_001 }, field: "experiments[0].contextTokens"},
		{name: "quiet minutes at cap", mutate: func(r map[string]any) { exp1(r)["quietMinutes"] = 2 * 168 * 60 }, ok: true},
		{name: "quiet minutes over cap", mutate: func(r map[string]any) { exp1(r)["quietMinutes"] = 2*168*60 + 1 }, field: "experiments[1].quietMinutes"},
		{name: "ping cost over cap", mutate: func(r map[string]any) {
			e := exp0(r)
			e["pingCostUsd"], e["costUsd"], e["costSqUsd"] = 126.0, 127.2, 10000.0
		}, field: "experiments[0].pingCostUsd"},
		{name: "rewrite past context at start", mutate: func(r map[string]any) {
			e := exp0(r)
			e["contextTokens"], e["costSqUsd"] = 10000, 1.61
		}, ok: true},
		{name: "rewrite over bound", mutate: func(r map[string]any) {
			e := exp0(r)
			e["rewriteUsd"], e["costUsd"], e["costSqUsd"] = 3001.0, 3001.35, 3001.35*3001.35
		}, field: "experiments[0].rewriteUsd"},
		{name: "too many episodes", mutate: func(r map[string]any) {
			e := exp0(r)
			e["episodes"] = 99
		}, field: "experiments: more than 100 episodes"},
		{name: "episodes at bound", mutate: func(r map[string]any) {
			e := exp0(r)
			e["episodes"] = 98
		}, ok: true},
		{name: "cost mismatch", mutate: func(r map[string]any) { exp0(r)["costUsd"] = 1.6 }, field: "experiments[0].costUsd"},
		{name: "cost rounding", mutate: func(r map[string]any) { exp0(r)["costUsd"] = 1.555 }, ok: true},
		{name: "costSq at lower bound", mutate: func(r map[string]any) { exp1(r)["costSqUsd"] = 2.205 }, ok: true},
		{name: "costSq below lower bound", mutate: func(r map[string]any) { exp1(r)["costSqUsd"] = 2.19 }, field: "experiments[1].costSqUsd"},
		{name: "costSq at upper bound", mutate: func(r map[string]any) { exp1(r)["costSqUsd"] = 4.41 }, ok: true},
		{name: "costSq above upper bound", mutate: func(r map[string]any) { exp1(r)["costSqUsd"] = 4.43 }, field: "experiments[1].costSqUsd"},
		{name: "costSq large relative rounding", mutate: func(r map[string]any) {
			// One episode of $500: costSq is 250000 give or take rounding.
			e := exp1(r)
			e["episodes"], e["returned"], e["contextTokens"] = 1, 1, 9_000_000
			e["rewriteUsd"], e["costUsd"], e["costSqUsd"] = 500.0, 500.0, 250000.2
		}, ok: true},
		{name: "zero cost", mutate: func(r map[string]any) {
			e := exp1(r)
			e["rewriteUsd"], e["costUsd"], e["costSqUsd"] = 0, 0, 0
		}, ok: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := validReport2()
			tc.mutate(r)
			_, err := DecodeReport(encode(t, r), testNow)
			if tc.ok {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("accepted (err %v)", err)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("refusal %q does not name %q", err, tc.field)
			}
		})
	}
}

func TestDecodeReportRules(t *testing.T) {
	long := strings.Repeat("a", 65)
	cases := []struct {
		name   string
		mutate func(map[string]any)
		raw    string // used instead of the mutated report when set
		ok     bool
	}{
		// structure
		{name: "not json", raw: `{"schema":`},
		{name: "array", raw: `[]`},
		{name: "trailing data", raw: string(mustJSON(validReport())) + ` {}`},
		{name: "unknown top-level field", mutate: func(r map[string]any) { r["hostname"] = "box" }},
		{name: "unknown plans field", mutate: func(r map[string]any) { r["plans"].(map[string]any)["x"] = 1 }},
		{name: "unknown pings field", mutate: func(r map[string]any) { r["pings"].(map[string]any)["x"] = 1 }},
		{name: "unknown outcome field", mutate: func(r map[string]any) { outcome0(r)["title"] = "secret" }},
		{name: "wrong type", mutate: func(r map[string]any) { r["version"] = 14 }},
		{name: "fractional count", mutate: func(r map[string]any) { r["pings"].(map[string]any)["sent"] = 1.5 }},
		{name: "huge number", raw: strings.Replace(string(mustJSON(validReport())), `"hours":21.5`, `"hours":1e400`, 1)},
		// schema
		{name: "schema missing", mutate: func(r map[string]any) { delete(r, "schema") }},
		{name: "schema 0", mutate: func(r map[string]any) { r["schema"] = 0 }},
		{name: "schema 2 without experiments", mutate: func(r map[string]any) { r["schema"] = 2 }},
		{name: "schema 3", mutate: func(r map[string]any) { r["schema"] = 3 }},
		// installId
		{name: "installId missing", mutate: func(r map[string]any) { delete(r, "installId") }},
		{name: "installId not uuid", mutate: func(r map[string]any) { r["installId"] = "my-laptop" }},
		{name: "installId upper case", mutate: func(r map[string]any) { r["installId"] = strings.ToUpper(testInstall) }, ok: true},
		// day
		{name: "day missing", mutate: func(r map[string]any) { delete(r, "day") }},
		{name: "day format", mutate: func(r map[string]any) { r["day"] = "2026/10/26" }},
		{name: "day not a date", mutate: func(r map[string]any) { r["day"] = "2026-02-30" }},
		{name: "day with time", mutate: func(r map[string]any) { r["day"] = "2026-10-26T00:00:00Z" }},
		{name: "day today", mutate: func(r map[string]any) { r["day"] = "2026-10-27" }, ok: true},
		{name: "day tomorrow", mutate: func(r map[string]any) { r["day"] = "2026-10-28" }},
		{name: "day 30 days old", mutate: func(r map[string]any) { r["day"] = "2026-09-27" }, ok: true},
		{name: "day 31 days old", mutate: func(r map[string]any) { r["day"] = "2026-09-26" }},
		// strings
		{name: "version missing", mutate: func(r map[string]any) { delete(r, "version") }},
		{name: "os missing", mutate: func(r map[string]any) { delete(r, "os") }},
		{name: "arch missing", mutate: func(r map[string]any) { delete(r, "arch") }},
		{name: "profile empty", mutate: func(r map[string]any) { r["profile"] = "" }, ok: true},
		{name: "installKind empty", mutate: func(r map[string]any) { r["installKind"] = "" }, ok: true},
		{name: "version 64 chars", mutate: func(r map[string]any) { r["version"] = "1.4.0-" + long[:58] }, ok: true},
		{name: "version 65 chars", mutate: func(r map[string]any) { r["version"] = "1.4.0-" + long[:59] }},
		{name: "version dev", mutate: func(r map[string]any) { r["version"] = "dev" }, ok: true},
		{name: "version rc", mutate: func(r map[string]any) { r["version"] = "0.1.0-rc.1" }, ok: true},
		{name: "version git describe", mutate: func(r map[string]any) { r["version"] = "0.0.0-dev.24.gc9771de" }, ok: true},
		{name: "version not semver", mutate: func(r map[string]any) { r["version"] = "latest" }},
		{name: "version space", mutate: func(r map[string]any) { r["version"] = "1.4 beta" }},
		{name: "version plus", mutate: func(r map[string]any) { r["version"] = "1.4.0+dirty" }},
		{name: "os unicode", mutate: func(r map[string]any) { r["os"] = "linüx" }},
		{name: "profile html", mutate: func(r map[string]any) { r["profile"] = "<b>" }},
		{name: "installKind path chars", mutate: func(r map[string]any) { r["installKind"] = "a_b.c:d/e-f" }, ok: true},
		{name: "arch newline", mutate: func(r map[string]any) { r["arch"] = "amd64\n" }},
		// sessions
		{name: "sessions absent", mutate: func(r map[string]any) { delete(r, "sessions") }, ok: true},
		{name: "sessions bad key", mutate: func(r map[string]any) { r["sessions"] = map[string]any{"my agent": 1} }},
		{name: "sessions empty key", mutate: func(r map[string]any) { r["sessions"] = map[string]any{"": 1} }},
		{name: "sessions negative", mutate: func(r map[string]any) { r["sessions"] = map[string]any{"claude": -1} }},
		{name: "sessions value string", mutate: func(r map[string]any) { r["sessions"] = map[string]any{"claude": "1"} }},
		{name: "sessions too many keys", mutate: func(r map[string]any) {
			m := map[string]any{}
			for i := 0; i < 65; i++ {
				m["h"+strings.Repeat("x", i)] = 1
			}
			r["sessions"] = m
		}},
		// plans
		{name: "plans.started negative", mutate: func(r map[string]any) { r["plans"].(map[string]any)["started"] = -1 }},
		{name: "plans.indefinite negative", mutate: func(r map[string]any) { r["plans"].(map[string]any)["indefinite"] = -1 }},
		{name: "plans.hours negative", mutate: func(r map[string]any) { r["plans"].(map[string]any)["hours"] = -0.5 }},
		{name: "plans.hours too large", mutate: func(r map[string]any) { r["plans"].(map[string]any)["hours"] = 1e12 }},
		// pings
		{name: "pings.sent negative", mutate: func(r map[string]any) { r["pings"].(map[string]any)["sent"] = -1 }},
		{name: "pings.verified negative", mutate: func(r map[string]any) { r["pings"].(map[string]any)["verified"] = -1 }},
		{name: "pings.cold negative", mutate: func(r map[string]any) { r["pings"].(map[string]any)["cold"] = -1 }},
		{name: "pings.failed negative", mutate: func(r map[string]any) { r["pings"].(map[string]any)["failed"] = -1 }},
		{name: "pings.costUsd negative", mutate: func(r map[string]any) { r["pings"].(map[string]any)["costUsd"] = -0.01 }},
		{name: "pings.sent too large", mutate: func(r map[string]any) { r["pings"].(map[string]any)["sent"] = 2e12 }},
		// outcomes
		{name: "outcomes absent", mutate: func(r map[string]any) { delete(r, "outcomes") }, ok: true},
		{name: "outcomes empty", mutate: func(r map[string]any) { r["outcomes"] = []any{} }, ok: true},
		{name: "outcome harness missing", mutate: func(r map[string]any) { delete(outcome0(r), "harness") }},
		{name: "outcome harness bad", mutate: func(r map[string]any) { outcome0(r)["harness"] = "a b" }},
		{name: "outcome model empty", mutate: func(r map[string]any) { outcome0(r)["model"] = "" }, ok: true},
		{name: "outcome model long", mutate: func(r map[string]any) { outcome0(r)["model"] = long }},
		{name: "outcome model bad", mutate: func(r map[string]any) { outcome0(r)["model"] = "claude opus" }},
		{name: "outcome wasted", mutate: func(r map[string]any) {
			o := outcome0(r)
			o["outcome"], o["avoidedUsd"], o["netUsd"] = "wasted", 0, -0.41
		}, ok: true},
		{name: "outcome unknown", mutate: func(r map[string]any) { outcome0(r)["outcome"] = "maybe" }},
		{name: "outcome missing", mutate: func(r map[string]any) { delete(outcome0(r), "outcome") }},
		{name: "outcome endedBy missing", mutate: func(r map[string]any) { delete(outcome0(r), "endedBy") }},
		{name: "outcome endedBy bad", mutate: func(r map[string]any) { outcome0(r)["endedBy"] = "user's choice" }},
		{name: "outcome count negative", mutate: func(r map[string]any) { outcome0(r)["count"] = -1 }},
		{name: "outcome pings negative", mutate: func(r map[string]any) { outcome0(r)["pings"] = -1 }},
		{name: "outcome contextTokens negative", mutate: func(r map[string]any) { outcome0(r)["contextTokens"] = -1 }},
		{name: "outcome idleMinutes negative", mutate: func(r map[string]any) { outcome0(r)["idleMinutes"] = -1 }},
		{name: "outcome pingCostUsd negative", mutate: func(r map[string]any) { outcome0(r)["pingCostUsd"] = -1 }},
		{name: "outcome avoidedUsd negative", mutate: func(r map[string]any) { outcome0(r)["avoidedUsd"] = -1 }},
		{name: "outcome netUsd negative", mutate: func(r map[string]any) {
			o := outcome0(r)
			o["pingCostUsd"], o["avoidedUsd"], o["netUsd"] = 4.0, 0.5, -3.5
		}, ok: true},
		{name: "outcome netUsd too large", mutate: func(r map[string]any) { outcome0(r)["netUsd"] = -1e10 }},
		// plausibility
		{name: "indefinite over started", mutate: func(r map[string]any) { r["plans"] = map[string]any{"started": 1, "indefinite": 2} }},
		{name: "plan hours at cap", mutate: func(r map[string]any) { r["plans"] = map[string]any{"started": 2, "hours": 336} }, ok: true},
		{name: "plan hours over cap", mutate: func(r map[string]any) { r["plans"] = map[string]any{"started": 2, "hours": 337} }},
		{name: "verified over sent", mutate: func(r map[string]any) { r["pings"] = map[string]any{"sent": 2, "verified": 3} }},
		{name: "cold over sent", mutate: func(r map[string]any) { r["pings"] = map[string]any{"sent": 2, "cold": 3} }},
		{name: "verified plus failed over sent", mutate: func(r map[string]any) { r["pings"] = map[string]any{"sent": 2, "verified": 2, "failed": 1} }},
		{name: "pings per session at cap", mutate: func(r map[string]any) { r["pings"] = map[string]any{"sent": 5 * 1440} }, ok: true},
		{name: "pings per session over cap", mutate: func(r map[string]any) { r["pings"] = map[string]any{"sent": 5*1440 + 1} }},
		{name: "pings without sessions", mutate: func(r map[string]any) { delete(r, "sessions"); r["pings"] = map[string]any{"sent": 1441} }},
		{name: "ping cost over cap", mutate: func(r map[string]any) { r["pings"] = map[string]any{"sent": 1, "costUsd": 25.01} }},
		{name: "outcome count zero", mutate: func(r map[string]any) { outcome0(r)["count"] = 0 }},
		{name: "outcome context over cap", mutate: func(r map[string]any) { outcome0(r)["contextTokens"] = 20_000_001 }},
		{name: "outcome avoided over context", mutate: func(r map[string]any) {
			o := outcome0(r)
			o["avoidedUsd"], o["netUsd"] = 31.5, 31.09
		}},
		{name: "outcome ping cost over cap", mutate: func(r map[string]any) {
			o := outcome0(r)
			o["pingCostUsd"], o["netUsd"] = 226.0, -218.2
		}},
		{name: "outcome net mismatch", mutate: func(r map[string]any) { outcome0(r)["netUsd"] = 7.5 }},
		{name: "outcome net rounding", mutate: func(r map[string]any) { outcome0(r)["netUsd"] = 7.395 }, ok: true},
		{name: "too many outcomes", mutate: func(r map[string]any) {
			o := outcome0(r)
			list := make([]any, 1001)
			for i := range list {
				list[i] = o
			}
			r["outcomes"] = list
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.raw)
			if tc.raw == "" {
				r := validReport()
				tc.mutate(r)
				body = encode(t, r)
			}
			_, err := DecodeReport(body, testNow)
			if tc.ok && err != nil {
				t.Fatalf("refused: %v", err)
			}
			if !tc.ok {
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("accepted (err %v)", err)
				}
			}
		})
	}
}

func TestCheckAmountNonFinite(t *testing.T) {
	// JSON cannot carry NaN or Inf; the rule still holds for decoded values.
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if checkAmount("x", v, true) == nil {
			t.Errorf("%v accepted", v)
		}
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
