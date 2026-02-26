# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

```bash
go build ./cmd/tspages        # build the binary
go test ./...                  # run all tests
go test ./internal/serve/...   # run tests for one package
go test -run TestHandler_ServesFile ./internal/serve/...  # run a single test
```

No linter, Makefile, or CI configuration exists. The module is `tspages` using Go 1.25.

## Architecture

tspages is a static site hosting platform for Tailscale networks. It runs multiple tsnet servers — one **control plane** for deploy/admin APIs and one **per site** for serving content.

```
pages.tailnet.ts.net     → control plane (deploy + admin)
docs.tailnet.ts.net      → serves docs site
demo.tailnet.ts.net      → serves demo site
```

### Package dependency flow

```
cmd/tspages/main.go
  ├── config          — TOML config loading with defaults
  ├── multihost       — manages per-site tsnet.Server lifecycle
  │     ├── serve     — static file handler (one per site)
  │     │     ├── compress — gzip/brotli compression + precompressed asset serving
  │     │     └── hints   — HTTP/103 Early Hints for HTML pages
  │     ├── auth      — WhoIs middleware + capability checks
  │     └── tsadapter — wraps tailscale LocalClient → auth.WhoIsClient
  ├── deploy          — ZIP upload + site/deployment management (control plane only)
  │     └── extract   — ZIP/tar/markdown extraction with zip-slip protection
  ├── admin           — web dashboard, Atom feeds, help docs, OpenAPI (control plane only)
  ├── webhook         — deploy/site event notifications with delivery tracking
  ├── analytics       — SQLite-based per-request recording + queries
  ├── metrics         — Prometheus counters/histograms/gauges
  ├── cli             — `tspages deploy` subcommand with server auto-discovery
  └── storage         — filesystem-based site/deployment storage + site config
```

### Deployment disk layout

```
data/sites/{site}/
  current              → symlink to active deployment
  deployments/{id}/
    .complete
    manifest.json
    config.toml        ← parsed from tspages.toml (if provided)
    content/
```

### Key design decisions

- **Site name = tsnet hostname = DNS label.** `ValidSiteName` in `storage/store.go` enforces lowercase alphanumeric + hyphens, max 63 chars. This is the single validation point — the deploy handler calls it before any other work.
- **`multihost.Manager`** owns the map of `siteName → *tsnet.Server`. `EnsureServer(site)` uses double-check locking: checks the map under lock, releases lock for blocking tsnet startup, re-acquires to store. `deploy.SiteManager` is the interface the deploy handler uses — keeps the dependency one-directional.
- **Serve handler is per-site.** `serve.NewHandler(store, site)` bakes in the site name at construction. Each site's mux is just `GET /{path...}`.
- **Auth is capability-based.** `auth.Middleware` calls `WhoIs` on each request, parses capabilities from the tailnet policy, and stores `[]Cap` in context. Handlers check `CanView`/`CanDeploy`/`IsAdmin` themselves. The `WhoIsClient` interface (`auth/caps.go`) decouples from the real tailscale client for testability.
- **Storage is symlink-based.** Deployments live at `data/sites/{site}/deployments/{id}/`. Activation atomically swaps a `current` symlink. The serve handler resolves symlinks via `filepath.EvalSymlinks` before the path containment check.
- **Per-deployment config is TOML-based.** Deployers include `tspages.toml` in their ZIP root. At deploy time, it's parsed, validated, stored as `config.toml` at the deployment level (alongside `manifest.json`), and removed from `content/`. The serve handler reads it per-request (via the `current` symlink). Server-level `[defaults]` in the main config merge under deployment values. `SPA` and `Analytics` use `*bool` so nil means "inherit default".
- **Tests inject capabilities directly** via `auth.ContextWithCaps` — no mock WhoIs needed. Deploy tests use a `mockEnsurer` to verify `EnsureServer` calls.

### Feature inventory

**Serving features:** SPA mode, clean URLs (strip .html), custom index/404 pages, per-path headers, redirects (named params + splats), trailing slash normalization, directory listings, gzip/brotli compression (on-the-fly + precompressed), smart Cache-Control (immutable for hashed assets), ETags, HTTP/103 Early Hints.

**Deploy features:** ZIP/tar.gz/tar/xz/markdown/HTML uploads, `tspages.toml` per-deployment config, Netlify-compatible `_redirects` and `_headers` file parsing, deploy without activation (`?activate=false`), atomic symlink activation, rollback via activating older deployments, deployment cleanup (count-based retention), site deletion.

**Webhooks:** Fire on `deploy.success`, `deploy.failed`, `site.created`, `site.deleted`. Configurable per-site via `webhook_url`, `webhook_events`, `webhook_secret` in site config. Delivery attempts logged with status tracking. Admin UI for viewing deliveries.

**Admin dashboard:** Sites overview, site detail (last 5 deployments), per-site deployments page (paginated), deployment detail with file diffs, global deployment feed (paginated), per-site and cross-site analytics, webhook delivery viewer, health checks (`/healthz`, `/sites/{site}/healthz`), help pages (embedded markdown), API docs (OpenAPI/Swagger), Atom feeds (`/feed.atom`, `/sites/{site}/feed.atom`).

**Auth:** Capability-based via Tailscale ACLs. Roles: `admin`, `deploy` (site-scoped), `view`, `metrics`. Site scoping supports exact names, `*` wildcard, and glob patterns.

**Observability:** SQLite analytics (request counts, top pages, visitor breakdown by OS/device/tags), Prometheus metrics (request counts/latency, deployment counts/size, active sites gauge), webhook delivery log.

**CLI:** `tspages deploy <path> <site>` with `--no-activate`, `--server` flag, env var, or auto-discovery via Tailscale API.
