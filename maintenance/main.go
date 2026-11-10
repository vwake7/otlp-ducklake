package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vwake7/otlp-ducklake/internal/config"
	"github.com/vwake7/otlp-ducklake/internal/lake"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("maintenance stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.ValidateMaintenance(); err != nil {
		return err
	}
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

	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			err := ldb.Maintain(runCtx, cfg.DeleteGrace)
			cancel()
			if err != nil {
				log.Error("maintenance pass failed", "err", err)
			} else {
				log.Info("maintenance pass finished")
			}
			timer.Reset(cfg.MaintenanceInterval)
		}
	}
}
