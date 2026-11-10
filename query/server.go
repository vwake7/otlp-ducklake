package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/vwake7/otlp-ducklake/internal/sqlguard"
)

// Querier runs one SELECT and returns row objects.
type Querier interface {
	Query(ctx context.Context, sql string) ([]map[string]any, error)
}

// Server serves POST /query.
type Server struct {
	DB      Querier
	Timeout time.Duration
	MaxRows int
	Log     *slog.Logger
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /query", s.query)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

type request struct {
	SQL string `json:"sql"`
}

func (s *Server) query(w http.ResponseWriter, r *http.Request) {
	var req request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "body must be {\"sql\": \"...\"}")
		return
	}
	stmt, err := sqlguard.Prepare(req.SQL, s.MaxRows)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.Timeout)
	defer cancel()
	rows, err := s.DB.Query(ctx, stmt)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeError(w, http.StatusRequestTimeout, "query exceeded 10s")
			return
		}
		s.log().Warn("query failed", "err", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	if err := enc.Encode(rows); err != nil {
		s.log().Warn("write response", "err", err)
	}
}

func (s *Server) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
