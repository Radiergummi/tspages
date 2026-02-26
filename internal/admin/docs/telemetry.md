# Telemetry

tspages exposes health checks, Prometheus metrics, and Atom feeds for monitoring deployments and platform status.

## Health checks

### Platform health

```
GET /healthz
```

Returns the overall platform status as JSON. This endpoint is **unauthenticated** -- it is designed for orchestrator
probes (Docker HEALTHCHECK, Kubernetes liveness).

Response (200 when healthy, 503 when degraded):

```json
{
  "status": "ok",
  "checks": {
    "storage": "ok",
    "analytics": "ok"
  }
}
```

Checks:

- **storage** -- verifies the data directory is readable
- **analytics** -- pings the SQLite database (or `"disabled"` if analytics are off)

### Per-site health

```
GET /sites/{site}/healthz
```

Returns health for a single site, including whether its tsnet server is running and which deployment is active.

Response (200 when healthy, 503 when the server is stopped):

```json
{
  "status": "ok",
  "site": "docs",
  "server": "running",
  "active_deployment": "a3f9c1e2"
}
```

Requires `view` (or `admin`) capability for the site.

### Local health listener

For Docker and other orchestrators that can't reach the Tailscale network, tspages can bind a plain HTTP listener on
localhost that serves `/healthz`:

```toml
[server]
health_addr = ":9091"
```

Or via environment variable:

```
TSPAGES_HEALTH_ADDR=:9091
```

The Docker image sets this by default. The Dockerfile HEALTHCHECK probes `http://localhost:9091/healthz` every 10
seconds.

## Prometheus metrics

```
GET /metrics
```

Returns metrics in Prometheus exposition format. Requires `metrics` (or `admin`) capability.

Available metrics:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `tspages_http_requests_total` | counter | `site`, `status` | Total HTTP requests by site and status code |
| `tspages_http_request_duration_seconds` | histogram | `site` | Request duration in seconds |
| `tspages_deployments_total` | counter | `site` | Total deployments by site |
| `tspages_deployment_size_bytes` | histogram | -- | Deployment upload size in bytes |
| `tspages_sites_active` | gauge | -- | Number of active site servers |

## Atom feeds

Deployment activity is available as Atom feeds (RFC 4287) for use in feed readers or CI notifications.

### Global feed

```
GET /feed.atom
```

Lists the most recent deployments across all sites the authenticated user has `view` access to (up to 50 entries).

### Per-site feed

```
GET /sites/{site}/feed.atom
```

Lists the most recent deployments for a single site (up to 50 entries). Requires `view` (or `admin`) capability for the
site.

Both feeds include autodiscovery `<link>` tags in the corresponding HTML pages, so feed readers can find them
automatically.
