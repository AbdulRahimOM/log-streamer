# Security and Deployment Notes

## Docker socket access

Docker collection requires server-side access to the Docker API. Access to a Docker socket commonly grants broad control over the host, including the ability to start privileged containers or mount host filesystems. Treat a mounted Docker socket as a high-privilege capability, restrict who can run the LogStreamer container, and do not expose the socket through a browser-facing endpoint.

Configure only the Docker containers or Compose project/services that LogStreamer should monitor. The browser-facing service must not offer arbitrary container discovery or Docker control. Docker connection details and secrets must never be included in API responses or logs.

## HTTP exposure

The dashboard is intended to be deployed behind a reverse proxy when publicly accessible. Use TLS and apply the deployment's access policy at that proxy. LogStreamer does not implement a complex dashboard authentication system. The HTTP ingestion endpoint uses a bearer token supplied through `LOGSTREAMER_INGEST_TOKEN`; keep it in deployment secrets and never place it in YAML or browser code. The Go `sloghandler` package accepts the token only in server-side application code and sends it as an authorization header.

## Untrusted log content

Log records can contain attacker-controlled text and structured fields. The dashboard renders log values as text, never as HTML, and keeps at most 500 records in its browser buffer. API and dashboard responses include content-type, referrer, frame, permissions, and content security headers. Apply request and connection limits to keep untrusted inputs from consuming unbounded memory or resources.

## Phase status

Phase 4 adds browser-local log controls and the optional bounded `sloghandler` forwarder. The ingestion token is server-side only and is required when an HTTP source is configured. Source status omits raw errors and paths. Docker socket access remains a high-privilege capability; restrict access to the LogStreamer process and its deployment.
