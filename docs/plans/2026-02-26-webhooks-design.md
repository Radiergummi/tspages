# Webhook Notifications

## Configuration

```toml
# Server-level (tspages.toml) — global defaults
webhook_url = "https://hooks.slack.com/services/T00/B00/xxx"
webhook_events = ["deploy.success", "deploy.failed", "site.created", "site.deleted"]
webhook_secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"  # optional
```

```toml
# Per-site (deployment tspages.toml) — overrides global entirely
webhook_url = "https://my-endpoint.example.com/hook"
webhook_events = ["deploy.success"]
webhook_secret = "whsec_..."
```

- `webhook_events` defaults to all 4 events when omitted.
- Per-site config replaces global (not merged).
- If `webhook_secret` is omitted, no signature headers are sent.

## Events

| Event            | Trigger                                        |
| ---------------- | ---------------------------------------------- |
| `deploy.success` | Deployment uploaded and activated/uploaded      |
| `deploy.failed`  | Deployment upload or extraction fails           |
| `site.created`   | New site created via admin                      |
| `site.deleted`   | Site deleted via admin                          |

## Payload

POST with `Content-Type: application/json`:

```json
{
  "type": "deploy.success",
  "timestamp": "2026-02-26T14:30:00Z",
  "data": {
    "site": "docs",
    "deployment_id": "a1b2c3d4",
    "created_by": "alice@example.com",
    "url": "https://docs.mynet.ts.net",
    "size_bytes": 245760
  }
}
```

`data` fields vary by event. `site.created`/`site.deleted` only include `site` and `created_by`.

## Standard Webhooks Headers

Every delivery includes:

- `webhook-id` — unique message ID (`msg_<random>`)
- `webhook-timestamp` — Unix timestamp (seconds)

When `webhook_secret` is configured:

- `webhook-signature` — `v1,<base64(HMAC-SHA256(...))>`

Signature computed over `{webhook-id}.{webhook-timestamp}.{raw_body_bytes}` per the Standard Webhooks spec. Uses `github.com/standard-webhooks/standard-webhooks/libraries/go` for signing.

## Delivery

- Non-blocking: fires in a background goroutine.
- Retries up to 3 times on non-2xx: delays 5s, 30s, 120s.
- Each attempt logged to SQLite.

## Delivery Log

New table in `analytics.db`:

```sql
CREATE TABLE webhook_deliveries (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    webhook_id TEXT NOT NULL,
    event      TEXT NOT NULL,
    site       TEXT NOT NULL,
    url        TEXT NOT NULL,
    payload    TEXT NOT NULL,
    attempt    INTEGER NOT NULL,
    status     INTEGER,
    error      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
```

## Package Structure

New package `internal/webhook/`:

- `webhook.go` — `Notifier` struct with `Fire(event, site, data)`. Resolves config (per-site overrides global), checks event filter, serializes payload, delivers with retries, logs to DB.
- `webhook_test.go` — tests with `httptest.Server`.

Call sites: deploy handler (success/failure), admin handler (site create/delete).

## Config Changes

Add to `SiteConfig` in `internal/storage/siteconfig.go`:

```go
WebhookURL    string   `toml:"webhook_url"`
WebhookEvents []string `toml:"webhook_events"`
WebhookSecret string   `toml:"webhook_secret"`
```

Add to server-level config in `config/config.go` under `[defaults]`.
