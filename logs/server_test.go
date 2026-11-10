package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vwake7/otlp-ducklake/internal/queue"
)

func TestLogsAccepted(t *testing.T) {
	dir := t.TempDir()
	q, err := queue.Open(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	srv := NewServer(q, nil, nil)
	body := []byte(`{"resourceLogs":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	r := q.NewReader()
	got, ok, _, err := r.TryReserve()
	if err != nil || !ok {
		t.Fatalf("reserve ok %v err %v", ok, err)
	}
	if got.BatchID == "" || string(got.Body) != string(body) {
		t.Fatalf("record %+v", got)
	}
}

func TestLogsRejectsBadJSON(t *testing.T) {
	dir := t.TempDir()
	q, err := queue.Open(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	srv := NewServer(q, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader([]byte(`[]`)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestLogsQueueFull(t *testing.T) {
	dir := t.TempDir()
	q, err := queue.Open(dir, 200)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	srv := NewServer(q, nil, nil)
	body := []byte(`{"resourceLogs":[]}`)
	var saw429 bool
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			saw429 = true
			break
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
	}
	if !saw429 {
		t.Fatal("expected 429 once the queue filled")
	}
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatal(err)
	}
}
