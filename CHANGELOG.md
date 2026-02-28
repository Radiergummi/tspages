# Changelog

All notable changes to tspages are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-02-28

### Added

- Site-scoped admin and deploy capabilities with glob pattern matching.

### Fixed

- Webhook SSRF via redirects and URL injection in CLI deploy.
- Site-scoped auth check on webhook detail and retry handlers, preventing
  cross-site information disclosure.
- Consistent `trimSuffix` handling on site feed path values.
- Consistent error logging for health handler JSON encoding and webhook
  delivery attempt queries.

## [0.2.0] - 2026-02-27

### Added

- xz decompression support for `tar.xz` uploads.
- Webhook delivery retry button in admin UI.
- Webhook latency tracking with custom chart tooltips.
- File content hashing for deployment manifests.
- Admin footer with `hide_footer` config option.

### Fixed

- SQLite `busy_timeout` in webhook tests to prevent `SQLITE_BUSY`.
- Invalid `/webhooks/{id}.json` route pattern removed.
- Responsive layout improvements across admin UI.

## [0.1.0] - 2026-02-26

### Added

- Static site hosting on Tailscale networks with per-site tsnet servers.
- ZIP, tar.gz, tar, and markdown/HTML upload support.
- Per-deployment `tspages.toml` configuration.
- Netlify-compatible `_redirects` and `_headers` file parsing.
- SPA routing mode and clean URLs (`.html` extension stripping).
- Custom index and 404 pages.
- Per-path headers and redirects with named params and splats.
- Trailing slash normalization and directory listings.
- Gzip and Brotli compression (on-the-fly and precompressed asset serving).
- Smart `Cache-Control` with immutable hashed assets, ETags, and
  `stale-while-revalidate`.
- HTTP/103 Early Hints for stylesheets and scripts.
- Deploy without activation (`?activate=false`), atomic symlink activation,
  rollback via older deployments, and count-based deployment cleanup.
- Webhook notifications on deploy, failure, site creation, and site deletion,
  with HMAC signing, SSRF protection, delivery logging, and retries.
- Webhook delivery log UI with filtering, pagination, and analytics.
- Admin dashboard with sites overview, deployment detail with file diffs,
  global deployment feed, per-site and cross-site analytics.
- Atom feeds for deployment activity (global and per-site).
- Health check endpoints (`/healthz` and `/sites/{site}/healthz`).
- Help pages with embedded markdown and OpenAPI/Swagger API docs.
- Capability-based auth via Tailscale ACLs (admin, deploy, view, metrics).
- SQLite analytics (request counts, top pages, visitor breakdown).
- Prometheus metrics (request counts/latency, deployment counts/size, active
  sites gauge).
- `tspages deploy` CLI subcommand with server auto-discovery.
- GitHub Actions deploy action.

[Unreleased]: https://github.com/Radiergummi/tspages/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/Radiergummi/tspages/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/Radiergummi/tspages/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/Radiergummi/tspages/releases/tag/v0.1.0
