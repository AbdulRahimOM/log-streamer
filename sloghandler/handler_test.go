package sloghandler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHandlerForwardsAndDelegatesGroupedStructuredRecords(t *testing.T) {
	var stdout bytes.Buffer
	received := make(chan map[string]any, 1)
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Query().Get("source") != "app" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization header missing")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type=%q", r.Header.Get("Content-Type"))
		}
		var record map[string]any
		if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
			t.Errorf("decode request: %v", err)
		}
		received <- record
		return response(r, http.StatusAccepted), nil
	})

	downstream := slog.NewJSONHandler(&stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler, err := New("http://logstreamer/api/logs?source=app", "test-token", downstream, WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(handler.WithAttrs([]slog.Attr{slog.String("fixed", "value")}).WithGroup("request"))
	logger.Info("connected", "connection_id", "c-1")
	shutdown(t, handler)

	if !bytes.Contains(stdout.Bytes(), []byte(`"fixed":"value"`)) || !bytes.Contains(stdout.Bytes(), []byte(`"connection_id":"c-1"`)) {
		t.Fatalf("downstream output did not receive record and attributes: %s", stdout.String())
	}
	select {
	case record := <-received:
		if record["level"] != "INFO" || record["msg"] != "connected" || record["time"] == nil {
			t.Fatalf("unexpected forwarded slog fields: %#v", record)
		}
		group, ok := record["request"].(map[string]any)
		if !ok || record["fixed"] != "value" || group["connection_id"] != "c-1" {
			t.Fatalf("WithAttrs/WithGroup not preserved: %#v", record)
		}
	case <-time.After(time.Second):
		t.Fatal("forwarded record was not received")
	}
}

func TestHandlerQueueOverflowDropsOnlyForwardedCopy(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var requests atomic.Int64
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
			return response(r, http.StatusAccepted), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})

	var stdout bytes.Buffer
	handler, err := New("http://logstreamer/?source=app", "token", slog.NewTextHandler(&stdout, nil), WithQueueCapacity(1), WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(handler)
	logger.Info("in flight")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first request did not start")
	}
	logger.Info("queued")
	start := time.Now()
	logger.Info("dropped remotely only")
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("Handle waited for blocked network work")
	}
	if !bytes.Contains(stdout.Bytes(), []byte("dropped remotely only")) {
		t.Fatal("queue overflow dropped the downstream stdout copy")
	}
	close(release)
	shutdown(t, handler)
	if got := requests.Load(); got != 2 {
		t.Fatalf("forwarded %d records; expected in-flight and queued records only", got)
	}
}

func TestHandlerRequestTimeoutAndHTTPFailureDoNotFailHandle(t *testing.T) {
	requestDone := make(chan struct{}, 2)
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/slow" {
			<-r.Context().Done()
			requestDone <- struct{}{}
			return nil, r.Context().Err()
		}
		return response(r, http.StatusInternalServerError), nil
	})
	var stdout bytes.Buffer
	for _, target := range []string{"/slow", "/fail"} {
		t.Run(target, func(t *testing.T) {
			handler, err := New("http://logstreamer"+target+"?source=app", "token", slog.NewTextHandler(&stdout, nil), WithRequestTimeout(20*time.Millisecond), WithHTTPClient(&http.Client{Transport: transport}))
			if err != nil {
				t.Fatal(err)
			}
			if err := slog.New(handler).Handler().Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "event", 0)); err != nil {
				t.Fatalf("asynchronous forwarding failure escaped Handle: %v", err)
			}
			shutdown(t, handler)
		})
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("request timeout did not cancel the slow request")
	}
}

func TestHandlerRespectsDownstreamLevelFilter(t *testing.T) {
	var stdout bytes.Buffer
	var requests atomic.Int64
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return response(r, http.StatusAccepted), nil
	})
	handler, err := New("http://logstreamer/?source=app", "token", slog.NewTextHandler(&stdout, &slog.HandlerOptions{Level: slog.LevelInfo}), WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(handler)
	logger.Debug("filtered")
	logger.Info("forwarded")
	shutdown(t, handler)
	if got := requests.Load(); got != 1 {
		t.Fatalf("forwarded %d records, want only the enabled INFO record", got)
	}
	if bytes.Contains(stdout.Bytes(), []byte("filtered")) || !bytes.Contains(stdout.Bytes(), []byte("forwarded")) {
		t.Fatalf("downstream level filtering was not respected: %s", stdout.String())
	}
}

func TestNewRequiresIngestionSourceAndCredentials(t *testing.T) {
	downstream := slog.NewTextHandler(io.Discard, nil)
	for _, tc := range []struct{ endpoint, token string }{
		{"http://localhost/api/logs", "token"},
		{"http://localhost/api/logs?source=app", ""},
	} {
		if _, err := New(tc.endpoint, tc.token, downstream); err == nil {
			t.Fatalf("New(%q, %q) succeeded without required configuration", tc.endpoint, tc.token)
		}
	}
}

func shutdown(t *testing.T, handler *Handler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handler.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func response(request *http.Request, status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    request,
	}
}
