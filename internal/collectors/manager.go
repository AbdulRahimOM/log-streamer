package collectors

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/abdulrahimom/logstreamer/internal/config"
	"github.com/abdulrahimom/logstreamer/internal/logstream"
)

const (
	composeReconcileInterval = 5 * time.Second
	filePollInterval         = 250 * time.Millisecond
)

type Manager struct {
	cfg             *config.Config
	publisher       *logstream.Publisher
	statuses        *Registry
	docker          dockerAPI
	composeInterval time.Duration
}

func NewManager(cfg *config.Config, publisher *logstream.Publisher) (*Manager, error) {
	needsDocker := false
	for _, source := range cfg.Sources {
		if source.Type == "docker" || source.Type == "compose" {
			needsDocker = true
			break
		}
	}
	var docker dockerAPI
	if needsDocker {
		var err error
		docker, err = newDockerClient(cfg.Docker.Host)
		if err != nil {
			return nil, errors.New("could not initialize Docker client from configured host")
		}
	}
	return newManager(cfg, publisher, docker), nil
}

func newManager(cfg *config.Config, publisher *logstream.Publisher, docker dockerAPI) *Manager {
	return &Manager{cfg: cfg, publisher: publisher, docker: docker, statuses: NewRegistry(cfg.Sources), composeInterval: composeReconcileInterval}
}

func (m *Manager) Statuses() *Registry { return m.statuses }

func (m *Manager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, source := range m.cfg.Sources {
		m.statuses.set(source.Name, "starting", "")
		if source.Type == "http" {
			m.statuses.set(source.Name, "running", "")
			continue
		}
		wg.Add(1)
		go func(source config.SourceConfig) {
			defer wg.Done()
			switch source.Type {
			case "docker":
				m.runDocker(ctx, source)
			case "compose":
				m.runCompose(ctx, source)
			case "file":
				m.runFile(ctx, source)
			}
		}(source)
	}
	<-ctx.Done()
	wg.Wait()
	for _, source := range m.cfg.Sources {
		m.statuses.set(source.Name, "stopped", "")
	}
	if m.docker != nil {
		_ = m.docker.Close()
	}
}

func (m *Manager) runDocker(ctx context.Context, source config.SourceConfig) {
	var since time.Time
	delay := durationOrDefault(m.cfg.Limits.ReconnectMin, time.Second)
	maxDelay := durationOrDefault(m.cfg.Limits.ReconnectMax, 30*time.Second)
	for ctx.Err() == nil {
		m.statuses.set(source.Name, "retrying", "source unavailable")
		id, err := findNamedContainer(ctx, m.docker, source.ContainerName)
		if err == nil {
			if since.IsZero() {
				since = time.Now()
			}
			connected := false
			err = followContainer(ctx, m.docker, m.cfg.Limits, source.Name, id, &since, func() {
				connected = true
				m.statuses.set(source.Name, "running", "")
			}, func(event logstream.Event) { m.publisher.Publish(event) })
			if connected {
				delay = durationOrDefault(m.cfg.Limits.ReconnectMin, time.Second)
			}
		}
		if ctx.Err() != nil {
			return
		}
		m.statuses.set(source.Name, "retrying", "source unavailable")
		if !wait(ctx, delay) {
			return
		}
		delay = nextDelay(delay, maxDelay)
	}
}

func (m *Manager) runCompose(ctx context.Context, source config.SourceConfig) {
	type stream struct {
		cancel context.CancelFunc
		done   chan struct{}
	}
	active := make(map[string]stream)
	defer func() {
		for _, child := range active {
			child.cancel()
		}
		for _, child := range active {
			<-child.done
		}
	}()

	delay := durationOrDefault(m.cfg.Limits.ReconnectMin, time.Second)
	maxDelay := durationOrDefault(m.cfg.Limits.ReconnectMax, 30*time.Second)
	for ctx.Err() == nil {
		containers, err := listComposeContainers(ctx, m.docker, source)
		if err != nil {
			m.statuses.set(source.Name, "retrying", "source discovery unavailable")
			if !wait(ctx, delay) {
				return
			}
			delay = nextDelay(delay, maxDelay)
			continue
		}
		delay = durationOrDefault(m.cfg.Limits.ReconnectMin, time.Second)
		m.statuses.set(source.Name, "running", "")
		wanted := make(map[string]struct{}, len(containers))
		for _, item := range containers {
			wanted[item.ID] = struct{}{}
			if _, exists := active[item.ID]; exists {
				continue
			}
			childCtx, cancel := context.WithCancel(ctx)
			child := stream{cancel: cancel, done: make(chan struct{})}
			active[item.ID] = child
			since := time.Now()
			go func(id string, childCtx context.Context, child stream, since time.Time) {
				defer close(child.done)
				m.followContainerUntilCancelled(childCtx, source.Name, id, since)
			}(item.ID, childCtx, child, since)
		}
		for id, child := range active {
			if _, exists := wanted[id]; exists {
				continue
			}
			child.cancel()
			<-child.done
			delete(active, id)
		}
		if !wait(ctx, m.composeInterval) {
			return
		}
	}
}

func (m *Manager) followContainerUntilCancelled(ctx context.Context, source, id string, initialSince time.Time) {
	since := initialSince
	delay := durationOrDefault(m.cfg.Limits.ReconnectMin, time.Second)
	maxDelay := durationOrDefault(m.cfg.Limits.ReconnectMax, 30*time.Second)
	for ctx.Err() == nil {
		connected := false
		_ = followContainer(ctx, m.docker, m.cfg.Limits, source, id, &since, func() { connected = true }, func(event logstream.Event) { m.publisher.Publish(event) })
		if connected {
			delay = durationOrDefault(m.cfg.Limits.ReconnectMin, time.Second)
		}
		if !wait(ctx, delay) {
			return
		}
		delay = nextDelay(delay, maxDelay)
	}
}

func (m *Manager) runFile(ctx context.Context, source config.SourceConfig) {
	delay := durationOrDefault(m.cfg.Limits.ReconnectMin, time.Second)
	maxDelay := durationOrDefault(m.cfg.Limits.ReconnectMax, 30*time.Second)
	for ctx.Err() == nil {
		m.statuses.set(source.Name, "retrying", "source unavailable")
		_ = tailFile(ctx, source.Path, filePollInterval, m.cfg.Limits.MaxEventBytes, func() {
			m.statuses.set(source.Name, "running", "")
		}, func(line string, timestamp time.Time) {
			m.publisher.Publish(logstream.Parse(line, source.Name, "file", timestamp))
		})
		if ctx.Err() != nil {
			return
		}
		m.statuses.set(source.Name, "retrying", "source unavailable")
		if !wait(ctx, delay) {
			return
		}
		delay = nextDelay(delay, maxDelay)
	}
}

func durationOrDefault(value string, fallback time.Duration) time.Duration {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}

func nextDelay(current, maximum time.Duration) time.Duration {
	if current >= maximum/2 {
		return maximum
	}
	return current * 2
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
