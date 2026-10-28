# Configuration Reference

LogStreamer reads one YAML configuration file. Set `LOGSTREAMER_CONFIG` to select its path; otherwise it reads `config.yaml` from the working directory. The checked-in `config.example.yaml` demonstrates every source type. Copy it to `config.yaml` and adjust source names/settings before starting later-phase service behavior.

Configuration is validated at startup. Unknown YAML fields and unsupported source types are errors. Source names must be non-empty and unique. Each source type accepts only its relevant settings.

## Top-level settings

| YAML key | Default | Description |
|---|---|---|
| `server.listen_address` | `:8080` | HTTP listen address. Overridden by `LOGSTREAMER_HTTP_ADDR`. |
| `docker.host` | `unix:///var/run/docker.sock` | Docker API connection address. |
| `limits.recent_capacity` | `500` | Maximum recent events retained in memory. |
| `limits.client_queue_capacity` | `256` | Maximum pending events per streaming client. |
| `limits.sse_keepalive` | `15s` | Planned SSE keepalive period. |
| `limits.reconnect_min` | `1s` | Minimum planned collector retry delay. |
| `limits.reconnect_max` | `30s` | Maximum planned collector retry delay; must be at least the minimum. |
| `limits.max_sse_clients` | `100` | Planned concurrent SSE connection limit. |
| `limits.ingest_max_body_bytes` | `1048576` | Maximum planned HTTP ingestion request size. |
| `limits.ingest_max_batch` | `100` | Maximum planned records per ingestion request. |

All capacities and limits must be greater than zero. Durations use Go duration syntax such as `500ms`, `5s`, or `1m`.

## Sources

Every source has a unique `name` and a `type`:

| Type | Required fields | Purpose |
|---|---|---|
| `docker` | `container_name` | Monitor a configured standalone container. |
| `compose` | `compose_project`, `compose_service` | Find Compose containers by project/service labels, including recreated instances. |
| `file` | `path` | Tail a host application log file. |
| `http` | none | Declare a named source for future authenticated HTTP ingestion. |

Fields belonging to another source type are rejected. The HTTP source is only a configuration declaration in Phase 1; the ingestion endpoint is not yet available.

## Environment and secrets

- `LOGSTREAMER_CONFIG`: config file path, default `config.yaml`.
- `LOGSTREAMER_HTTP_ADDR`: overrides `server.listen_address`.
- `LOGSTREAMER_INGEST_TOKEN`: secret reserved for the future ingestion endpoint. It is not stored in YAML and is not needed by Phase 1 config validation.

The application does not automatically read `.env` files. Export variables in the shell or configure them in the deployment environment. Never commit a real ingestion token.
