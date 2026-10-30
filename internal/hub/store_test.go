package hub

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"testing"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// forEachStore runs fn against SQLite and, when HUB_TEST_POSTGRES_URL is
// set, against an emptied Postgres database too.
func forEachStore(t *testing.T, fn func(t *testing.T, s *Store)) {
	t.Run("sqlite", func(t *testing.T) {
		fn(t, newSQLiteStore(t))
	})
	url := os.Getenv("HUB_TEST_POSTGRES_URL")
	if url == "" {
		return
	}
	t.Run("postgres", func(t *testing.T) {
		db, err := sql.Open("pgx", url)
		if err != nil {
			t.Fatal(err)
		}
		for _, tbl := range []string{"experiment_sums", "outcomes", "report_sessions", "reports", "hub_kv", "new_installs", "schema_migrations"} {
			if _, err := db.Exec(`DROP TABLE IF EXISTS ` + tbl); err != nil {
				t.Fatal(err)
			}
		}
		db.Close()
		s, err := OpenStore(context.Background(), "", url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		fn(t, s)
	})
}

func newSQLiteStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(context.Background(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mkReport(install, day string, o ...model.OutcomeSum) *Report {
	return &Report{
		Schema: 1, InstallID: install, Day: day, Version: "1.4.0", OS: "linux", Arch: "amd64",
		Profile: "public", InstallKind: "deb",
		Sessions: map[model.Harness]int{"claude": 2},
		Plans:    model.DayPlans{Started: 1, Hours: 2},
		Pings:    model.DayPings{Sent: 10, Verified: 9, Cold: 1, CostUSD: 0.5},
		Outcomes: o,
	}
}

func saved(model_ string, net, cost float64) model.OutcomeSum {
	return model.OutcomeSum{Harness: "claude", Model: model_, Outcome: model.OutcomeSaved, EndedBy: "returned",
		Count: 1, Pings: 3, PingCostUSD: cost, AvoidedUSD: net + cost, NetUSD: net}
}

func wasted(model_ string, cost float64) model.OutcomeSum {
	return model.OutcomeSum{Harness: "codex", Model: model_, Outcome: model.OutcomeWasted, EndedBy: "expired",
		Count: 1, Pings: 2, PingCostUSD: cost, NetUSD: -cost}
}

const (
	idA = "aaaaaaaa-0000-4000-8000-000000000001"
	idB = "bbbbbbbb-0000-4000-8000-000000000002"
	idC = "cccccccc-0000-4000-8000-000000000003"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestMigrateTwice(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		s, err := OpenStore(context.Background(), dir, "")
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
}

func TestUpsertReplaces(t *testing.T) {
	forEachStore(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		created, err := s.Upsert(ctx, mkReport(idA, "2026-10-20", saved("m1", 5, 1), wasted("m2", 2)), testNow, Admission{})
		if err != nil || !created {
			t.Fatalf("first upsert: created=%v err=%v", created, err)
		}
		r2 := mkReport(idA, "2026-10-20", saved("m1", 1, 0.5))
		r2.Version = "1.5.0"
		r2.Sessions = map[model.Harness]int{"codex": 1}
		created, err = s.Upsert(ctx, r2, testNow, Admission{})
		if err != nil || created {
			t.Fatalf("second upsert: created=%v err=%v", created, err)
		}
		sum, err := s.Summary(ctx, Range{From: "2026-10-01", To: "2026-10-27"})
		if err != nil {
			t.Fatal(err)
		}
		if sum.Installs != 1 || !near(sum.Net, 1) || !near(sum.Wasted, 0) || sum.Pings != 10 {
			t.Fatalf("after replace: %+v", sum)
		}
		rows, err := s.Breakdown(ctx, Range{From: "2026-10-01", To: "2026-10-27"}, "version")
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Key != "1.5.0" {
			t.Fatalf("version breakdown: %+v", rows)
		}
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM report_sessions`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("sessions rows %d err %v", n, err)
		}
	})
}

func seed(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	reports := []*Report{
		mkReport(idA, "2026-10-01", saved("opus", 10, 1)),
		mkReport(idA, "2026-10-20", saved("opus", 4, 1), wasted("gpt", 0.5)),
		mkReport(idB, "2026-10-21", wasted("gpt", 1.5)),
		mkReport(idC, "2026-10-10", saved("sonnet", 2, 0.25)),
	}
	reports[2].OS = "darwin"
	reports[2].Version = "1.3.0"
	reports[3].OS = "windows"
	reports[3].Pings.Sent = 4
	reports[3].Pings.Cold = 0
	for _, r := range reports {
		if _, err := s.Upsert(ctx, r, testNow, Admission{}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSummary(t *testing.T) {
	forEachStore(t, func(t *testing.T, s *Store) {
		seed(t, s)
		ctx := context.Background()
		sum, err := s.Summary(ctx, Range{From: "2026-10-01", To: "2026-10-27"})
		if err != nil {
			t.Fatal(err)
		}
		want := Summary{Installs: 3, ActiveInstalls: 1, Saved: 16, Wasted: 2, Net: 14,
			PingCost: 2, Pings: 34, Cold: 3, Returns: 3}
		if sum.Installs != want.Installs || sum.ActiveInstalls != want.ActiveInstalls ||
			!near(sum.Saved, want.Saved) || !near(sum.Wasted, want.Wasted) || !near(sum.Net, want.Net) ||
			!near(sum.PingCost, want.PingCost) || sum.Pings != want.Pings || sum.Cold != want.Cold ||
			sum.Returns != want.Returns {
			t.Fatalf("summary\n got %+v\nwant %+v", sum, want)
		}

		// Active means a report in the last 7 days of the range.
		sum, err = s.Summary(ctx, Range{From: "2026-10-01", To: "2026-10-16"})
		if err != nil {
			t.Fatal(err)
		}
		if sum.Installs != 2 || sum.ActiveInstalls != 1 {
			t.Fatalf("early range: %+v", sum)
		}
		// A range shorter than 7 days: every install in it is active.
		sum, err = s.Summary(ctx, Range{From: "2026-10-01", To: "2026-10-02"})
		if err != nil {
			t.Fatal(err)
		}
		if sum.Installs != 1 || sum.ActiveInstalls != 1 || !near(sum.Net, 10) {
			t.Fatalf("short range: %+v", sum)
		}
		// Empty range.
		sum, err = s.Summary(ctx, Range{From: "2025-01-01", To: "2025-01-31"})
		if err != nil {
			t.Fatal(err)
		}
		if sum != (Summary{}) {
			t.Fatalf("empty range: %+v", sum)
		}
	})
}

func TestSeries(t *testing.T) {
	forEachStore(t, func(t *testing.T, s *Store) {
		seed(t, s)
		ctx := context.Background()
		rg := Range{From: "2026-10-19", To: "2026-10-22"}
		want := map[string][]float64{
			"net":      {0, 3.5, -1.5, 0},
			"saved":    {0, 4, 0, 0},
			"wasted":   {0, 0.5, 1.5, 0},
			"installs": {0, 1, 1, 0},
			"pings":    {0, 10, 10, 0},
		}
		for _, metric := range SeriesMetrics {
			pts, err := s.Series(ctx, rg, metric)
			if err != nil {
				t.Fatal(metric, err)
			}
			if len(pts) != 4 || pts[0].Day != "2026-10-19" || pts[3].Day != "2026-10-22" {
				t.Fatalf("%s days: %+v", metric, pts)
			}
			for i, p := range pts {
				if !near(p.Value, want[metric][i]) {
					t.Errorf("%s[%s] = %v, want %v", metric, p.Day, p.Value, want[metric][i])
				}
			}
		}
		if _, err := s.Series(ctx, rg, "bogus"); err == nil {
			t.Fatal("unknown metric accepted")
		}
	})
}

func TestBreakdown(t *testing.T) {
	forEachStore(t, func(t *testing.T, s *Store) {
		seed(t, s)
		ctx := context.Background()
		rg := Range{From: "2026-10-01", To: "2026-10-27"}

		rows, err := s.Breakdown(ctx, rg, "model")
		if err != nil {
			t.Fatal(err)
		}
		want := []BreakdownRow{
			{Key: "opus", Installs: 1, Saved: 14, Net: 14, Pings: 6},
			{Key: "sonnet", Installs: 1, Saved: 2, Net: 2, Pings: 3},
			{Key: "gpt", Installs: 2, Wasted: 2, Net: -2, Pings: 4},
		}
		checkRows(t, "model", rows, want)

		rows, err = s.Breakdown(ctx, rg, "harness")
		if err != nil {
			t.Fatal(err)
		}
		checkRows(t, "harness", rows, []BreakdownRow{
			{Key: "claude", Installs: 2, Saved: 16, Net: 16, Pings: 9},
			{Key: "codex", Installs: 2, Wasted: 2, Net: -2, Pings: 4},
		})

		rows, err = s.Breakdown(ctx, rg, "os")
		if err != nil {
			t.Fatal(err)
		}
		checkRows(t, "os", rows, []BreakdownRow{
			{Key: "linux", Installs: 1, Saved: 14, Wasted: 0.5, Net: 13.5, Pings: 20},
			{Key: "windows", Installs: 1, Saved: 2, Net: 2, Pings: 4},
			{Key: "darwin", Installs: 1, Wasted: 1.5, Net: -1.5, Pings: 10},
		})

		for _, by := range []string{"version", "installKind", "profile"} {
			if _, err := s.Breakdown(ctx, rg, by); err != nil {
				t.Fatal(by, err)
			}
		}
		rows, err = s.Breakdown(ctx, rg, "installKind")
		if err != nil {
			t.Fatal(err)
		}
		checkRows(t, "installKind", rows, []BreakdownRow{
			{Key: "deb", Installs: 3, Saved: 16, Wasted: 2, Net: 14, Pings: 34},
		})
		if _, err := s.Breakdown(ctx, rg, "install_kind"); err == nil {
			t.Fatal("unknown dimension accepted")
		}
	})
}

func checkRows(t *testing.T, by string, got, want []BreakdownRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %+v, want %+v", by, got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Key != w.Key || g.Installs != w.Installs || !near(g.Saved, w.Saved) ||
			!near(g.Wasted, w.Wasted) || !near(g.Net, w.Net) || g.Pings != w.Pings {
			t.Errorf("%s[%d]: got %+v, want %+v", by, i, g, w)
		}
	}
}

func TestPurge(t *testing.T) {
	forEachStore(t, func(t *testing.T, s *Store) {
		seed(t, s)
		ctx := context.Background()
		n, err := s.Purge(ctx, "2026-10-15")
		if err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Fatalf("purged %d reports, want 2", n)
		}
		sum, err := s.Summary(ctx, Range{From: "2026-01-01", To: "2026-10-27"})
		if err != nil {
			t.Fatal(err)
		}
		if sum.Installs != 2 || !near(sum.Net, 2) {
			t.Fatalf("after purge: %+v", sum)
		}
		var left int
		if err := s.db.QueryRow(s.q(`SELECT COUNT(*) FROM outcomes WHERE day < ?`), "2026-10-15").Scan(&left); err != nil || left != 0 {
			t.Fatalf("outcomes left %d err %v", left, err)
		}
	})
}

func TestUpsertConcurrent(t *testing.T) {
	forEachStore(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		errc := make(chan error, 20)
		for i := 0; i < 20; i++ {
			go func(i int) {
				id := idA
				if i%2 == 1 {
					id = idB
				}
				day := time.Date(2026, 10, 1+i%5, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
				_, err := s.Upsert(ctx, mkReport(id, day, saved("m", 1, 0)), testNow, Admission{})
				errc <- err
			}(i)
		}
		for i := 0; i < 20; i++ {
			if err := <-errc; err != nil {
				t.Fatal(err)
			}
		}
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM reports`).Scan(&n); err != nil || n != 10 {
			t.Fatalf("reports %d err %v", n, err)
		}
	})
}

func TestNetworkOf(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.77":            "203.0.113.0/24",
		"::ffff:203.0.113.77":     "203.0.113.0/24",
		"2001:db8:1:2:3:4:5:6":    "2001:db8:1:2::/64",
		"not-an-address":          "not-an-address",
		"2001:db8:1:2::abcd:ef01": "2001:db8:1:2::/64",
	} {
		if got := NetworkOf(in); got != want {
			t.Errorf("NetworkOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAdmission(t *testing.T) {
	forEachStore(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		netA := Admission{Network: "203.0.113.0/24", Limit: 2}
		netB := Admission{Network: "198.51.100.0/24", Limit: 2}
		up := func(id, day string, a Admission, at time.Time) error {
			_, err := s.Upsert(ctx, mkReport(id, day), at, a)
			return err
		}
		if err := up(idA, "2026-10-26", netA, testNow); err != nil {
			t.Fatal(err)
		}
		if err := up(idB, "2026-10-26", netA, testNow); err != nil {
			t.Fatal(err)
		}
		if err := up(idC, "2026-10-26", netA, testNow); !errors.Is(err, ErrNetworkFull) {
			t.Fatalf("third new install: %v", err)
		}
		// Known installs are not new, whatever day they report for.
		if err := up(idA, "2026-10-27", netA, testNow); err != nil {
			t.Fatal(err)
		}
		// Another network has its own count.
		if err := up(idC, "2026-10-26", netB, testNow); err != nil {
			t.Fatal(err)
		}
		// The next UTC day starts over, and the old rows and salt go.
		tomorrow := testNow.Add(24 * time.Hour)
		const idD = "dddddddd-0000-4000-8000-000000000004"
		if err := up(idD, "2026-10-27", netA, tomorrow); err != nil {
			t.Fatal(err)
		}
		var rows, salts int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM new_installs`).Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("new_installs rows %d err %v", rows, err)
		}
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM hub_kv`).Scan(&salts); err != nil || salts != 1 {
			t.Fatalf("salts %d err %v", salts, err)
		}
		// No network or no limit: never refused.
		const idE = "eeeeeeee-0000-4000-8000-000000000005"
		if err := up(idE, "2026-10-27", Admission{Limit: 1}, tomorrow); err != nil {
			t.Fatal(err)
		}
		// Nothing stored for a refused install.
		var n int
		if err := s.db.QueryRow(s.q(`SELECT COUNT(*) FROM reports WHERE install_id = ?`), idC).Scan(&n); err != nil || n != 1 {
			t.Fatalf("idC reports %d err %v", n, err)
		}
	})
}

func TestEstablished(t *testing.T) {
	forEachStore(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		// idA reports on three days, idB on one.
		for _, day := range []string{"2026-10-20", "2026-10-21", "2026-10-22"} {
			if _, err := s.Upsert(ctx, mkReport(idA, day, saved("m", 2, 0)), testNow, Admission{}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Upsert(ctx, mkReport(idB, "2026-10-22", saved("m", 1000, 0)), testNow, Admission{}); err != nil {
			t.Fatal(err)
		}
		all := Range{From: "2026-10-01", To: "2026-10-27"}
		est := Range{From: "2026-10-01", To: "2026-10-27", MinDays: 3}
		sum, err := s.Summary(ctx, est)
		if err != nil {
			t.Fatal(err)
		}
		if sum.Installs != 1 || sum.Pending != 1 || !near(sum.Net, 6) || sum.Pings != 30 {
			t.Fatalf("established: %+v", sum)
		}
		sum, err = s.Summary(ctx, all)
		if err != nil {
			t.Fatal(err)
		}
		if sum.Installs != 2 || sum.Pending != 0 || !near(sum.Net, 1006) {
			t.Fatalf("all: %+v", sum)
		}
		pts, err := s.Series(ctx, est, "net")
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pts {
			if p.Day == "2026-10-22" && !near(p.Value, 2) {
				t.Fatalf("series 10-22: %v", p.Value)
			}
		}
		for _, by := range []string{"model", "version"} {
			rows, err := s.Breakdown(ctx, est, by)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].Installs != 1 || !near(rows[0].Net, 6) {
				t.Fatalf("breakdown %s: %+v", by, rows)
			}
		}
	})
}

// expSum is one experiment entry whose episodes cost costs each, all paid
// as re-writes in the hold arm and split evenly with pings in the ping arm.
func expSum(experiment, arm string, costs ...float64) model.ExperimentSum {
	e := model.ExperimentSum{Experiment: experiment, Arm: arm, Harness: "claude", Model: "opus",
		Episodes: len(costs), Returned: len(costs), Warm: 1, ContextTokens: 1_000_000, QuietMinutes: 30 * int64(len(costs))}
	for _, c := range costs {
		e.CostUSD += c
		e.CostSqUSD += c * c
	}
	e.RewriteUSD = e.CostUSD
	if arm == model.ArmPing {
		e.Pings, e.PingCalls = 2*len(costs), 2*len(costs)
		e.PingCostUSD, e.RewriteUSD = e.CostUSD/2, e.CostUSD/2
	}
	return e
}

func mkReport2(install, day string, e ...model.ExperimentSum) *Report {
	r := mkReport(install, day)
	r.Schema = 2
	r.Experiments = append([]model.ExperimentSum{}, e...)
	return r
}

func TestUpsertReplacesExperiments(t *testing.T) {
	forEachStore(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		r := mkReport2(idA, "2026-10-20", expSum("x-1", "hold", 1, 2), expSum("x-1", "ping", 1), expSum("x-2", "hold", 3))
		if err := r.Validate(testNow); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Upsert(ctx, r, testNow, Admission{}); err != nil {
			t.Fatal(err)
		}
		count := func() int {
			var n int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM experiment_sums`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		if n := count(); n != 3 {
			t.Fatalf("rows %d", n)
		}
		if _, err := s.Upsert(ctx, mkReport2(idA, "2026-10-20", expSum("x-1", "ping", 4, 4)), testNow, Admission{}); err != nil {
			t.Fatal(err)
		}
		if n := count(); n != 1 {
			t.Fatalf("rows after replace %d", n)
		}
		exps, err := s.Experiments(ctx, Range{From: "2026-10-01", To: "2026-10-27"})
		if err != nil {
			t.Fatal(err)
		}
		if len(exps) != 1 || exps[0].Experiment != "x-1" || len(exps[0].Arms) != 1 ||
			exps[0].Arms[0].Episodes != 2 || !near(exps[0].Arms[0].MeanCostUSD, 4) || exps[0].Diff != nil {
			t.Fatalf("after replace: %+v", exps)
		}
		// A schema 1 report for the day clears its experiments too.
		if _, err := s.Upsert(ctx, mkReport(idA, "2026-10-20"), testNow, Admission{}); err != nil {
			t.Fatal(err)
		}
		if n := count(); n != 0 {
			t.Fatalf("rows after schema 1 %d", n)
		}
	})
}

func TestExperiments(t *testing.T) {
	forEachStore(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		// Hold episodes cost 0.5, 2.5, 3, 4 and ping ones 0.5, 0.5, 1, 1,
		// over two installs and days. Hold: mean 2.5, var (31.5 - 100/4)/3
		// = 6.5/3, se² = 6.5/12. Ping: mean 0.75, var (2.5 - 9/4)/3 = 1/12,
		// se² = 1/48. diff = -1.75 ± 1.96·√(27/48) = -1.75 ± 1.47.
		for _, r := range []*Report{
			mkReport2(idA, "2026-10-20", expSum("busy-1", "hold", 0.5, 2.5), expSum("busy-1", "ping", 0.5, 0.5),
				expSum("lone-1", "hold", 1, 1), expSum("lone-1", "ping", 2)),
			mkReport2(idB, "2026-10-21", expSum("busy-1", "hold", 3, 4), expSum("busy-1", "ping", 1, 1)),
			mkReport(idC, "2026-10-21"),
		} {
			if err := r.Validate(testNow); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Upsert(ctx, r, testNow, Admission{}); err != nil {
				t.Fatal(err)
			}
		}
		exps, err := s.Experiments(ctx, Range{From: "2026-10-01", To: "2026-10-27"})
		if err != nil {
			t.Fatal(err)
		}
		if len(exps) != 2 || exps[0].Experiment != "busy-1" || exps[1].Experiment != "lone-1" {
			t.Fatalf("experiments: %+v", exps)
		}
		e := exps[0]
		if len(e.Arms) != 2 {
			t.Fatalf("arms: %+v", e.Arms)
		}
		hold, ping := e.Arms[0], e.Arms[1]
		if hold.Arm != "hold" || hold.Installs != 2 || hold.Episodes != 4 || hold.Returned != 4 || hold.Warm != 2 ||
			hold.Pings != 0 || !near(hold.MeanCostUSD, 2.5) || !near(hold.SECostUSD, math.Sqrt(6.5/12)) ||
			!near(hold.WarmRate, 0.5) || !near(hold.MeanQuietMinutes, 30) {
			t.Fatalf("hold: %+v", hold)
		}
		if ping.Arm != "ping" || ping.Installs != 2 || ping.Episodes != 4 || ping.Pings != 8 || ping.PingCalls != 8 ||
			!near(ping.MeanCostUSD, 0.75) || !near(ping.SECostUSD, math.Sqrt(1.0/48)) || !near(ping.PingsPerEpisode, 2) {
			t.Fatalf("ping: %+v", ping)
		}
		half := 1.96 * math.Sqrt(27.0/48)
		if d := e.Diff; d == nil || !near(d.MeanCostUSD, -1.75) || !near(d.CILowUSD, -1.75-half) ||
			!near(d.CIHighUSD, -1.75+half) || !d.Significant {
			t.Fatalf("diff: %+v", e.Diff)
		}
		// lone-1's ping arm has one episode: no difference yet.
		if exps[1].Diff != nil || len(exps[1].Arms) != 2 || exps[1].Arms[1].SECostUSD != 0 {
			t.Fatalf("lone-1: %+v", exps[1])
		}

		// Only idA's day: two episodes an arm, too spread to tell apart.
		exps, err = s.Experiments(ctx, Range{From: "2026-10-20", To: "2026-10-20"})
		if err != nil {
			t.Fatal(err)
		}
		// Hold 0.5, 2.5: mean 1.5, var 2, se 1. Ping 0.5, 0.5: mean 0.5,
		// se 0. diff = -1 ± 1.96, which spans 0.
		if d := exps[0].Diff; d == nil || !near(d.MeanCostUSD, -1) || !near(d.CIHighUSD, 0.96) || d.Significant {
			t.Fatalf("one day: %+v", exps[0].Diff)
		}

		// Established installs only: idA and idB have one report each.
		exps, err = s.Experiments(ctx, Range{From: "2026-10-01", To: "2026-10-27", MinDays: 2})
		if err != nil || len(exps) != 0 {
			t.Fatalf("established: %+v %v", exps, err)
		}
		exps, err = s.Experiments(ctx, Range{From: "2025-01-01", To: "2025-01-31"})
		if err != nil || exps == nil || len(exps) != 0 {
			t.Fatalf("empty range: %#v %v", exps, err)
		}

		// Purge takes the experiment rows with their reports.
		if _, err := s.Purge(ctx, "2026-10-21"); err != nil {
			t.Fatal(err)
		}
		var left int
		if err := s.db.QueryRow(s.q(`SELECT COUNT(*) FROM experiment_sums WHERE day < ?`), "2026-10-21").Scan(&left); err != nil || left != 0 {
			t.Fatalf("experiment rows left %d err %v", left, err)
		}
	})
}

func TestMeanSE(t *testing.T) {
	for _, tc := range []struct {
		n             int64
		sum, sq       float64
		wantM, wantSE float64
	}{
		{0, 0, 0, 0, 0},
		{1, 3, 9, 3, 0},
		{4, 10, 30, 2.5, math.Sqrt(5.0 / 12)},
		// Rounding can put sq just under sum²/n: clamped, not NaN.
		{2, 2, 1.9999999, 1, 0},
	} {
		m, se := meanSE(tc.n, tc.sum, tc.sq)
		if !near(m, tc.wantM) || !near(se, tc.wantSE) {
			t.Errorf("meanSE(%d, %v, %v) = %v, %v", tc.n, tc.sum, tc.sq, m, se)
		}
	}
	if experimentDiff(ExperimentArm{Episodes: 1}, ExperimentArm{Episodes: 100}) != nil {
		t.Error("diff with one hold episode")
	}
}

func TestRobust(t *testing.T) {
	median, trimmed, cut, top := robust(nil)
	if median != 0 || trimmed != 0 || cut != 0 || top != 0 {
		t.Fatal("empty")
	}
	// 200 installs: 199 at 1, one at 1e6. The outlier is cut, the median
	// ignores it, and it holds almost all of the total.
	nets := make([]float64, 200)
	for i := range nets {
		nets[i] = 1
	}
	nets[17] = 1e6
	nets[3] = -0.5
	median, trimmed, cut, top = robust(nets)
	if median != 1 || cut != 2 || !near(trimmed, 196.5) || top < 0.99 {
		t.Fatalf("median %v trimmed %v cut %d top %v", median, trimmed, cut, top)
	}
	median, _, _, _ = robust([]float64{1, 3})
	if median != 2 {
		t.Fatalf("even median %v", median)
	}
}

func TestSummaryRobust(t *testing.T) {
	forEachStore(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		if _, err := s.Upsert(ctx, mkReport(idA, "2026-10-20", saved("m", 4, 0)), testNow, Admission{}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Upsert(ctx, mkReport(idB, "2026-10-20", saved("m", 10, 0)), testNow, Admission{}); err != nil {
			t.Fatal(err)
		}
		// idC closed nothing: its net is 0.
		if _, err := s.Upsert(ctx, mkReport(idC, "2026-10-20"), testNow, Admission{}); err != nil {
			t.Fatal(err)
		}
		sum, err := s.Summary(ctx, Range{From: "2026-10-01", To: "2026-10-27"})
		if err != nil {
			t.Fatal(err)
		}
		if !near(sum.MedianNet, 4) || !near(sum.TrimmedNet, 14) || sum.Trimmed != 0 || !near(sum.TopShare, 10.0/14) {
			t.Fatalf("robust: %+v", sum)
		}
	})
}
