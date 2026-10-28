// Package sloghandler wraps a downstream slog.Handler and asynchronously
// forwards a bounded copy of each accepted record to LogStreamer ingestion.
//
// Create a configured HTTP source in LogStreamer and provide its ingestion URL
// (including the source query parameter) and bearer token:
//
//	package main
//
//	import (
//		"context"
//		"log"
//		"log/slog"
//		"os"
//		"time"
//
//		"github.com/abdulrahimom/logstreamer/sloghandler"
//	)
//
//	func main() {
//		downstream := slog.NewJSONHandler(os.Stdout, nil)
//		h, err := sloghandler.New(
//			"http://localhost:8080/api/logs?source=my-app",
//			os.Getenv("LOGSTREAMER_INGEST_TOKEN"),
//			downstream,
//		)
//		if err != nil { log.Fatal(err) }
//		logger := slog.New(h)
//		logger.Info("service started", "port", 8080)
//		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//		defer cancel()
//		if err := h.Shutdown(ctx); err != nil { log.Print(err) }
//	}
//
// Handle delegates synchronously to the downstream handler, then queues the
// forwarded JSON record without waiting for network I/O. The forwarding queue
// defaults to 256 records and drops remote copies on overflow. Shutdown drains
// pending copies until its context expires.
package sloghandler
