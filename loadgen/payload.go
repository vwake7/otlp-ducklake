package main

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"time"
)

var services = []string{"api", "checkout", "auth", "billing", "worker", "gateway"}

var levels = []struct {
	number int
	text   string
}{
	{5, "DEBUG"},
	{9, "INFO"},
	{13, "WARN"},
	{17, "ERROR"},
}

var messages = []string{
	"accepted order",
	"request finished",
	"cache miss",
	"upstream timeout",
	"retrying publish",
	"user signed in",
	"payment declined",
	"queue depth high",
}

type exportRequest struct {
	ResourceLogs []resourceLog `json:"resourceLogs"`
}

type resourceLog struct {
	Resource  resource   `json:"resource"`
	ScopeLogs []scopeLog `json:"scopeLogs"`
}

type resource struct {
	Attributes []attribute `json:"attributes"`
}

type scopeLog struct {
	LogRecords []logRecord `json:"logRecords"`
}

type logRecord struct {
	TimeUnixNano         string      `json:"timeUnixNano"`
	ObservedTimeUnixNano string      `json:"observedTimeUnixNano"`
	SeverityNumber       int         `json:"severityNumber"`
	SeverityText         string      `json:"severityText"`
	Body                 stringValue `json:"body"`
	Attributes           []attribute `json:"attributes"`
	TraceID              string      `json:"traceId"`
	SpanID               string      `json:"spanId"`
}

type attribute struct {
	Key   string      `json:"key"`
	Value stringValue `json:"value"`
}

type stringValue struct {
	StringValue string `json:"stringValue"`
}

// payload builds one OTLP JSON logs request with n random records around now.
func payload(rng *rand.Rand, n int, now time.Time) ([]byte, error) {
	service := services[rng.IntN(len(services))]
	records := make([]logRecord, n)
	base := now.UTC().UnixNano()
	for i := range records {
		level := levels[rng.IntN(len(levels))]
		ts := base - int64(rng.IntN(5_000_000_000))
		stamp := fmt.Sprintf("%d", ts)
		records[i] = logRecord{
			TimeUnixNano:         stamp,
			ObservedTimeUnixNano: stamp,
			SeverityNumber:       level.number,
			SeverityText:         level.text,
			Body:                 stringValue{StringValue: fmt.Sprintf("%s %d", messages[rng.IntN(len(messages))], rng.IntN(1_000_000))},
			Attributes: []attribute{
				{Key: "order.id", Value: stringValue{StringValue: fmt.Sprintf("%d", rng.IntN(1_000_000))}},
				{Key: "region", Value: stringValue{StringValue: regions[rng.IntN(len(regions))]}},
			},
			TraceID: fmt.Sprintf("%016x%016x", rng.Uint64(), rng.Uint64()),
			SpanID:  fmt.Sprintf("%016x", rng.Uint64()),
		}
	}
	body, err := json.Marshal(exportRequest{
		ResourceLogs: []resourceLog{{
			Resource: resource{Attributes: []attribute{
				{Key: "service.name", Value: stringValue{StringValue: service}},
			}},
			ScopeLogs: []scopeLog{{LogRecords: records}},
		}},
	})
	if err != nil {
		return nil, err
	}
	return body, nil
}

var regions = []string{"eu-west", "eu-central", "us-east", "ap-south"}
