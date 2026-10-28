package collectors

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abdulrahimom/logstreamer/internal/config"
	"github.com/abdulrahimom/logstreamer/internal/logstream"
)

func TestFileCollectorRetriesUntilFileAppears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "late.log")
	cfg := &config.Config{
		Limits:  config.LimitsConfig{ReconnectMin: "5ms", ReconnectMax: "10ms", MaxEventBytes: 128},
		Sources: []config.SourceConfig{{Name: "late-file", Type: "file", Path: path}},
	}
	publisher := logstream.NewPublisher(10, 10)
	manager := newManager(cfg, publisher, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		manager.runFile(ctx, cfg.Sources[0])
		close(done)
	}()

	waitForSourceState(t, manager.Statuses(), "late-file", "retrying")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		cancel()
		t.Fatal(err)
	}
	waitForSourceState(t, manager.Statuses(), "late-file", "running")
	subscriber, _, _ := publisher.Subscribe(logstream.Filter{})
	defer publisher.Unsubscribe(subscriber)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if _, err := file.WriteString("INFO collector recovered\n"); err != nil {
		_ = file.Close()
		cancel()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		cancel()
		t.Fatal(err)
	}
	select {
	case delivery := <-subscriber.C:
		if delivery.Event.Source != "late-file" || delivery.Event.Origin != "file" || delivery.Event.Level != "INFO" || delivery.Event.Message != "INFO collector recovered" {
			cancel()
			t.Fatalf("unexpected recovered file event: %#v", delivery.Event)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("file collector did not publish after the file appeared")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("file collector did not stop after cancellation")
	}
}

func waitForSourceState(t *testing.T, registry *Registry, name, state string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, status := range registry.Snapshot() {
			if status.Name == name && status.State == state {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("source %q did not reach state %q: %#v", name, state, registry.Snapshot())
}
