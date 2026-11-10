// Package otlp decodes an OTLP JSON logs export into flat table rows.
package otlp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Row is one log event, ready for the logs table.
type Row struct {
	Timestamp      time.Time
	Observed       time.Time
	HasObserved    bool
	SeverityNumber int16
	SeverityText   string
	Body           string
	TraceID        string
	SpanID         string
	Resource       map[string]string
	Attributes     map[string]string
}

// Validate accepts an OTLP JSON logs request. An empty export is valid.
func Validate(body []byte) error {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return fmt.Errorf("body is not a JSON object")
	}
	var probe struct {
		ResourceLogs json.RawMessage `json:"resourceLogs"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&probe); err != nil {
		return fmt.Errorf("body is not OTLP logs JSON: %w", err)
	}
	if len(bytes.TrimSpace(probe.ResourceLogs)) == 0 {
		return nil
	}
	if probe.ResourceLogs[0] != '[' {
		return fmt.Errorf("resourceLogs is not an array")
	}
	return nil
}

// Flatten turns an OTLP JSON logs request into rows.
// An unset event time is taken from the observed time.
// When both are unset, the timestamp is the time Flatten is called.
func Flatten(body []byte) ([]Row, error) {
	if err := Validate(body); err != nil {
		return nil, err
	}
	var req exportRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var rows []Row
	for _, rl := range req.ResourceLogs {
		resource := attrsToMap(rl.Resource.Attributes)
		for _, sl := range rl.ScopeLogs {
			for _, rec := range sl.LogRecords {
				event, hasEvent := rec.TimeUnixNano.Time()
				observed, hasObserved := rec.ObservedTimeUnixNano.Time()
				ts := event
				if !hasEvent && hasObserved {
					ts = observed
				}
				if ts.IsZero() {
					ts = now
				}
				rows = append(rows, Row{
					Timestamp:      ts,
					Observed:       observed,
					HasObserved:    hasObserved,
					SeverityNumber: rec.SeverityNumber,
					SeverityText:   rec.SeverityText,
					Body:           rec.Body.text(),
					TraceID:        rec.TraceID,
					SpanID:         rec.SpanID,
					Resource:       resource,
					Attributes:     attrsToMap(rec.Attributes),
				})
			}
		}
	}
	return rows, nil
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
	TimeUnixNano         nanoTime    `json:"timeUnixNano"`
	ObservedTimeUnixNano nanoTime    `json:"observedTimeUnixNano"`
	SeverityNumber       int16       `json:"severityNumber"`
	SeverityText         string      `json:"severityText"`
	Body                 anyValue    `json:"body"`
	Attributes           []attribute `json:"attributes"`
	TraceID              string      `json:"traceId"`
	SpanID               string      `json:"spanId"`
}

type attribute struct {
	Key   string   `json:"key"`
	Value anyValue `json:"value"`
}

type anyValue struct {
	StringValue *string         `json:"stringValue"`
	IntValue    *nanoTime       `json:"intValue"`
	BoolValue   *bool           `json:"boolValue"`
	DoubleValue *float64        `json:"doubleValue"`
	BytesValue  *string         `json:"bytesValue"`
	ArrayValue  json.RawMessage `json:"arrayValue"`
	KVListValue json.RawMessage `json:"kvlistValue"`
}

func (v anyValue) text() string {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.IntValue != nil:
		if n, ok := v.IntValue.Int(); ok {
			return strconv.FormatInt(n, 10)
		}
	case v.BoolValue != nil:
		return strconv.FormatBool(*v.BoolValue)
	case v.DoubleValue != nil:
		return strconv.FormatFloat(*v.DoubleValue, 'f', -1, 64)
	case v.BytesValue != nil:
		return *v.BytesValue
	case len(v.ArrayValue) > 0:
		return string(v.ArrayValue)
	case len(v.KVListValue) > 0:
		return string(v.KVListValue)
	}
	return ""
}

func attrsToMap(attrs []attribute) map[string]string {
	if len(attrs) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(attrs))
	for _, a := range attrs {
		out[a.Key] = a.Value.text()
	}
	return out
}

// nanoTime accepts an OTLP JSON int64, which is either a decimal string or a number.
type nanoTime struct {
	raw string
}

func (n *nanoTime) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		n.raw = ""
		return nil
	}
	if b[0] == '"' {
		s, err := strconv.Unquote(string(b))
		if err != nil {
			return err
		}
		n.raw = s
		return nil
	}
	n.raw = string(b)
	return nil
}

func (n nanoTime) Int() (int64, bool) {
	if n.raw == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(n.raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func (n nanoTime) Time() (time.Time, bool) {
	v, ok := n.Int()
	if !ok || v == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, v).UTC(), true
}
