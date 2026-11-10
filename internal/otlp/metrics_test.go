package otlp

import "testing"

func TestFlattenGaugeAndHistogram(t *testing.T) {
	body := []byte(`{
		"resourceMetrics": [{
			"resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "api"}}]},
			"scopeMetrics": [{
				"metrics": [
					{
						"name": "queue.depth",
						"unit": "1",
						"gauge": {"dataPoints": [{
							"timeUnixNano": "1730000000000000000",
							"asInt": "7",
							"attributes": [{"key": "worker", "value": {"stringValue": "a"}}]
						}]}
					},
					{
						"name": "http.server.duration",
						"unit": "ms",
						"histogram": {"dataPoints": [{
							"timeUnixNano": "1730000000000000000",
							"count": "9",
							"sum": 12.5,
							"bucketCounts": ["4", "5"],
							"explicitBounds": [10]
						}]}
					}
				]
			}]
		}]
	}`)
	rows, err := FlattenMetrics(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("rows %d", len(rows))
	}
	if rows[0].Name != "queue.depth" || rows[0].Type != "gauge" || rows[0].Value != 7 {
		t.Fatalf("gauge %+v", rows[0])
	}
	if rows[0].Resource["service.name"] != "api" || rows[0].Attributes["worker"] != "a" {
		t.Fatalf("attrs %+v %+v", rows[0].Resource, rows[0].Attributes)
	}
	if rows[1].Attributes["le"] != "10" || rows[1].Attributes["component"] != "bucket" || rows[1].Value != 4 {
		t.Fatalf("bucket %+v", rows[1])
	}
	if rows[2].Attributes["le"] != "+Inf" || rows[2].Value != 5 {
		t.Fatalf("inf %+v", rows[2])
	}
	if rows[3].Attributes["component"] != "count" || rows[3].Value != 9 {
		t.Fatalf("count %+v", rows[3])
	}
	if rows[4].Attributes["component"] != "sum" || rows[4].Value != 12.5 {
		t.Fatalf("sum %+v", rows[4])
	}
}

func TestValidateMetricsRejectsArray(t *testing.T) {
	if err := ValidateMetrics([]byte(`[]`)); err == nil {
		t.Fatal("expected error")
	}
}
