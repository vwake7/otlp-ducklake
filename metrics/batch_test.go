package main

import (
	"context"
	"errors"
	"testing"

	"github.com/vwake7/otlp-ducklake/internal/otlp"
)

type fakeCommit struct {
	calls  int
	offset int64
	rows   int
	err    error
}

func (f *fakeCommit) Commit(_ context.Context, rows []otlp.MetricRow, _ string, offset int64) error {
	f.calls++
	f.offset = offset
	f.rows = len(rows)
	return f.err
}

func TestApplySkipsCommittedOffsets(t *testing.T) {
	body := []byte(`{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"queue.depth","gauge":{"dataPoints":[{"timeUnixNano":"1730000000000000000","asInt":"1"}]}}]}]}]}`)
	commit := &fakeCommit{}
	var acked []int64
	cursor, err := Apply(context.Background(), commit, "metrics", 2, []Queued{
		{Offset: 1, Body: body},
		{Offset: 2, Body: body},
		{Offset: 3, Body: body},
	}, func(offsets ...int64) error {
		acked = offsets
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if cursor != 3 || commit.calls != 1 || commit.rows != 1 || commit.offset != 3 {
		t.Fatalf("cursor %d commit %+v", cursor, commit)
	}
	if len(acked) != 3 {
		t.Fatalf("acked %v", acked)
	}
}

func TestApplyDoesNotAckWhenCommitFails(t *testing.T) {
	body := []byte(`{"resourceMetrics":[]}`)
	commit := &fakeCommit{err: errors.New("catalog down")}
	acked := false
	cursor, err := Apply(context.Background(), commit, "metrics", -1, []Queued{{Offset: 1, Body: body}}, func(offsets ...int64) error {
		acked = true
		return nil
	})
	if err == nil || cursor != -1 || acked {
		t.Fatalf("cursor %d acked %v err %v", cursor, acked, err)
	}
}
