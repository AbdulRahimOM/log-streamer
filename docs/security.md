# Security and Deployment Notes

## Docker socket access

Docker and Compose collection require server-side access to the Docker API. The default quick-start config and `compose.yaml` use HTTP ingestion and do not mount or connect to a Docker socket. Direct collection is enabled only with a Docker/Compose source and, in containers, the explicit `compose.docker.yaml` override.

Access to a Docker socket commonly grants broad control over the host, including the ability to start privileged containers or mount host filesystems. The example image runs as non-root; on Linux, `DOCKER_GID` adds the host socket's group so the process can connect. Docker group access still grants a highly privileged Docker API capability and is not a security boundary. The socket mount is intentionally absent by default and must never be exposed through a public TCP endpoint.

On Linux, obtain the socket group ID with `stat -c '%g' /var/run/docker.sock`. Docker Desktop runs its engine in a VM and its socket ownership/path mapping can differ; inspect the socket available to the Docker CLI (`stat -f '%g' /var/run/docker.sock` on macOS) and verify the container can access it. For security-sensitive deployments, use a restricted Docker socket proxy or a separate collector with narrowly scoped host access. Configure only the required container names or Compose labels.

## HTTP exposure

The dashboard is intended to be deployed behind a reverse proxy when publicly accessible. Use TLS and apply the deployment's access policy at that proxy. LogStreamer does not implement a complex dashboard authentication system. The HTTP ingestion endpoint uses a bearer token supplied through `LOGSTREAMER_INGEST_TOKEN`; keep it in deployment secrets and never place it in YAML or browser code. The Go `sloghandler` package accepts the token only in server-side application code and sends it as an authorization header.

## Untrusted log content

Log records can contain attacker-controlled text and structured fields. The dashboard renders log values as text, never as HTML, and keeps at most 500 records in its browser buffer. API and dashboard responses include content-type, referrer, frame, permissions, and content security headers. Apply request and connection limits to keep untrusted inputs from consuming unbounded memory or resources.

## Phase status

The ingestion token is server-side only and is required when an HTTP source is configured. Source status omits raw errors and paths. The default quick start is HTTP-only; file collection also requires no Docker socket. Keep local `.env` and `config.yaml` files out of version control and pass credentials through deployment secrets.
