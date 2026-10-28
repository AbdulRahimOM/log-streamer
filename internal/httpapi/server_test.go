package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abdulrahimom/logstreamer/internal/collectors"
	"github.com/abdulrahimom/logstreamer/internal/config"
	"github.com/abdulrahimom/logstreamer/internal/logstream"
)

func testServer(t *testing.T, maxClients int, keepalive string) (*Server, *logstream.Publisher) {
	return testServerWithOrigins(t, maxClients, keepalive, nil)
}

func testServerWithOrigins(t *testing.T, maxClients int, keepalive string, origins []string) (*Server, *logstream.Publisher) {
	t.Helper()
	cfg := &config.Config{Server: config.ServerConfig{ListenAddress: ":8080", CORSAllowedOrigins: origins}, IngestToken: "test-token", Limits: config.LimitsConfig{SSEKeepalive: keepalive, MaxSSEClients: maxClients, IngestMaxBodyBytes: 1024, IngestMaxBatch: 2, MaxEventBytes: 128}, Sources: []config.SourceConfig{{Name: "api", Type: "docker"}, {Name: "external", Type: "http"}}}
	publisher := logstream.NewPublisher(10, 8)
	server, err := New(cfg, publisher, collectors.NewRegistry(cfg.Sources))
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
	serveRequest(s, http.MethodGet, path, nil, nil, ctx, recorder)
}

func serveRequest(s *Server, method, path string, body io.Reader, headers http.Header, ctx context.Context, recorder *flushRecorder) {
	request := httptest.NewRequest(method, path, body).WithContext(ctx)
	request.Header = headers.Clone()
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
		if path == "/" {
			for _, feature := range []string{"id=\"source-filter\"", "id=\"level-filter\"", "id=\"pause\"", "id=\"auto-scroll\"", "id=\"clear\"", "new EventSource('/api/logs/stream')", "const maxEvents = 500", "events.shift()"} {
				if !strings.Contains(body, feature) {
					t.Errorf("dashboard is missing %q", feature)
				}
			}
			for _, header := range []string{"Content-Security-Policy", "X-Frame-Options", "Permissions-Policy"} {
				if recorder.Header().Get(header) == "" {
					t.Errorf("dashboard response missing %s security header", header)
				}
			}
		}
	}
}

func TestCORSExactOriginsAndRouteScopedPreflight(t *testing.T) {
	server, _ := testServerWithOrigins(t, 2, "1s", []string{"http://localhost:5173"})

	t.Run("allowed services GET", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/api/services", nil)
		r.Header.Set("Origin", "http://localhost:5173")
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" || w.Header().Get("Vary") == "" {
			t.Fatalf("status=%d headers=%v", w.Code, w.Header())
		}
		if w.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Fatal("CORS must not allow credentials")
		}
	})

	t.Run("reject unlisted origin", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/api/services", nil)
		r.Header.Set("Origin", "https://attacker.example")
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("status=%d headers=%v", w.Code, w.Header())
		}
	})

	t.Run("stream reconnect preflight", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodOptions, "/api/logs/stream", nil)
		r.Header.Set("Origin", "http://localhost:5173")
		r.Header.Set("Access-Control-Request-Method", "GET")
		r.Header.Set("Access-Control-Request-Headers", "Last-Event-ID")
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Headers") != "Last-Event-ID" || w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
			t.Fatalf("status=%d headers=%v", w.Code, w.Header())
		}
	})

	t.Run("reject unapproved preflight header", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodOptions, "/api/services", nil)
		r.Header.Set("Origin", "http://localhost:5173")
		r.Header.Set("Access-Control-Request-Method", "GET")
		r.Header.Set("Access-Control-Request-Headers", "Authorization")
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status=%d, want forbidden", w.Code)
		}
	})

	t.Run("cors does not cover ingestion", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodOptions, "/api/logs", nil)
		r.Header.Set("Origin", "https://attacker.example")
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("unexpected CORS header on ingestion route: %v", w.Header())
		}
	})
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

func TestSSECORSAllowedOrigin(t *testing.T) {
	server, _ := testServerWithOrigins(t, 2, "1s", []string{"http://localhost:5173"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder := newFlushRecorder()
	recorder.onFlush = func(count int) {
		if count == 1 {
			cancel()
		}
	}
	headers := http.Header{"Origin": []string{"http://localhost:5173"}}
	serveRequest(server, http.MethodGet, "/api/logs/stream", nil, headers, ctx, recorder)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("status=%d headers=%v", recorder.Code, recorder.Header())
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

func TestHTTPIngestionPublishesWithConfiguredSourceIdentity(t *testing.T) {
	server, publisher := testServer(t, 2, "1s")
	headers := http.Header{"Authorization": []string{"Bearer test-token"}}
	recorder := newFlushRecorder()
	body := `{"timestamp":"2025-01-02T03:04:05Z","level":"info","message":"hello","source":"caller","fields":{"trace":"abc"}}`
	serveRequest(server, http.MethodPost, "/api/logs?source=external", strings.NewReader(body), headers, context.Background(), recorder)
	if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"accepted":1`) {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	sub, history, _ := publisher.Subscribe(logstream.Filter{})
	defer publisher.Unsubscribe(sub)
	if len(history) != 1 {
		t.Fatalf("published %d events", len(history))
	}
	event := history[0].Event
	if event.Source != "external" || event.Origin != "http" || event.Level != "INFO" || event.Message != "hello" || event.Fields["source"] != "caller" || event.Fields["trace"] != "abc" {
		t.Fatalf("ingested event identity or fields were wrong: %#v", event)
	}
}

func TestHTTPIngestionAuthAndSourceSelection(t *testing.T) {
	server, _ := testServer(t, 2, "1s")
	cases := []struct {
		name, path, token string
		want              int
	}{
		{"missing token", "/api/logs?source=external", "", http.StatusUnauthorized},
		{"wrong token", "/api/logs?source=external", "wrong", http.StatusUnauthorized},
		{"missing source", "/api/logs", "test-token", http.StatusBadRequest},
		{"unknown source", "/api/logs?source=not-configured", "test-token", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := make(http.Header)
			if tc.token != "" {
				headers.Set("Authorization", "Bearer "+tc.token)
			}
			recorder := newFlushRecorder()
			serveRequest(server, http.MethodPost, tc.path, strings.NewReader(`{"message":"hi"}`), headers, context.Background(), recorder)
			if recorder.Code != tc.want {
				t.Fatalf("status=%d, want %d: %q", recorder.Code, tc.want, recorder.Body.String())
			}
		})
	}
}

func TestHTTPIngestionRejectsWholeInvalidBatchWithoutPublishing(t *testing.T) {
	server, publisher := testServer(t, 2, "1s")
	recorder := newFlushRecorder()
	headers := http.Header{"Authorization": []string{"Bearer test-token"}}
	body := `[{"message":"valid"},{"level":"INFO"}]`
	serveRequest(server, http.MethodPost, "/api/logs?source=external", strings.NewReader(body), headers, context.Background(), recorder)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	sub, history, _ := publisher.Subscribe(logstream.Filter{})
	publisher.Unsubscribe(sub)
	if len(history) != 0 {
		t.Fatalf("invalid batch partially published %d events", len(history))
	}
}

func TestHTTPIngestionEnforcesBodyEventAndBatchLimits(t *testing.T) {
	server, _ := testServer(t, 2, "1s")
	headers := http.Header{"Authorization": []string{"Bearer test-token"}}
	cases := []struct {
		name, body string
		want       int
	}{
		{"body bytes", strings.Repeat("x", 1100), http.StatusRequestEntityTooLarge},
		{"event bytes", `{"message":"` + strings.Repeat("x", 130) + `"}`, http.StatusRequestEntityTooLarge},
		{"batch count", `[{"message":"one"},{"message":"two"},{"message":"three"}]`, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := newFlushRecorder()
			serveRequest(server, http.MethodPost, "/api/logs?source=external", strings.NewReader(tc.body), headers, context.Background(), recorder)
			if recorder.Code != tc.want {
				t.Fatalf("status=%d, want %d: %q", recorder.Code, tc.want, recorder.Body.String())
			}
		})
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
