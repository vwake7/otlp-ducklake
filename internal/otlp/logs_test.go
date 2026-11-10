package otlp

import "testing"

func TestFlattenOneRecord(t *testing.T) {
	body := []byte(`{
		"resourceLogs": [{
			"resource": {"attributes": [
				{"key": "service.name", "value": {"stringValue": "api"}},
				{"key": "pid", "value": {"intValue": "7"}}
			]},
			"scopeLogs": [{
				"logRecords": [{
					"timeUnixNano": "1730000000000000000",
					"observedTimeUnixNano": "1730000000000000001",
					"severityNumber": 9,
					"severityText": "INFO",
					"body": {"stringValue": "accepted order 17"},
					"attributes": [{"key": "ready", "value": {"boolValue": true}}],
					"traceId": "abcd",
					"spanId": "ef01"
				}]
			}]
		}]
	}`)
	rows, err := Flatten(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows %d", len(rows))
	}
	row := rows[0]
	if row.Body != "accepted order 17" || row.SeverityText != "INFO" || row.SeverityNumber != 9 {
		t.Fatalf("row %+v", row)
	}
	if row.Resource["service.name"] != "api" || row.Resource["pid"] != "7" {
		t.Fatalf("resource %+v", row.Resource)
	}
	if row.Attributes["ready"] != "true" {
		t.Fatalf("attributes %+v", row.Attributes)
	}
	if row.TraceID != "abcd" || row.SpanID != "ef01" {
		t.Fatalf("ids %s %s", row.TraceID, row.SpanID)
	}
	if row.Timestamp.UnixNano() != 1730000000000000000 {
		t.Fatalf("timestamp %s", row.Timestamp)
	}
}

func TestFlattenUsesObservedTime(t *testing.T) {
	body := []byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{
		"observedTimeUnixNano": "1730000000000000000",
		"body": {"stringValue": "line"}
	}]}]}]}`)
	rows, err := Flatten(body)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Timestamp.UnixNano() != 1730000000000000000 || !rows[0].HasObserved {
		t.Fatalf("row %+v", rows[0])
	}
}

func TestValidateRejectsNonObject(t *testing.T) {
	if err := Validate([]byte(`[]`)); err == nil {
		t.Fatal("expected error")
	}
	if err := Validate([]byte(`{"resourceLogs":{}}`)); err == nil {
		t.Fatal("expected error")
	}
}
