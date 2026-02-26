# API Reference

All API endpoints are on the control plane hostname (e.g., `pages.your-tailnet.ts.net`). Sites are served on their own
hostnames. Every endpoint that returns HTML also supports JSON via `Accept: application/json` or a `.json` URL suffix
(e.g., `/sites.json`).

## Deploy a site

```
POST /deploy/{site}
PUT  /deploy/{site}
PUT  /deploy/{site}/{filename}
```

Upload your site's build output. The format is auto-detected (see [Upload Formats](upload-formats)). The `{filename}`
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

## List deployments

```
GET /deploy/{site}
```

Returns a JSON array of deployments for a site, including which one is active.

Requires `deploy` capability for the site.

## Activate a deployment

```
POST /deploy/{site}/{id}/activate
```

Switches live traffic to a specific deployment. Useful for rollbacks.

Requires `deploy` capability for the site.

## Delete a deployment

```
DELETE /deploy/{site}/{id}
```

Removes a deployment's files. Cannot delete the currently active deployment -- activate a different one first.

Requires `deploy` capability for the site.

## Delete all inactive deployments

```
DELETE /deploy/{site}/deployments
```

Removes all deployments except the currently active one.

Requires `deploy` capability for the site.

## Create a site

```
POST /sites
Content-Type: application/x-www-form-urlencoded
```

Creates an empty site directory. Body: `name=my-site`. Returns a redirect to `/sites/{name}` (or JSON with
`Accept: application/json`).

Requires `admin` access for the site name.

## Delete a site

```
DELETE /deploy/{site}
```

Stops the site's server and removes all deployments.

Requires `admin` access for the site.

## Admin dashboard

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

## Prometheus metrics

```
GET /metrics
```

Returns metrics in Prometheus exposition format. Includes request counts, request durations, deployment counts, deployment
sizes, and active site count.

Requires `metrics` (or `admin`) capability.

## Browse sites

Each site is served at the root of its own hostname:

```
https://docs.your-tailnet.ts.net/
https://demo.your-tailnet.ts.net/style.css
```

Directory paths serve `index.html` automatically (configurable via `index_page`). Responses include ETags based on the
deployment ID, so unchanged content returns `304 Not Modified` on repeat requests.

Requires `view` capability for the site.
