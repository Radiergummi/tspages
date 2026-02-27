# Webhooks

tspages can send HTTP notifications when deploy and site events occur. Webhooks are configured per-site (or globally
via server defaults) and follow the [Standard Webhooks](https://www.standardwebhooks.com/) specification.

## Configuration

Add webhook fields to `tspages.toml` in your deployment archive:

```toml
webhook_url = "https://example.com/hook"
webhook_events = ["deploy.success", "deploy.failed"]
webhook_secret = "whsec_your_base64_secret_here"
```

Or set them as server-wide defaults:

```toml
[defaults]
webhook_url = "https://example.com/hook"
webhook_events = ["deploy.success"]
webhook_secret = "whsec_your_base64_secret_here"
```

Per-site values override defaults entirely -- if a deployment sets `webhook_url`, all three fields (URL, events, secret)
come from the deployment config.

See [Per-Site Configuration](per-site-config) and [Configuration](configuration) for details on config merging.

## Fields

| Field            | Type       | Default | Description                                              |
|------------------|------------|---------|----------------------------------------------------------|
| `webhook_url`    | `string`   | --      | HTTPS endpoint to receive notifications. Must be `http://` or `https://`. |
| `webhook_events` | `string[]` | all     | Events to send. When empty, all events fire.             |
| `webhook_secret` | `string`   | --      | Signing secret (Standard Webhooks format, e.g. `whsec_...`). |

## Events

| Event            | Fired when                             | Data fields                                        |
|------------------|----------------------------------------|----------------------------------------------------|
| `deploy.success` | A deployment completes and is activated | `site`, `deployment_id`, `created_by`, `url`, `size_bytes` |
| `deploy.failed`  | A deployment fails                     | `site`, `error`                                    |
| `site.created`   | A new site is created                  | `site`, `created_by`                               |
| `site.deleted`   | A site is deleted                      | `site`, `deleted_by`                               |

## Payload format

Each delivery sends a JSON POST with three Standard Webhooks headers:

- `webhook-id` -- unique message ID (`msg_` prefix)
- `webhook-timestamp` -- Unix timestamp
- `webhook-signature` -- HMAC signature (only when `webhook_secret` is set)

```json
{
  "type": "deploy.success",
  "timestamp": "2025-01-15T10:30:00Z",
  "data": {
    "site": "docs",
    "deployment_id": "a3f9c1e2",
    "created_by": "alice@example.com",
    "url": "https://docs.tailnet.ts.net",
    "size_bytes": 1048576
  }
}
```

## Retries

Failed deliveries (non-2xx responses or network errors) are retried up to 3 times with increasing delays: 5 seconds,
30 seconds, 2 minutes. Receivers returning 406 (Not Acceptable) are not retried.

## Viewing deliveries

Admins can view webhook delivery history in the dashboard:

- **All deliveries**: `GET /webhooks` -- filterable by event type and status
- **Per-site**: `GET /sites/{site}/webhooks`
- **Single delivery**: `GET /webhooks/{id}` -- shows all attempts with status codes, errors, and payload

## Retrying a delivery

On the webhook detail page (`/webhooks/{id}`), admins can click **Retry** to re-send the original payload. This records
a new attempt under the same webhook ID. The retry uses the current site config for signing, so a rotated
`webhook_secret` will be used for the new attempt.

Programmatically:

```
POST /webhooks/{id}/retry
```

Returns a redirect (HTML) or `{"status": N}` (JSON).

## Security

- Webhook URLs are validated to require `http://` or `https://` schemes
- Connections to private/internal IP ranges (loopback, RFC 1918, link-local, CGNAT) are blocked
- Request timeouts: 5s dial, 10s total
- Signing uses the Standard Webhooks HMAC-SHA256 scheme -- verify with any
  [Standard Webhooks library](https://www.standardwebhooks.com/)
