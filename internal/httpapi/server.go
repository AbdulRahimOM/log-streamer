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
	s.handler = securityHeaders(mux)
	return s, nil
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
		next.ServeHTTP(w, r)
	})
}
