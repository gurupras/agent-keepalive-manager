package hub

import (
	"context"
	"database/sql"
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
		for _, tbl := range []string{"outcomes", "report_sessions", "reports", "schema_migrations"} {
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
		created, err := s.Upsert(ctx, mkReport(idA, "2026-10-20", saved("m1", 5, 1), wasted("m2", 2)), testNow)
		if err != nil || !created {
			t.Fatalf("first upsert: created=%v err=%v", created, err)
		}
		r2 := mkReport(idA, "2026-10-20", saved("m1", 1, 0.5))
		r2.Version = "1.5.0"
		r2.Sessions = map[model.Harness]int{"codex": 1}
		created, err = s.Upsert(ctx, r2, testNow)
		if err != nil || created {
			t.Fatalf("second upsert: created=%v err=%v", created, err)
		}
		sum, err := s.Summary(ctx, Range{"2026-10-01", "2026-10-27"})
		if err != nil {
			t.Fatal(err)
		}
		if sum.Installs != 1 || !near(sum.Net, 1) || !near(sum.Wasted, 0) || sum.Pings != 10 {
			t.Fatalf("after replace: %+v", sum)
		}
		rows, err := s.Breakdown(ctx, Range{"2026-10-01", "2026-10-27"}, "version")
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
		if _, err := s.Upsert(ctx, r, testNow); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSummary(t *testing.T) {
	forEachStore(t, func(t *testing.T, s *Store) {
		seed(t, s)
		ctx := context.Background()
		sum, err := s.Summary(ctx, Range{"2026-10-01", "2026-10-27"})
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
		sum, err = s.Summary(ctx, Range{"2026-10-01", "2026-10-16"})
		if err != nil {
			t.Fatal(err)
		}
		if sum.Installs != 2 || sum.ActiveInstalls != 1 {
			t.Fatalf("early range: %+v", sum)
		}
		// A range shorter than 7 days: every install in it is active.
		sum, err = s.Summary(ctx, Range{"2026-10-01", "2026-10-02"})
		if err != nil {
			t.Fatal(err)
		}
		if sum.Installs != 1 || sum.ActiveInstalls != 1 || !near(sum.Net, 10) {
			t.Fatalf("short range: %+v", sum)
		}
		// Empty range.
		sum, err = s.Summary(ctx, Range{"2025-01-01", "2025-01-31"})
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
		rg := Range{"2026-10-19", "2026-10-22"}
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
		rg := Range{"2026-10-01", "2026-10-27"}

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
		sum, err := s.Summary(ctx, Range{"2026-01-01", "2026-10-27"})
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
				_, err := s.Upsert(ctx, mkReport(id, day, saved("m", 1, 0)), testNow)
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
