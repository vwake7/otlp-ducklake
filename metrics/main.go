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

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("metrics stopped", "err", err)
		os.Exit(1)
	}
}

type lakeCommit struct {
	db    *lake.DB
	stats *telemetry.Stats
}

func (c lakeCommit) Commit(ctx context.Context, rows []otlp.MetricRow, writerID string, offset int64) error {
	start := time.Now()
	err := c.db.CommitMetrics(ctx, rows, writerID, offset)
	c.stats.ObserveCommit(len(rows), time.Since(start), err)
	return err
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.ValidateMetrics(); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.MetricsQueueDir, 0o755); err != nil {
		return err
	}
	q, err := queue.Open(cfg.MetricsQueueDir, cfg.QueueMaxBytes)
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
	if err := ldb.EnsureMetricsSchema(ctx); err != nil {
		return err
	}
	cursor, err := ldb.Cursor(ctx, cfg.MetricsWriterID)
	if err != nil {
		return err
	}
	log.Info("writer cursor", "writer", cfg.MetricsWriterID, "offset", cursor)

	stats := &telemetry.Stats{}
	api := NewServer(q, log, stats)
	httpServer := &http.Server{
		Addr:              cfg.MetricsAddr,
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
		Commit:   lakeCommit{db: ldb, stats: stats},
		WriterID: cfg.MetricsWriterID,
		Cursor:   cursor,
		Bytes:    cfg.FlushBytes,
		Interval: cfg.FlushInterval,
		Log:      log,
		Stats:    stats,
		Service:  "metrics",
		Insert:   ldb.InsertMetrics,
	}
	log.Info("metrics listening", "addr", cfg.MetricsAddr, "queue", cfg.MetricsQueueDir)
	runErr := flusher.Run(ctx)
	select {
	case err := <-errCh:
		return err
	default:
		return runErr
	}
}
