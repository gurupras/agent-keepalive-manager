package hub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"time"
)

const (
	purgeEvery      = 6 * time.Hour
	shutdownTimeout = 15 * time.Second
)

// Run serves the hub until ctx is cancelled, then shuts down gracefully.
func Run(ctx context.Context, cfg Config, ui fs.FS) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	logger := cfg.Logger
	store, err := OpenStore(ctx, cfg.DataDir, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	srv, err := NewServer(cfg, store, ui)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          log.New(&redactingWriter{log: logger}, "", 0),
	}
	backend := "sqlite"
	if cfg.DatabaseURL != "" {
		backend = "postgres"
	}
	logger.Info("agent-keepalive-hub listening", "addr", ln.Addr().String(), "basePath", cfg.BasePath+"/",
		"database", backend, "dashboard", cfg.Dashboard, "ingestToken", cfg.IngestToken != "",
		"version", cfg.Version)

	purgeCtx, stopPurge := context.WithCancel(ctx)
	purgeDone := make(chan struct{})
	go func() {
		defer close(purgeDone)
		runPurge(purgeCtx, store, cfg.RetentionDays, logger)
	}()

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()

	select {
	case err := <-errc:
		stopPurge()
		<-purgeDone
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	stopPurge()
	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err = httpSrv.Shutdown(shutCtx)
	if serr := <-errc; serr != nil && !errors.Is(serr, http.ErrServerClosed) && err == nil {
		err = serr
	}
	<-purgeDone
	return err
}

// runPurge deletes reports older than retentionDays now and every
// purgeEvery. Zero keeps everything.
func runPurge(ctx context.Context, store *Store, retentionDays int, logger *slog.Logger) {
	if retentionDays <= 0 {
		return
	}
	t := time.NewTicker(purgeEvery)
	defer t.Stop()
	for {
		cutoff := utcDay(time.Now()).AddDate(0, 0, -retentionDays).Format(time.DateOnly)
		n, err := store.Purge(ctx, cutoff)
		switch {
		case err != nil && ctx.Err() == nil:
			logger.Error("retention purge", "err", err)
		case n > 0:
			logger.Info("retention purge", "deleted", n, "before", cutoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// HealthURL is the healthz URL for a hub listening on listen under basePath,
// reached over loopback.
func HealthURL(listen, basePath string) (string, error) {
	bp, err := NormalizeBasePath(basePath)
	if err != nil {
		return "", err
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("listen address %q: %w", listen, err)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + bp + "/healthz", nil
}

// redactingWriter sends net/http's own error log (TLS handshake errors and
// the like, which name the peer) to slog with addresses removed.
type redactingWriter struct{ log *slog.Logger }

var addrRe = regexp.MustCompile(`\[[0-9A-Fa-f:.%a-z]+\](:\d+)?|\b\d{1,3}(\.\d{1,3}){3}(:\d+)?\b|\b[0-9A-Fa-f]{0,4}(:[0-9A-Fa-f]{0,4}){2,7}\b`)

func (w *redactingWriter) Write(p []byte) (int, error) {
	msg := addrRe.ReplaceAllString(string(bytes.TrimSpace(p)), "[addr]")
	w.log.Warn("http server", "msg", msg)
	return len(p), nil
}
