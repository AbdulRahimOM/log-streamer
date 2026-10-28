# Configuration Reference

LogStreamer reads one YAML configuration file. Set `LOGSTREAMER_CONFIG` to select its path; otherwise it reads `config.yaml` from the working directory. The checked-in `config.example.yaml` demonstrates every source type. Copy it to `config.yaml` and adjust source names/settings before starting the service.

Configuration is validated at startup. Unknown YAML fields and unsupported source types are errors. Source names must be non-empty and unique. Each source type accepts only its relevant settings.

## Top-level settings

| YAML key | Default | Description |
|---|---|---|
| `server.listen_address` | `:8080` | HTTP listen address. Overridden by `LOGSTREAMER_HTTP_ADDR`. |
| `docker.host` | `unix:///var/run/docker.sock` | Docker API connection address. |
| `limits.recent_capacity` | `500` | Maximum recent events retained in memory. |
| `limits.client_queue_capacity` | `256` | Maximum pending events per streaming client. |
| `limits.sse_keepalive` | `15s` | SSE keepalive comment period. |
| `limits.reconnect_min` | `1s` | Minimum collector retry delay. |
| `limits.reconnect_max` | `30s` | Maximum collector retry delay; must be at least the minimum. |
| `limits.max_sse_clients` | `100` | Maximum concurrent SSE connections. |
| `limits.ingest_max_body_bytes` | `1048576` | Maximum HTTP ingestion request size. |
| `limits.ingest_max_batch` | `100` | Maximum records per ingestion request. |
| `limits.max_event_bytes` | `1048576` | Maximum size of each Docker, file, or HTTP event. |

All capacities and limits must be greater than zero. Durations use Go duration syntax such as `500ms`, `5s`, or `1m`.

## Sources

Every source has a unique `name` and a `type`:

| Type | Required fields | Purpose |
|---|---|---|
| `docker` | `container_name` | Follow a configured running container by its exact name. |
| `compose` | `compose_project`, `compose_service` | Follow running Compose containers by project/service labels. |
| `file` | `path` | Tail a host application log file. |
| `http` | none | Accept authenticated events at `POST /api/logs?source=<name>`. |

Fields belonging to another source type are rejected. `/api/services` reports each configured source's operational state without exposing paths or credentials. Docker and Compose sources start at the current time; file sources start at EOF. HTTP sources are ready when the server starts.

## HTTP ingestion

Send `POST /api/logs?source=<configured-http-source>` with `Authorization: Bearer <token>` and a JSON content type. The body may be one event object or a nonempty array. Each event requires a string `message` or `msg`; `timestamp`, `level`, `fields`, and extra metadata are optional. The query selector must name a configured HTTP source. Any `source` in the body is metadata and cannot change the configured event identity. The service validates the complete request before publishing and responds `202` with `{"accepted":N}`. Invalid JSON/events return `400`, invalid credentials `401`, an unconfigured source `404`, and body, event, or batch limit violations `413`.

`limits.max_event_bytes` applies to each individual Docker/file line and each HTTP event. Oversized Docker and file lines are drained through the next newline and skipped.

## Environment and secrets

- `LOGSTREAMER_CONFIG`: config file path, default `config.yaml`.
- `LOGSTREAMER_HTTP_ADDR`: overrides `server.listen_address`.
- `LOGSTREAMER_INGEST_TOKEN`: bearer token for HTTP ingestion. It is required at startup when any `http` source is configured. It is not stored in YAML.

The application does not automatically read `.env` files. Export variables in the shell or configure them in the deployment environment. Never commit a real ingestion token.
