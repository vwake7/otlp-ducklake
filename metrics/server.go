package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/JohanLindvall/diskqueue"
	"github.com/vwake7/otlp-ducklake/internal/otlp"
	"github.com/vwake7/otlp-ducklake/internal/queue"
	"github.com/vwake7/otlp-ducklake/internal/telemetry"
)

const maxBody = 16 << 20

// Server accepts OTLP JSON metrics and appends each request to the queue.
type Server struct {
	Queue    *diskqueue.Queue[queue.Record]
	Draining atomic.Bool
	stats    *telemetry.Stats
	log      *slog.Logger
}

func NewServer(q *diskqueue.Queue[queue.Record], log *slog.Logger, stats *telemetry.Stats) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{Queue: q, stats: stats, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/metrics", s.metrics)
	mux.HandleFunc("GET /healthz", s.health)
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	if s.Draining.Load() {
		http.Error(w, "draining", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	if s.Draining.Load() {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		http.Error(w, "request body is too large", http.StatusBadRequest)
		return
	}
	if err := otlp.ValidateMetrics(body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rec := queue.Record{BatchID: newID(), Body: json.RawMessage(body), AcceptedUnixNano: time.Now().UnixNano()}
	if err := s.Queue.Add(rec); err != nil {
		status := http.StatusTooManyRequests
		if errors.Is(err, diskqueue.ErrRecordTooLarge) {
			status = http.StatusBadRequest
		}
		s.log.Warn("queue add failed", "err", err, "status", status)
		w.WriteHeader(status)
		return
	}
	if rows, err := otlp.FlattenMetrics(body); err == nil {
		s.stats.AddAccepted(len(rows))
	}
	w.WriteHeader(http.StatusOK)
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	dst := make([]byte, 36)
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:], b[10:])
	return string(dst)
}
