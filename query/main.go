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
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("query stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.ValidateQuery(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ldb, err := lake.OpenRetry(ctx, cfg, true, log)
	if err != nil {
		return err
	}
	defer ldb.Close()

	api := &Server{
		DB:      ldb,
		Timeout: cfg.QueryTimeout,
		MaxRows: cfg.QueryMaxRows,
		Log:     log,
	}
	srv := &http.Server{
		Addr:              cfg.QueryAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	log.Info("query listening", "addr", cfg.QueryAddr)
	err = srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
