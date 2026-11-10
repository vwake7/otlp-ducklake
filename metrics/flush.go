package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/JohanLindvall/diskqueue"
	"github.com/vwake7/otlp-ducklake/internal/otlp"
	"github.com/vwake7/otlp-ducklake/internal/queue"
	"github.com/vwake7/otlp-ducklake/internal/telemetry"
)

// Flusher reserves queue records and commits them in batches.
type Flusher struct {
	Queue    *diskqueue.Queue[queue.Record]
	Commit   Committer
	WriterID string
	Cursor   int64
	Bytes    int64
	Interval time.Duration
	Log      *slog.Logger
	Stats    *telemetry.Stats
	Service  string
	Insert   func(context.Context, []otlp.MetricRow) error
	lastEmit time.Time
}

// Run commits until ctx is cancelled, then drains records already in the queue.
func (f *Flusher) Run(ctx context.Context) error {
	if f.Log == nil {
		f.Log = slog.Default()
	}
	reader := f.Queue.NewReader()
	var pending []Queued
	var size int64
	started := time.Now()
	for {
		if ctx.Err() != nil {
			return f.drain(reader, &pending, &size)
		}
		if len(pending) > 0 && (size >= f.Bytes || time.Since(started) >= f.Interval) {
			if err := f.commit(ctx, reader, &pending, &size); err != nil {
				f.Log.Error("flush failed", "err", err)
				if sleep(ctx, time.Second) != nil {
					return f.drain(reader, &pending, &size)
				}
			} else {
				started = time.Now()
			}
			continue
		}
		wait, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		rec, ok, offset, err := reader.Reserve(wait)
		cancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			return err
		}
		if !ok {
			f.maybeEmit(ctx, reader, pending)
			continue
		}
		pending = append(pending, Queued{Offset: offset, Body: rec.Body, Accepted: rec.AcceptedUnixNano})
		size += int64(len(rec.Body))
		f.maybeEmit(ctx, reader, pending)
	}
}

func (f *Flusher) drain(reader *diskqueue.Reader[queue.Record], pending *[]Queued, size *int64) error {
	for {
		rec, ok, offset, err := reader.TryReserve()
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		*pending = append(*pending, Queued{Offset: offset, Body: rec.Body, Accepted: rec.AcceptedUnixNano})
		*size += int64(len(rec.Body))
		if *size >= f.Bytes {
			if err := f.commit(context.Background(), reader, pending, size); err != nil {
				return err
			}
		}
	}
	if len(*pending) == 0 {
		return nil
	}
	return f.commit(context.Background(), reader, pending, size)
}

func (f *Flusher) commit(ctx context.Context, reader *diskqueue.Reader[queue.Record], pending *[]Queued, size *int64) error {
	items := *pending
	next, err := Apply(ctx, f.Commit, f.WriterID, f.Cursor, items, reader.AckBatch)
	if next > f.Cursor {
		f.Cursor = next
	}
	if err != nil {
		return err
	}
	*pending = nil
	*size = 0
	return nil
}

func (f *Flusher) maybeEmit(ctx context.Context, reader *diskqueue.Reader[queue.Record], pending []Queued) {
	if f.Stats == nil || f.Insert == nil {
		return
	}
	now := time.Now()
	if !f.lastEmit.IsZero() && now.Sub(f.lastEmit) < telemetry.Interval {
		return
	}
	rows := f.Stats.Rows(f.Service, f.Queue.Size(), telemetry.AgeSeconds(headAccepted(reader, pending), now), now)
	if err := f.Insert(ctx, rows); err != nil && f.Log != nil {
		f.Log.Error("telemetry insert failed", "err", err)
	}
	f.lastEmit = now
}

func headAccepted(reader *diskqueue.Reader[queue.Record], pending []Queued) int64 {
	if len(pending) > 0 {
		return pending[0].Accepted
	}
	rec, ok, err := reader.TryPeek()
	if err != nil || !ok {
		return 0
	}
	return rec.AcceptedUnixNano
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
