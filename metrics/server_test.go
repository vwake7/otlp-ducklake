package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vwake7/otlp-ducklake/internal/queue"
)

func TestMetricsAccepted(t *testing.T) {
	dir := t.TempDir()
	q, err := queue.Open(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	srv := NewServer(q, nil, nil)
	body := []byte(`{"resourceMetrics":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}
