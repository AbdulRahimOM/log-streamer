# LogStreamer

LogStreamer collects logs from HTTP clients, Docker containers, Docker Compose services, and host files, then shows them in a live browser dashboard. Run it alongside your services to watch their logs in one place.

## Get started

You need Go 1.24 or newer to run LogStreamer locally. To use Docker Compose, install Docker Engine and the Compose plugin. The quick start uses OpenSSL to generate an ingestion token; another secure random-token generator works too.

### Run locally

Create a config file and set an ingestion token:

```sh
cp config.example.yaml config.yaml
export LOGSTREAMER_INGEST_TOKEN="$(openssl rand -hex 32)"
```

Start LogStreamer:

```sh
go run ./cmd/logstreamer
```

Open <http://127.0.0.1:8080> to view the dashboard. The example config enables an HTTP source named `quickstart`. Send it a test log from another terminal:

```sh
curl -i \
  -H "Authorization: Bearer $LOGSTREAMER_INGEST_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"level":"INFO","message":"hello from LogStreamer","fields":{"component":"quick-start"}}' \
  'http://127.0.0.1:8080/api/logs?source=quickstart'
```

The token is required when an HTTP source is configured. The Go application does not load `.env` files; export the variable in your shell or configure it in your process manager.

### Run with Docker Compose

Copy the environment template, generate a token, then paste it as the value of `LOGSTREAMER_INGEST_TOKEN` in `.env`:

```sh
cp .env.example .env
openssl rand -hex 32
```

Then start LogStreamer:

```sh
docker compose up --build -d
```

Open <http://127.0.0.1:8080>. The default Compose setup runs the HTTP quick start. Stop it with `docker compose down`.

## Choose log sources

The default `config.example.yaml` accepts logs sent to LogStreamer over HTTP. To collect logs directly from Docker, Docker Compose, or a host file, use the corresponding example config and set `LOGSTREAMER_CONFIG` to its path:

| Source | Example config |
|---|---|
| HTTP ingestion | [`config.example.yaml`](config.example.yaml) |
| Docker container | [`config.docker.example.yaml`](config.docker.example.yaml) |
| Docker Compose service | [`config.compose.example.yaml`](config.compose.example.yaml) |
| Host file | [`config.file.example.yaml`](config.file.example.yaml) |

Docker and Compose collection need access to the Docker Engine. For detailed configuration and container setup steps, see the [configuration guide](docs/configuration.md) and [operations guide](docs/operations.md).

## Dashboard and API

The dashboard at `/` displays live logs and lets you filter by source and level. The service also provides a health check at `/healthz`, a configured source list at `/api/services`, and an HTTP ingestion endpoint at `/api/logs`. See the [HTTP and SSE API reference](docs/api.md) for request formats, authentication, and stream details.

## Documentation

- [Configuration guide](docs/configuration.md)
- [HTTP and SSE API](docs/api.md)
- [Security and deployment](docs/security.md)
- [Operations and limitations](docs/operations.md)
- [Architecture notes](docs/architecture.md)
