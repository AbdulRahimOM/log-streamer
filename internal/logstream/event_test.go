package logstream

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseJSONPreservesTimestampFieldsAndConfiguredSource(t *testing.T) {
	wantTime := time.Date(2025, 2, 3, 4, 5, 6, 7, time.UTC)
	event := Parse(`{"timestamp":"2025-02-03T04:05:06.000000007Z","level":"warning","msg":"hello","service":"payload-service","attempt":2}`, "configured", "stderr", time.Now())
	if !event.Timestamp.Equal(wantTime) || event.Source != "configured" || event.Origin != "stderr" || event.Level != "WARN" || event.Message != "hello" {
		t.Fatalf("unexpected parsed event: %#v", event)
	}
	if event.Fields["service"] != "payload-service" || event.Fields["attempt"] != json.Number("2") {
		t.Fatalf("extra fields not preserved: %#v", event.Fields)
	}
}

func TestParsePlainTextAndUnknownLevels(t *testing.T) {
	received := time.Now()
	if event := Parse("2025-01-01 INFO started", "api", "", received); event.Level != "INFO" || event.Message != "2025-01-01 INFO started" || !event.Timestamp.Equal(received) {
		t.Fatalf("unexpected plain event: %#v", event)
	}
	if event := Parse("something happened", "api", "", received); event.Level != "UNKNOWN" {
		t.Fatalf("unknown level = %q", event.Level)
	}
}

func TestParseInvalidTimestampUsesReceiptTime(t *testing.T) {
	received := time.Now()
	event := Parse(`{"time":"n/a","level":"debug","message":"test"}`, "api", "", received)
	if !event.Timestamp.Equal(received) || event.Level != "DEBUG" {
		t.Fatalf("unexpected fallback event: %#v", event)
	}
}

func TestParseRejectsTrailingJSONAndKeepsRawLine(t *testing.T) {
	raw := `{"level":"ERROR"} trailing`
	event := Parse(raw, "api", "", time.Now())
	if event.Message != raw || event.Level != "ERROR" {
		t.Fatalf("malformed JSON was treated as structured: %#v", event)
	}
}
