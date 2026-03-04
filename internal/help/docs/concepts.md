---
description: How tspages works — architecture, deployment model, and request flow.
category: guide
---

# Concepts

This page explains how tspages is structured and how the pieces fit together. Understanding these
concepts will help you reason about configuration, access control, and troubleshooting.

## Architecture

tspages runs as a single process that manages multiple [tsnet](https://tailscale.com/kb/1244/tsnet)
servers — lightweight Tailscale nodes embedded in the application. There's one **control plane**
server and one server **per site**:

```
pages.your-tailnet.ts.net    → control plane (deploy API, admin dashboard)
docs.your-tailnet.ts.net     → serves the "docs" site
blog.your-tailnet.ts.net     → serves the "blog" site
status.your-tailnet.ts.net   → serves the "status" site
```

The control plane handles deployments, the admin dashboard, analytics, webhooks, and API endpoints.
Each site server handles only `GET` requests for its own static content.

All servers register on your tailnet using the same reusable auth key and tag (e.g., `tag:pages`).
This means a single set of [grants](authorization) controls access to everything — per-site
granularity comes from the capability fields, not from separate network policies.

## Sites and hostnames

A site name is simultaneously a tsnet hostname and a DNS label. Creating a site called `docs` starts
a new tsnet server that registers as `docs.your-tailnet.ts.net`.

Because of this, site names must follow DNS label rules:

- Lowercase letters, digits, and hyphens only
- Max 63 characters
- Cannot start or end with a hyphen

The control plane hostname defaults to `pages` but is configurable via `tailscale.hostname` in the
server config.

## Deployments

A deployment is a snapshot of your site's files at a point in time. When you deploy, tspages:

1. Receives the upload (ZIP, tar.gz, single file, etc.)
2. Extracts files into a new deployment directory
3. Parses `tspages.toml` from the archive root (if present) and stores it as deployment config
4. Activates the deployment by atomically swapping a symlink

Each site can have multiple deployments on disk. Only one is **active** at a time — the one the
`current` symlink points to. The disk layout looks like this:

```
data/sites/docs/
  current             → symlink to deployments/a3f9c1e2
  deployments/
    a3f9c1e2/
      manifest.json   ← metadata (timestamp, uploader, size, comment)
      config.toml     ← parsed from tspages.toml
      content/
        index.html
        style.css
    b7d4e0f1/
      ...
```

This design has a few useful properties:

- **Rollbacks are instant.** Activating an older deployment just swaps the symlink — no files are
  copied or re-uploaded.
- **Deployments are atomic.** Files are fully written before the symlink changes. Visitors never see
  a half-deployed site.
- **Old deployments are auto-cleaned.** After each deploy, tspages removes the oldest deployments
  beyond the `max_deployments` limit (default 10). The active deployment is never removed.

## Request flow

When a request arrives at a site server (e.g., `GET /about` on `docs.your-tailnet.ts.net`):

1. **Auth check.** tspages calls the local Tailscale daemon's `WhoIs` to identify the caller and
   extract their capabilities. If the caller doesn't have `view` access for this site, they get a
   `403`.
2. **Symlink resolution.** The handler resolves the `current` symlink to find the active deployment's
   content directory.
3. **Redirects.** If the deployment config defines redirect rules, they're checked first-match before
   file resolution.
4. **File lookup.** The path is resolved through a series of fallbacks: exact file → directory
   index → `.html` extension → SPA fallback → custom 404 page → built-in 404.
5. **Response.** The file is served with ETags (based on deployment ID), compression
   (gzip/brotli, including precompressed assets), cache headers (immutable for hashed filenames),
   and any custom headers from the deployment config.

## Configuration layers

tspages has two layers of configuration that merge together:

**Server config** (`tspages.toml` passed to the binary) defines global settings like the hostname,
data directory, and resource limits. It can also set `[defaults]` for per-site options like
`spa_routing` or `headers`.

**Deployment config** (`tspages.toml` inside the uploaded archive) defines per-site settings for
that specific deployment. Values here override server defaults.

The merge rules are straightforward:

- Boolean fields (`spa_routing`, `analytics`, etc.): deployment value wins when explicitly set;
  omitted fields inherit the server default
- String fields (`index_page`, `not_found_page`): deployment value wins when non-empty
- `headers`: deployment patterns overlay defaults per-path
- `redirects`: deployment value entirely replaces defaults (no merging)

See [Configuration](configuration) for server-level settings and
[Per-Site Configuration](per-site-config) for deployment-level settings.

## Access model

tspages doesn't use API keys, tokens, or passwords. Instead, it relies on Tailscale's identity
system: every request comes from a known node on your tailnet, and tspages checks what that node is
allowed to do.

Access levels are hierarchical:

- **view** — browse a site's static content
- **deploy** — everything in view, plus upload and manage deployments
- **admin** — everything in deploy, plus create/delete sites and access the admin dashboard
- **metrics** — scrape the Prometheus endpoint (independent of site access)

Each level can be scoped to specific sites using the `sites` field in the grant. Omitting `sites`
(or setting it to `["*"]`) means all sites.

See [Authorization](authorization) for grant examples.
