# Webhook Deliveries UI Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Surface webhook delivery history in the admin UI with global and per-site views, filtering, pagination, and a recent deliveries section on the site detail page.

**Architecture:** Add query methods to `internal/webhook` for listing/filtering deliveries. Add two new handlers (`WebhooksHandler`, `SiteWebhooksHandler`) following existing admin handler patterns. Create a new `webhooks.gohtml` template. Extend the site detail handler and template with a recent deliveries section.

**Tech Stack:** Go templates, SQLite queries, existing Tailwind UI patterns

---

### Task 1: Add delivery query methods to webhook package

**Files:**
- Modify: `internal/webhook/webhook.go`
- Modify: `internal/webhook/webhook_test.go`

**Step 1: Write failing tests**

Add to `internal/webhook/webhook_test.go`:

```go
func TestNotifier_ListDeliveries(t *testing.T) {
	n, db := testNotifier(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer ts.Close()

	cfg := storage.SiteConfig{WebhookURL: ts.URL}
	n.Fire("deploy.success", "docs", cfg, map[string]any{"site": "docs"})
	n.Fire("site.created", "blog", cfg, map[string]any{"site": "blog"})
	time.Sleep(500 * time.Millisecond)

	// List all
	deliveries, total, err := n.ListDeliveries("", "", "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(deliveries) != 2 {
		t.Errorf("len = %d, want 2", len(deliveries))
	}

	// Filter by site
	deliveries, total, err = n.ListDeliveries("docs", "", "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}

	// Filter by event
	deliveries, total, err = n.ListDeliveries("", "site.created", "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
}

func TestNotifier_ListDeliveries_StatusFilter(t *testing.T) {
	n, db := testNotifier(t)
	// Insert test data directly
	db.Exec(`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
		VALUES ('msg_1', 'deploy.success', 'docs', 'http://x', '{}', 1, 200, '', '2026-01-01T00:00:00Z')`)
	db.Exec(`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
		VALUES ('msg_2', 'deploy.failed', 'docs', 'http://x', '{}', 1, 500, 'err', '2026-01-01T00:01:00Z')`)
	db.Exec(`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
		VALUES ('msg_2', 'deploy.failed', 'docs', 'http://x', '{}', 2, 500, 'err', '2026-01-01T00:02:00Z')`)

	// Filter succeeded
	deliveries, total, err := n.ListDeliveries("", "", "succeeded", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}

	// Filter failed
	deliveries, total, err = n.ListDeliveries("", "", "failed", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
}

func TestNotifier_GetDeliveryAttempts(t *testing.T) {
	n, db := testNotifier(t)
	db.Exec(`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
		VALUES ('msg_1', 'deploy.success', 'docs', 'http://x', '{"type":"deploy.success"}', 1, 500, 'timeout', '2026-01-01T00:00:00Z')`)
	db.Exec(`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
		VALUES ('msg_1', 'deploy.success', 'docs', 'http://x', '{"type":"deploy.success"}', 2, 200, '', '2026-01-01T00:00:05Z')`)

	attempts, err := n.GetDeliveryAttempts("msg_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 {
		t.Errorf("len = %d, want 2", len(attempts))
	}
	if attempts[0].Attempt != 1 || attempts[1].Attempt != 2 {
		t.Error("attempts not in order")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/webhook/... -run "ListDeliveries|GetDeliveryAttempts" -v`
Expected: FAIL

**Step 3: Implement query methods**

Add to `internal/webhook/webhook.go`:

```go
// DeliverySummary represents a grouped webhook delivery (one row per webhook_id).
type DeliverySummary struct {
	WebhookID    string `json:"webhook_id"`
	Event        string `json:"event"`
	Site         string `json:"site"`
	URL          string `json:"url"`
	Attempts     int    `json:"attempts"`
	Succeeded    bool   `json:"succeeded"`
	FirstAttempt string `json:"first_attempt"`
	LastAttempt  string `json:"last_attempt"`
}

// DeliveryAttempt represents a single delivery attempt.
type DeliveryAttempt struct {
	Attempt   int    `json:"attempt"`
	Status    int    `json:"status"`
	Error     string `json:"error"`
	CreatedAt string `json:"created_at"`
	Payload   string `json:"payload"`
}

// ListDeliveries returns grouped deliveries, most recent first.
// Filters: site (empty = all), event (empty = all), status ("succeeded"/"failed"/empty = all).
// Returns results and total count for pagination.
func (n *Notifier) ListDeliveries(site, event, status string, limit, offset int) ([]DeliverySummary, int, error) {
	// Build WHERE clauses for the inner grouped query
	var conditions []string
	var args []any

	if site != "" {
		conditions = append(conditions, "site = ?")
		args = append(args, site)
	}
	if event != "" {
		conditions = append(conditions, "event = ?")
		args = append(args, event)
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Status filter applies to the grouped result via HAVING
	having := ""
	switch status {
	case "succeeded":
		having = "HAVING succeeded = 1"
	case "failed":
		having = "HAVING succeeded = 0"
	}

	// Count total
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM (
		SELECT webhook_id, MAX(CASE WHEN status BETWEEN 200 AND 299 THEN 1 ELSE 0 END) as succeeded
		FROM webhook_deliveries %s GROUP BY webhook_id %s
	)`, where, having)
	var total int
	if err := n.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Fetch page
	query := fmt.Sprintf(`SELECT webhook_id, event, site, url,
		MAX(attempt) as attempts,
		MAX(CASE WHEN status BETWEEN 200 AND 299 THEN 1 ELSE 0 END) as succeeded,
		MIN(created_at) as first_attempt,
		MAX(created_at) as last_attempt
		FROM webhook_deliveries %s
		GROUP BY webhook_id %s
		ORDER BY first_attempt DESC
		LIMIT ? OFFSET ?`, where, having)

	pageArgs := append(append([]any{}, args...), limit, offset)
	rows, err := n.db.Query(query, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var results []DeliverySummary
	for rows.Next() {
		var d DeliverySummary
		var succeeded int
		if err := rows.Scan(&d.WebhookID, &d.Event, &d.Site, &d.URL, &d.Attempts, &succeeded, &d.FirstAttempt, &d.LastAttempt); err != nil {
			return nil, 0, err
		}
		d.Succeeded = succeeded == 1
		results = append(results, d)
	}
	return results, total, rows.Err()
}

// GetDeliveryAttempts returns all attempts for a given webhook_id, ordered by attempt number.
func (n *Notifier) GetDeliveryAttempts(webhookID string) ([]DeliveryAttempt, error) {
	rows, err := n.db.Query(
		`SELECT attempt, status, error, created_at, payload FROM webhook_deliveries WHERE webhook_id = ? ORDER BY attempt`,
		webhookID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []DeliveryAttempt
	for rows.Next() {
		var a DeliveryAttempt
		if err := rows.Scan(&a.Attempt, &a.Status, &a.Error, &a.CreatedAt, &a.Payload); err != nil {
			return nil, err
		}
		results = append(results, a)
	}
	return results, rows.Err()
}
```

**Step 4: Run tests**

Run: `go test ./internal/webhook/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/webhook/
git commit -m "feat(webhook): add delivery query methods for listing and filtering"
```

---

### Task 2: Create webhooks template

**Files:**
- Create: `internal/admin/templates/webhooks.gohtml`

**Step 1: Create the template**

Create `internal/admin/templates/webhooks.gohtml` following the pattern of `deployments.gohtml`. It should:

- Show a table with columns: Event, Site (only in global view), Status, Attempts, URL, Time
- Event column: badge with event name
- Status column: green "succeeded" or red "failed" badge
- Site column: link to `/sites/{site}` (only shown when `.Global` is true)
- URL column: truncated, monospace
- Time column: relative time with absolute tooltip
- Pagination controls matching deployments template pattern
- Filter bar at the top with event dropdown and status dropdown
- Empty state message when no deliveries

The template data struct will have:
```go
struct {
    Deliveries []webhook.DeliverySummary
    Page       int
    TotalPages int
    Site       string // empty for global view
    Global     bool
    Event      string // current filter
    Status     string // current filter
    User       UserInfo
}
```

Use the same Tailwind classes and layout patterns as `deployments.gohtml`. The filter bar should be a `<form>` with `method="GET"` and two `<select>` elements that auto-submit on change via a tiny inline script.

**Step 2: Verify template compiles**

Run: `go build ./cmd/tspages`
Expected: won't fully test until handler registers it, but template syntax should be valid.

**Step 3: Commit**

```bash
git add internal/admin/templates/webhooks.gohtml
git commit -m "feat(webhook): add webhooks delivery log template"
```

---

### Task 3: Add webhook handlers and routes

**Files:**
- Modify: `internal/admin/handler.go`
- Modify: `internal/admin/render.go`
- Modify: `cmd/tspages/main.go`

**Step 1: Register the template**

In `internal/admin/render.go`, add alongside the other template vars:

```go
webhooksTmpl = newTmpl("templates/layout.gohtml", "templates/webhooks.gohtml")
```

**Step 2: Add the handlers**

Add to `internal/admin/handler.go`:

```go
// WebhooksHandler handles GET /webhooks (global, admin only).
type WebhooksHandler struct {
    handlerDeps
    notifier *webhook.Notifier
}

const webhooksPageSize = 50

func (h *WebhooksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    caps := auth.CapsFromContext(r.Context())
    identity := auth.IdentityFromContext(r.Context())
    if !auth.IsAdmin(caps) {
        RenderError(w, r, http.StatusForbidden, "forbidden")
        return
    }

    event := r.URL.Query().Get("event")
    status := r.URL.Query().Get("status")
    page := 1
    if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
        page = p
    }
    offset := (page - 1) * webhooksPageSize

    deliveries, total, err := h.notifier.ListDeliveries("", event, status, webhooksPageSize, offset)
    if err != nil {
        RenderError(w, r, http.StatusInternalServerError, "listing deliveries")
        return
    }

    totalPages := (total + webhooksPageSize - 1) / webhooksPageSize
    if totalPages == 0 { totalPages = 1 }

    // ... render (JSON or HTML) ...
}

// SiteWebhooksHandler handles GET /sites/{site}/webhooks (per-site).
type SiteWebhooksHandler struct {
    handlerDeps
    notifier *webhook.Notifier
}

func (h *SiteWebhooksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    siteName := trimSuffix(r.PathValue("site"))
    // ... validate site, check caps (CanView or CanDeploy) ...
    // ... same pagination/filter logic but pass siteName to ListDeliveries ...
}
```

**Step 3: Register in Handlers struct and NewHandlers**

Add to the `Handlers` struct:

```go
Webhooks     *WebhooksHandler
SiteWebhooks *SiteWebhooksHandler
```

Initialize in `NewHandlers`.

**Step 4: Wire routes in main.go**

Add to the mux:

```go
mux.Handle("GET /webhooks", withAuth(h.Webhooks))
mux.Handle("GET /webhooks.json", withAuth(h.Webhooks))
mux.Handle("GET /sites/{site}/webhooks", withAuth(h.SiteWebhooks))
mux.Handle("GET /sites/{site}/webhooks.json", withAuth(h.SiteWebhooks))
```

**Step 5: Run tests and build**

Run: `go test ./... && go build ./cmd/tspages`
Expected: PASS + compiles

**Step 6: Commit**

```bash
git add internal/admin/handler.go internal/admin/render.go cmd/tspages/main.go
git commit -m "feat(webhook): add webhook delivery log handlers and routes"
```

---

### Task 4: Add "Webhooks" to nav and recent deliveries to site detail

**Files:**
- Modify: `internal/admin/templates/layout.gohtml`
- Modify: `internal/admin/templates/site.gohtml`
- Modify: `internal/admin/handler.go` (SiteHandler)

**Step 1: Add nav link**

In `internal/admin/templates/layout.gohtml`, add a "Webhooks" nav link between Analytics and API, following the exact same HTML pattern:

```html
<a
    class="flex items-center px-4 text-sm font-medium border-b-2 no-underline transition-colors
    text-muted border-transparent hover:text-black dark:hover:text-base-200
    aria-[current=page]:text-blue-500 aria-[current=page]:border-b-blue-500"
    href="/webhooks"
    {{if eq (nav) "webhooks"}}aria-current="page"{{end}}>
    Webhooks
</a>
```

**Step 2: Add recent deliveries to site detail handler**

In `internal/admin/handler.go` `SiteHandler.ServeHTTP`, after fetching deployments, query recent webhook deliveries:

```go
var recentDeliveries []webhook.DeliverySummary
if h.notifier != nil {
    recentDeliveries, _, _ = h.notifier.ListDeliveries(siteName, "", "", 5, 0)
}
```

The `SiteHandler` needs a `notifier *webhook.Notifier` field (or access via `handlerDeps` — choose the simplest approach matching the codebase pattern).

Pass `recentDeliveries` to the template data struct.

**Step 3: Add recent deliveries section to site template**

In `internal/admin/templates/site.gohtml`, after the deployments table section (after the `</section>` that closes the deployments section), add:

```html
{{if .RecentDeliveries}}
    <section>
        <header class="flex items-center mb-4 gap-4">
            <h2 class="text-sm font-semibold uppercase tracking-wide text-muted flex items-center gap-2 me-auto">
                Recent Webhook Deliveries
                <span class="inline-block text-xs font-semibold px-2 py-0.5 rounded-full bg-base-500/10 text-muted">{{len .RecentDeliveries}}</span>
            </h2>
            <a href="/sites/{{.Site.Name}}/webhooks" class="text-sm text-blue-500 no-underline hover:underline">View all</a>
        </header>

        <table class="w-full border-collapse rounded-md overflow-hidden bg-surface">
            <thead>
            <tr>
                <th class="text-left px-4 py-3 text-xs uppercase tracking-wider text-muted font-medium border-b-2 border-paper dark:border-base-950">Event</th>
                <th class="text-left px-4 py-3 text-xs uppercase tracking-wider text-muted font-medium border-b-2 border-paper dark:border-base-950">Status</th>
                <th class="text-left px-4 py-3 text-xs uppercase tracking-wider text-muted font-medium border-b-2 border-paper dark:border-base-950">URL</th>
                <th class="text-left px-4 py-3 text-xs uppercase tracking-wider text-muted font-medium border-b-2 border-paper dark:border-base-950">Time</th>
            </tr>
            </thead>
            <tbody class="[&>tr:last-child>td]:border-b-0">
            {{range .RecentDeliveries}}
                <tr>
                    <td class="px-4 py-3 text-sm border-b border-paper dark:border-base-950">
                        <span class="inline-block text-xs font-semibold px-2 py-0.5 rounded-full bg-base-500/10 text-muted">{{.Event}}</span>
                    </td>
                    <td class="px-4 py-3 text-sm border-b border-paper dark:border-base-950">
                        {{if .Succeeded}}
                            <span class="inline-block text-xs font-semibold uppercase tracking-wide px-2 py-0.5 rounded-full bg-green-500/10 text-green-600 dark:text-green-400">ok</span>
                        {{else}}
                            <span class="inline-block text-xs font-semibold uppercase tracking-wide px-2 py-0.5 rounded-full bg-red-500/10 text-red-600 dark:text-red-400">failed</span>
                        {{end}}
                    </td>
                    <td class="px-4 py-3 text-sm border-b border-paper dark:border-base-950 font-mono text-muted truncate max-w-[200px]" title="{{.URL}}">{{.URL}}</td>
                    <td class="px-4 py-3 text-sm border-b border-paper dark:border-base-950 text-muted">{{.FirstAttempt}}</td>
                </tr>
            {{end}}
            </tbody>
        </table>
    </section>
{{end}}
```

**Step 4: Run tests and build**

Run: `go test ./... && go build ./cmd/tspages`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/admin/templates/layout.gohtml internal/admin/templates/site.gohtml internal/admin/handler.go
git commit -m "feat(webhook): add nav link and recent deliveries to site detail"
```
