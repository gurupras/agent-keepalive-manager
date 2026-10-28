package hub

import (
	"encoding/json"
	"errors"
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
		{name: "schema 2", mutate: func(r map[string]any) { r["schema"] = 2 }},
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
		{name: "version 64 chars", mutate: func(r map[string]any) { r["version"] = long[:64] }, ok: true},
		{name: "version 65 chars", mutate: func(r map[string]any) { r["version"] = long }},
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
		{name: "outcome wasted", mutate: func(r map[string]any) { outcome0(r)["outcome"] = "wasted"; outcome0(r)["netUsd"] = -0.41 }, ok: true},
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
		{name: "outcome netUsd negative", mutate: func(r map[string]any) { outcome0(r)["netUsd"] = -3.5 }, ok: true},
		{name: "outcome netUsd too large", mutate: func(r map[string]any) { outcome0(r)["netUsd"] = -1e10 }},
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
