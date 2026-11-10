// Command loadgen posts random OTLP JSON logs to the logs service.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

const maxBody = 16 << 20

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("loadgen stopped", "err", err)
		os.Exit(1)
	}
}

type config struct {
	URL      string
	Rate     int
	Batch    int
	Workers  int
	Duration time.Duration
}

func run(log *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if cfg.Duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Duration)
		defer cancel()
	}
	log.Info("loadgen starting", "url", cfg.URL, "rate", cfg.Rate, "batch", cfg.Batch, "workers", cfg.Workers)
	return generate(ctx, cfg, log)
}

func loadConfig() (config, error) {
	cfg := config{
		URL:     envDefault("INGEST_URL", "http://127.0.0.1:8080/v1/logs"),
		Rate:    10000,
		Batch:   1000,
		Workers: 4,
	}
	var err error
	if cfg.Rate, err = envInt("LOAD_RATE", cfg.Rate); err != nil {
		return config{}, err
	}
	if cfg.Batch, err = envInt("LOAD_BATCH", cfg.Batch); err != nil {
		return config{}, err
	}
	if cfg.Workers, err = envInt("LOAD_WORKERS", cfg.Workers); err != nil {
		return config{}, err
	}
	if v := os.Getenv("LOAD_DURATION"); v != "" {
		cfg.Duration, err = time.ParseDuration(v)
		if err != nil {
			return config{}, fmt.Errorf("LOAD_DURATION: %w", err)
		}
	}
	if cfg.Rate < 1 {
		return config{}, fmt.Errorf("LOAD_RATE must be positive")
	}
	if cfg.Batch < 1 {
		return config{}, fmt.Errorf("LOAD_BATCH must be positive")
	}
	if cfg.Workers < 1 {
		return config{}, fmt.Errorf("LOAD_WORKERS must be positive")
	}
	return cfg, nil
}

func generate(ctx context.Context, cfg config, log *slog.Logger) error {
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        cfg.Workers * 2,
			MaxIdleConnsPerHost: cfg.Workers * 2,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	jobs := make(chan struct{}, cfg.Workers)
	var records atomic.Int64
	var errors atomic.Int64
	var dropped atomic.Int64
	for i := 0; i < cfg.Workers; i++ {
		rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(i+1)))
		go func() {
			for range jobs {
				n, err := post(ctx, client, cfg.URL, rng, cfg.Batch)
				if err != nil {
					if ctx.Err() != nil {
						continue
					}
					errors.Add(1)
					log.Warn("post failed", "err", err)
					continue
				}
				records.Add(int64(n))
			}
		}()
	}

	interval := time.Duration(float64(time.Second) * float64(cfg.Batch) / float64(cfg.Rate))
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	report := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer report.Stop()
	var prev int64
	for {
		select {
		case <-ctx.Done():
			close(jobs)
			log.Info("loadgen finished", "records", records.Load(), "errors", errors.Load(), "dropped_batches", dropped.Load())
			return nil
		case <-ticker.C:
			select {
			case jobs <- struct{}{}:
			default:
				dropped.Add(1)
			}
		case <-report.C:
			cur := records.Load()
			log.Info("load", "records_per_sec", cur-prev, "errors", errors.Load(), "dropped_batches", dropped.Load())
			prev = cur
		}
	}
}

func post(ctx context.Context, client *http.Client, url string, rng *rand.Rand, batch int) (int, error) {
	body, err := payload(rng, batch, time.Now())
	if err != nil {
		return 0, err
	}
	if len(body) > maxBody {
		return 0, fmt.Errorf("batch is %d bytes, logs accepts %d", len(body), maxBody)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("logs returned %d", resp.StatusCode)
	}
	return batch, nil
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}
