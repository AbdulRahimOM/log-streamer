# Configuration Reference

LogStreamer reads one YAML configuration file. Set `LOGSTREAMER_CONFIG` to select its path; otherwise it reads `config.yaml` from the working directory. The checked-in `config.example.yaml` demonstrates every source type. Copy it to `config.yaml` and adjust source names/settings before collectors are enabled.

Configuration is validated at startup. Unknown YAML fields and unsupported source types are errors. Source names must be non-empty and unique. Each source type accepts only its relevant settings.

## Top-level settings

| YAML key | Default | Description |
|---|---|---|
| `server.listen_address` | `:8080` | HTTP listen address. Overridden by `LOGSTREAMER_HTTP_ADDR`. |
| `docker.host` | `unix:///var/run/docker.sock` | Docker API connection address. |
| `limits.recent_capacity` | `500` | Maximum recent events retained in memory. |
| `limits.client_queue_capacity` | `256` | Maximum pending events per streaming client. |
| `limits.sse_keepalive` | `15s` | SSE keepalive comment period. |
| `limits.reconnect_min` | `1s` | Minimum collector retry delay (used in Phase 3). |
| `limits.reconnect_max` | `30s` | Maximum collector retry delay; must be at least the minimum (used in Phase 3). |
| `limits.max_sse_clients` | `100` | Maximum concurrent SSE connections. |
| `limits.ingest_max_body_bytes` | `1048576` | Maximum HTTP ingestion request size (used in Phase 3). |
| `limits.ingest_max_batch` | `100` | Maximum records per ingestion request (used in Phase 3). |

All capacities and limits must be greater than zero. Durations use Go duration syntax such as `500ms`, `5s`, or `1m`.

## Sources

Every source has a unique `name` and a `type`:

| Type | Required fields | Purpose |
|---|---|---|
| `docker` | `container_name` | Configure a standalone container to monitor (collector added in Phase 3). |
| `compose` | `compose_project`, `compose_service` | Configure a Compose service to monitor by labels (collector added in Phase 3). |
| `file` | `path` | Configure a host application log file to tail (collector added in Phase 3). |
| `http` | none | Declare a source for authenticated HTTP ingestion (added in Phase 3). |

Fields belonging to another source type are rejected. In Phase 2, `/api/services` reports these sources as `configured`; they do not collect or ingest records until Phase 3.

## Environment and secrets

- `LOGSTREAMER_CONFIG`: config file path, default `config.yaml`.
- `LOGSTREAMER_HTTP_ADDR`: overrides `server.listen_address`.
- `LOGSTREAMER_INGEST_TOKEN`: secret reserved for the Phase 3 ingestion endpoint. It is not stored in YAML and is not used by Phase 2.

The application does not automatically read `.env` files. Export variables in the shell or configure them in the deployment environment. Never commit a real ingestion token.
