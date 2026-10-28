# Configuration Reference

LogStreamer reads one YAML file. Set `LOGSTREAMER_CONFIG` to select it; otherwise the service reads `config.yaml` from the working directory. The checked-in `config.example.yaml` is a minimal HTTP-ingestion quick start and does not require Docker access. Collector examples are separate: `config.docker.example.yaml`, `config.compose.example.yaml`, and `config.file.example.yaml`.

Configuration is validated at startup. Unknown YAML fields and unsupported source types are errors. Source names must be non-empty and unique. Each source type accepts only its relevant settings.

## Settings

| YAML key | Default | Description |
|---|---|---|
| `server.listen_address` | `:8080` | HTTP listen address. Overridden by `LOGSTREAMER_HTTP_ADDR`. |
| `server.cors_allowed_origins` | `[]` | Exact HTTP(S) origins allowed to read `/api/services` and `/api/logs/stream` from a browser. No wildcard or credentials. |
| `docker.host` | `unix:///var/run/docker.sock` | Docker API connection address; needed only for Docker and Compose sources. |
| `limits.recent_capacity` | `500` | Maximum recent events retained in memory. |
| `limits.client_queue_capacity` | `256` | Maximum pending events per streaming client. |
| `limits.sse_keepalive` | `15s` | SSE keepalive comment period. |
| `limits.reconnect_min` | `1s` | Minimum collector retry delay. |
| `limits.reconnect_max` | `30s` | Maximum collector retry delay; must be at least the minimum. |
| `limits.max_sse_clients` | `100` | Maximum concurrent SSE connections. |
| `limits.ingest_max_body_bytes` | `1048576` | Maximum HTTP ingestion request size. |
| `limits.ingest_max_batch` | `100` | Maximum records per ingestion request. |
| `limits.max_event_bytes` | `1048576` | Maximum size of each Docker/file line or HTTP event. |

All capacities and limits must be greater than zero. Durations use Go duration syntax such as `500ms`, `5s`, or `1m`.

For a separately hosted LogLens, add its exact origin (including scheme and port, without a path) to `server.cors_allowed_origins`, for example:

```yaml
server:
  listen_address: "127.0.0.1:8080"
  cors_allowed_origins:
    - "http://localhost:5173"
    - "https://logs.example.com"
```

The default empty list disables cross-origin browser access. CORS applies only to the read-only services and stream routes; it is not authentication or an access control for non-browser clients.

## Sources

Every source has a unique `name` and a `type`:

| Type | Required fields | Example file | Docker socket needed |
|---|---|---|---|
| `http` | none | `config.example.yaml` | No |
| `docker` | `container_name` | `config.docker.example.yaml` | Yes |
| `compose` | `compose_project`, `compose_service` | `config.compose.example.yaml` | Yes |
| `file` | `path` | `config.file.example.yaml` | No |

Docker sources match exact configured container names. Compose sources use the standard project and service labels. Docker and Compose start at the current time; file sources start at EOF and detect truncation and replacement. A file path must be readable by the service process. When running in Compose, mount the host file at the same path configured in the container.

`/api/services` reports source names, types, states, and safe generic errors; it does not expose file paths or credentials.

## HTTP ingestion

The quick-start configuration accepts events at `POST /api/logs?source=quickstart`. Send `Authorization: Bearer <token>` and a JSON content type. The body may be one event object or a nonempty array. Each event requires a string `message` or `msg`; `timestamp`, `level`, `fields`, and extra metadata are optional. The query selector must match a configured HTTP source. A body `source` value is metadata and cannot override configured identity.

The service validates the complete request before publishing and responds `202` with `{"accepted":N}`. Invalid JSON/events return `400`, invalid credentials `401`, an unconfigured source `404`, and body, event, or batch limit violations `413`. See [the API reference](api.md) for examples.

## Environment and secrets

- `LOGSTREAMER_CONFIG`: configuration file path, default `config.yaml`.
- `LOGSTREAMER_HTTP_ADDR`: overrides `server.listen_address`.
- `LOGSTREAMER_INGEST_TOKEN`: bearer token required when an HTTP source is configured. Keep it in deployment secrets and never in YAML.

The application does not automatically read `.env` files. Export variables in the shell or configure them in the deployment environment. The checked-in `.env.example` is a template for copying to `.env`; Compose reads `.env` for interpolation. `LOGSTREAMER_PORT`, `LOGSTREAMER_CONFIG_FILE`, `LOGSTREAMER_FILE_PATH`, and `DOCKER_GID` are Compose-example variables, not application settings. `DOCKER_GID` is needed only with `compose.docker.yaml`.
