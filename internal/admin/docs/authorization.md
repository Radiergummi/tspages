# Authorization

All access is controlled via Tailscale Application Grants with a custom capability. No tokens or API keys -- a node's
Tailscale identity is its credential.

## How it works

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

## Access levels

Each capability object has an `access` level that determines what actions are allowed. Higher levels include all actions
of the levels below them.

| Level     | What it allows                                                                                                       |
|-----------|----------------------------------------------------------------------------------------------------------------------|
| `view`    | Browse a site's static content (`GET` on the site hostname).                                                         |
| `deploy`  | Everything in `view`, plus: upload, list, activate, and delete deployments.                                          |
| `admin`   | Everything in `deploy`, plus: create sites (`POST /sites`), delete sites (`DELETE /deploy/{site}`), admin dashboard, and metrics. |
| `metrics` | Scrape the Prometheus metrics endpoint (`GET /metrics`). Does not grant access to any site content or admin features. |

The `view`, `deploy`, and `admin` levels are scoped by `sites`. The `metrics` level is global -- it applies to the
control plane, not to individual sites, so the `sites` field is ignored.

Access is **closed by default**. A node with no matching capability grant gets `403 Forbidden` on every request,
including static content. You must explicitly grant at least `view` access.

## Capability schema

Capability name is set via `tailscale.capability` in your config (default: `tspages.mazetti.me/cap/pages`). See
[Configuration](configuration) for details.

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
| `access` | `string`   | One of `admin`, `deploy`, `view`, or `metrics`.            |
| `sites`  | `[]string` | Sites this cap applies to. `["*"]` or omitted = all sites. |

The `sites` field supports glob patterns (`*` matches any sequence, `?` matches one character) -- for example,
`["staging-*"]` matches all sites whose names start with `staging-`.

When a node matches multiple grants, capabilities are **merged**: the union of all `sites` lists across all capability
objects for each access level.

## Example grants

All examples below use `tspages.mazetti.me/cap/pages` as the capability name. Replace this with whatever you set in
`tailscale.capability` in your config.

**Let all tailnet members browse all sites:**

```json
{
  "src": ["autogroup:member"],
  "dst": ["tag:pages"],
  "ip": ["443"],
  "app": {
    "tspages.mazetti.me/cap/pages": [
      { "access": "view" }
    ]
  }
}
```

**Let CI deploy to specific sites:**

A GitHub Actions runner joins the tailnet as `tag:ci` (via
[tailscale/github-action](https://github.com/tailscale/github-action)). This grant lets it deploy to `docs` and
`demo` but not create or delete sites.

```json
{
  "src": ["tag:ci"],
  "dst": ["tag:pages"],
  "ip": ["443"],
  "app": {
    "tspages.mazetti.me/cap/pages": [
      { "access": "deploy", "sites": ["docs", "demo"] }
    ]
  }
}
```

**Let CI deploy to all existing sites:**

Omitting `sites` means all sites. This lets the CI node deploy to any existing site but **not** create or delete
sites -- that requires `admin`.

```json
{
  "src": ["tag:ci-prod"],
  "dst": ["tag:pages"],
  "ip": ["443"],
  "app": {
    "tspages.mazetti.me/cap/pages": [
      { "access": "deploy" }
    ]
  }
}
```

**Give a team full admin access:**

Admins can create and delete sites, deploy to any site, and access the admin dashboard.

```json
{
  "src": ["group:engineering"],
  "dst": ["tag:pages"],
  "ip": ["443"],
  "app": {
    "tspages.mazetti.me/cap/pages": [
      { "access": "admin" }
    ]
  }
}
```

**Let a team manage specific sites:**

This grants admin over `docs` and `staging` only -- the team can create, delete, and deploy to those sites but not
others.

```json
{
  "src": ["group:docs-team"],
  "dst": ["tag:pages"],
  "ip": ["443"],
  "app": {
    "tspages.mazetti.me/cap/pages": [
      { "access": "admin", "sites": ["docs", "staging"] }
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
  "src": ["group:security"],
  "dst": ["tag:pages"],
  "ip": ["443"],
  "app": {
    "tspages.mazetti.me/cap/pages": [
      { "access": "view", "sites": ["security-reports"] }
    ]
  }
}
```

**Let a Prometheus server scrape metrics:**

A Prometheus node on your tailnet (`tag:monitoring`) gets access to `GET /metrics` only -- no site content, no deploy,
no admin.

```json
{
  "src": ["tag:monitoring"],
  "dst": ["tag:pages"],
  "ip": ["443"],
  "app": {
    "tspages.mazetti.me/cap/pages": [
      { "access": "metrics" }
    ]
  }
}
```
