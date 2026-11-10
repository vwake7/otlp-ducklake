package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeDB struct {
	sql  string
	rows []map[string]any
	wait bool
}

func (f *fakeDB) Query(ctx context.Context, sql string) ([]map[string]any, error) {
	f.sql = sql
	if f.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.rows, nil
}

func TestQueryReturnsRows(t *testing.T) {
	db := &fakeDB{rows: []map[string]any{{"body": "hi"}}}
	srv := &Server{DB: db, Timeout: time.Second, MaxRows: 1000}
	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewReader([]byte(`{"sql":"SELECT body FROM logs WHERE timestamp >= 'a' AND timestamp < 'b'"}`)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if db.sql == "" || len(db.sql) < 5 {
		t.Fatalf("sql %s", db.sql)
	}
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if rows[0]["body"] != "hi" {
		t.Fatalf("rows %v", rows)
	}
}

func TestQueryTimeout(t *testing.T) {
	srv := &Server{DB: &fakeDB{wait: true}, Timeout: 20 * time.Millisecond, MaxRows: 1000}
	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewReader([]byte(`{"sql":"SELECT 1 FROM logs WHERE timestamp >= 'a' AND timestamp < 'b'"}`)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestTimeout {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
}

func TestQueryAcceptsUnbounded(t *testing.T) {
	db := &fakeDB{}
	srv := &Server{DB: db, Timeout: time.Second, MaxRows: 1000}
	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewReader([]byte(`{"sql":"SELECT * FROM logs"}`)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if db.sql != "SELECT * FROM logs LIMIT 1000" {
		t.Fatalf("sql %s", db.sql)
	}
}

func TestQueryRejectsWrite(t *testing.T) {
	srv := &Server{DB: &fakeDB{}, Timeout: time.Second, MaxRows: 1000}
	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewReader([]byte(`{"sql":"DELETE FROM logs"}`)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
}
