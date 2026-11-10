// Package telemetry keeps each writer's in-memory counters and the gauge rows
// inserted into the metrics table.
package telemetry

import (
	"sync/atomic"
	"time"

	"github.com/vwake7/otlp-ducklake/internal/otlp"
)

// Interval is how often a writer copies its counters into the metrics table.
const Interval = 5 * time.Second

const (
	nameAccepted    = "ducklake.writer.accepted_rows"
	nameInserted    = "ducklake.writer.inserted_rows"
	nameQueueBytes  = "ducklake.writer.queue_bytes"
	nameOldestAge   = "ducklake.writer.queue_oldest_age_seconds"
	nameFlush       = "ducklake.writer.flush_duration_seconds"
	nameCommitFails = "ducklake.writer.commit_failures"
)

// Stats are the live counters for one process. Restarting the process resets them.
type Stats struct {
	accepted       atomic.Int64
	inserted       atomic.Int64
	commitFailures atomic.Int64
	flushNanos     atomic.Int64
}

// AddAccepted adds rows whose request returned 200.
func (s *Stats) AddAccepted(n int) {
	if s == nil || n <= 0 {
		return
	}
	s.accepted.Add(int64(n))
}

// ObserveCommit records one flush attempt. Inserted rows increase only after a successful commit.
func (s *Stats) ObserveCommit(rows int, d time.Duration, err error) {
	if s == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	s.flushNanos.Store(d.Nanoseconds())
	if err != nil {
		s.commitFailures.Add(1)
		return
	}
	if rows > 0 {
		s.inserted.Add(int64(rows))
	}
}

// Rows builds the six gauges for service, which is "logs" or "metrics".
func (s *Stats) Rows(service string, queueBytes int64, oldestAgeSeconds float64, now time.Time) []otlp.MetricRow {
	if s == nil {
		return nil
	}
	resource := map[string]string{"service.name": service}
	values := []struct {
		name  string
		value float64
	}{
		{nameAccepted, float64(s.accepted.Load())},
		{nameInserted, float64(s.inserted.Load())},
		{nameQueueBytes, float64(queueBytes)},
		{nameOldestAge, oldestAgeSeconds},
		{nameFlush, float64(s.flushNanos.Load()) / float64(time.Second)},
		{nameCommitFails, float64(s.commitFailures.Load())},
	}
	rows := make([]otlp.MetricRow, len(values))
	for i, v := range values {
		rows[i] = otlp.MetricRow{
			Timestamp:  now,
			Name:       v.name,
			Type:       "gauge",
			Value:      v.value,
			Resource:   resource,
			Attributes: map[string]string{},
		}
	}
	return rows
}

// AgeSeconds is how long a queue record accepted at unixNano has been waiting.
// A zero stamp means the queue is empty or the record predates the stamp.
func AgeSeconds(unixNano int64, now time.Time) float64 {
	if unixNano <= 0 {
		return 0
	}
	age := now.Sub(time.Unix(0, unixNano)).Seconds()
	if age < 0 {
		return 0
	}
	return age
}
