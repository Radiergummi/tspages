---
description: Complete HTTP API reference for the tspages control plane.
category: reference
---

# API Reference

All API endpoints are on the control plane hostname (e.g., `pages.your-tailnet.ts.net`). Sites are
served on their own hostnames. Every endpoint that returns HTML also supports JSON via
`Accept: application/json` or a `.json` URL suffix (e.g., `/sites.json`).

## Deploy a site

```
POST /deploy/{site}
PUT  /deploy/{site}
PUT  /deploy/{site}/{filename}
```

Upload your site's build output. The format is auto-detected (see [Upload Formats](upload-formats)).
The `{filename}` variant is useful for format detection when uploading single files (e.g.,
`PUT /deploy/notes/README.md` triggers Markdown rendering).

Query parameters:

- `?activate=false` -- upload without switching live traffic (useful for staging)
- `?comment=...` -- attach a human-readable note to the deployment (falls back to `Comment` header)

Response:

```json
{
  "deployment_id": "a3f9c1e2",
  "site": "docs",
  "url": "https://docs.your-tailnet.ts.net/"
}
```

Requires `deploy` capability for the target site. If the site doesn't exist yet, it is created
automatically (requires `admin`).

Old deployments are auto-cleaned after each deploy, keeping the most recent `max_deployments`
(default 10). The active deployment is never removed.

## List deployments

```
GET /deploy/{site}
```

Returns a JSON array of deployments for a site, including which one is active.

Response:

```json
[
  {
    "id": "a3f9c1e2",
    "active": true,
    "created_at": "2025-03-02T10:30:45Z",
    "created_by": "Alice",
    "size_bytes": 1048576,
    "comment": "v2.1.0"
  },
  {
    "id": "b7d4e8f1",
    "active": false,
    "created_at": "2025-03-01T14:20:00Z",
    "created_by": "Bob",
    "size_bytes": 982315
  }
]
```

Fields with zero values (`failed`, `failed_reason`, `comment`, `created_by_avatar`) are omitted.

Requires `deploy` capability for the site.

## Activate a deployment

```
POST /deploy/{site}/{id}/activate
```

Switches live traffic to a specific deployment. Useful for rollbacks.

Response:

```json
{
  "id": "a3f9c1e2",
  "active": true,
  "created_at": "2025-03-02T10:30:45Z",
  "created_by": "Alice",
  "size_bytes": 1048576
}
```

Requires `deploy` capability for the site.

## Delete a deployment

```
DELETE /deploy/{site}/{id}
```

Removes a deployment's files. Cannot delete the currently active deployment -- activate a different
one first.

Returns `204 No Content` on success.

Requires `deploy` capability for the site.

## Delete all inactive deployments

```
DELETE /deploy/{site}/deployments
```

Removes all deployments except the currently active one.

Response:

```json
{
  "deleted": 3
}
```

Requires `deploy` capability for the site.

## Create a site

```
POST /sites
Content-Type: application/x-www-form-urlencoded
```

Creates an empty site directory. Body: `name=my-site`. Returns a redirect to `/sites/{name}` (or
JSON with `Accept: application/json`):

```json
{
  "name": "my-site"
}
```

Requires `admin` access for the site name.

## Delete a site

```
DELETE /deploy/{site}
```

Stops the site's server and removes all deployments.

Returns `204 No Content` on success.

Requires `admin` access for the site.

## Errors

When a request fails, the response body depends on the `Accept` header. With
`Accept: application/json` (or a `.json` URL suffix):

```json
{
  "error": "invalid site name"
}
```

Without it, errors are returned as plain text.

Common error codes:

| Status | Meaning |
| ------ | ------- |
| `400`  | Invalid site name, empty upload, upload too large, bad config |
| `403`  | Missing required capability for this action |
| `404`  | Site or deployment not found |
| `409`  | Cannot delete the active deployment; cannot activate a failed deployment |
| `413`  | Upload exceeds `max_upload_mb` |
| `500`  | Internal error (storage, server startup) |

## Admin dashboard

```
GET /sites                           # all sites
GET /sites/{site}                    # site detail (last 5 deployments)
GET /sites/{site}/deployments        # all deployments for a site (paginated)
GET /sites/{site}/deployments/{id}   # deployment detail with file listing and diff
GET /deployments                     # global deployment feed (paginated)
GET /analytics                       # cross-site analytics (admins)
GET /sites/{site}/analytics          # per-site analytics
```

The sites list is accessible to any authenticated user; admins see all sites, others see only sites
they have `view` or `deploy` access to. Deployment detail pages show a diff against the previous
deployment (added, removed, and changed files).

## Browse sites

Each site is served at the root of its own hostname:

```
https://docs.your-tailnet.ts.net/
https://demo.your-tailnet.ts.net/style.css
```

Directory paths serve `index.html` automatically (configurable via `index_page`). Responses include
ETags based on the deployment ID, so unchanged content returns `304 Not Modified` on repeat
requests.

Requires `view` capability for the site.
