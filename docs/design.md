# DuckLake

Logs and metrics are separate writers. They do not share a queue or a table. Both commit through one Quack catalog, and Parquet for both lands under one S3 prefix.

```
                 POST /v1/logs                          POST /v1/metrics
                      |                                       |
                      v                                       v
                 +---------+                            +----------+
                 |  logs   |                            | metrics  |
                 |  logs   |                            | metrics  |
                 +----+----+                            +----+-----+
                      |                                      |
           ./data/logs-queue                     ./data/metrics-queue
                      |                                      |
                      +------------------+-------------------+
                                         |
                                         v
                                   +-----------+
                                   |   quack   |  ./data/catalog
                                   +-----+-----+
                                         |
                                         v
                              s3://ducklake-api/lake/
                                         ^
                                         |
                                   +-----+-----+
                                   |   query   |  POST /query :8081
                                   +-----+-----+
                                         ^
                                         |  Infinity
                                   +-----+-----+
                                   |  grafana  |  :3000
                                   +-----------+
```

## Logs

- `POST /v1/logs` takes an OTLP JSON body. One request is one queue record.
- A flush decodes that JSON into one row per log event and inserts `logs`.
- `timestamp` is the event time. If it is unset, it is the observed time, then the flush time.
- `severity_number`, `severity_text`, `body`, `trace_id`, `span_id` are columns. `resource` and `attributes` are `MAP(VARCHAR, VARCHAR)`.
- Partitioned by the hour of `timestamp`.

## Metrics

- `POST /v1/metrics` takes an OTLP JSON body. One request is one queue record.
- A gauge or sum data point is one row in `metrics`: `timestamp`, `metric_name`, `metric_type`, `unit`, `value`, `resource`, `attributes`.
- A histogram is one row per bucket, plus one row for the count and one for the sum. The bucket bound is `attributes['le']`.
- Partitioned by the hour of `timestamp`.

## Both paths

- **200** after the queue `fsync`. **400** for a body that is not valid for that path. **429** when the queue is full or the process is draining.
- Each queue is a bind-mounted directory on the Mac, capped at 8 GB. Only that process opens it.
- Flush at 256 MiB or 10 seconds. One flush is one transaction: the rows, then `writer_cursors (writer_id, committed_offset)`.
- `committed_offset` is the byte position of the last queue record in the flush, not a request count.
- On restart, a record at or below `max(committed_offset)` for that writer is acked and skipped. A record past it is inserted.
- A new HTTP request is a new queue record. The cursor does not suppress it.
- An insert of 10 rows or fewer stays in the Quack catalog. A larger flush writes Parquet under `s3://ducklake-api/lake/main/logs/` or `s3://ducklake-api/lake/main/metrics/`.
- No authentication.

## Writer telemetry

- Each writer keeps its own counters in memory. The HTTP handler updates them when a request finishes. The flusher updates them when a commit finishes. Restarting the process sets them back to zero.
- Every 5 seconds the flush loop reads those counters and inserts gauge rows into `metrics`. The check runs on the flush goroutine, between data commits, because the process has one DuckDB connection.
- The insert writes only `metrics`. It does not append `writer_cursors`. A telemetry commit must not move the queue cursor.
- `metric_type` is `gauge`. `resource['service.name']` is `logs` or `metrics`. Names are `ducklake.writer.accepted_rows`, `ducklake.writer.inserted_rows`, `ducklake.writer.queue_bytes`, `ducklake.writer.queue_oldest_age_seconds`, `ducklake.writer.flush_duration_seconds`, and `ducklake.writer.commit_failures`.
- Queue bytes and oldest age are read from the queue at insert time. The other values are the in-memory counters.
- Six rows stay in the Quack catalog. They are under the 10-row inline limit.
- A writer does not POST these rows to `/v1/metrics`. A missing 5-second sample means that process is dead or its commit is stuck.

## Maintenance and query

- One maintenance client, hourly. It merges small files for `logs` and for `metrics`, expires older snapshots, and deletes unreferenced files 15 minutes later.
- `POST /query` takes `{"sql":"..."}`. One `SELECT`. A `timestamp` filter is not required. `LIMIT` is at most 1,000. Timeout is 10 seconds.
- **200** is a JSON array. **400** is a rejected statement. **408** is a timeout, with no rows.

## Grafana

- One Grafana container on port 3000. It does not attach DuckLake and it does not open a queue.
- The Infinity plugin (`yesoreyeram-infinity-datasource`) is installed at start. One provisioned datasource POSTs to `http://quack:8081/query`.
- The body is `{"sql":"..."}`. A panel may still filter on the dashboard time range. The query API accepts the statement either way.
- Dashboard timezone is UTC. A time filter uses the epoch range `${__from}` and `${__to}`, because the date macro follows the browser timezone.
- The Logs - Telemetry panel reads `metrics` for one `metric_name` where `resource['service.name']` is `logs`. `accepted_rows`, `inserted_rows`, and `commit_failures` are `value - LAG(value, 1, value) OVER (ORDER BY timestamp)`. A negative difference means the process restarted, and that sample is left out. The other three metrics stay the stored reading. Infinity reads the JSON array as a time series.
- A logs panel selects `timestamp`, `severity_text`, and `body` from `logs`. Infinity reads the same JSON array as a table.
- The query service still caps `LIMIT` at 1,000 and the request at 10 seconds. A wide range returns **408** and the panel is empty.
- Grafana state is the bind mount `./data/grafana`. The admin password stays in `.env`.

## Services

- Every on-disk path is a bind mount under `./data`. There are no Docker volumes.
- `quack`: catalog at `./data/catalog`.
- `logs`: writer id `logs`, `POST /v1/logs` on port 8080, queue at `./data/logs-queue`.
- `metrics`: writer id `metrics`, `POST /v1/metrics` on port 8082, queue at `./data/metrics-queue`.
- `maintenance` and `query`: no local disk. `query` is `POST /query` on port 8081.
- `grafana`: Infinity plugin, port 3000, state at `./data/grafana`. It calls `query` only.
- S3 is outside Docker.
