package otlp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// MetricRow is one numeric sample, ready for the metrics table.
type MetricRow struct {
	Timestamp  time.Time
	Name       string
	Type       string
	Unit       string
	Value      float64
	Resource   map[string]string
	Attributes map[string]string
}

// ValidateMetrics accepts an OTLP JSON metrics request. An empty export is valid.
func ValidateMetrics(body []byte) error {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return fmt.Errorf("body is not a JSON object")
	}
	var probe struct {
		ResourceMetrics json.RawMessage `json:"resourceMetrics"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&probe); err != nil {
		return fmt.Errorf("body is not OTLP metrics JSON: %w", err)
	}
	if len(bytes.TrimSpace(probe.ResourceMetrics)) == 0 {
		return nil
	}
	if probe.ResourceMetrics[0] != '[' {
		return fmt.Errorf("resourceMetrics is not an array")
	}
	return nil
}

// FlattenMetrics turns an OTLP JSON metrics request into rows.
// A gauge or sum data point is one row. A histogram is one row per bucket,
// plus one row for the count and one for the sum. The bucket bound is attributes["le"].
// Count and sum rows set attributes["component"] to "count" or "sum".
func FlattenMetrics(body []byte) ([]MetricRow, error) {
	if err := ValidateMetrics(body); err != nil {
		return nil, err
	}
	var req metricExport
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var rows []MetricRow
	for _, rm := range req.ResourceMetrics {
		resource := attrsToMap(rm.Resource.Attributes)
		for _, sm := range rm.ScopeMetrics {
			for _, metric := range sm.Metrics {
				rows = append(rows, metricRows(metric, resource, now)...)
			}
		}
	}
	return rows, nil
}

func metricRows(metric otlpMetric, resource map[string]string, now time.Time) []MetricRow {
	switch {
	case metric.Gauge != nil:
		return numberRows(metric, "gauge", metric.Gauge.DataPoints, resource, now)
	case metric.Sum != nil:
		extra := map[string]string{}
		if metric.Sum.IsMonotonic != nil {
			extra["is_monotonic"] = strconv.FormatBool(*metric.Sum.IsMonotonic)
		}
		if label := temporality(metric.Sum.AggregationTemporality); label != "" {
			extra["aggregation_temporality"] = label
		}
		return numberRows(metric, "sum", metric.Sum.DataPoints, resource, now, extra)
	case metric.Histogram != nil:
		return histogramRows(metric, metric.Histogram.DataPoints, resource, now)
	default:
		return nil
	}
}

func numberRows(metric otlpMetric, kind string, points []numberPoint, resource map[string]string, now time.Time, extra ...map[string]string) []MetricRow {
	rows := make([]MetricRow, 0, len(points))
	for _, point := range points {
		value, ok := point.value()
		if !ok {
			continue
		}
		attrs := attrsToMap(point.Attributes)
		if len(extra) > 0 {
			for k, v := range extra[0] {
				attrs[k] = v
			}
		}
		rows = append(rows, MetricRow{
			Timestamp:  pointTime(point.TimeUnixNano, now),
			Name:       metric.Name,
			Type:       kind,
			Unit:       metric.Unit,
			Value:      value,
			Resource:   resource,
			Attributes: attrs,
		})
	}
	return rows
}

func histogramRows(metric otlpMetric, points []histogramPoint, resource map[string]string, now time.Time) []MetricRow {
	var rows []MetricRow
	for _, point := range points {
		ts := pointTime(point.TimeUnixNano, now)
		base := attrsToMap(point.Attributes)
		for i, count := range point.BucketCounts {
			attrs := copyAttrs(base)
			attrs["le"] = bucketBound(point.ExplicitBounds, i)
			attrs["component"] = "bucket"
			value, _ := count.float()
			rows = append(rows, MetricRow{
				Timestamp: ts, Name: metric.Name, Type: "histogram", Unit: metric.Unit,
				Value: value, Resource: resource, Attributes: attrs,
			})
		}
		if point.Count.raw != "" {
			attrs := copyAttrs(base)
			attrs["component"] = "count"
			value, _ := point.Count.float()
			rows = append(rows, MetricRow{
				Timestamp: ts, Name: metric.Name, Type: "histogram", Unit: metric.Unit,
				Value: value, Resource: resource, Attributes: attrs,
			})
		}
		if point.Sum != nil {
			attrs := copyAttrs(base)
			attrs["component"] = "sum"
			rows = append(rows, MetricRow{
				Timestamp: ts, Name: metric.Name, Type: "histogram", Unit: metric.Unit,
				Value: *point.Sum, Resource: resource, Attributes: attrs,
			})
		}
	}
	return rows
}

func bucketBound(bounds []float64, i int) string {
	if i >= 0 && i < len(bounds) {
		return strconv.FormatFloat(bounds[i], 'f', -1, 64)
	}
	return "+Inf"
}

func pointTime(ts nanoTime, now time.Time) time.Time {
	if t, ok := ts.Time(); ok {
		return t
	}
	return now
}

func temporality(v *int) string {
	if v == nil {
		return ""
	}
	switch *v {
	case 1:
		return "delta"
	case 2:
		return "cumulative"
	default:
		return strconv.Itoa(*v)
	}
}

func copyAttrs(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}

type metricExport struct {
	ResourceMetrics []resourceMetrics `json:"resourceMetrics"`
}

type resourceMetrics struct {
	Resource     resource      `json:"resource"`
	ScopeMetrics []scopeMetric `json:"scopeMetrics"`
}

type scopeMetric struct {
	Metrics []otlpMetric `json:"metrics"`
}

type otlpMetric struct {
	Name      string     `json:"name"`
	Unit      string     `json:"unit"`
	Gauge     *dataBatch `json:"gauge"`
	Sum       *sumBatch  `json:"sum"`
	Histogram *histBatch `json:"histogram"`
}

type dataBatch struct {
	DataPoints []numberPoint `json:"dataPoints"`
}

type sumBatch struct {
	DataPoints             []numberPoint `json:"dataPoints"`
	AggregationTemporality *int          `json:"aggregationTemporality"`
	IsMonotonic            *bool         `json:"isMonotonic"`
}

type histBatch struct {
	DataPoints []histogramPoint `json:"dataPoints"`
}

type numberPoint struct {
	TimeUnixNano nanoTime    `json:"timeUnixNano"`
	AsDouble     *float64    `json:"asDouble"`
	AsInt        flexNum     `json:"asInt"`
	Attributes   []attribute `json:"attributes"`
}

func (p numberPoint) value() (float64, bool) {
	if p.AsDouble != nil {
		return *p.AsDouble, true
	}
	return p.AsInt.float()
}

type histogramPoint struct {
	TimeUnixNano   nanoTime    `json:"timeUnixNano"`
	Count          flexNum     `json:"count"`
	Sum            *float64    `json:"sum"`
	BucketCounts   []flexNum   `json:"bucketCounts"`
	ExplicitBounds []float64   `json:"explicitBounds"`
	Attributes     []attribute `json:"attributes"`
}

// flexNum accepts an OTLP JSON number, which may be a decimal string or a JSON number.
type flexNum struct {
	raw string
}

func (n *flexNum) UnmarshalJSON(b []byte) error {
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

func (n flexNum) float() (float64, bool) {
	if n.raw == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(n.raw, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
