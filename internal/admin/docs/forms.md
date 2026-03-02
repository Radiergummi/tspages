---
description: Collect form submissions from static sites with storage, webhooks, and a dashboard.
category: reference
---

# Forms

tspages can accept HTML form submissions on any site. When enabled, POST requests are parsed,
stored in SQLite, and optionally forwarded via webhooks. All submitters are authenticated Tailscale
users -- no CAPTCHA or spam protection is needed.

## Enabling forms

Add a `[forms]` section to `tspages.toml` in your deployment archive:

```toml
[forms]
enabled = true
```

Or as a server-wide default:

```toml
[defaults.forms]
enabled = true
```

Once enabled, any POST request to the site is treated as a form submission. Standard HTML forms work
out of the box:

```html
<form action="/contact" method="POST">
  <input name="name" type="text" required>
  <input name="email" type="email" required>
  <textarea name="message" required></textarea>
  <button type="submit">Send</button>
</form>
```

When forms are not enabled, POST requests return 405 Method Not Allowed.

## Form metadata

By default, tspages infers display names from paths and field keys: `/contact` becomes "Contact",
`first_name` becomes "First Name", and `lastName` becomes "Last Name". You can override these with
explicit metadata:

```toml
[forms]
enabled = true

[[forms.endpoints]]
path = "/contact"
name = "Contact Form"
description = "General inquiries from the docs site"

[[forms.endpoints.fields]]
key = "name"
label = "Full Name"

[[forms.endpoints.fields]]
key = "email"
label = "Email Address"
type = "email"

[[forms.endpoints.fields]]
key = "message"
label = "Message"
type = "text"
```

Metadata is optional and purely for display in the admin dashboard. Paths without a
`[[forms.endpoints]]` entry still accept submissions -- they show with inferred names.

### Endpoint fields

| Field         | Type     | Description                              |
| ------------- | -------- | ---------------------------------------- |
| `path`        | `string` | Path that accepts submissions. Required. |
| `name`        | `string` | Display name in the dashboard.           |
| `description` | `string` | Short description shown in the dashboard.|

### Field descriptors

| Field       | Type     | Description                                                    |
| ----------- | -------- | -------------------------------------------------------------- |
| `key`       | `string` | Form field name (matches `<input name="...">` in HTML).        |
| `label`     | `string` | Display label in the dashboard (overrides automatic inference). |
| `type`      | `string` | Display hint: `text`, `email`, `number`, `url`, `tel`, `date`. |
| `aggregate` | `string` | Summary function for the table footer (see below).             |

Field `type` is a display hint only -- it does not enforce server-side validation.

### Column summary

The submissions table footer shows a summary statistic per column. By default, tspages auto-detects
the appropriate summary:

- **Numeric columns** (all values parse as numbers): shows `<min>–<max>, Ø <avg>`
- **Categorical columns** (some values repeat): shows the count of unique values
- **All-unique columns** (e.g., free-text messages): omitted (the count would equal the row count)

Set `aggregate` on a field descriptor to override auto-detection:

```toml
[[forms.endpoints.fields]]
key = "rating"
label = "Rating"
aggregate = "avg"
```

**General aggregates** (work on any value type):

| Value             | Output          | Description                    |
| ----------------- | --------------- | ------------------------------ |
| `unique`          | `3 unique`      | Count of distinct values       |
| `total`           | `5`             | Total number of submissions    |
| `count_empty`     | `2`             | Count of empty values          |
| `count_not_empty` | `5`             | Count of non-empty values      |
| `percent_empty`   | `29%`           | Percentage empty               |
| `percent_not_empty`| `71%`          | Percentage non-empty           |

**Numeric aggregates** (non-numeric values are skipped):

| Value    | Output     | Description                   |
| -------- | ---------- | ----------------------------- |
| `sum`    | `150`      | Sum of all values             |
| `avg`    | `Ø 4.2`   | Arithmetic mean               |
| `mean`   | `Ø 4.2`   | Alias for `avg`               |
| `median` | `4`        | Median value                  |
| `min`    | `1`        | Minimum value                 |
| `max`    | `10`       | Maximum value                 |
| `range`  | `1–10`     | Min and max                   |

## How submissions work

When a POST request arrives at a forms-enabled site:

1. **Body parsing** -- accepts `application/x-www-form-urlencoded` and `multipart/form-data` (text
   fields only; file uploads are ignored). Other content types return 415.
2. **Size limit** -- request bodies over 64 KB return 413.
3. **Storage** -- the submission is stored in the analytics SQLite database with the form fields,
   submitter identity, node info, and timestamp.
4. **Webhook** -- if a `webhook_url` is configured with the `form.submission` event, a webhook is
   fired (see below).
5. **Response** -- content-type aware:
   - Requests with `Accept: application/json` receive `{"ok": true, "id": "sub_..."}`.
   - All other requests receive a 303 redirect back to the form page (Post/Redirect/Get pattern).

### Submitter identity

Because all requests pass through Tailscale authentication, every submission automatically captures
the submitter's login name, node name, and IP address. No login forms or session management needed.

## Webhook notifications

Form submissions can trigger webhooks via the existing webhook system. Add `form.submission` to
your webhook events:

```toml
webhook_url = "https://example.com/hook"
webhook_events = ["deploy.success", "form.submission"]
webhook_secret = "whsec_your_base64_secret_here"
```

The payload follows the standard webhook format:

```json
{
  "type": "form.submission",
  "timestamp": "2026-03-02T12:00:00Z",
  "data": {
    "id": "sub_a1b2c3d4e5f67890",
    "site": "docs",
    "path": "/contact",
    "fields": {"name": "Alice", "message": "Hello"},
    "submitter": "alice@example.com",
    "node": "alice-laptop"
  }
}
```

See [Webhooks](webhooks) for details on delivery, retries, and signing.

## Viewing submissions

The admin dashboard shows form submissions per site at `/sites/{site}/submissions`. Submissions are
grouped by form path -- select a path to see its submissions in a transposed table:

- **Rows** are form field keys (with inferred or configured labels)
- **Columns** are individual submissions (most recent first, showing submitter and timestamp)
- Up to 20 submissions are shown; use **Export CSV** for the full dataset

The submissions page is accessible to users with the `deploy` capability for the site.

## Submissions API

All endpoints are on the control plane. Read operations require the `deploy` capability; delete
operations require `admin`.

### List submissions

```
GET /sites/{site}/submissions.json
```

Query parameters:

| Parameter | Type     | Default | Description                  |
| --------- | -------- | ------- | ---------------------------- |
| `path`    | `string` | --      | Filter by form path          |
| `since`   | `string` | --      | Lower bound (RFC 3339)       |
| `until`   | `string` | --      | Upper bound (RFC 3339)       |
| `limit`   | `int`    | `50`    | Max results (capped at 1000) |
| `offset`  | `int`    | `0`     | Pagination offset            |

Returns a JSON array of submissions, ordered newest first.

### Get a submission

```
GET /sites/{site}/submissions/{id}
```

Returns a single submission object, or 404 if not found.

### Delete a submission

```
DELETE /sites/{site}/submissions/{id}
```

Returns 204 No Content, or 404 if the submission does not exist. Requires `admin` capability.

### Bulk delete submissions

```
DELETE /sites/{site}/submissions?path=/contact&before=2026-01-01T00:00:00Z
```

Query parameters:

| Parameter | Type     | Description                                |
| --------- | -------- | ------------------------------------------ |
| `path`    | `string` | Only delete submissions from this path     |
| `before`  | `string` | Only delete submissions before this date   |

At least one filter (`path` or `before`) is required -- unfiltered bulk deletes are rejected with 400.

Returns `{"deleted": N}`. Requires `admin` capability.

An HTML form alias is available for browsers: `POST /sites/{site}/submissions/delete` (redirects
back to the submissions page).

### Export submissions

```
GET /sites/{site}/submissions/export?format=csv&path=/contact
```

Query parameters:

| Parameter | Type     | Default | Description                          |
| --------- | -------- | ------- | ------------------------------------ |
| `format`  | `string` | `json`  | Export format: `csv` or `json`       |
| `path`    | `string` | --      | Filter by form path                  |
| `since`   | `string` | --      | Lower bound (RFC 3339)               |
| `until`   | `string` | --      | Upper bound (RFC 3339)               |

CSV columns: `id`, `path`, `created_at`, `submitter`, `node`, `ip`, plus one column per unique
field key found across all matching submissions.

## Submission object

All API responses use this shape:

```json
{
  "id": "sub_a1b2c3d4e5f67890",
  "site": "docs",
  "path": "/contact",
  "fields": {"name": "Alice", "email": "alice@example.com", "message": "Hello"},
  "submitter": "alice@example.com",
  "node": "alice-laptop",
  "ip": "100.64.1.2",
  "created_at": "2026-03-02T12:00:00Z"
}
```

## JavaScript submissions

For single-page apps or dynamic forms, submit via `fetch`:

```javascript
const response = await fetch("/contact", {
  method: "POST",
  headers: {
    "Content-Type": "application/x-www-form-urlencoded",
    "Accept": "application/json",
  },
  body: new URLSearchParams({ name: "Alice", message: "Hello" }),
});
const { ok, id } = await response.json();
```

The `Accept: application/json` header tells tspages to return JSON instead of redirecting.

## Scope and limitations

- **No file uploads** -- multipart file fields are silently ignored; only text fields are stored.
- **No server-side validation** -- tspages stores whatever fields arrive. Validate on the client or
  in your webhook consumer.
- **No email delivery** -- use webhooks to forward submissions to email services, Slack, or any HTTP
  endpoint.
- **64 KB body limit** -- requests larger than 64 KB are rejected with 413.
- **Authenticated only** -- all submitters are Tailscale users. Public (Funnel) sites accept
  anonymous submissions with empty submitter fields.
