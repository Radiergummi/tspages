# Redirect Rules Design

## Problem

Static sites frequently need redirects — renamed pages, moved directories, vanity URLs. Currently tspages has no redirect support; deployers must restructure content or use client-side redirects.

## Design

### Config format

In `tspages.toml`:

```toml
[[redirects]]
from = "/old-page"
to = "/new-page"
status = 301

[[redirects]]
from = "/blog/:slug"
to = "/posts/:slug"

[[redirects]]
from = "/docs/*"
to = "/v2/docs/*"
status = 302
```

### Pattern types

Three pattern forms, consistent with the existing header pattern style:

- **Exact**: `/old-page` matches only that path
- **Named params**: `/blog/:slug` captures a single path segment, substituted into `to`
- **Splat**: `/docs/*` captures the remainder of the path, substituted into `to`'s `*`

### Behavior

- `status` defaults to 301 if omitted. Only 301 and 302 are valid.
- `to` may start with `/` (internal) or be a full URL (external redirect).
- Redirect matching runs in `serve.Handler.ServeHTTP` **before** path resolution, right after config loading. First match wins (order matters).
- Named params in `to` must exist in `from`. Splat in `to` requires splat in `from`.

### Storage

- `Redirects []RedirectRule` added to `SiteConfig` struct.
- `RedirectRule` has `From`, `To`, `Status` fields.
- Stored in `config.toml` alongside existing fields.

### Merge strategy

Deployment redirects completely replace default redirects (not element-by-element merge).

### Validation

- `from` must start with `/`
- `to` must start with `/` or be a full URL (`http://` or `https://`)
- `status` must be 301 or 302 (or 0 for default 301)
- Named params in `to` must appear in `from`
- No duplicate `from` patterns

### What's not included

- Custom headers are already implemented (existing `[headers]` config).
- Regex patterns — named params and splats cover the common cases without regex complexity.
- Query string matching — redirects match on path only.
