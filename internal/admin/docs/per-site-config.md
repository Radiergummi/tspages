# Per-Site Configuration

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

## Fields

| Field            | Type                         | Default        | Description                                                               |
|------------------|------------------------------|----------------|---------------------------------------------------------------------------|
| `spa`            | `bool`                       | `false`        | When true, unresolved paths serve the index page instead of 404.          |
| `analytics`      | `bool`                       | `true`         | When false, disables analytics recording for this site.                   |
| `index_page`     | `string`                     | `"index.html"` | File served for directory paths.                                          |
| `not_found_page` | `string`                     | `"404.html"`   | Custom 404 page. Falls back to a built-in default if the file is missing. |
| `headers`        | `map[pattern]map[name]value` | --             | Custom response headers keyed by path pattern.                            |
| `redirects`      | `array`                      | --             | Redirect rules, evaluated first-match.                                    |

## Header patterns

| Pattern     | Matches                     |
|-------------|-----------------------------|
| `/*`        | All paths                   |
| `/*.css`    | Any path ending with `.css` |
| `/assets/*` | Any path under `/assets/`   |
| `/exact`    | Exactly `/exact`            |

## Redirect rules

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

## Merge with server defaults

The server config can define `[defaults]` with the same fields. Per-deployment values override defaults:

- `spa`, `analytics`: deployment value wins when set; `nil` inherits the default
- `index_page`, `not_found_page`: deployment value wins when non-empty
- `headers`: deployment path patterns overlay defaults per-path
- `redirects`: deployment value entirely replaces defaults (no merging)
