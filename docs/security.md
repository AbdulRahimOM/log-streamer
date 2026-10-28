# Security and Deployment Notes

## Docker socket access

Docker collection requires server-side access to the Docker API. Access to a Docker socket commonly grants broad control over the host, including the ability to start privileged containers or mount host filesystems. Treat a mounted Docker socket as a high-privilege capability, restrict who can run the LogStreamer container, and do not expose the socket through a browser-facing endpoint.

Configure only the Docker containers or Compose project/services that LogStreamer should monitor. The browser-facing service must not offer arbitrary container discovery or Docker control. Docker connection details and secrets must never be included in API responses or logs.

## HTTP exposure

The dashboard is intended to be deployed behind a reverse proxy when publicly accessible. Use TLS and apply the deployment's access policy at that proxy. LogStreamer does not implement a complex dashboard authentication system. The future HTTP ingestion endpoint uses a bearer token supplied through `LOGSTREAMER_INGEST_TOKEN`; keep it in deployment secrets and never place it in YAML or browser code.

## Untrusted log content

Log records can contain attacker-controlled text and structured fields. The dashboard must render these values as text, never as HTML. Apply request and connection limits to keep untrusted inputs from consuming unbounded memory or resources.

## Phase status

Phase 2 serves the dashboard, SSE stream, source list, and health endpoint. Source collectors and HTTP ingestion are not implemented until Phase 3; the service does not yet access Docker or read configured log files.
