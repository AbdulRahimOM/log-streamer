package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/abdulrahimom/logstreamer/internal/collectors"
	"github.com/abdulrahimom/logstreamer/internal/config"
	"github.com/abdulrahimom/logstreamer/internal/envfile"
	"github.com/abdulrahimom/logstreamer/internal/httpapi"
	"github.com/abdulrahimom/logstreamer/internal/logstream"
)

func main() {
	loadLocalEnv := flag.Bool("load-local-env", false, "load .env for local development")
	flag.Parse()
	if err := envfile.LoadLocal(*loadLocalEnv); err != nil {
		fmt.Fprintf(os.Stderr, "environment file error: %v\n", err)
		os.Exit(1)
	}
	cfg, err := config.LoadFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	publisher := logstream.NewPublisher(cfg.Limits.RecentCapacity, cfg.Limits.ClientQueueCapacity)
	manager, err := collectors.NewManager(cfg, publisher)
	if err != nil {
		fmt.Fprintf(os.Stderr, "collector configuration error: %v\n", err)
		os.Exit(1)
	}
	server, err := httpapi.New(cfg, publisher, manager.Statuses())
	if err != nil {
		fmt.Fprintf(os.Stderr, "server configuration error: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	managerDone := make(chan struct{})
	go func() {
		defer close(managerDone)
		manager.Run(ctx)
	}()
	log.Printf("LogStreamer listening on %s", cfg.Server.ListenAddress)
	serverErr := server.Start(ctx)
	stop()
	<-managerDone
	if serverErr != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", serverErr)
		os.Exit(1)
	}
}
