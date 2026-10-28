# Operations and Limitations

## Docker and Compose sources

Docker collection requires access to the Docker Engine API. The Compose files do not mount the Docker socket by default. Enable direct collection only with the optional `compose.docker.yaml` override and a Docker-enabled source configuration. See [security and deployment](security.md) for Linux and Docker Desktop setup and socket risks.

Docker sources match exact configured container names. Compose sources reconcile project/service labels every five seconds and support multiple replicas. Both start from the current time and retry with the configured bounded backoff. Reconnection can produce duplicates near a stream boundary; delivery is not exactly once.

## File sources

File sources seek to EOF when opened, poll every 250 ms, and resume when a temporarily missing file appears. They detect truncation and replacement and read the new contents from the beginning. Oversized lines are drained through the next newline and skipped. In containers, mount only the configured host log file and mount it read-only.

## Memory and streaming

History is held only in memory and is lost on process restart. `limits.recent_capacity` bounds server history, `limits.client_queue_capacity` bounds pending events per SSE client, and the dashboard keeps at most 500 events in the browser. A slow SSE client is disconnected when its queue fills. SSE IDs are process-local; `Last-Event-ID` replay is not implemented. Browser reconnection may receive a fresh history snapshot with duplicates.

HTTP ingestion is protected by one server-side bearer token shared by all configured HTTP sources. The source query selects the source identity. Request, event, and batch sizes are bounded by configuration. The optional `sloghandler` uses its own bounded forwarding queue; queue overflow and delivery failures drop the forwarded copy while preserving downstream logging.

## Reverse proxies

When a reverse proxy serves the dashboard, disable response buffering for the SSE route and set a read timeout longer than `limits.sse_keepalive`. For nginx, the handler already sends `X-Accel-Buffering: no`; an explicit location can also use:

```nginx
location /api/logs/stream {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 1h;
}
```

Use TLS and the deployment's authentication policy at the proxy. Keep the Docker API private; never publish its TCP endpoint publicly.

## Troubleshooting

- `401` from ingestion: confirm `LOGSTREAMER_INGEST_TOKEN` is set identically at the service and client.
- `404` from ingestion: confirm the `source` query matches a configured HTTP source.
- A source stuck in `retrying`: verify the container name/Compose labels, file path and permissions, or Docker socket access.
- No old file records on startup: file collection intentionally starts at EOF.
- Missing or repeated records around reconnect: delivery is best-effort across source reconnects; gaps and duplicates are possible because the service does not persist history or provide exactly-once delivery.
