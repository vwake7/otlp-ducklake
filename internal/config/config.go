// Package config loads process settings from the environment.
// godotenv fills any unset variable from a .env file in the working directory.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config is the full set of settings. Each command validates the subset it needs.
type Config struct {
	QuackToken string
	QuackHost  string
	QuackPort  int

	S3AccessKeyID     string
	S3SecretAccessKey string
	S3Region          string
	S3Endpoint        string
	S3Bucket          string
	S3Prefix          string
	S3URLStyle        string

	WriterID        string
	LogsAddr        string
	QueueDir        string
	MetricsWriterID string
	MetricsAddr     string
	MetricsQueueDir string
	QueueMaxBytes   int64
	FlushBytes      int64
	FlushInterval   time.Duration

	QueryAddr    string
	QueryTimeout time.Duration
	QueryMaxRows int

	MaintenanceInterval time.Duration
	DeleteGrace         time.Duration
}

// Load reads .env when it is present, then reads the environment.
// Variables already set in the process are left unchanged.
func Load() (Config, error) {
	_ = godotenv.Load()
	var cfg Config
	var err error
	cfg.QuackToken = os.Getenv("QUACK_TOKEN")
	cfg.QuackHost = envDefault("QUACK_HOST", "localhost")
	cfg.QuackPort, err = envInt("QUACK_PORT", 9494)
	if err != nil {
		return Config{}, err
	}
	cfg.S3AccessKeyID = os.Getenv("S3_ACCESS_KEY_ID")
	cfg.S3SecretAccessKey = os.Getenv("S3_SECRET_ACCESS_KEY")
	cfg.S3Region = envDefault("S3_REGION", "gra")
	cfg.S3Endpoint = stripEndpoint(envDefault("S3_ENDPOINT", "s3.gra.io.cloud.ovh.net"))
	cfg.S3Bucket = os.Getenv("S3_BUCKET")
	cfg.S3Prefix = strings.Trim(envDefault("S3_PREFIX", "lake"), "/")
	cfg.S3URLStyle = envDefault("S3_URL_STYLE", "path")

	cfg.WriterID = envFirst("LOGS_WRITER_ID", "WRITER_ID", "logs")
	cfg.LogsAddr = envFirst("LOGS_ADDR", "INGEST_ADDR", ":8080")
	cfg.QueueDir = envFirst("LOGS_QUEUE_DIR", "QUEUE_DIR", "./data/logs-queue")
	cfg.MetricsWriterID = envDefault("METRICS_WRITER_ID", "metrics")
	cfg.MetricsAddr = envDefault("METRICS_ADDR", ":8082")
	cfg.MetricsQueueDir = envDefault("METRICS_QUEUE_DIR", "./data/metrics-queue")
	cfg.QueueMaxBytes, err = envInt64("QUEUE_MAX_BYTES", 8<<30)
	if err != nil {
		return Config{}, err
	}
	cfg.FlushBytes, err = envInt64("FLUSH_BYTES", 256<<20)
	if err != nil {
		return Config{}, err
	}
	cfg.FlushInterval, err = envDuration("FLUSH_INTERVAL", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	cfg.QueryAddr = envDefault("QUERY_ADDR", ":8081")
	cfg.QueryTimeout, err = envDuration("QUERY_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	cfg.QueryMaxRows, err = envInt("QUERY_MAX_ROWS", 1000)
	if err != nil {
		return Config{}, err
	}
	cfg.MaintenanceInterval, err = envDuration("MAINTENANCE_INTERVAL", time.Hour)
	if err != nil {
		return Config{}, err
	}
	cfg.DeleteGrace, err = envDuration("DELETE_GRACE", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// QuackURI is the client attach target, for example quack:quack:9494.
func (c Config) QuackURI() string {
	return fmt.Sprintf("quack:%s:%d", c.QuackHost, c.QuackPort)
}

// DataPath is the shared Parquet location, for example s3://bucket/lake.
func (c Config) DataPath() string {
	if c.S3Prefix == "" {
		return "s3://" + c.S3Bucket
	}
	return "s3://" + c.S3Bucket + "/" + c.S3Prefix
}

// ValidateShared checks the catalog and object-storage settings.
func (c Config) ValidateShared() error {
	missing := required(map[string]string{
		"QUACK_TOKEN":          c.QuackToken,
		"QUACK_HOST":           c.QuackHost,
		"S3_ACCESS_KEY_ID":     c.S3AccessKeyID,
		"S3_SECRET_ACCESS_KEY": c.S3SecretAccessKey,
		"S3_REGION":            c.S3Region,
		"S3_ENDPOINT":          c.S3Endpoint,
		"S3_BUCKET":            c.S3Bucket,
	})
	if c.QuackPort <= 0 {
		missing = append(missing, "QUACK_PORT")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing settings: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ValidateMetrics checks the metrics writer settings.
func (c Config) ValidateMetrics() error {
	if err := c.ValidateShared(); err != nil {
		return err
	}
	missing := required(map[string]string{
		"METRICS_WRITER_ID": c.MetricsWriterID,
		"METRICS_QUEUE_DIR": c.MetricsQueueDir,
	})
	if c.QueueMaxBytes <= 0 {
		missing = append(missing, "QUEUE_MAX_BYTES")
	}
	if c.FlushBytes <= 0 {
		missing = append(missing, "FLUSH_BYTES")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing settings: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ValidateLogs checks the logs writer settings.
func (c Config) ValidateLogs() error {
	if err := c.ValidateShared(); err != nil {
		return err
	}
	missing := required(map[string]string{
		"WRITER_ID": c.WriterID,
		"QUEUE_DIR": c.QueueDir,
	})
	if c.QueueMaxBytes <= 0 {
		missing = append(missing, "QUEUE_MAX_BYTES")
	}
	if c.FlushBytes <= 0 {
		missing = append(missing, "FLUSH_BYTES")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing settings: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ValidateQuery checks the read service settings.
func (c Config) ValidateQuery() error {
	if err := c.ValidateShared(); err != nil {
		return err
	}
	if c.QueryMaxRows <= 0 {
		return fmt.Errorf("missing settings: QUERY_MAX_ROWS")
	}
	if c.QueryTimeout <= 0 {
		return fmt.Errorf("missing settings: QUERY_TIMEOUT")
	}
	return nil
}

// ValidateMaintenance checks the maintenance service settings.
func (c Config) ValidateMaintenance() error {
	if err := c.ValidateShared(); err != nil {
		return err
	}
	if c.DeleteGrace <= 0 {
		return fmt.Errorf("missing settings: DELETE_GRACE")
	}
	return nil
}

func required(vals map[string]string) []string {
	var missing []string
	for _, name := range []string{
		"QUACK_TOKEN", "QUACK_HOST",
		"S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY", "S3_REGION", "S3_ENDPOINT", "S3_BUCKET",
		"WRITER_ID", "QUEUE_DIR", "METRICS_WRITER_ID", "METRICS_QUEUE_DIR",
	} {
		if v, ok := vals[name]; ok && strings.TrimSpace(v) == "" {
			missing = append(missing, name)
		}
	}
	return missing
}

func envFirst(primary, legacy, fallback string) string {
	if v := os.Getenv(primary); v != "" {
		return v
	}
	if v := os.Getenv(legacy); v != "" {
		return v
	}
	return fallback
}

func envDefault(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

func envInt64(key string, fallback int64) (int64, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

func stripEndpoint(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	return strings.TrimRight(s, "/")
}
