// Package sloghandler provides a slog.Handler that writes records to a
// downstream handler and forwards a bounded, asynchronous copy to LogStreamer.
package sloghandler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	defaultQueueCapacity  = 256
	defaultRequestTimeout = 2 * time.Second
)

// Option configures the forwarding worker.
type Option func(*options) error

type options struct {
	queueCapacity  int
	requestTimeout time.Duration
	client         *http.Client
}

// WithQueueCapacity sets the maximum number of waiting forwarded records.
// Records arriving while the queue is full are still sent to the downstream
// handler but their forwarded copies are dropped.
func WithQueueCapacity(capacity int) Option {
	return func(opts *options) error {
		if capacity <= 0 {
			return errors.New("sloghandler: queue capacity must be positive")
		}
		opts.queueCapacity = capacity
		return nil
	}
}

// WithRequestTimeout sets the maximum duration of an individual HTTP request.
func WithRequestTimeout(timeout time.Duration) Option {
	return func(opts *options) error {
		if timeout <= 0 {
			return errors.New("sloghandler: request timeout must be positive")
		}
		opts.requestTimeout = timeout
		return nil
	}
}

// WithHTTPClient supplies the client used by the forwarding worker. Request
// contexts still enforce the configured request timeout.
func WithHTTPClient(client *http.Client) Option {
	return func(opts *options) error {
		if client == nil {
			return errors.New("sloghandler: HTTP client must not be nil")
		}
		opts.client = client
		return nil
	}
}

// Handler synchronously delegates records to downstream and asynchronously
// forwards a structured JSON copy to LogStreamer.
type Handler struct {
	downstream slog.Handler
	forwardOps []forwardOp
	shared     *shared
}

type forwardOp struct {
	group string
	attrs []slog.Attr
}

type shared struct {
	endpoint string
	token    string
	timeout  time.Duration
	client   *http.Client
	queue    chan []byte
	done     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	closed   bool
}

// New creates a forwarding handler. endpoint must be the complete ingestion
// URL, including a source query parameter such as
// http://localhost:8080/api/logs?source=my-app. token is sent as a bearer
// credential. downstream receives every record synchronously; for example,
// use slog.NewJSONHandler(os.Stdout, nil) to preserve local JSON output.
func New(endpoint, token string, downstream slog.Handler, opts ...Option) (*Handler, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || strings.TrimSpace(parsed.Query().Get("source")) == "" {
		return nil, errors.New("sloghandler: endpoint must be an HTTP ingestion URL with a source query parameter")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("sloghandler: bearer token must not be empty")
	}
	if downstream == nil {
		return nil, errors.New("sloghandler: downstream handler must not be nil")
	}
	settings := options{queueCapacity: defaultQueueCapacity, requestTimeout: defaultRequestTimeout, client: &http.Client{}}
	for _, option := range opts {
		if option == nil {
			return nil, errors.New("sloghandler: option must not be nil")
		}
		if err := option(&settings); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &shared{
		endpoint: endpoint, token: token, timeout: settings.requestTimeout,
		client: settings.client, queue: make(chan []byte, settings.queueCapacity),
		done: make(chan struct{}), ctx: ctx, cancel: cancel,
	}
	go h.run()
	return &Handler{
		downstream: downstream,
		shared:     h,
	}, nil
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.downstream.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	stdoutErr := h.downstream.Handle(ctx, record)

	var encoded bytes.Buffer
	var forward slog.Handler = slog.NewJSONHandler(&encoded, nil)
	for _, operation := range h.forwardOps {
		if operation.group != "" {
			forward = forward.WithGroup(operation.group)
		} else if len(operation.attrs) > 0 {
			forward = forward.WithAttrs(operation.attrs)
		}
	}
	if err := forward.Handle(ctx, record); err != nil {
		return stdoutErr
	}
	if encoded.Len() == 0 {
		return stdoutErr
	}
	payload := bytes.TrimSpace(encoded.Bytes())
	if !json.Valid(payload) {
		return stdoutErr
	}
	snapshot := append([]byte(nil), payload...)
	h.shared.mu.Lock()
	if !h.shared.closed {
		select {
		case h.shared.queue <- snapshot:
		default:
			// A full queue must not delay or fail the downstream log path.
		}
	}
	h.shared.mu.Unlock()
	return stdoutErr
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{
		downstream: h.downstream.WithAttrs(attrs),
		forwardOps: appendForwardAttrs(h.forwardOps, attrs),
		shared:     h.shared,
	}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{
		downstream: h.downstream.WithGroup(name),
		forwardOps: append(append([]forwardOp(nil), h.forwardOps...), forwardOp{group: name}),
		shared:     h.shared,
	}
}

func appendForwardAttrs(operations []forwardOp, attrs []slog.Attr) []forwardOp {
	copyOfAttrs := append([]slog.Attr(nil), attrs...)
	return append(append([]forwardOp(nil), operations...), forwardOp{attrs: copyOfAttrs})
}

// Shutdown stops accepting forwarded copies, drains queued requests, and
// waits for the worker. If ctx expires first, in-flight work is canceled and
// ctx's error is returned. Downstream logging remains available on clones but
// no further remote copies are queued.
func (h *Handler) Shutdown(ctx context.Context) error {
	h.shared.mu.Lock()
	if !h.shared.closed {
		h.shared.closed = true
		close(h.shared.queue)
	}
	h.shared.mu.Unlock()
	select {
	case <-h.shared.done:
		return nil
	case <-ctx.Done():
		h.shared.cancel()
		return ctx.Err()
	}
}

func (h *shared) run() {
	defer close(h.done)
	for payload := range h.queue {
		if h.ctx.Err() != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(h.ctx, h.timeout)
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.endpoint, bytes.NewReader(payload))
		if err == nil {
			request.Header.Set("Authorization", "Bearer "+h.token)
			request.Header.Set("Content-Type", "application/json")
			response, requestErr := h.client.Do(request)
			if requestErr == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
				_ = response.Body.Close()
			}
		}
		cancel()
	}
}
