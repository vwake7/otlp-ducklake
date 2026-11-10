package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vwake7/otlp-ducklake/internal/config"
	"github.com/vwake7/otlp-ducklake/internal/lake"
	"github.com/vwake7/otlp-ducklake/internal/otlp"
	"github.com/vwake7/otlp-ducklake/internal/queue"
	"github.com/vwake7/otlp-ducklake/internal/telemetry"
)

type observedCommit struct {
	inner Committer
	stats *telemetry.Stats
}

func (o observedCommit) Commit(ctx context.Context, rows []otlp.Row, writerID string, offset int64) error {
	start := time.Now()
	err := o.inner.Commit(ctx, rows, writerID, offset)
	o.stats.ObserveCommit(len(rows), time.Since(start), err)
	return err
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("logs stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.ValidateLogs(); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.QueueDir, 0o755); err != nil {
		return err
	}
	q, err := queue.Open(cfg.QueueDir, cfg.QueueMaxBytes)
	if err != nil {
		return err
	}
	defer q.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ldb, err := lake.OpenRetry(ctx, cfg, false, log)
	if err != nil {
		return err
	}
	defer ldb.Close()
	if err := ldb.EnsureSchema(ctx); err != nil {
		return err
	}
	if err := ldb.EnsureMetricsSchema(ctx); err != nil {
		return err
	}
	cursor, err := ldb.Cursor(ctx, cfg.WriterID)
	if err != nil {
		return err
	}
	log.Info("writer cursor", "writer", cfg.WriterID, "offset", cursor)

	stats := &telemetry.Stats{}
	api := NewServer(q, log, stats)
	httpServer := &http.Server{
		Addr:              cfg.LogsAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		api.Draining.Store(true)
		shut, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = httpServer.Shutdown(shut)
	}()
	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			stop()
		}
	}()

	flusher := &Flusher{
		Queue:    q,
		Commit:   observedCommit{inner: ldb, stats: stats},
		WriterID: cfg.WriterID,
		Cursor:   cursor,
		Bytes:    cfg.FlushBytes,
		Interval: cfg.FlushInterval,
		Log:      log,
		Stats:    stats,
		Service:  "logs",
		Insert:   ldb.InsertMetrics,
	}
	log.Info("logs listening", "addr", cfg.LogsAddr, "queue", cfg.QueueDir)
	runErr := flusher.Run(ctx)
	select {
	case err := <-errCh:
		return err
	default:
		return runErr
	}
}
