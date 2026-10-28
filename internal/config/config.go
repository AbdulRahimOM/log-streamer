package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultPath           = "config.yaml"
	DefaultDockerHost     = "unix:///var/run/docker.sock"
	DefaultRecentCapacity = 500
	DefaultMaxEventBytes  = 1 << 20
)

// Config is the validated, process-wide service configuration.
type Config struct {
	Server      ServerConfig   `yaml:"server"`
	Docker      DockerConfig   `yaml:"docker"`
	Limits      LimitsConfig   `yaml:"limits"`
	Sources     []SourceConfig `yaml:"sources"`
	IngestToken string         `yaml:"-"`
}

type ServerConfig struct {
	ListenAddress      string   `yaml:"listen_address"`
	CORSAllowedOrigins []string `yaml:"cors_allowed_origins,omitempty"`
}

type DockerConfig struct {
	Host string `yaml:"host"`
}

type LimitsConfig struct {
	RecentCapacity      int    `yaml:"recent_capacity"`
	ClientQueueCapacity int    `yaml:"client_queue_capacity"`
	SSEKeepalive        string `yaml:"sse_keepalive"`
	ReconnectMin        string `yaml:"reconnect_min"`
	ReconnectMax        string `yaml:"reconnect_max"`
	MaxSSEClients       int    `yaml:"max_sse_clients"`
	IngestMaxBodyBytes  int64  `yaml:"ingest_max_body_bytes"`
	IngestMaxBatch      int    `yaml:"ingest_max_batch"`
	MaxEventBytes       int    `yaml:"max_event_bytes"`
}

type SourceConfig struct {
	Name           string `yaml:"name"`
	Type           string `yaml:"type"`
	ContainerName  string `yaml:"container_name,omitempty"`
	ComposeProject string `yaml:"compose_project,omitempty"`
	ComposeService string `yaml:"compose_service,omitempty"`
	Path           string `yaml:"path,omitempty"`
}

// Load reads YAML from path, applies defaults, and validates the result.
func Load(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("config path must not be empty")
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %q: %w", path, err)
	}
	defer f.Close()

	decoder := yaml.NewDecoder(f)
	decoder.KnownFields(true)
	cfg := Config{}
	cfg.applyDefaults()
	if err := decoder.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("config file is empty")
		}
		return nil, fmt.Errorf("decode config %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("config must contain exactly one YAML document")
		}
		return nil, fmt.Errorf("decode config %q: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadFromEnv loads LOGSTREAMER_CONFIG (or config.yaml), then applies supported
// environment overrides. The ingestion token is never read from YAML.
func LoadFromEnv() (*Config, error) {
	path := os.Getenv("LOGSTREAMER_CONFIG")
	if path == "" {
		path = DefaultPath
	}
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	if value, ok := os.LookupEnv("LOGSTREAMER_HTTP_ADDR"); ok {
		cfg.Server.ListenAddress = value
	}
	cfg.IngestToken = os.Getenv("LOGSTREAMER_INGEST_TOKEN")
	if err := cfg.ValidateRuntime(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Server.ListenAddress == "" {
		c.Server.ListenAddress = ":8080"
	}
	if c.Docker.Host == "" {
		c.Docker.Host = DefaultDockerHost
	}
	if c.Limits.RecentCapacity == 0 {
		c.Limits.RecentCapacity = DefaultRecentCapacity
	}
	if c.Limits.ClientQueueCapacity == 0 {
		c.Limits.ClientQueueCapacity = 256
	}
	if c.Limits.SSEKeepalive == "" {
		c.Limits.SSEKeepalive = "15s"
	}
	if c.Limits.ReconnectMin == "" {
		c.Limits.ReconnectMin = "1s"
	}
	if c.Limits.ReconnectMax == "" {
		c.Limits.ReconnectMax = "30s"
	}
	if c.Limits.MaxSSEClients == 0 {
		c.Limits.MaxSSEClients = 100
	}
	if c.Limits.IngestMaxBodyBytes == 0 {
		c.Limits.IngestMaxBodyBytes = 1 << 20
	}
	if c.Limits.IngestMaxBatch == 0 {
		c.Limits.IngestMaxBatch = 100
	}
	if c.Limits.MaxEventBytes == 0 {
		c.Limits.MaxEventBytes = DefaultMaxEventBytes
	}
}

// Validate checks all fields that can be verified without opening a source.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Server.ListenAddress) == "" {
		return errors.New("server.listen_address must not be empty")
	}
	seenOrigins := make(map[string]struct{}, len(c.Server.CORSAllowedOrigins))
	for _, origin := range c.Server.CORSAllowedOrigins {
		if err := validateOrigin(origin); err != nil {
			return fmt.Errorf("server.cors_allowed_origins: %w", err)
		}
		if _, exists := seenOrigins[origin]; exists {
			return fmt.Errorf("server.cors_allowed_origins contains duplicate origin %q", origin)
		}
		seenOrigins[origin] = struct{}{}
	}
	if strings.TrimSpace(c.Docker.Host) == "" {
		return errors.New("docker.host must not be empty")
	}
	if c.Limits.RecentCapacity <= 0 {
		return errors.New("limits.recent_capacity must be greater than zero")
	}
	if c.Limits.ClientQueueCapacity <= 0 {
		return errors.New("limits.client_queue_capacity must be greater than zero")
	}
	if c.Limits.MaxSSEClients <= 0 {
		return errors.New("limits.max_sse_clients must be greater than zero")
	}
	if c.Limits.IngestMaxBodyBytes <= 0 {
		return errors.New("limits.ingest_max_body_bytes must be greater than zero")
	}
	if c.Limits.IngestMaxBatch <= 0 {
		return errors.New("limits.ingest_max_batch must be greater than zero")
	}
	if c.Limits.MaxEventBytes <= 0 {
		return errors.New("limits.max_event_bytes must be greater than zero")
	}
	if _, err := parsePositiveDuration("limits.sse_keepalive", c.Limits.SSEKeepalive); err != nil {
		return err
	}
	minBackoff, err := parsePositiveDuration("limits.reconnect_min", c.Limits.ReconnectMin)
	if err != nil {
		return err
	}
	maxBackoff, err := parsePositiveDuration("limits.reconnect_max", c.Limits.ReconnectMax)
	if err != nil {
		return err
	}
	if maxBackoff < minBackoff {
		return errors.New("limits.reconnect_max must be greater than or equal to limits.reconnect_min")
	}
	names := make(map[string]struct{}, len(c.Sources))
	for i, source := range c.Sources {
		prefix := fmt.Sprintf("sources[%d]", i)
		if strings.TrimSpace(source.Name) == "" {
			return fmt.Errorf("%s.name must not be empty", prefix)
		}
		if _, exists := names[source.Name]; exists {
			return fmt.Errorf("duplicate source name %q", source.Name)
		}
		names[source.Name] = struct{}{}

		if err := validateSource(prefix, source); err != nil {
			return err
		}
	}
	return nil
}

func validateOrigin(origin string) error {
	if strings.TrimSpace(origin) != origin || origin == "" || origin == "*" {
		return fmt.Errorf("invalid origin %q: use an exact http(s) origin", origin)
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("invalid origin %q: use an exact http(s) origin without a path", origin)
	}
	return nil
}

// ValidateRuntime checks deployment-only requirements after environment
// overrides have been applied.
func (c Config) ValidateRuntime() error {
	if err := c.Validate(); err != nil {
		return err
	}
	for _, source := range c.Sources {
		if source.Type == "http" && strings.TrimSpace(c.IngestToken) == "" {
			return errors.New("LOGSTREAMER_INGEST_TOKEN is required when an HTTP source is configured")
		}
	}
	return nil
}

func validateSource(prefix string, source SourceConfig) error {
	need := func(field, value string) error {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s.%s is required for source type %q", prefix, field, source.Type)
		}
		return nil
	}
	forbid := func(fields ...string) error {
		for _, field := range fields {
			var value string
			switch field {
			case "container_name":
				value = source.ContainerName
			case "compose_project":
				value = source.ComposeProject
			case "compose_service":
				value = source.ComposeService
			case "path":
				value = source.Path
			}
			if strings.TrimSpace(value) != "" {
				return fmt.Errorf("%s.%s is not valid for source type %q", prefix, field, source.Type)
			}
		}
		return nil
	}

	switch source.Type {
	case "docker":
		if err := need("container_name", source.ContainerName); err != nil {
			return err
		}
		return forbid("compose_project", "compose_service", "path")
	case "compose":
		if err := need("compose_project", source.ComposeProject); err != nil {
			return err
		}
		if err := need("compose_service", source.ComposeService); err != nil {
			return err
		}
		return forbid("container_name", "path")
	case "file":
		if err := need("path", source.Path); err != nil {
			return err
		}
		return forbid("container_name", "compose_project", "compose_service")
	case "http":
		return forbid("container_name", "compose_project", "compose_service", "path")
	default:
		return fmt.Errorf("%s.type %q is unsupported; expected docker, compose, file, or http", prefix, source.Type)
	}
}

func parsePositiveDuration(field, value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", field, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", field)
	}
	return d, nil
}
