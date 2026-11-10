package main

import (
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vwake7/otlp-ducklake/internal/otlp"
)

func TestPayloadIsOTLP(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	body, err := payload(rng, 25, time.Date(2026, 11, 7, 11, 27, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := otlp.Flatten(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 25 {
		t.Fatalf("rows %d", len(rows))
	}
	if rows[0].Body == "" || rows[0].SeverityText == "" || rows[0].Timestamp.IsZero() {
		t.Fatalf("row %+v", rows[0])
	}
	if rows[0].Resource["service.name"] == "" {
		t.Fatalf("resource %+v", rows[0].Resource)
	}
}

func TestPostSendsBatch(t *testing.T) {
	var got int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		rows, err := otlp.Flatten(body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		got = len(rows)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rng := rand.New(rand.NewPCG(3, 4))
	n, err := post(t.Context(), srv.Client(), srv.URL, rng, 10)
	if err != nil {
		t.Fatal(err)
	}
	if n != 10 || got != 10 {
		t.Fatalf("sent %d received %d", n, got)
	}
}
