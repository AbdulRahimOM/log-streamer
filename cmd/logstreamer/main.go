package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/abdulrahimom/logstreamer/internal/config"
	"github.com/abdulrahimom/logstreamer/internal/httpapi"
	"github.com/abdulrahimom/logstreamer/internal/logstream"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	publisher := logstream.NewPublisher(cfg.Limits.RecentCapacity, cfg.Limits.ClientQueueCapacity)
	server, err := httpapi.New(cfg, publisher)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server configuration error: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("LogStreamer listening on %s", cfg.Server.ListenAddress)
	if err := server.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
