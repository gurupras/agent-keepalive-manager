// Command agent-keepalive-hub receives usage reports from agent-keepalive
// installs and serves the dashboard of their totals (docs/metrics.md).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gurupras/agent-keepalive-manager/hub/web"
	"github.com/gurupras/agent-keepalive-manager/internal/hub"
)

// version is set at link time: -ldflags "-X main.version=1.4.0".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 {
		switch args[0] {
		case "serve", "healthcheck", "version":
			cmd, args = args[0], args[1:]
		}
	}
	if cmd == "version" {
		fmt.Fprintln(stdout, "agent-keepalive-hub", version)
		return 0
	}

	cfg, logLevel, err := parseFlags(cmd, args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "agent-keepalive-hub:", err)
		return 2
	}

	if cmd == "healthcheck" {
		return healthcheck(cfg, stderr)
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: logLevel}))
	cfg.Logger = logger
	cfg.Version = version
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := hub.Run(ctx, cfg, web.FS()); err != nil {
		logger.Error("hub stopped", "err", err)
		return 1
	}
	return 0
}

// parseFlags reads flags, each defaulting to its HUB_* environment variable
// and then to the documented default.
func parseFlags(cmd string, args []string, stderr io.Writer) (hub.Config, slog.Level, error) {
	var cfg hub.Config
	var level slog.Level
	fs := flag.NewFlagSet("agent-keepalive-hub "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)

	retention, err := envInt("HUB_RETENTION_DAYS", 730)
	if err != nil {
		return cfg, level, err
	}
	trust, err := envBool("HUB_TRUST_PROXY", false)
	if err != nil {
		return cfg, level, err
	}
	maxDaily, err := envFloat("HUB_MAX_DAILY_USD", 2000)
	if err != nil {
		return cfg, level, err
	}
	perNetwork, err := envInt("HUB_NEW_INSTALLS_PER_NETWORK", 5)
	if err != nil {
		return cfg, level, err
	}
	established, err := envInt("HUB_ESTABLISHED_DAYS", 3)
	if err != nil {
		return cfg, level, err
	}
	fs.StringVar(&cfg.Listen, "listen", env("HUB_LISTEN", ":8080"), "address to listen on (HUB_LISTEN)")
	fs.StringVar(&cfg.DataDir, "data-dir", env("HUB_DATA_DIR", "./data"), "directory of the SQLite database (HUB_DATA_DIR)")
	fs.StringVar(&cfg.DatabaseURL, "database-url", env("HUB_DATABASE_URL", ""), "postgres:// URL to use Postgres instead of SQLite (HUB_DATABASE_URL)")
	fs.StringVar(&cfg.BasePath, "base-path", env("HUB_BASE_PATH", "/"), "serve under this path, e.g. /hub (HUB_BASE_PATH)")
	fs.StringVar(&cfg.IngestToken, "ingest-token", env("HUB_INGEST_TOKEN", ""), "require this bearer token on reports (HUB_INGEST_TOKEN)")
	fs.StringVar(&cfg.Dashboard, "dashboard", env("HUB_DASHBOARD", hub.DashboardPublic), "dashboard mode: public, token or off (HUB_DASHBOARD)")
	fs.StringVar(&cfg.DashboardToken, "dashboard-token", env("HUB_DASHBOARD_TOKEN", ""), "token for --dashboard token (HUB_DASHBOARD_TOKEN; prefer the variable)")
	fs.BoolVar(&cfg.TrustProxy, "trust-proxy", trust, "use X-Forwarded-For and X-Forwarded-Proto from a reverse proxy (HUB_TRUST_PROXY)")
	fs.IntVar(&cfg.RetentionDays, "retention-days", retention, "delete reports older than this many days; 0 keeps all (HUB_RETENTION_DAYS)")
	fs.Float64Var(&cfg.MaxDailyUSD, "max-daily-usd", maxDaily, "refuse a report saving more than this in a day; 0 is no cap (HUB_MAX_DAILY_USD)")
	fs.IntVar(&cfg.NewInstallsPerNetwork, "new-installs-per-network", perNetwork, "most new installs one /24 or /64 may add per day; 0 is no limit (HUB_NEW_INSTALLS_PER_NETWORK)")
	fs.IntVar(&cfg.EstablishedDays, "established-days", established, "daily reports an install needs before the dashboard counts it; 0 counts all (HUB_ESTABLISHED_DAYS)")
	fs.TextVar(&level, "log-level", levelFromEnv(), "debug, info, warn or error (HUB_LOG_LEVEL)")
	if err := fs.Parse(args); err != nil {
		return cfg, level, err
	}
	if fs.NArg() > 0 {
		return cfg, level, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return cfg, level, nil
}

func env(name, def string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return def
}

func envFloat(name string, def float64) (float64, error) {
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return n, nil
}

func envInt(name string, def int) (int, error) {
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return n, nil
}

func envBool(name string, def bool) (bool, error) {
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	return b, nil
}

func levelFromEnv() *slog.Level {
	l := slog.LevelInfo
	if v := os.Getenv("HUB_LOG_LEVEL"); v != "" {
		if err := l.UnmarshalText([]byte(v)); err != nil {
			l = slog.LevelInfo
		}
	}
	return &l
}

// healthcheck asks the hub configured by the same flags and environment
// whether it is healthy: exit 0 if it answers ok, 1 otherwise.
func healthcheck(cfg hub.Config, stderr io.Writer) int {
	url, err := hub.HealthURL(cfg.Listen, cfg.BasePath)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}
