# otlp-ducklake

This project checks how far [DuckLake](https://ducklake.select/) can scale for an OpenTelemetry workload. It accepts OTLP logs and metrics over HTTP, keeps each request in a private disk queue, and bulk-commits into DuckLake through a [Quack](https://github.com/duckdb/duckdb-quack) catalog. Parquet lands on S3.

Logs and metrics are separate writers. They do not share a queue or a table. A query API and a Grafana dashboard read what was committed.

## Services

- `quack` holds the catalog on `./data/catalog` and listens on port 9494.
- `logs` accepts `POST /v1/logs` on port 8080. Queue: `./data/logs-queue`.
- `metrics` accepts `POST /v1/metrics` on port 8082. Queue: `./data/metrics-queue`.
- `query` accepts `POST /query` on port 8081. One `SELECT`, at most 1000 rows, 10 second timeout.
- `maintenance` merges small Parquet files hourly.
- `grafana` is on port 3000. The Infinity plugin posts to the query API.
- `loadgen` is optional. It posts logs at 10,000 records/s.

## Get started

- Docker, and an S3 bucket. Copy the env file and fill in `QUACK_TOKEN`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, and `S3_BUCKET`. `QUACK_TOKEN` is a password you choose. The services share it. `.env` is not committed.

```bash
cp .env.example .env
```

- Start the stack. The first build compiles the Go services and pulls DuckDB 2.0.0 for Quack.

```bash
docker compose up --build -d
```

- Send a query. A fresh catalog has no rows until a writer flushes.

```bash
curl -sS -X POST http://127.0.0.1:8081/query \
  -H 'content-type: application/json' \
  -d '{"sql":"SELECT count(*) AS n FROM logs"}'
```

- Generate log load. This starts only the load generator. The writers above must already be up.

```bash
docker compose --profile load up --build -d loadgen
```

- Open Grafana at http://localhost:3000. The login is `GRAFANA_ADMIN_USER` / `GRAFANA_ADMIN_PASSWORD` from `.env` (`admin` / `admin` unless you changed them). The DuckLake dashboard reads the last 15 minutes. Widen the range if the panels are empty.

- Attach from the DuckDB 2.0.0 CLI. The CLI major version must match the Quack image. Load the same `.env` values, then:

```sql
CREATE SECRET s3 (TYPE s3, KEY_ID '<S3_ACCESS_KEY_ID>', SECRET '<S3_SECRET_ACCESS_KEY>', REGION '<S3_REGION>', ENDPOINT '<S3_ENDPOINT>', URL_STYLE 'path', USE_SSL true);
CREATE SECRET quack (TYPE quack, TOKEN '<QUACK_TOKEN>');
ATTACH 'ducklake:quack:localhost:9494' AS lake (DATA_PATH 's3://<S3_BUCKET>/<S3_PREFIX>', READ_ONLY);
SELECT count(*) FROM lake.logs;
```

Compare `TIMESTAMPTZ` columns with `now()` or a timestamp that includes `+00`. A bare `TIMESTAMP` literal is read in the session time zone.

- Stop everything, including the load generator. `./data` is left on disk.

```bash
docker compose --profile load down
```

## License

Apache License 2.0. See [LICENSE](LICENSE).
