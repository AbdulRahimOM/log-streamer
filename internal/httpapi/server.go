package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abdulrahimom/logstreamer/internal/collectors"
	"github.com/abdulrahimom/logstreamer/internal/config"
	"github.com/abdulrahimom/logstreamer/internal/logstream"
)

type Server struct {
	handler    http.Handler
	listen     string
	keepalive  time.Duration
	maxClients chan struct{}
	clients    atomic.Int64
	stop       chan struct{}
	stopOnce   sync.Once
}

func New(cfg *config.Config, publisher *logstream.Publisher, statuses *collectors.Registry) (*Server, error) {
	if statuses == nil {
		statuses = collectors.NewRegistry(cfg.Sources)
	}
	for _, source := range cfg.Sources {
		if source.Type == "http" && strings.TrimSpace(cfg.IngestToken) == "" {
			return nil, fmt.Errorf("LOGSTREAMER_INGEST_TOKEN is required when an HTTP source is configured")
		}
	}
	keepalive, err := time.ParseDuration(cfg.Limits.SSEKeepalive)
	if err != nil {
		return nil, fmt.Errorf("parse SSE keepalive: %w", err)
	}
	s := &Server{
		listen: cfg.Server.ListenAddress, keepalive: keepalive,
		maxClients: make(chan struct{}, cfg.Limits.MaxSSEClients), stop: make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", dashboard)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/services", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"services": statuses.Snapshot()})
	})
	mux.HandleFunc("GET /api/logs/stream", s.stream(publisher))
	mux.Handle("POST /api/logs", ingestionHandler(cfg, publisher))
	s.handler = securityHeaders(cors(mux, cfg.Server.CORSAllowedOrigins))
	return s, nil
}

// cors limits cross-origin browser access to the two read-only APIs. Requests
// without Origin remain available to local tools and same-origin clients.
func cors(next http.Handler, allowedOrigins []string) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isServices := r.URL.Path == "/api/services"
		isStream := r.URL.Path == "/api/logs/stream"
		crossOriginRoute := isServices || isStream
		if crossOriginRoute {
			w.Header().Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.Header().Add("Vary", "Access-Control-Request-Method")
				w.Header().Add("Vary", "Access-Control-Request-Headers")
				origin := r.Header.Get("Origin")
				if _, ok := allowed[origin]; !ok || origin == "" || r.Header.Get("Access-Control-Request-Method") != http.MethodGet {
					http.Error(w, "CORS preflight is not allowed", http.StatusForbidden)
					return
				}
				requestedHeaders := strings.Split(strings.ToLower(r.Header.Get("Access-Control-Request-Headers")), ",")
				for _, header := range requestedHeaders {
					header = strings.TrimSpace(header)
					if header != "" && header != "last-event-id" {
						http.Error(w, "CORS preflight header is not allowed", http.StatusForbidden)
						return
					}
				}
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET")
				if strings.TrimSpace(r.Header.Get("Access-Control-Request-Headers")) != "" {
					w.Header().Set("Access-Control-Allow-Headers", "Last-Event-ID")
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				if _, ok := allowed[origin]; !ok {
					http.Error(w, "origin is not allowed", http.StatusForbidden)
					return
				}
				if r.Method == http.MethodGet {
					w.Header().Set("Access-Control-Allow-Origin", origin)
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) ActiveClients() int64 { return s.clients.Load() }

func (s *Server) Start(ctx context.Context) error {
	server := &http.Server{
		Addr: s.listen, Handler: s.handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	case <-ctx.Done():
		s.stopOnce.Do(func() { close(s.stop) })
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-errCh
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

func (s *Server) stream(publisher *logstream.Publisher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter := logstream.Filter{Source: strings.TrimSpace(r.URL.Query().Get("source"))}
		if value := strings.TrimSpace(r.URL.Query().Get("level")); value != "" {
			if err := logstream.ValidateFilter("level", value); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			filter.Level = strings.ToUpper(value)
			if filter.Level == "WARNING" {
				filter.Level = "WARN"
			}
			if filter.Level == "TRACE" {
				filter.Level = "DEBUG"
			}
			if filter.Level == "ERR" || filter.Level == "FATAL" || filter.Level == "PANIC" {
				filter.Level = "ERROR"
			}
		}
		filter.Origin = strings.TrimSpace(r.URL.Query().Get("origin"))
		if filter.Origin != "" {
			if err := logstream.ValidateFilter("origin", filter.Origin); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}

		select {
		case s.maxClients <- struct{}{}:
			defer func() { <-s.maxClients }()
			s.clients.Add(1)
			defer s.clients.Add(-1)
		default:
			http.Error(w, "maximum stream clients reached", http.StatusServiceUnavailable)
			return
		}

		subscriber, snapshot, highWater := publisher.Subscribe(filter)
		defer publisher.Unsubscribe(subscriber)
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming is not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		for _, delivery := range snapshot {
			if err := writeEvent(w, flusher, delivery); err != nil {
				return
			}
		}
		ticker := time.NewTicker(s.keepalive)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-s.stop:
				return
			case delivery, open := <-subscriber.C:
				if !open {
					return
				}
				if delivery.ID <= highWater {
					continue
				}
				if err := writeEvent(w, flusher, delivery); err != nil {
					return
				}
			case <-ticker.C:
				if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}

func writeEvent(w http.ResponseWriter, flusher http.Flusher, delivery logstream.Delivery) error {
	data, err := json.Marshal(delivery.Event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", delivery.ID, data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}
