package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/abdulrahimom/logstreamer/internal/config"
	"github.com/abdulrahimom/logstreamer/internal/logstream"
)

func ingestionHandler(cfg *config.Config, publisher *logstream.Publisher) http.Handler {
	allowedSources := make(map[string]struct{})
	for _, source := range cfg.Sources {
		if source.Type == "http" {
			allowedSources[source.Name] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(allowedSources) == 0 {
			http.NotFound(w, r)
			return
		}
		authorization := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(authorization, prefix) || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(authorization, prefix)), []byte(cfg.IngestToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		source := strings.TrimSpace(r.URL.Query().Get("source"))
		if source == "" {
			http.Error(w, "source query parameter is required", http.StatusBadRequest)
			return
		}
		if _, ok := allowedSources[source]; !ok {
			http.Error(w, "HTTP source is not configured", http.StatusNotFound)
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, cfg.Limits.IngestMaxBodyBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "invalid request body", http.StatusBadRequest)
			}
			return
		}
		records, err := decodeEvents(body, cfg.Limits.IngestMaxBatch, cfg.Limits.MaxEventBytes, source, time.Now())
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, errIngestTooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			http.Error(w, err.Error(), status)
			return
		}
		for _, event := range records {
			publisher.Publish(event)
		}
		writeJSON(w, http.StatusAccepted, map[string]int{"accepted": len(records)})
	})
}

var errIngestTooLarge = errors.New("event or batch limit exceeded")

func decodeEvents(body []byte, maxBatch, maxEventBytes int, source string, receivedAt time.Time) ([]logstream.Event, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, errors.New("request body must contain an event")
	}
	var raws []json.RawMessage
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(body, &raws); err != nil {
			return nil, errors.New("malformed JSON batch")
		}
		if len(raws) == 0 {
			return nil, errors.New("event batch must not be empty")
		}
		if len(raws) > maxBatch {
			return nil, errIngestTooLarge
		}
	} else {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(body, &object); err != nil || object == nil {
			return nil, errors.New("request must be a JSON event or array of events")
		}
		raws = []json.RawMessage{append([]byte(nil), body...)}
	}

	events := make([]logstream.Event, 0, len(raws))
	for _, raw := range raws {
		if len(raw) > maxEventBytes {
			return nil, errIngestTooLarge
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			return nil, errors.New("each event must be a JSON object")
		}
		rawMessage, hasMessage := object["msg"]
		if !hasMessage {
			rawMessage, hasMessage = object["message"]
		}
		if !hasMessage {
			return nil, errors.New("each event requires a string message or msg")
		}
		if len(rawMessage) == 0 || rawMessage[0] != '"' {
			return nil, errors.New("event message must be a string")
		}
		var message string
		if err := json.Unmarshal(rawMessage, &message); err != nil {
			return nil, errors.New("event message must be a string")
		}
		if level, ok := object["level"]; ok {
			var value string
			if err := json.Unmarshal(level, &value); err != nil {
				return nil, errors.New("event level must be a string")
			}
		}
		if fields, ok := object["fields"]; ok {
			var value map[string]json.RawMessage
			if err := json.Unmarshal(fields, &value); err != nil || value == nil {
				return nil, errors.New("event fields must be a JSON object")
			}
		}
		events = append(events, logstream.Parse(string(raw), source, "http", receivedAt))
	}
	return events, nil
}
