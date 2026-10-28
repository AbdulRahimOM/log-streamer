package collectors

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abdulrahimom/logstreamer/internal/config"
	"github.com/abdulrahimom/logstreamer/internal/logstream"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

type fakeDocker struct {
	mu          sync.Mutex
	list        func(client.ContainerListOptions) (client.ContainerListResult, error)
	inspect     func(string) (client.ContainerInspectResult, error)
	logs        func(context.Context, string, client.ContainerLogsOptions) (client.ContainerLogsResult, error)
	listCalls   int
	logCalls    []string
	closeCalled bool
}

func (d *fakeDocker) ContainerList(_ context.Context, options client.ContainerListOptions) (client.ContainerListResult, error) {
	d.mu.Lock()
	d.listCalls++
	d.mu.Unlock()
	if d.list != nil {
		return d.list(options)
	}
	return client.ContainerListResult{}, nil
}

func (d *fakeDocker) ContainerInspect(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	if d.inspect != nil {
		return d.inspect(id)
	}
	return client.ContainerInspectResult{Container: container.InspectResponse{Config: &container.Config{}}}, nil
}

func (d *fakeDocker) ContainerLogs(ctx context.Context, id string, options client.ContainerLogsOptions) (client.ContainerLogsResult, error) {
	d.mu.Lock()
	d.logCalls = append(d.logCalls, id)
	d.mu.Unlock()
	if d.logs != nil {
		return d.logs(ctx, id, options)
	}
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func (d *fakeDocker) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closeCalled = true
	return nil
}

func TestFindNamedContainerRequiresExactName(t *testing.T) {
	docker := &fakeDocker{list: func(options client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{Items: []container.Summary{
			{ID: "wrong", Names: []string{"/api-old"}},
			{ID: "right", Names: []string{"/api"}},
		}}, nil
	}}
	id, err := findNamedContainer(context.Background(), docker, "api")
	if err != nil || id != "right" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestComposeSelectionRequiresBothConfiguredLabels(t *testing.T) {
	docker := &fakeDocker{list: func(options client.ContainerListOptions) (client.ContainerListResult, error) {
		if !options.Filters["label"][composeProjectLabel+"=demo"] || !options.Filters["label"][composeServiceLabel+"=worker"] {
			t.Fatalf("missing Docker label filters: %#v", options.Filters)
		}
		return client.ContainerListResult{Items: []container.Summary{
			{ID: "match", Labels: map[string]string{composeProjectLabel: "demo", composeServiceLabel: "worker"}},
			{ID: "wrong-service", Labels: map[string]string{composeProjectLabel: "demo", composeServiceLabel: "api"}},
		}}, nil
	}}
	items, err := listComposeContainers(context.Background(), docker, config.SourceConfig{ComposeProject: "demo", ComposeService: "worker"})
	if err != nil || len(items) != 1 || items[0].ID != "match" {
		t.Fatalf("items=%#v err=%v", items, err)
	}
}

func TestFollowContainerDemultiplexesStreamsAndPreservesDockerTimestamp(t *testing.T) {
	stamp := time.Now().Add(-time.Second).UTC().Truncate(time.Second)
	payload := dockerFrame(1, stamp.Format(time.RFC3339Nano)+" INFO from stdout\n")
	payload = append(payload, dockerFrame(2, stamp.Add(time.Millisecond).Format(time.RFC3339Nano)+" ERROR from stderr\n")...)
	docker := &fakeDocker{logs: func(_ context.Context, _ string, options client.ContainerLogsOptions) (client.ContainerLogsResult, error) {
		if !options.Follow || !options.ShowStdout || !options.ShowStderr || !options.Timestamps || options.Since == "" {
			t.Fatalf("unexpected log options: %#v", options)
		}
		return io.NopCloser(bytes.NewReader(payload)), nil
	}}
	since := stamp.Add(-time.Second)
	var events []logstream.Event
	err := followContainer(context.Background(), docker, config.LimitsConfig{MaxEventBytes: 1024}, "configured", "id", &since, nil, func(event logstream.Event) {
		events = append(events, event)
	})
	if err == nil || len(events) != 2 {
		t.Fatalf("err=%v events=%#v", err, events)
	}
	if events[0].Source != "configured" || events[0].Origin != "stdout" || events[0].Level != "INFO" || events[0].Message != "INFO from stdout" || !events[0].Timestamp.Equal(stamp) {
		t.Fatalf("unexpected stdout event: %#v", events[0])
	}
	if events[1].Origin != "stderr" || events[1].Level != "ERROR" || events[1].Message != "ERROR from stderr" {
		t.Fatalf("unexpected stderr event: %#v", events[1])
	}
	if !since.Equal(stamp.Add(time.Millisecond)) {
		t.Fatalf("resume timestamp=%v", since)
	}
}

func TestManagerRetriesDockerFailuresAndStopsOnCancellation(t *testing.T) {
	docker := &fakeDocker{list: func(client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{}, errors.New("sensitive socket failure")
	}}
	cfg := &config.Config{
		Limits:  config.LimitsConfig{ReconnectMin: "1ms", ReconnectMax: "2ms"},
		Sources: []config.SourceConfig{{Name: "api", Type: "docker", ContainerName: "api"}},
	}
	manager := newManager(cfg, logstream.NewPublisher(10, 10), docker)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { manager.Run(ctx); close(done) }()
	deadline := time.Now().Add(time.Second)
	for {
		docker.mu.Lock()
		calls := docker.listCalls
		docker.mu.Unlock()
		if calls >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Docker failure was not retried")
		}
		time.Sleep(time.Millisecond)
	}
	status := manager.Statuses().Snapshot()
	if len(status) != 1 || status[0].State != "retrying" || status[0].LastError != "source unavailable" || strings.Contains(status[0].LastError, "sensitive") {
		t.Fatalf("unsafe or incorrect retry status: %#v", status)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("manager did not stop after cancellation")
	}
	status = manager.Statuses().Snapshot()
	if len(status) != 1 || status[0].State != "stopped" || status[0].LastError != "" || !docker.closeCalled {
		t.Fatalf("unexpected shutdown status: %#v dockerClosed=%v", status, docker.closeCalled)
	}
}

func TestComposeReconciliationStartsReplacementWithoutDuplicateStreams(t *testing.T) {
	var mu sync.Mutex
	streams := map[string]int{}
	closed := map[string]bool{}
	docker := &fakeDocker{}
	docker.list = func(_ client.ContainerListOptions) (client.ContainerListResult, error) {
		docker.mu.Lock()
		call := docker.listCalls
		docker.mu.Unlock()
		id := "one"
		if call >= 2 {
			id = "two"
		}
		labels := map[string]string{composeProjectLabel: "demo", composeServiceLabel: "worker"}
		return client.ContainerListResult{Items: []container.Summary{
			{ID: id, Labels: labels},
			{ID: "replica", Labels: labels},
		}}, nil
	}
	docker.logs = func(ctx context.Context, id string, _ client.ContainerLogsOptions) (client.ContainerLogsResult, error) {
		mu.Lock()
		streams[id]++
		mu.Unlock()
		reader := &contextReadCloser{ctx: ctx, closed: make(chan struct{})}
		go func() {
			select {
			case <-ctx.Done():
				_ = reader.Close()
			case <-reader.closed:
			}
			mu.Lock()
			closed[id] = true
			mu.Unlock()
		}()
		return reader, nil
	}
	cfg := &config.Config{
		Limits:  config.LimitsConfig{ReconnectMin: "1ms", ReconnectMax: "2ms", MaxEventBytes: 128},
		Sources: []config.SourceConfig{{Name: "worker", Type: "compose", ComposeProject: "demo", ComposeService: "worker"}},
	}
	manager := newManager(cfg, logstream.NewPublisher(10, 10), docker)
	manager.composeInterval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { manager.runCompose(ctx, cfg.Sources[0]); close(done) }()
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		startedReplacement := streams["two"] > 0 && streams["replica"] > 0
		oldClosed := closed["one"]
		mu.Unlock()
		if startedReplacement && oldClosed {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("Compose container replacement was not reconciled")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(15 * time.Millisecond)
	mu.Lock()
	oldCount, newCount, replicaCount := streams["one"], streams["two"], streams["replica"]
	mu.Unlock()
	if oldCount != 1 || newCount != 1 || replicaCount != 1 {
		cancel()
		t.Fatalf("duplicate or missing replica log streams: old=%d replacement=%d replica=%d", oldCount, newCount, replicaCount)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Compose collector did not join its streams")
	}
}

type contextReadCloser struct {
	ctx    context.Context
	closed chan struct{}
	once   sync.Once
}

func (r *contextReadCloser) Read([]byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	case <-r.closed:
		return 0, io.EOF
	}
}

func (r *contextReadCloser) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

func dockerFrame(stream byte, data string) []byte {
	payload := []byte(data)
	frame := make([]byte, 8+len(payload))
	frame[0] = stream
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)
	return frame
}
