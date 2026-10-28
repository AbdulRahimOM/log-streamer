# LogStreamer

LogStreamer collects configured application logs and streams them to a small browser dashboard. Start with HTTP ingestion; Docker access is optional.

## Quick start

### Requirements

- Go 1.24 or newer for a local run.
- Docker Engine and the Compose plugin only for a container run or Docker/Compose collection.
- OpenSSL to generate a local ingestion token (or another secure random-token generator).

### Run locally

```sh
cp config.example.yaml config.yaml
cp .env.example .env
```

Set `LOGSTREAMER_INGEST_TOKEN` in `.env` to a random value, then export the file and run the service:

```sh
openssl rand -hex 32
set -a
. ./.env
set +a
go run ./cmd/logstreamer
```

Open <http://127.0.0.1:8080>. Send a test event from another terminal:

```sh
curl -i \
  -H "Authorization: Bearer $LOGSTREAMER_INGEST_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"level":"INFO","message":"hello from LogStreamer","fields":{"component":"quick-start"}}' \
  'http://127.0.0.1:8080/api/logs?source=quickstart'
```

The `.env` file is not loaded by the Go application itself. Export its values as shown, or configure environment variables in your process manager.

### Run with Docker Compose

```sh
cp .env.example .env
# Set LOGSTREAMER_INGEST_TOKEN in .env to a random secret.
docker compose up --build -d
docker compose logs -f logstreamer
```

Open <http://127.0.0.1:8080>. The default Compose service uses the HTTP-only quick-start config, mounts no Docker socket, and publishes the web port only on loopback. Stop it with `docker compose down`.

## Configure sources

The quick-start `config.example.yaml` configures one HTTP source named `quickstart`. Set `LOGSTREAMER_CONFIG` to use another YAML file.

| Source | Example | Notes |
|---|---|---|
| HTTP ingestion | `config.example.yaml` | Requires `LOGSTREAMER_INGEST_TOKEN`; select the configured source in the `source` query. |
| Docker container | `config.docker.example.yaml` | Set the exact running container name; Docker Engine access is required. |
| Docker Compose | `config.compose.example.yaml` | Set the Compose project and service labels; Docker Engine access is required. |
| Host file | `config.file.example.yaml` | Set a readable path; file collection starts at EOF. |

For a local Docker source run, set `LOGSTREAMER_CONFIG=config.docker.example.yaml` and make sure the current user can access the configured Docker Engine endpoint. For a file source, set `LOGSTREAMER_CONFIG=config.file.example.yaml` and edit the path to a file readable by the process.

### Docker/Compose collection in a container

The default Compose setup does not grant Docker API access. To enable direct container collection, first set `LOGSTREAMER_CONFIG_FILE=./config.docker.example.yaml` (or `./config.compose.example.yaml`) in `.env`, then use the socket override.

On Linux, find the socket group ID and start with the override:

```sh
DOCKER_GID="$(stat -c '%g' /var/run/docker.sock)" \
  docker compose -f compose.yaml -f compose.docker.yaml up --build -d
```

On macOS, Docker Desktop runs its engine in a VM and may expose a socket with different ownership. If `/var/run/docker.sock` is available, inspect its group with `stat -f '%g' /var/run/docker.sock` and provide that value as `DOCKER_GID`:

```sh
DOCKER_GID="$(stat -f '%g' /var/run/docker.sock)" \
  docker compose -f compose.yaml -f compose.docker.yaml up --build -d
```

Socket path and group mapping depend on the Docker Desktop version and configuration; verify access in your environment. Do not expose the Docker API over a public TCP port.

For security-sensitive deployments, prefer a restricted Docker socket proxy or a separate collector with narrowly scoped access. A non-root container with Docker socket access still has access to a highly privileged API.

### File collection in a container

Create or select an existing host log file, then set `LOGSTREAMER_CONFIG_FILE=./config.file.example.yaml` and `LOGSTREAMER_FILE_PATH` in `.env` to its absolute host path. Start using the file override:

```sh
docker compose -f compose.yaml -f compose.file.yaml up --build -d
```

The override mounts only that file read-only at `/var/log/host-service.log`. Edit the example config if you choose a different in-container path.

## Dashboard and API

- Dashboard: `GET /`
- Health: `GET /healthz`
- Configured sources: `GET /api/services`
- Live events: `GET /api/logs/stream`
- HTTP ingestion: `POST /api/logs?source=<configured-http-source>`

The dashboard filters by source and level and supports pause/resume, auto-scroll, and clearing its local buffer. For request examples, response codes, and SSE event details, see [the API reference](docs/api.md).

## Optional Go `slog` handler

The public `sloghandler` package forwards records asynchronously while preserving a downstream handler such as stdout. Configure an HTTP source in LogStreamer and pass its URL, including the source selector, and the ingestion token:

```go
package main

import (
    "context"
    "log"
    "log/slog"
    "os"
    "time"

    "github.com/abdulrahimom/logstreamer/sloghandler"
)

func main() {
    downstream := slog.NewJSONHandler(os.Stdout, nil)
    handler, err := sloghandler.New(
        "http://127.0.0.1:8080/api/logs?source=my-app",
        os.Getenv("LOGSTREAMER_INGEST_TOKEN"),
        downstream,
    )
    if err != nil {
        log.Fatal(err)
    }
    logger := slog.New(handler)
    logger.Info("service started", "port", 8080)

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := handler.Shutdown(ctx); err != nil {
        log.Print(err)
    }
}
```

See [the package documentation](sloghandler/doc.go) for imports, queue behavior, and options.

## Troubleshooting

- `401`: verify the client bearer token matches `LOGSTREAMER_INGEST_TOKEN`.
- `404` from ingestion: check the `source` query against an HTTP source in the active config.
- A Docker source is retrying: confirm the configured name, Docker context/socket, and socket permissions.
- A file source is retrying: confirm the path exists and is readable. Existing file contents are intentionally skipped at startup.
- SSE updates stop behind a proxy: disable buffering for `/api/logs/stream`; see [operations guidance](docs/operations.md).

## More documentation

- [Configuration reference](docs/configuration.md)
- [HTTP and SSE API](docs/api.md)
- [Architecture and data flow](docs/architecture.md)
- [Security and deployment](docs/security.md)
- [Operations and limitations](docs/operations.md)
