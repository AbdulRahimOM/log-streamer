package main

import (
	"fmt"
	"os"

	"github.com/abdulrahimom/logstreamer/internal/config"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	// Phase 1 establishes and validates configuration. The HTTP server and
	// source collectors are added in later phases.
	fmt.Printf("configuration valid: %d sources configured; service not started (Phase 1)\n", len(cfg.Sources))
}
