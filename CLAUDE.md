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
  │     ├── auth      — WhoIs middleware + capability checks
  │     └── tsadapter — wraps tailscale LocalClient → auth.WhoIsClient
  ├── deploy          — ZIP upload handler (control plane only)
  │     └── extract   — ZIP extraction with zip-slip protection
  ├── admin           — status endpoint (control plane only)
  └── storage         — filesystem-based site/deployment storage
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
