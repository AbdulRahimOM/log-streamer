# HTTP and SSE API

All routes are served by the same Go process. The health and services routes do not require authentication. Protect public access with a TLS-terminating reverse proxy and its access policy.

For cross-origin browser clients, configure exact origins with `server.cors_allowed_origins`. CORS is enabled only for `GET /api/services` and `GET /api/logs/stream`, including `Last-Event-ID` reconnect preflights; credentials and wildcard origins are not allowed. Requests without an `Origin` header continue to work for local tools.

## `GET /healthz`

Returns `200 application/json` with `{"status":"ok"}` when the HTTP service is available.

## `GET /api/services`

Returns configured sources sorted by name:

```json
{"services":[{"name":"quickstart","type":"http","state":"running"}]}
```

Collector states are `starting`, `running`, `retrying`, or `stopped`. Errors are generic safe summaries. Paths, Docker connection details, and tokens are omitted.

## `GET /api/logs/stream`

Streams Server-Sent Events with `Content-Type: text/event-stream`. Each event has a process-local sequence ID and JSON data:

```text
id: 12
data: {"timestamp":"2026-01-02T03:04:05Z","source":"quickstart","origin":"http","level":"INFO","message":"ready","fields":{"region":"local"}}
```

`origin` and `fields` are optional. Query filters are single values:

| Query | Values |
|---|---|
| `source` | Configured source name |
| `level` | `DEBUG`, `INFO`, `WARN`, `ERROR`, or `UNKNOWN` |
| `origin` | `stdout`, `stderr`, `file`, or `http` |

The server sends periodic keepalive comments. Slow clients are disconnected when their bounded queue fills. Native browser reconnection works, but `Last-Event-ID` is not used to resume at an exact position; a reconnect receives the current recent-history snapshot and may include duplicates.

## `POST /api/logs?source=<configured-http-source>`

Requires `Authorization: Bearer <LOGSTREAMER_INGEST_TOKEN>` and `Content-Type: application/json`. The `source` query value selects a configured `type: http` source. The request can be one event object or a nonempty array. Each event requires a string `message` or `msg`; `timestamp`, `level`, `fields`, and extra metadata are optional.

```sh
curl -i \
  -H "Authorization: Bearer $LOGSTREAMER_INGEST_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"level":"INFO","message":"hello from curl","fields":{"component":"demo"}}' \
  'http://127.0.0.1:8080/api/logs?source=quickstart'
```

Successful requests return `202` and `{"accepted":N}`. Invalid payloads return `400`, missing or incorrect credentials `401`, an unknown or non-HTTP source `404`, and body/event/batch limit violations `413`. A batch is fully validated before any of its records are published.
