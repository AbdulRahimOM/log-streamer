package logstream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Event is a normalized record emitted by a configured source.
type Event struct {
	Timestamp time.Time      `json:"timestamp"`
	Source    string         `json:"source"`
	Origin    string         `json:"origin,omitempty"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
}

var levelPattern = regexp.MustCompile(`(?i)(?:^|\b)(DEBUG|INFO|WARN(?:ING)?|ERROR|ERR|TRACE|FATAL|PANIC)(?:\b|:)`)

// Parse converts a JSON record or plain-text line into a normalized event.
// source is always authoritative; origin should be empty when it is unknown.
func Parse(raw, source, origin string, receivedAt time.Time) Event {
	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}
	event := Event{Timestamp: receivedAt, Source: source, Origin: origin, Level: "UNKNOWN", Message: raw}

	var record map[string]any
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&record); err == nil && record != nil {
		if decoder.Decode(new(any)) == io.EOF {
			if message, ok := firstStringValue(record, "msg", "message"); ok {
				event.Message = message
			} else {
				event.Message = raw
			}
			if timestamp, ok := parseTimestamp(firstValue(record, "timestamp", "time", "ts")); ok {
				event.Timestamp = timestamp
			}
			event.Level = normalizeLevel(firstString(record, "level", "severity"))
			fields := make(map[string]any)
			for key, value := range record {
				switch strings.ToLower(key) {
				case "timestamp", "time", "ts", "level", "severity", "msg", "message":
				case "fields":
					if nested, ok := value.(map[string]any); ok {
						for field, fieldValue := range nested {
							fields[field] = fieldValue
						}
					} else {
						fields[key] = value
					}
				default:
					fields[key] = value
				}
			}
			if len(fields) > 0 {
				event.Fields = fields
			}
			return event
		}
	}
	if match := levelPattern.FindStringSubmatch(raw); len(match) == 2 {
		event.Level = normalizeLevel(match[1])
	}
	return event
}

func firstValue(record map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := record[key]; ok {
			return value
		}
	}
	return nil
}

func firstString(record map[string]any, keys ...string) string {
	value, _ := firstStringValue(record, keys...)
	return value
}

func firstStringValue(record map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := record[key].(string); ok {
			return value, true
		}
	}
	return "", false
}

func parseTimestamp(value any) (time.Time, bool) {
	switch v := value.(type) {
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999"} {
			if parsed, err := time.Parse(layout, v); err == nil {
				return parsed, true
			}
		}
	case json.Number:
		f, err := strconv.ParseFloat(string(v), 64)
		if err == nil {
			seconds := int64(f)
			nanos := int64((f - float64(seconds)) * float64(time.Second))
			return time.Unix(seconds, nanos), true
		}
	case float64:
		seconds := int64(v)
		nanos := int64((v - float64(seconds)) * float64(time.Second))
		return time.Unix(seconds, nanos), true
	}
	return time.Time{}, false
}

func normalizeLevel(level string) string {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "TRACE":
		return "TRACE"
	case "DEBUG":
		return "DEBUG"
	case "INFO", "INFORMATION":
		return "INFO"
	case "WARN", "WARNING":
		return "WARN"
	case "ERROR", "ERR", "FATAL", "PANIC", "CRITICAL", "CRIT":
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// ValidateFilter checks that requested filter values are meaningful.
func ValidateFilter(field, value string) error {
	switch field {
	case "level":
		if normalizeLevel(value) == "UNKNOWN" && !strings.EqualFold(value, "UNKNOWN") {
			return fmt.Errorf("unsupported level %q", value)
		}
	case "origin":
		if value != "stdout" && value != "stderr" && value != "file" && value != "http" {
			return fmt.Errorf("unsupported origin %q", value)
		}
	}
	return nil
}
