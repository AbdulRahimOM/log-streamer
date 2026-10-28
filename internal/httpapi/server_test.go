package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abdulrahimom/logstreamer/internal/config"
	"github.com/abdulrahimom/logstreamer/internal/logstream"
)

func testServer(t *testing.T, maxClients int, keepalive string) (*Server, *logstream.Publisher) {
	t.Helper()
	cfg := &config.Config{Server: config.ServerConfig{ListenAddress: ":8080"}, Limits: config.LimitsConfig{SSEKeepalive: keepalive, MaxSSEClients: maxClients}, Sources: []config.SourceConfig{{Name: "api", Type: "docker"}, {Name: "external", Type: "http"}}}
	publisher := logstream.NewPublisher(10, 8)
	server, err := New(cfg, publisher)
	if err != nil {
		t.Fatal(err)
	}
	return server, publisher
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
	onFlush func(int)
	count   int
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{}, 8)}
}

func (r *flushRecorder) Flush() {
	r.ResponseRecorder.Flush()
	r.count++
	select {
	case r.flushed <- struct{}{}:
	default:
	}
	if r.onFlush != nil {
		r.onFlush(r.count)
	}
}

func serve(s *Server, path string, ctx context.Context, recorder *flushRecorder) {
	request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	s.Handler().ServeHTTP(recorder, request)
}

func TestHealthServicesAndDashboard(t *testing.T) {
	server, _ := testServer(t, 2, "1s")
	for path := range map[string]struct{}{"/healthz": {}, "/api/services": {}, "/": {}} {
		recorder := newFlushRecorder()
		serve(server, path, context.Background(), recorder)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d", path, recorder.Code)
		}
		body := recorder.Body.String()
		if path == "/api/services" {
			var payload struct {
				Services []struct{ Name, Type, State string } `json:"services"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Services) != 2 || payload.Services[0].State != "configured" || strings.Contains(body, "token") {
				t.Fatalf("unexpected services response: %s", body)
			}
		}
		if path == "/" && (!strings.Contains(body, "textContent") || strings.Contains(body, "innerHTML")) {
			t.Fatal("dashboard must render event content as text")
		}
	}
}

func TestSSEHeadersEventIDAndJSON(t *testing.T) {
	server, publisher := testServer(t, 2, "1s")
	publisher.Publish(logstream.Event{Source: "api", Level: "INFO", Message: "history"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder := newFlushRecorder()
	recorder.onFlush = func(count int) {
		if count == 2 {
			cancel()
		}
	}
	serve(server, "/api/logs/stream?source=api&level=INFO", ctx, recorder)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "text/event-stream; charset=utf-8" || recorder.Header().Get("Cache-Control") == "" {
		t.Fatalf("unexpected SSE response: status=%d headers=%v", recorder.Code, recorder.Header())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "id: 1\n") || !strings.Contains(body, `"message":"history"`) || !strings.Contains(body, `"source":"api"`) {
		t.Fatalf("unexpected SSE data: %s", body)
	}
}

func TestSSEKeepaliveAndClientCancellation(t *testing.T) {
	api, _ := testServer(t, 2, "1ms")
	ctx, cancel := context.WithCancel(context.Background())
	recorder := newFlushRecorder()
	recorder.onFlush = func(count int) {
		if count == 2 {
			cancel()
		}
	}
	serve(api, "/api/logs/stream", ctx, recorder)
	if !strings.Contains(recorder.Body.String(), ": keepalive\n\n") {
		t.Fatalf("missing keepalive: %q", recorder.Body.String())
	}
	if api.ActiveClients() != 0 {
		t.Fatal("client cancellation did not release its slot")
	}
}

func TestSSEClientLimitAndInvalidFilter(t *testing.T) {
	server, _ := testServer(t, 1, "1s")
	ctx, cancel := context.WithCancel(context.Background())
	firstRecorder := newFlushRecorder()
	done := make(chan struct{})
	go func() { serve(server, "/api/logs/stream", ctx, firstRecorder); close(done) }()
	select {
	case <-firstRecorder.flushed:
	case <-time.After(time.Second):
		t.Fatal("first stream did not start")
	}
	secondRecorder := newFlushRecorder()
	serve(server, "/api/logs/stream", context.Background(), secondRecorder)
	if secondRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("second stream status=%d", secondRecorder.Code)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first stream did not cancel")
	}
	bad := newFlushRecorder()
	serve(server, "/api/logs/stream?origin=container", context.Background(), bad)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid filter status=%d", bad.Code)
	}
}

func TestServerShutdownClosesStreams(t *testing.T) {
	server, _ := testServer(t, 2, "1s")
	ctx, cancel := context.WithCancel(context.Background())
	recorder := newFlushRecorder()
	done := make(chan struct{})
	go func() { serve(server, "/api/logs/stream", ctx, recorder); close(done) }()
	select {
	case <-recorder.flushed:
	case <-time.After(time.Second):
		t.Fatal("stream did not start")
	}
	server.stopOnce.Do(func() { close(server.stop) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("server shutdown did not close stream")
	}
	cancel()
}
