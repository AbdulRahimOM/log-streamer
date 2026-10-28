package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validYAML = `
sources:
  - name: api
    type: docker
    container_name: messenger-api
  - name: worker
    type: compose
    compose_project: messenger
    compose_service: worker
  - name: host-app
    type: file
    path: /var/log/host-app.log
  - name: external
    type: http
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesDefaultsAndLoadsAllSourceTypes(t *testing.T) {
	cfg, err := Load(writeConfig(t, validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.ListenAddress != ":8080" {
		t.Errorf("listen address = %q, want :8080", cfg.Server.ListenAddress)
	}
	if cfg.Docker.Host != DefaultDockerHost {
		t.Errorf("docker host = %q, want %q", cfg.Docker.Host, DefaultDockerHost)
	}
	if cfg.Limits.RecentCapacity != 500 {
		t.Errorf("recent capacity = %d, want 500", cfg.Limits.RecentCapacity)
	}
	if cfg.Limits.MaxEventBytes != DefaultMaxEventBytes {
		t.Errorf("max event bytes = %d, want %d", cfg.Limits.MaxEventBytes, DefaultMaxEventBytes)
	}
	if len(cfg.Sources) != 4 {
		t.Fatalf("loaded %d sources, want 4", len(cfg.Sources))
	}
}

func TestLoadFromEnvAppliesOverridesAfterYAML(t *testing.T) {
	path := writeConfig(t, validYAML+"server:\n  listen_address: ':7070'\n")
	t.Setenv("LOGSTREAMER_CONFIG", path)
	t.Setenv("LOGSTREAMER_HTTP_ADDR", ":9090")
	t.Setenv("LOGSTREAMER_INGEST_TOKEN", "test-secret")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.ListenAddress != ":9090" {
		t.Errorf("listen address = %q, want :9090", cfg.Server.ListenAddress)
	}
	if cfg.IngestToken != "test-secret" {
		t.Errorf("ingestion token override not applied")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	_, err := Load(writeConfig(t, validYAML+"unexpected: true\n"))
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}

func TestValidateRejectsDuplicateNames(t *testing.T) {
	_, err := Load(writeConfig(t, `sources:
  - name: same
    type: http
  - name: same
    type: http
`))
	if err == nil || !strings.Contains(err.Error(), "duplicate source name") {
		t.Fatalf("expected duplicate-name error, got %v", err)
	}
}

func TestValidateRejectsMissingAndConflictingSourceFields(t *testing.T) {
	cases := []struct {
		name, yaml, contains string
	}{
		{"missing docker name", "sources:\n  - name: app\n    type: docker\n", "container_name is required"},
		{"missing compose service", "sources:\n  - name: app\n    type: compose\n    compose_project: demo\n", "compose_service is required"},
		{"conflicting file field", "sources:\n  - name: app\n    type: file\n    path: /tmp/app.log\n    container_name: app-container\n", "container_name is not valid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("expected error containing %q, got %v", tc.contains, err)
			}
		})
	}
}

func TestValidateRejectsUnknownSourceType(t *testing.T) {
	_, err := Load(writeConfig(t, "sources:\n  - name: app\n    type: kafka\n"))
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported source error, got %v", err)
	}
}

func TestValidateRejectsInvalidLimits(t *testing.T) {
	cases := []struct {
		name, yaml, contains string
	}{
		{"zero recent", "limits:\n  recent_capacity: 0\n", "recent_capacity must be greater than zero"},
		{"negative recent", "limits:\n  recent_capacity: -1\n", "recent_capacity must be greater than zero"},
		{"bad keepalive", "limits:\n  sse_keepalive: never\n", "sse_keepalive must be a valid duration"},
		{"inverted backoff", "limits:\n  reconnect_min: 5s\n  reconnect_max: 1s\n", "reconnect_max must be greater than or equal"},
		{"zero max event bytes", "limits:\n  max_event_bytes: 0\n", "max_event_bytes must be greater than zero"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, validYAML+tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("expected error containing %q, got %v", tc.contains, err)
			}
		})
	}
}

func TestTokenNeverAppearsInConfigErrors(t *testing.T) {
	path := writeConfig(t, "limits:\n  recent_capacity: 0\n")
	t.Setenv("LOGSTREAMER_CONFIG", path)
	t.Setenv("LOGSTREAMER_INGEST_TOKEN", "must-not-leak")
	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("secret leaked in error: %v", err)
	}
}

func TestLoadFromEnvRequiresTokenWhenHTTPSourceIsConfigured(t *testing.T) {
	t.Setenv("LOGSTREAMER_CONFIG", writeConfig(t, validYAML))
	t.Setenv("LOGSTREAMER_INGEST_TOKEN", "")
	_, err := LoadFromEnv()
	if err == nil || !strings.Contains(err.Error(), "LOGSTREAMER_INGEST_TOKEN is required") {
		t.Fatalf("expected HTTP token startup error, got %v", err)
	}
}
