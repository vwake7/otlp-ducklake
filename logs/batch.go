package main

import (
	"context"
	"encoding/json"

	"github.com/vwake7/otlp-ducklake/internal/otlp"
)

// Queued is one reserved queue record.
type Queued struct {
	Offset   int64
	Body     json.RawMessage
	Accepted int64
}

// Committer writes a flush and the writer's cursor in one transaction.
type Committer interface {
	Commit(ctx context.Context, rows []otlp.Row, writerID string, offset int64) error
}

// Apply inserts rows whose offsets are above cursor, stores the new cursor,
// then acks every offset in the batch. Rows at or below cursor are skipped.
// When commit succeeds and ack fails, the returned cursor is the committed one.
func Apply(ctx context.Context, c Committer, writerID string, cursor int64, items []Queued, ack func(offsets ...int64) error) (int64, error) {
	maxOff := cursor
	offsets := make([]int64, 0, len(items))
	var rows []otlp.Row
	for _, item := range items {
		offsets = append(offsets, item.Offset)
		if item.Offset > maxOff {
			maxOff = item.Offset
		}
		if item.Offset <= cursor {
			continue
		}
		part, err := otlp.Flatten(item.Body)
		if err != nil {
			return cursor, err
		}
		rows = append(rows, part...)
	}
	if maxOff > cursor {
		if err := c.Commit(ctx, rows, writerID, maxOff); err != nil {
			return cursor, err
		}
	}
	if err := ack(offsets...); err != nil {
		return maxOff, err
	}
	return maxOff, nil
}
