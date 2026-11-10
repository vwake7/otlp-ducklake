// Package lake attaches the DuckLake catalog and writes or queries Parquet on object storage.
package lake

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/duckdb/duckdb-go/v2"
	"github.com/vwake7/otlp-ducklake/internal/config"
	"github.com/vwake7/otlp-ducklake/internal/otlp"
)

// DB is one embedded DuckDB process attached to Quack.
type DB struct {
	db   *sql.DB
	conn *sql.Conn
}

// Open loads the DuckLake, httpfs, and Quack extensions and attaches the catalog.
// readOnly is the query service. Writers and maintenance attach read-write.
func Open(ctx context.Context, cfg config.Config, readOnly bool) (*DB, error) {
	threads := runtime.NumCPU()
	if threads < 1 {
		threads = 1
	}
	boot := []string{
		fmt.Sprintf("SET threads = %d", threads),
		"INSTALL ducklake", "LOAD ducklake",
		"INSTALL httpfs", "LOAD httpfs",
		"INSTALL quack", "LOAD quack",
		s3Secret(cfg),
		quackSecret(cfg),
		attach(cfg, readOnly),
	}
	var once sync.Once
	var bootErr error
	connector, err := duckdb.NewConnector("", func(execer driver.ExecerContext) error {
		once.Do(func() {
			bootErr = execAll(execer, boot)
		})
		if bootErr != nil {
			return bootErr
		}
		// ATTACH is instance-wide. USE and the temp macro are per connection.
		return execAll(execer, []string{"USE lake", pairsMacro})
	})
	if err != nil {
		return nil, err
	}
	sqlDB := sql.OpenDB(connector)
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	return &DB{db: sqlDB, conn: conn}, nil
}

// OpenRetry attaches the catalog, waiting while Quack is still starting or the lake does not exist yet.
func OpenRetry(ctx context.Context, cfg config.Config, readOnly bool, log *slog.Logger) (*DB, error) {
	delay := time.Second
	for {
		db, err := Open(ctx, cfg, readOnly)
		if err == nil {
			return db, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
		log.Warn("waiting for catalog", "err", err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		if delay < 15*time.Second {
			delay *= 2
		}
	}
}

func execAll(execer driver.ExecerContext, stmts []string) error {
	for _, stmt := range stmts {
		if _, err := execer.ExecContext(context.Background(), stmt, nil); err != nil {
			return fmt.Errorf("%s: %w", firstLine(stmt), err)
		}
	}
	return nil
}

// Close closes the connection and the database.
func (d *DB) Close() error {
	var err error
	if d.conn != nil {
		err = d.conn.Close()
	}
	if d.db != nil {
		if cerr := d.db.Close(); err == nil {
			err = cerr
		}
	}
	return err
}

// EnsureSchema creates the logs table and the writer cursor table.
func (d *DB) EnsureSchema(ctx context.Context) error {
	for _, stmt := range []string{logsSchemaSQL, cursorSchemaSQL} {
		if _, err := d.conn.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// Cursor returns the highest committed queue offset for writerID, or -1.
func (d *DB) Cursor(ctx context.Context, writerID string) (int64, error) {
	var offset sql.NullInt64
	err := d.conn.QueryRowContext(ctx, `SELECT max(committed_offset) FROM writer_cursors WHERE writer_id = ?`, writerID).Scan(&offset)
	if err != nil {
		return -1, err
	}
	if !offset.Valid {
		return -1, nil
	}
	return offset.Int64, nil
}

// Commit inserts rows and the cursor in one transaction.
func (d *DB) Commit(ctx context.Context, rows []otlp.Row, writerID string, offset int64) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if len(rows) > 0 {
		path, err := writeBatch("ducklake-logs", rows)
		if err != nil {
			return err
		}
		defer os.Remove(path)
		if _, err := tx.ExecContext(ctx, insertLogsSQL(path)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO writer_cursors VALUES (?, ?)`, writerID, offset); err != nil {
		return err
	}
	return tx.Commit()
}

// EnsureMetricsSchema creates the metrics table and the writer cursor table.
func (d *DB) EnsureMetricsSchema(ctx context.Context) error {
	for _, stmt := range []string{metricsSchemaSQL, cursorSchemaSQL} {
		if _, err := d.conn.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// CommitMetrics inserts metric rows and the cursor in one transaction.
func (d *DB) CommitMetrics(ctx context.Context, rows []otlp.MetricRow, writerID string, offset int64) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if len(rows) > 0 {
		path, err := writeMetricBatch(rows)
		if err != nil {
			return err
		}
		defer os.Remove(path)
		if _, err := tx.ExecContext(ctx, insertMetricsSQL(path)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO writer_cursors VALUES (?, ?)`, writerID, offset); err != nil {
		return err
	}
	return tx.Commit()
}

// InsertMetrics writes gauge rows and does not touch writer_cursors.
func (d *DB) InsertMetrics(ctx context.Context, rows []otlp.MetricRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	path, err := writeMetricBatch(rows)
	if err != nil {
		return err
	}
	defer os.Remove(path)
	if _, err := tx.ExecContext(ctx, insertMetricsSQL(path)); err != nil {
		return err
	}
	return tx.Commit()
}

// Query runs a SELECT and returns JSON-ready row objects.
func (d *DB) Query(ctx context.Context, query string) ([]map[string]any, error) {
	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(names))
		for i, name := range names {
			row[name] = normalize(vals[i])
		}
		out = append(out, row)
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, rows.Err()
}

// Maintain merges small files, expires older snapshots, and deletes files
// that have been unreferenced for grace.
func (d *DB) Maintain(ctx context.Context, grace time.Duration) error {
	seconds := int64(grace.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	interval := fmt.Sprintf("%d seconds", seconds)
	stmts := []string{
		"CALL ducklake_merge_adjacent_files('lake', 'logs')",
		"CALL ducklake_merge_adjacent_files('lake', 'metrics')",
		fmt.Sprintf("CALL ducklake_expire_snapshots('lake', older_than => now() - INTERVAL '%s')", interval),
		fmt.Sprintf("CALL ducklake_cleanup_old_files('lake', older_than => now() - INTERVAL '%s')", interval),
	}
	for _, stmt := range stmts {
		if _, err := d.conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return nil
}

func normalize(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	default:
		return t
	}
}

func writeBatch(prefix string, rows []otlp.Row) (string, error) {
	f, err := os.CreateTemp("", prefix+"-*.ndjson")
	if err != nil {
		return "", err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, row := range rows {
		rec := map[string]any{
			"timestamp":       row.Timestamp.UTC().Format(time.RFC3339Nano),
			"severity_number": row.SeverityNumber,
			"severity_text":   row.SeverityText,
			"body":            row.Body,
			"trace_id":        row.TraceID,
			"span_id":         row.SpanID,
			"resource_json":   pairsJSON(row.Resource),
			"attributes_json": pairsJSON(row.Attributes),
		}
		if row.HasObserved {
			rec["observed_timestamp"] = row.Observed.UTC().Format(time.RFC3339Nano)
		}
		if err := enc.Encode(rec); err != nil {
			os.Remove(f.Name())
			return "", err
		}
	}
	return f.Name(), nil
}

func pairsJSON(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]map[string]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, map[string]string{"k": k, "v": m[k]})
	}
	buf, _ := json.Marshal(pairs)
	return string(buf)
}

func insertLogsSQL(path string) string {
	return fmt.Sprintf(`
INSERT INTO logs
SELECT
    timestamp,
    observed_timestamp,
    severity_number,
    severity_text,
    body,
    trace_id,
    span_id,
    pairs_to_map(resource_json),
    pairs_to_map(attributes_json)
FROM read_json(%s, columns = {
    timestamp: 'TIMESTAMPTZ',
    observed_timestamp: 'TIMESTAMPTZ',
    severity_number: 'SMALLINT',
    severity_text: 'VARCHAR',
    body: 'VARCHAR',
    trace_id: 'VARCHAR',
    span_id: 'VARCHAR',
    resource_json: 'VARCHAR',
    attributes_json: 'VARCHAR'
}, format = 'newline_delimited')`, sqlString(path))
}

func s3Secret(cfg config.Config) string {
	return fmt.Sprintf(`CREATE OR REPLACE SECRET (
    TYPE s3,
    PROVIDER config,
    KEY_ID %s,
    SECRET %s,
    REGION %s,
    ENDPOINT %s,
    URL_STYLE %s,
    USE_SSL true
)`, sqlString(cfg.S3AccessKeyID), sqlString(cfg.S3SecretAccessKey), sqlString(cfg.S3Region), sqlString(cfg.S3Endpoint), sqlString(cfg.S3URLStyle))
}

func quackSecret(cfg config.Config) string {
	return fmt.Sprintf(`CREATE OR REPLACE SECRET (
    TYPE quack,
    TOKEN %s
)`, sqlString(cfg.QuackToken))
}

func attach(cfg config.Config, readOnly bool) string {
	opts := fmt.Sprintf("DATA_PATH %s", sqlString(cfg.DataPath()))
	if readOnly {
		opts += ", READ_ONLY"
	}
	return fmt.Sprintf("ATTACH 'ducklake:%s' AS lake (%s)", cfg.QuackURI(), opts)
}

func sqlString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 80 {
		return s[:80]
	}
	return s
}

const pairsMacro = `
CREATE OR REPLACE TEMP MACRO pairs_to_map(j) AS (
    CASE
        WHEN j IS NULL OR j = '' OR j = '[]' THEN map_from_entries([]::STRUCT(k VARCHAR, v VARCHAR)[])
        ELSE map_from_entries(from_json(j, '[{"k":"VARCHAR","v":"VARCHAR"}]'))
    END
)`

const logsSchemaSQL = `
CREATE TABLE IF NOT EXISTS logs (
    timestamp TIMESTAMPTZ,
    observed_timestamp TIMESTAMPTZ,
    severity_number SMALLINT,
    severity_text VARCHAR,
    body VARCHAR,
    trace_id VARCHAR,
    span_id VARCHAR,
    resource MAP(VARCHAR, VARCHAR),
    attributes MAP(VARCHAR, VARCHAR)
) PARTITIONED BY (year(timestamp), month(timestamp), day(timestamp), hour(timestamp))`

func writeMetricBatch(rows []otlp.MetricRow) (string, error) {
	f, err := os.CreateTemp("", "ducklake-metrics-*.ndjson")
	if err != nil {
		return "", err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, row := range rows {
		rec := map[string]any{
			"timestamp":       row.Timestamp.UTC().Format(time.RFC3339Nano),
			"metric_name":     row.Name,
			"metric_type":     row.Type,
			"unit":            row.Unit,
			"value":           row.Value,
			"resource_json":   pairsJSON(row.Resource),
			"attributes_json": pairsJSON(row.Attributes),
		}
		if err := enc.Encode(rec); err != nil {
			os.Remove(f.Name())
			return "", err
		}
	}
	return f.Name(), nil
}

func insertMetricsSQL(path string) string {
	return fmt.Sprintf(`
INSERT INTO metrics
SELECT
    timestamp,
    metric_name,
    metric_type,
    unit,
    value,
    pairs_to_map(resource_json),
    pairs_to_map(attributes_json)
FROM read_json(%s, columns = {
    timestamp: 'TIMESTAMPTZ',
    metric_name: 'VARCHAR',
    metric_type: 'VARCHAR',
    unit: 'VARCHAR',
    value: 'DOUBLE',
    resource_json: 'VARCHAR',
    attributes_json: 'VARCHAR'
}, format = 'newline_delimited')`, sqlString(path))
}

const metricsSchemaSQL = `
CREATE TABLE IF NOT EXISTS metrics (
    timestamp TIMESTAMPTZ,
    metric_name VARCHAR,
    metric_type VARCHAR,
    unit VARCHAR,
    value DOUBLE,
    resource MAP(VARCHAR, VARCHAR),
    attributes MAP(VARCHAR, VARCHAR)
) PARTITIONED BY (year(timestamp), month(timestamp), day(timestamp), hour(timestamp))`

const cursorSchemaSQL = `
CREATE TABLE IF NOT EXISTS writer_cursors (
    writer_id VARCHAR,
    committed_offset BIGINT
)`
