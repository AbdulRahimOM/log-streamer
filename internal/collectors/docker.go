package collectors

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/abdulrahimom/logstreamer/internal/config"
	"github.com/abdulrahimom/logstreamer/internal/logstream"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

const (
	composeProjectLabel = "com.docker.compose.project"
	composeServiceLabel = "com.docker.compose.service"
)

type dockerAPI interface {
	ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerLogs(context.Context, string, client.ContainerLogsOptions) (client.ContainerLogsResult, error)
	Close() error
}

func newDockerClient(host string) (dockerAPI, error) {
	return client.New(client.WithHost(host), client.WithAPIVersionNegotiation())
}

func listComposeContainers(ctx context.Context, docker dockerAPI, source config.SourceConfig) ([]container.Summary, error) {
	filters := client.Filters{}.
		Add("label", composeProjectLabel+"="+source.ComposeProject).
		Add("label", composeServiceLabel+"="+source.ComposeService)
	result, err := docker.ContainerList(ctx, client.ContainerListOptions{Filters: filters})
	if err != nil {
		return nil, err
	}
	matched := make([]container.Summary, 0, len(result.Items))
	for _, item := range result.Items {
		if item.Labels[composeProjectLabel] == source.ComposeProject && item.Labels[composeServiceLabel] == source.ComposeService {
			matched = append(matched, item)
		}
	}
	return matched, nil
}

func findNamedContainer(ctx context.Context, docker dockerAPI, configuredName string) (string, error) {
	filters := client.Filters{}.Add("name", configuredName)
	result, err := docker.ContainerList(ctx, client.ContainerListOptions{Filters: filters})
	if err != nil {
		return "", err
	}
	for _, item := range result.Items {
		for _, name := range item.Names {
			if strings.TrimPrefix(name, "/") == configuredName {
				return item.ID, nil
			}
		}
	}
	return "", errors.New("configured container is not running")
}

type logLineSink struct {
	max       int
	line      []byte
	discard   bool
	origin    string
	consume   func(string, string)
	oversized func()
}

func newLogLineSink(max int, origin string, consume func(string, string), oversized func()) *logLineSink {
	return &logLineSink{max: max, line: make([]byte, 0, min(max, 4096)), origin: origin, consume: consume, oversized: oversized}
}

func (s *logLineSink) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			if s.discard {
				if s.oversized != nil {
					s.oversized()
				}
			} else {
				line := strings.TrimSuffix(string(s.line), "\r")
				s.consume(line, s.origin)
			}
			s.line = s.line[:0]
			s.discard = false
			continue
		}
		if s.discard {
			continue
		}
		if len(s.line) == s.max {
			s.line = s.line[:0]
			s.discard = true
			continue
		}
		s.line = append(s.line, b)
	}
	return len(p), nil
}

func (s *logLineSink) Flush() {
	if s.discard {
		if s.oversized != nil {
			s.oversized()
		}
	} else if len(s.line) > 0 {
		s.consume(string(s.line), s.origin)
	}
	s.line = s.line[:0]
	s.discard = false
}

func followContainer(ctx context.Context, docker dockerAPI, cfg config.LimitsConfig, source string, containerID string, since *time.Time, onConnected func(), publish func(logstream.Event)) error {
	inspect, err := docker.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return err
	}
	tty := inspect.Container.Config != nil && inspect.Container.Config.Tty
	logs, err := docker.ContainerLogs(ctx, containerID, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: true,
		Since: since.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	defer logs.Close()
	if onConnected != nil {
		onConnected()
	}
	consume := func(line, origin string) {
		line = strings.TrimSuffix(line, "\r")
		fallback := time.Now()
		if split := strings.IndexByte(line, ' '); split > 0 {
			if timestamp, parseErr := time.Parse(time.RFC3339Nano, line[:split]); parseErr == nil {
				fallback = timestamp
				line = line[split+1:]
				if fallback.After(*since) {
					*since = fallback
				}
			}
		}
		event := logstream.Parse(line, source, origin, fallback)
		publish(event)
	}
	tooLong := func() {}
	stdout := newLogLineSink(cfg.MaxEventBytes, "stdout", consume, tooLong)
	if tty {
		_, err = io.Copy(stdout, logs)
		stdout.Flush()
	} else {
		stderr := newLogLineSink(cfg.MaxEventBytes, "stderr", consume, tooLong)
		_, err = copyDockerFrames(stdout, stderr, logs)
		stdout.Flush()
		stderr.Flush()
	}
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New("container log stream ended")
}

// copyDockerFrames demultiplexes Docker's 8-byte framed stream while copying
// payloads in bounded chunks. Unlike stdcopy.StdCopy, it never allocates a
// buffer sized from an untrusted frame length.
func copyDockerFrames(stdout, stderr io.Writer, source io.Reader) (int64, error) {
	var total int64
	header := make([]byte, 8)
	buffer := make([]byte, 32*1024)
	for {
		_, err := io.ReadFull(source, header)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return total, nil
		}
		if err != nil {
			return total, err
		}
		stream := header[0]
		remaining := uint64(binary.BigEndian.Uint32(header[4:8]))
		var destination io.Writer
		switch stream {
		case 0, 1:
			destination = stdout
		case 2:
			destination = stderr
		case 3:
			return total, errors.New("docker daemon reported a stream error")
		default:
			return total, errors.New("docker daemon returned an invalid stream")
		}
		for remaining > 0 {
			chunk := uint64(len(buffer))
			if remaining < chunk {
				chunk = remaining
			}
			n, readErr := io.ReadFull(source, buffer[:int(chunk)])
			if n > 0 {
				written, writeErr := destination.Write(buffer[:n])
				total += int64(written)
				if writeErr != nil {
					return total, writeErr
				}
				if written != n {
					return total, io.ErrShortWrite
				}
				remaining -= uint64(n)
			}
			if readErr != nil {
				return total, readErr
			}
		}
	}
}
