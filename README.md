# tspages

> A lightweight static site hosting platform for your Tailscale network.

Have you ever wanted to host internal documentation, dashboards, or demos for your team without the hassle of setting up
and maintaining a full server, or exposing sensitive content on the public internet? tspages is a simple, secure
solution for hosting static sites directly on your tailnet.  
Each site gets its own tsnet hostname, served over HTTPS with automatic TLS, and access is controlled via Tailscale
Application Grants—no shared secrets, no separate auth layer.

Features include:

- **Easy deployment**: Upload a ZIP or tarball of your static site, or even a single Markdown file, and it's live
  immediately.
- **Admin dashboard**: View all your sites, deployments, and analytics in one place. Activate or roll back deployments
  with a click. Drop a folder onto the dashboard to deploy right away.
- **Fine-grained access control**: Use Tailscale's existing ACL system to control who can view or deploy to each site,
  down to the individual user or group level.
- **Per-site configuration**: Customize 404 pages, headers, redirects, and SPA behavior on a per-deployment basis with
  an optional `tspages.toml` included in your upload.
- **Built-in analytics**: See request counts, top pages, visitor info, and more for each site.

## Quick Start

### Prerequisites

You need a **reusable auth key** tagged with `tag:pages` (or any tag you choose). Create one in the
[Tailscale admin console](https://login.tailscale.com/admin/settings/keys) under Settings > Keys > Generate auth key.
Make sure **Reusable** is checked, since tspages registers multiple devices with the same key.

### 1. Add a tailnet grant

In the Tailscale admin console, go to Access Controls and add a grant so tailnet members can use tspages:

```json
{
  "grants": [
    {
      "src": ["autogroup:member"],
      "dst": ["tag:pages"],
      "ip": ["443"],
      "app": {
        "tspages.mazetti.me/cap/pages": [
          { "access": "admin" }
        ]
      }
    }
  ]
}
```

> Start with `admin` access to set things up. You can narrow permissions later -- see [Authorization](#authorization)
> for fine-grained examples.

### 2. Run tspages

#### Docker (recommended)

```bash
docker run -d \
  -v tspages-state:/state \
  -v tspages-data:/data \
  -e TS_AUTHKEY=tskey-auth-... \
  ghcr.io/radiergummi/tspages:latest
```

That's it. The default configuration works out of the box -- state is stored in `/state`, site data in `/data`.

#### Binary

Download the latest release from [GitHub](https://github.com/Radiergummi/tspages/releases/latest), then run:

```bash
TS_AUTHKEY=tskey-auth-... ./tspages
```

This uses `./state` and `./data` in the current directory. See [Configuration Reference](#configuration-reference) for
all options.

### 3. Deploy a site

```bash
cd your-site/dist
zip -r ../site.zip .
curl -sf --upload-file ../site.zip \
  https://pages.your-tailnet.ts.net/deploy/my-site
```

Your site is live at `https://my-site.your-tailnet.ts.net/`. Open `https://pages.your-tailnet.ts.net/sites` to see
the admin dashboard.

## Admin Dashboard

Every tspages instance includes a built-in admin panel at the control plane hostname. Admins get a full overview of all
sites, deployments, and traffic -- deployers see only the sites they have access to.

### Sites overview

The main view lists all sites with their last deploy info and a request sparkline.

<img width="2254" height="1990" alt="Sites overview showing all hosted sites with deploy info and request sparklines" src="https://github.com/user-attachments/assets/91f5b3da-c40e-41e4-87e9-3d434a28dd4b" />

### Site detail

Drill into a site to see its deployment history, activate or roll back deployments, and manage the site.

<img width="2254" height="1990" alt="Site detail page with deployment history and management actions" src="https://github.com/user-attachments/assets/4dc2f56d-9253-4866-ab8f-f1914ef56e13" />

### Deployment detail

Each deployment shows a file listing and a diff against the previous deployment (added, removed, and changed files).

<img width="2254" height="1990" alt="Deployment detail showing file diff against previous deployment" src="https://github.com/user-attachments/assets/ca5b8cbe-39ae-4d3d-9e55-e5af65343b30" />

### Deployment feed

A global, paginated feed of all deployments across all sites.

<img width="2254" height="1990" alt="Global deployment feed showing all recent deployments" src="https://github.com/user-attachments/assets/9742dcfe-ec62-477f-acd3-ac388a8ff62c" />

### Analytics

Cross-site and per-site analytics with request counts, top pages, visitors, and device breakdowns.

<img width="2254" height="1990" alt="Analytics dashboard with request counts, top pages, and visitor info" src="https://github.com/user-attachments/assets/f3bb2a2a-1d05-4c51-a119-239d4c17f6de" />

## Architecture

```
pages.your-tailnet.ts.net     → control plane: POST /deploy/{site}, GET /sites
docs.your-tailnet.ts.net      → serves docs site at /
demo.your-tailnet.ts.net      → serves demo site at /
```

Each deployed site gets its own tsnet hostname. The `pages` hostname serves as the control plane for deploy and admin
APIs.

## Upload Formats

The deploy endpoint auto-detects the upload format from magic bytes, so `Content-Type` is only needed as a fallback.

### Archives

| Format     | How to upload                                                 |
|------------|---------------------------------------------------------------|
| **ZIP**    | `zip -r site.zip . && curl --upload-file site.zip ...`        |
| **tar.gz** | `tar czf site.tar.gz . && curl --upload-file site.tar.gz ...` |
| **tar**    | `tar cf site.tar . && curl --upload-file site.tar ...`        |

Archives should contain files directly at the root (not wrapped in a parent directory). tar entries with symlinks or
hardlinks are rejected.

### Single files

For quick, one-off pages you can upload a single file instead of an archive. It is rendered into a styled HTML page
and served as `index.html`.

| Format         | Detection                                                                                  |
|----------------|--------------------------------------------------------------------------------------------|
| **Markdown**   | `?format=markdown` query param, `Content-Type: text/markdown`, or `.md`/`.markdown` in URL |
| **HTML**       | Content starts with `<` (after trimming whitespace)                                        |
| **Plain text** | Anything else                                                                              |

Examples:

```bash
# Markdown (detected by filename in the URL)
curl --upload-file README.md https://pages.your-tailnet.ts.net/deploy/notes/README.md

# Markdown (detected by query param)
curl --upload-file notes.txt "https://pages.your-tailnet.ts.net/deploy/notes?format=markdown"

# Plain text
echo "System is under maintenance" | curl -T - https://pages.your-tailnet.ts.net/deploy/status
```

## Per-Site Configuration

Each deployment can include a `tspages.toml` at the root of the archive. This file is parsed at deploy time, stored
alongside the deployment metadata, and removed from the served content. Settings take effect immediately when the
deployment is activated.

```toml
spa = true
not_found_page = "404.html"

[headers]
"/*" = { X-Frame-Options = "DENY", X-Content-Type-Options = "nosniff" }
"/*.js" = { Cache-Control = "public, max-age=31536000, immutable" }

[[redirects]]
from = "/blog/:slug"
to = "/posts/:slug"
status = 301

[[redirects]]
from = "/docs/*"
to = "/wiki/*"
```

### Fields

| Field            | Type                         | Default        | Description                                                               |
|------------------|------------------------------|----------------|---------------------------------------------------------------------------|
| `spa`            | `bool`                       | `false`        | When true, unresolved paths serve the index page instead of 404.          |
| `analytics`      | `bool`                       | `true`         | When false, disables analytics recording for this site.                   |
| `index_page`     | `string`                     | `"index.html"` | File served for directory paths.                                          |
| `not_found_page` | `string`                     | `"404.html"`   | Custom 404 page. Falls back to a built-in default if the file is missing. |
| `headers`        | `map[pattern]map[name]value` | --             | Custom response headers keyed by path pattern.                            |
| `redirects`      | `array`                      | --             | Redirect rules, evaluated first-match.                                    |

### Header patterns

| Pattern     | Matches                     |
|-------------|-----------------------------|
| `/*`        | All paths                   |
| `/*.css`    | Any path ending with `.css` |
| `/assets/*` | Any path under `/assets/`   |
| `/exact`    | Exactly `/exact`            |

### Redirect rules

Each redirect has a `from` pattern, a `to` target, and an optional `status` (301 or 302, default 301). Redirects are
checked before file resolution -- the first matching rule wins.

**Patterns:**

- `/exact` -- literal match
- `/blog/:slug` -- named segment, captured and substituted into `to`
- `/docs/*` -- splat, captures all remaining path segments

**`to` targets** can be a path (`/new/path`) or a full URL (`https://example.com/...`). Named params and `*` from the
`from` pattern are substituted into `to`.

**Validation rules:**

- `from` must be unique across all rules
- Named params used in `to` must appear in `from`
- `*` in `to` requires `*` in `from`

### Merge with server defaults

The server config can define `[defaults]` with the same fields. Per-deployment values override defaults:

- `spa`, `analytics`: deployment value wins when set; `nil` inherits the default
- `index_page`, `not_found_page`: deployment value wins when non-empty
- `headers`: deployment path patterns overlay defaults per-path
- `redirects`: deployment value entirely replaces defaults (no merging)

## Authorization

All access is controlled via Tailscale Application Grants with a custom capability. No tokens or API keys -- a node's
Tailscale identity is its credential.

### How it works

On every request, tspages calls `WhoIs` on the local Tailscale daemon to identify the caller. This returns the caller's
node info, including any application capabilities granted by your tailnet policy. tspages then checks those capabilities
against the requested action.

tspages registers every server -- the control plane and each site -- as a separate tailnet device using the same auth
key. All devices get the same tag (e.g., `tag:pages`), so a single set of grants covers all of them. Per-site access
control happens at the application level: a grant giving `view: ["docs"]` lets a user browse `docs.your-tailnet.ts.net`
but not `demo.your-tailnet.ts.net`, even though both are `tag:pages` devices.

This means:

- **One auth key** (reusable, tagged `tag:pages`) registers all servers
- **One set of grants** targeting `dst: ["tag:pages"]` controls all access
- **Capability fields** (`access`, `sites`) provide per-site granularity

The auth key must be **reusable** since multiple tsnet servers register with it. Generate one in the Tailscale admin
console with Tags set to `tag:pages`, or use an OAuth client to create auth keys programmatically.

### Access levels

Each capability object has an `access` level that determines what actions are allowed. Higher levels include all actions
of the levels below them.

| Level    | What it allows                                                                                                       |
|----------|----------------------------------------------------------------------------------------------------------------------|
| `view`   | Browse a site's static content (`GET` on the site hostname).                                                         |
| `deploy` | Everything in `view`, plus: upload, list, activate, and delete deployments.                                          |
| `admin`  | Everything in `deploy`, plus: create sites (`POST /sites`), delete sites (`DELETE /deploy/{site}`), admin dashboard. |

All levels are scoped by `sites`. Omitting `sites` means all sites.

Access is **closed by default**. A node with no matching capability grant gets `403 Forbidden` on every request,
including static content. You must explicitly grant at least `view` access.

### Capability schema

Capability name is set via `tailscale.capability` in your config (e.g., `tspages.mazetti.me/cap/pages`).

```json
{
  "access": "deploy",
  "sites": [
    "docs",
    "demo"
  ]
}
```

| Field    | Type       | Meaning                                                    |
|----------|------------|------------------------------------------------------------|
| `access` | `string`   | One of `admin`, `deploy`, or `view`.                       |
| `sites`  | `[]string` | Sites this cap applies to. `["*"]` or omitted = all sites. |

The `sites` field supports glob patterns (`*` matches any sequence, `?` matches one character) -- for example,
`["staging-*"]` matches all sites whose names start with `staging-`.

When a node matches multiple grants, capabilities are **merged**: the union of all `sites` lists across all capability
objects for each access level.

### Example grants

All examples below use `tspages.mazetti.me/cap/pages` as the capability name. Replace this with whatever you set in
`tailscale.capability` in your config.

**Let all tailnet members browse all sites:**

```json
{
  "src": [
    "autogroup:member"
  ],
  "dst": [
    "tag:pages"
  ],
  "ip": [
    "443"
  ],
  "app": {
    "tspages.mazetti.me/cap/pages": [
      {
        "access": "view"
      }
    ]
  }
}
```

**Let CI deploy to specific sites:**

A GitHub Actions runner joins the tailnet as `tag:ci` (
via [tailscale/github-action](https://github.com/tailscale/github-action)). This grant lets it deploy to `docs` and
`demo` but not create or delete sites.

```json
{
  "src": [
    "tag:ci"
  ],
  "dst": [
    "tag:pages"
  ],
  "ip": [
    "443"
  ],
  "app": {
    "tspages.mazetti.me/cap/pages": [
      {
        "access": "deploy",
        "sites": [
          "docs",
          "demo"
        ]
      }
    ]
  }
}
```

**Let CI deploy to all existing sites:**

Omitting `sites` means all sites. This lets the CI node deploy to any existing site but **not** create or delete
sites -- that requires `admin`.

```json
{
  "src": [
    "tag:ci-prod"
  ],
  "dst": [
    "tag:pages"
  ],
  "ip": [
    "443"
  ],
  "app": {
    "tspages.mazetti.me/cap/pages": [
      {
        "access": "deploy"
      }
    ]
  }
}
```

**Give a team full admin access:**

Admins can create and delete sites, deploy to any site, and access the admin dashboard.

```json
{
  "src": [
    "group:engineering"
  ],
  "dst": [
    "tag:pages"
  ],
  "ip": [
    "443"
  ],
  "app": {
    "tspages.mazetti.me/cap/pages": [
      {
        "access": "admin"
      }
    ]
  }
}
```

**Let a team manage specific sites:**

This grants admin over `docs` and `staging` only -- the team can create, delete, and deploy to those sites but not
others.

```json
{
  "src": [
    "group:docs-team"
  ],
  "dst": [
    "tag:pages"
  ],
  "ip": [
    "443"
  ],
  "app": {
    "tspages.mazetti.me/cap/pages": [
      {
        "access": "admin",
        "sites": [
          "docs",
          "staging"
        ]
      }
    ]
  }
}
```

**Restrict a site to a specific group:**

Only the `group:security` team can view the `security-reports` site. Other tailnet members with unscoped `view` from a
broader grant will also have access -- if you want a site truly restricted, don't include it in any wildcard view grants
and instead grant access explicitly.

```json
{
  "src": [
    "group:security"
  ],
  "dst": [
    "tag:pages"
  ],
  "ip": [
    "443"
  ],
  "app": {
    "tspages.mazetti.me/cap/pages": [
      {
        "access": "view",
        "sites": [
          "security-reports"
        ]
      }
    ]
  }
}
```

## API

All API endpoints are on the control plane hostname (e.g., `pages.your-tailnet.ts.net`). Sites are served on their own
hostnames. Every endpoint that returns HTML also supports JSON via `Accept: application/json` or a `.json` URL suffix
(e.g., `/sites.json`).

### Deploy a site

```
POST /deploy/{site}
PUT  /deploy/{site}
PUT  /deploy/{site}/{filename}
```

Upload your site's build output. The format is auto-detected (see [Upload Formats](#upload-formats)). The `{filename}`
variant is useful for format detection when uploading single files (e.g., `PUT /deploy/notes/README.md` triggers
Markdown rendering).

Query parameters:

- `?activate=false` -- upload without switching live traffic (useful for staging)

Response:

```json
{
  "deployment_id": "a3f9c1e2",
  "site": "docs",
  "url": "https://docs.your-tailnet.ts.net/"
}
```

Requires `deploy` capability for the target site. If the site doesn't exist yet, it is created automatically (requires
`admin`).

Old deployments are auto-cleaned after each deploy, keeping the most recent `max_deployments` (default 10). The active
deployment is never removed.

### List deployments

```
GET /deploy/{site}
```

Returns a JSON array of deployments for a site, including which one is active.

Requires `deploy` capability for the site.

### Activate a deployment

```
POST /deploy/{site}/{id}/activate
```

Switches live traffic to a specific deployment. Useful for rollbacks.

Requires `deploy` capability for the site.

### Delete a deployment

```
DELETE /deploy/{site}/{id}
```

Removes a deployment's files. Cannot delete the currently active deployment -- activate a different one first.

Requires `deploy` capability for the site.

### Delete all inactive deployments

```
DELETE /deploy/{site}/deployments
```

Removes all deployments except the currently active one.

Requires `deploy` capability for the site.

### Create a site

```
POST /sites
Content-Type: application/x-www-form-urlencoded
```

Creates an empty site directory. Body: `name=my-site`. Returns a redirect to `/sites/{name}` (or JSON with
`Accept: application/json`).

Requires `admin` access for the site name.

### Delete a site

```
DELETE /deploy/{site}
```

Stops the site's server and removes all deployments.

Requires `admin` access for the site.

### Admin dashboard

```
GET /sites                           # all sites
GET /sites/{site}                    # site detail with deployment list
GET /sites/{site}/deployments/{id}   # deployment detail with file listing and diff
GET /deployments                     # global deployment feed (paginated)
GET /analytics                       # cross-site analytics (admins)
GET /sites/{site}/analytics          # per-site analytics
```

The sites list is accessible to any authenticated user; admins see all sites, others see only sites they have `view` or
`deploy` access to. Deployment detail pages show a diff against the previous deployment (added, removed, and changed
files).

### Browse sites

Each site is served at the root of its own hostname:

```
https://docs.your-tailnet.ts.net/
https://demo.your-tailnet.ts.net/style.css
```

Directory paths serve `index.html` automatically (configurable via `index_page`). Responses include ETags based on the
deployment ID, so unchanged content returns `304 Not Modified` on repeat requests.

Requires `view` capability for the site.

## Analytics

tspages records per-request analytics for all sites (enabled by default). Data is stored in a local SQLite database at
`{data_dir}/analytics.db`.

Each event captures: timestamp, site, path, HTTP status, user identity (login name, display name), node info (name, IP,
OS, tags), and device type. Recording is async and non-blocking -- events are dropped rather than queued if the system
is under heavy load.

### Viewing analytics

Admins can view analytics via the dashboard:

- **Cross-site**: `GET /analytics` -- overview of all sites
- **Per-site**: `GET /sites/{site}/analytics`

Both views support a `?range=` parameter with ISO 8601 durations: `PT24H` (default), `P7D`, `P30D`, `P1Y`, or `all`.

### Disabling analytics

Per-site in the deployment's `tspages.toml`:

```toml
analytics = false
```

Or as a server-wide default:

```toml
[defaults]
analytics = false
```

### Purging analytics data

Admins can delete all analytics data for a site:

```
POST /sites/{site}/analytics/purge
```

## GitHub Actions

```yaml
name: Deploy to tspages

on:
  push:
    branches: [ main ]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Build
        run: npm ci && npm run build

      - name: Connect to tailnet
        uses: tailscale/github-action@v2
        with:
          oauth-client-id: ${{ secrets.TS_OAUTH_CLIENT_ID }}
          oauth-secret: ${{ secrets.TS_OAUTH_SECRET }}
          tags: tag:ci

      - name: Deploy
        run: |
          cd dist
          zip -r ../deploy.zip .
          curl -sf \
            --upload-file ../deploy.zip \
            -H "Content-Type: application/zip" \
            https://pages.your-tailnet.ts.net/deploy/docs
```

The runner joins the tailnet as `tag:ci`. The tailnet policy grants deploy access -- no secrets in the `curl` command.

## Configuration Reference

```toml
[tailscale]
hostname = "pages"                        # control plane tsnet hostname (default: "pages")
state_dir = "/var/lib/tspages"             # tsnet state directory (default: "./state")
auth_key = ""                             # reusable, tagged key; or set TS_AUTHKEY env var
capability = "tspages.mazetti.me/cap/pages"   # required

[server]
data_dir = "/data"    # site storage root (default: "./data")
max_upload_mb = 500        # max upload size in MB (default: 500)
max_sites = 100        # max concurrent site servers (default: 100)
max_deployments = 10         # max deployments kept per site (default: 10)
log_level = "warn"     # "debug", "info", "warn", "error" (default: "warn")

# Server-wide defaults for per-site config. Deployments can override these
# via their own tspages.toml included in the archive.
[defaults]
spa = false
analytics = true
index_page = "index.html"
not_found_page = "404.html"

[defaults.headers]
"/*" = { X-Frame-Options = "DENY" }
```

The `log_level` can also be set via the `TSPAGES_LOG_LEVEL` environment variable (config file takes precedence).

Run with: `tspages -config /path/to/tspages.toml`

## Local Development

To work on the admin frontend with hot reloading:

```bash
# Terminal 1: Vite dev server
npx vite

# Terminal 2: Go server with dev mode
go run ./cmd/tspages -dev
```

Open http://localhost:8080 in your browser. The `-dev` flag:

- Serves CSS/JS from the Vite dev server with hot module replacement
- Re-parses Go templates from disk on every request (refresh to see changes)
- Provides a localhost listener with mock admin auth (no Tailscale required to browse the UI)

The tsnet control plane still starts normally alongside the dev server. Production builds use `npx vite build`, which
outputs to `internal/admin/assets/dist/` (embedded at compile time).

## Security

- **Archive extraction** rejects path traversal (zip-slip and tar equivalents), symlinks, hardlinks, and enforces size
  limits on both compressed and decompressed content
- **Site names** must be valid DNS labels (lowercase alphanumeric and hyphens, max 63 characters)
- **Auth** uses the local Tailscale daemon's WhoIs -- identity is verified by Tailscale, not forgeable by the remote
  peer
- **Deployments** are atomic: files are fully written before the `current` symlink is swapped
- **State directory** (`state_dir`) should be `0700` -- it contains the node key and certificates
