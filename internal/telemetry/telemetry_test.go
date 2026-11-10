package telemetry

import (
	"errors"
	"testing"
	"time"
)

func TestRowsAndCommitObservation(t *testing.T) {
	var s Stats
	s.AddAccepted(5)
	s.ObserveCommit(4, 1500*time.Millisecond, nil)
	s.ObserveCommit(2, time.Second, errors.New("catalog down"))

	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	rows := s.Rows("logs", 80, 2.5, now)
	if len(rows) != 6 {
		t.Fatalf("rows %d", len(rows))
	}
	got := map[string]float64{}
	for _, row := range rows {
		if row.Type != "gauge" || row.Resource["service.name"] != "logs" || !row.Timestamp.Equal(now) {
			t.Fatalf("row %+v", row)
		}
		got[row.Name] = row.Value
	}
	if got[nameAccepted] != 5 || got[nameInserted] != 4 || got[nameCommitFails] != 1 {
		t.Fatalf("values %v", got)
	}
	if got[nameQueueBytes] != 80 || got[nameOldestAge] != 2.5 || got[nameFlush] != 1 {
		t.Fatalf("gauges %v", got)
	}
}

func TestAgeSeconds(t *testing.T) {
	now := time.Unix(100, 0)
	if AgeSeconds(0, now) != 0 {
		t.Fatal("empty queue")
	}
	if got := AgeSeconds(time.Unix(90, 0).UnixNano(), now); got != 10 {
		t.Fatalf("age %v", got)
	}
}
