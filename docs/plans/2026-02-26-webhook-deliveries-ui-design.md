# Webhook Deliveries UI

## Routes

| Route | Access | Description |
|---|---|---|
| `GET /webhooks` | Admin only | Global delivery log, all sites |
| `GET /webhooks.json` | Admin only | JSON variant |
| `GET /sites/{site}/webhooks` | Site deployers | Per-site delivery log |
| `GET /sites/{site}/webhooks.json` | Site deployers | JSON variant |

## Site Detail Page (`/sites/{site}`)

New section below deployments: "Recent Webhook Deliveries" showing the last 5 deliveries for that site, with a "View all" link to `/sites/{site}/webhooks`.

## Full List View

Most recent first, paginated (50 per page). Each row:

- Event type badge
- Site name (global view only)
- HTTP status with color (green 2xx, red 4xx/5xx, gray for network error)
- Attempt count (e.g., "3/4")
- Timestamp (relative)
- Expand to show: full payload JSON, error message, webhook URL

## Filtering

Query params: `?event=deploy.success&status=failed`

- Event filter: dropdown with 4 event types
- Status filter: "All", "Succeeded" (any attempt got 2xx), "Failed" (no 2xx)

## Queries

Deliveries grouped by `webhook_id` for display (one row per event, not per attempt). Detail expansion shows all attempts for that `webhook_id`.

```sql
-- List distinct deliveries, most recent first
SELECT webhook_id, event, site, url,
       MAX(attempt) as attempts,
       MAX(CASE WHEN status BETWEEN 200 AND 299 THEN 1 ELSE 0 END) as succeeded,
       MIN(created_at) as first_attempt,
       MAX(created_at) as last_attempt
FROM webhook_deliveries
WHERE site = ?  -- omit for global
GROUP BY webhook_id
ORDER BY first_attempt DESC
LIMIT ? OFFSET ?

-- Detail: all attempts for one webhook_id
SELECT attempt, status, error, created_at, payload
FROM webhook_deliveries
WHERE webhook_id = ?
ORDER BY attempt
```

## Navigation

"Webhooks" tab in admin header nav (between Analytics and Help). Per-site link from site detail sub-nav alongside Deployments and Analytics.

## No Cleanup

Delivery records kept indefinitely. Revisit if storage becomes a concern.
