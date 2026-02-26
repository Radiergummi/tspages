# Webhook Notifications Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fire webhook notifications on deploy success/failure and site create/delete events, with Standard Webhooks signing, retry logic, and a persistent delivery log.

**Architecture:** New `internal/webhook` package with a `Notifier` that receives events, resolves config (per-site overrides global), filters by event type, delivers via HTTP POST with retries, and logs each attempt to SQLite. Handlers call `notifier.Fire()` after operations complete. The notifier shares the analytics SQLite DB.

**Tech Stack:** Go stdlib `net/http`, `database/sql`, `modernc.org/sqlite`, `github.com/standard-webhooks/standard-webhooks/libraries/go`

---

### Task 1: Add webhook fields to SiteConfig

**Files:**
- Modify: `internal/storage/siteconfig.go`
- Modify: `internal/storage/siteconfig_test.go`

**Step 1: Write the failing test**

Add to `internal/storage/siteconfig_test.go`:

```go
func TestParseSiteConfig_Webhook(t *testing.T) {
	input := `
webhook_url = "https://example.com/hook"
webhook_events = ["deploy.success", "site.created"]
webhook_secret = "whsec_dGVzdHNlY3JldA=="
`
	cfg, err := ParseSiteConfig([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.WebhookURL != "https://example.com/hook" {
		t.Errorf("webhook_url = %q", cfg.WebhookURL)
	}
	if len(cfg.WebhookEvents) != 2 || cfg.WebhookEvents[0] != "deploy.success" {
		t.Errorf("webhook_events = %v", cfg.WebhookEvents)
	}
	if cfg.WebhookSecret != "whsec_dGVzdHNlY3JldA==" {
		t.Errorf("webhook_secret = %q", cfg.WebhookSecret)
	}
}

func TestSiteConfig_Validate_WebhookURL(t *testing.T) {
	cfg := SiteConfig{WebhookURL: "not-a-url"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for invalid webhook_url")
	}
}

func TestSiteConfig_Validate_WebhookEvents(t *testing.T) {
	cfg := SiteConfig{
		WebhookURL:    "https://example.com/hook",
		WebhookEvents: []string{"deploy.success", "invalid.event"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for invalid webhook event")
	}
}

func TestSiteConfig_Merge_Webhook(t *testing.T) {
	defaults := SiteConfig{
		WebhookURL:    "https://global.example.com/hook",
		WebhookEvents: []string{"deploy.success"},
	}
	site := SiteConfig{
		WebhookURL:    "https://site.example.com/hook",
		WebhookEvents: []string{"site.created"},
	}
	merged := site.Merge(defaults)
	if merged.WebhookURL != "https://site.example.com/hook" {
		t.Errorf("webhook_url = %q, want site override", merged.WebhookURL)
	}
	if len(merged.WebhookEvents) != 1 || merged.WebhookEvents[0] != "site.created" {
		t.Errorf("webhook_events = %v, want site override", merged.WebhookEvents)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/... -run TestParseSiteConfig_Webhook -v`
Expected: FAIL — fields don't exist yet.

**Step 3: Write minimal implementation**

In `internal/storage/siteconfig.go`, add fields to `SiteConfig`:

```go
type SiteConfig struct {
	// ... existing fields ...
	WebhookURL    string   `toml:"webhook_url"`
	WebhookEvents []string `toml:"webhook_events"`
	WebhookSecret string   `toml:"webhook_secret"`
}
```

Add validation in `Validate()`:

```go
if c.WebhookURL != "" {
	if _, err := url.Parse(c.WebhookURL); err != nil || (!strings.HasPrefix(c.WebhookURL, "http://") && !strings.HasPrefix(c.WebhookURL, "https://")) {
		return fmt.Errorf("webhook_url: must be a valid HTTP(S) URL")
	}
}
validEvents := map[string]bool{
	"deploy.success": true, "deploy.failed": true,
	"site.created": true, "site.deleted": true,
}
for _, e := range c.WebhookEvents {
	if !validEvents[e] {
		return fmt.Errorf("webhook_events: unknown event %q", e)
	}
}
```

Add merge logic in `Merge()`:

```go
if c.WebhookURL != "" {
	merged.WebhookURL = c.WebhookURL
	merged.WebhookEvents = c.WebhookEvents
	merged.WebhookSecret = c.WebhookSecret
}
```

Note: when per-site sets `webhook_url`, all three webhook fields replace global (not merge). This is the "override" behavior from the design.

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/storage/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/storage/siteconfig.go internal/storage/siteconfig_test.go
git commit -m "feat(webhook): add webhook config fields to SiteConfig"
```

---

### Task 2: Add standard-webhooks dependency

**Step 1: Add the dependency**

```bash
go get github.com/standard-webhooks/standard-webhooks/libraries/go
```

**Step 2: Verify it resolves**

```bash
go mod tidy
```

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "feat(webhook): add standard-webhooks dependency"
```

---

### Task 3: Create the webhook Notifier — core logic

**Files:**
- Create: `internal/webhook/webhook.go`
- Create: `internal/webhook/webhook_test.go`

**Step 1: Write the failing tests**

Create `internal/webhook/webhook_test.go`:

```go
package webhook

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"tspages/internal/storage"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNotifier_FiresWebhook(t *testing.T) {
	var called atomic.Int32
	var receivedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		receivedBody, _ = io.ReadAll(r.Body)
		// Check standard webhook headers present
		if r.Header.Get("webhook-id") == "" {
			t.Error("missing webhook-id header")
		}
		if r.Header.Get("webhook-timestamp") == "" {
			t.Error("missing webhook-timestamp header")
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	db := testDB(t)
	n, err := NewNotifier(db)
	if err != nil {
		t.Fatal(err)
	}

	cfg := storage.SiteConfig{WebhookURL: ts.URL}
	n.Fire("deploy.success", "docs", cfg, map[string]any{
		"deployment_id": "abc123",
	})

	// Wait for async delivery
	time.Sleep(500 * time.Millisecond)

	if called.Load() != 1 {
		t.Errorf("webhook called %d times, want 1", called.Load())
	}

	var payload map[string]any
	json.Unmarshal(receivedBody, &payload)
	if payload["type"] != "deploy.success" {
		t.Errorf("type = %v", payload["type"])
	}
}

func TestNotifier_RespectsEventFilter(t *testing.T) {
	var called atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(200)
	}))
	defer ts.Close()

	db := testDB(t)
	n, err := NewNotifier(db)
	if err != nil {
		t.Fatal(err)
	}

	cfg := storage.SiteConfig{
		WebhookURL:    ts.URL,
		WebhookEvents: []string{"site.created"},
	}
	n.Fire("deploy.success", "docs", cfg, nil)

	time.Sleep(500 * time.Millisecond)

	if called.Load() != 0 {
		t.Errorf("webhook called %d times, want 0 (filtered)", called.Load())
	}
}

func TestNotifier_NoURL_Noop(t *testing.T) {
	db := testDB(t)
	n, err := NewNotifier(db)
	if err != nil {
		t.Fatal(err)
	}

	// Should not panic or error
	cfg := storage.SiteConfig{}
	n.Fire("deploy.success", "docs", cfg, nil)
}

func TestNotifier_SignsWithSecret(t *testing.T) {
	var sigHeader string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sigHeader = r.Header.Get("webhook-signature")
		w.WriteHeader(200)
	}))
	defer ts.Close()

	db := testDB(t)
	n, err := NewNotifier(db)
	if err != nil {
		t.Fatal(err)
	}

	cfg := storage.SiteConfig{
		WebhookURL:    ts.URL,
		WebhookSecret: "whsec_dGVzdHNlY3JldHRlc3RzZWNyZXQ=",
	}
	n.Fire("deploy.success", "docs", cfg, nil)

	time.Sleep(500 * time.Millisecond)

	if sigHeader == "" {
		t.Error("expected webhook-signature header when secret is set")
	}
	if !strings.HasPrefix(sigHeader, "v1,") {
		t.Errorf("signature = %q, want v1,... prefix", sigHeader)
	}
}

func TestNotifier_NoSignatureWithoutSecret(t *testing.T) {
	var sigHeader string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sigHeader = r.Header.Get("webhook-signature")
		w.WriteHeader(200)
	}))
	defer ts.Close()

	db := testDB(t)
	n, err := NewNotifier(db)
	if err != nil {
		t.Fatal(err)
	}

	cfg := storage.SiteConfig{WebhookURL: ts.URL}
	n.Fire("deploy.success", "docs", cfg, nil)

	time.Sleep(500 * time.Millisecond)

	if sigHeader != "" {
		t.Errorf("expected no signature header, got %q", sigHeader)
	}
}

func TestNotifier_RetriesOnFailure(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	db := testDB(t)
	n, err := NewNotifier(db)
	if err != nil {
		t.Fatal(err)
	}
	n.retryDelays = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}

	cfg := storage.SiteConfig{WebhookURL: ts.URL}
	n.Fire("deploy.success", "docs", cfg, nil)

	time.Sleep(500 * time.Millisecond)

	if calls.Load() != 3 {
		t.Errorf("webhook called %d times, want 3 (2 failures + 1 success)", calls.Load())
	}
}

func TestNotifier_LogsDeliveries(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer ts.Close()

	db := testDB(t)
	n, err := NewNotifier(db)
	if err != nil {
		t.Fatal(err)
	}

	cfg := storage.SiteConfig{WebhookURL: ts.URL}
	n.Fire("deploy.success", "docs", cfg, nil)

	time.Sleep(500 * time.Millisecond)

	var count int
	db.QueryRow("SELECT COUNT(*) FROM webhook_deliveries").Scan(&count)
	if count != 1 {
		t.Errorf("delivery log has %d rows, want 1", count)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/webhook/... -v`
Expected: FAIL — package doesn't exist yet.

**Step 3: Write the implementation**

Create `internal/webhook/webhook.go`:

```go
package webhook

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"tspages/internal/storage"
)

var allEvents = []string{"deploy.success", "deploy.failed", "site.created", "site.deleted"}

// Notifier sends webhook notifications and logs delivery attempts.
type Notifier struct {
	db          *sql.DB
	client      *http.Client
	retryDelays []time.Duration
}

func NewNotifier(db *sql.DB) (*Notifier, error) {
	if err := migrate(db); err != nil {
		return nil, err
	}
	return &Notifier{
		db:     db,
		client: &http.Client{Timeout: 10 * time.Second},
		retryDelays: []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute},
	}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS webhook_deliveries (
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
		)`)
	return err
}

// Fire sends a webhook notification asynchronously.
// cfg is the resolved (merged) site config. data is event-specific payload fields.
func (n *Notifier) Fire(event, site string, cfg storage.SiteConfig, data map[string]any) {
	if cfg.WebhookURL == "" {
		return
	}

	// Check event filter.
	events := cfg.WebhookEvents
	if len(events) == 0 {
		events = allEvents
	}
	matched := false
	for _, e := range events {
		if e == event {
			matched = true
			break
		}
	}
	if !matched {
		return
	}

	go n.deliver(event, site, cfg, data)
}

func (n *Notifier) deliver(event, site string, cfg storage.SiteConfig, data map[string]any) {
	msgID := "msg_" + randomHex(16)
	now := time.Now()

	payload, _ := json.Marshal(map[string]any{
		"type":      event,
		"timestamp": now.UTC().Format(time.RFC3339),
		"data":      data,
	})

	maxAttempts := 1 + len(n.retryDelays)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		status, err := n.send(cfg.WebhookURL, cfg.WebhookSecret, msgID, now, payload)
		n.logDelivery(msgID, event, site, cfg.WebhookURL, string(payload), attempt, status, err)

		if err == nil && status >= 200 && status < 300 {
			return
		}

		if attempt < maxAttempts {
			time.Sleep(n.retryDelays[attempt-1])
		}
	}
}

func (n *Notifier) send(url, secret, msgID string, ts time.Time, payload []byte) (int, error) {
	req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", msgID)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(ts.Unix(), 10))

	if secret != "" {
		wh, err := standardwebhooks.NewWebhook(strings.TrimPrefix(secret, "whsec_"))
		if err == nil {
			sig, err := wh.Sign(msgID, ts, payload)
			if err == nil {
				req.Header.Set("webhook-signature", sig)
			}
		}
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, nil
}

func (n *Notifier) logDelivery(webhookID, event, site, url, payload string, attempt, status int, err error) {
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	_, dbErr := n.db.Exec(
		`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		webhookID, event, site, url, payload, attempt, status, errStr, time.Now().UTC().Format(time.RFC3339),
	)
	if dbErr != nil {
		log.Printf("webhook: log delivery: %v", dbErr)
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/webhook/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/webhook/
git commit -m "feat(webhook): add Notifier with signing, retries, and delivery log"
```

---

### Task 4: Wire Notifier into main.go and handlers

**Files:**
- Modify: `cmd/tspages/main.go`
- Modify: `internal/deploy/handler.go`
- Modify: `internal/deploy/manage.go`
- Modify: `internal/admin/handler.go`

**Step 1: Create the Notifier in main.go**

In `cmd/tspages/main.go`, after the `recorder` is created:

```go
import "tspages/internal/webhook"

// ... after recorder creation ...
notifier, err := webhook.NewNotifier(recorder.DB())
if err != nil {
	log.Fatalf("creating webhook notifier: %v", err)
}
```

This requires exposing the `*sql.DB` from the Recorder. Add a method to `internal/analytics/recorder.go`:

```go
func (r *Recorder) DB() *sql.DB { return r.db }
```

**Step 2: Add Notifier to deploy handler**

In `internal/deploy/handler.go`, add `notifier` field to `Handler` and update constructor. After the successful deploy response (line ~215), fire the webhook:

```go
// After metrics.CountDeploy and before writeJSON:
if n.notifier != nil {
	resolvedCfg := siteCfg.Merge(n.defaults)
	n.notifier.Fire("deploy.success", site, resolvedCfg, map[string]any{
		"site":          site,
		"deployment_id": id,
		"created_by":    deployedBy,
		"url":           resp.URL,
		"size_bytes":    len(body),
	})
}
```

On deploy failure (extraction error), fire `deploy.failed`:

```go
if n.notifier != nil {
	resolvedCfg := storage.SiteConfig{}.Merge(n.defaults) // no per-site config available on failure
	n.notifier.Fire("deploy.failed", site, resolvedCfg, map[string]any{
		"site":  site,
		"error": err.Error(),
	})
}
```

The Handler needs `defaults storage.SiteConfig` and `notifier *webhook.Notifier` fields, plus updated constructor.

**Step 3: Add Notifier to site delete handler**

In `internal/deploy/manage.go` `DeleteHandler`, after successful deletion:

```go
if h.notifier != nil {
	resolvedCfg := storage.SiteConfig{}.Merge(h.defaults)
	h.notifier.Fire("site.deleted", site, resolvedCfg, map[string]any{
		"site":       site,
		"created_by": identity.DisplayName,
	})
}
```

The DeleteHandler needs `notifier`, `defaults` fields and the identity from context.

**Step 4: Add Notifier to site create handler**

In `internal/admin/handler.go` `CreateSiteHandler`, after successful creation:

```go
if h.notifier != nil {
	identity := auth.IdentityFromContext(r.Context())
	resolvedCfg := storage.SiteConfig{}.Merge(h.defaults)
	h.notifier.Fire("site.created", name, resolvedCfg, map[string]any{
		"site":       name,
		"created_by": identity.DisplayName,
	})
}
```

**Step 5: Update constructors in main.go to pass notifier and defaults**

Pass `notifier` and `cfg.Defaults` to the handlers that need them.

**Step 6: Run all tests**

Run: `go test ./... -v`
Expected: PASS — existing tests pass (they don't set a notifier, so it's nil → no-op).

**Step 7: Verify it builds**

Run: `go build ./cmd/tspages`
Expected: Compiles cleanly.

**Step 8: Commit**

```bash
git add cmd/tspages/main.go internal/deploy/handler.go internal/deploy/manage.go internal/admin/handler.go internal/analytics/recorder.go
git commit -m "feat(webhook): wire Notifier into deploy and admin handlers"
```

---

### Task 5: Update existing tests and add integration test

**Files:**
- Modify: `internal/deploy/handler_test.go`
- Modify: `internal/deploy/manage_test.go`

**Step 1: Update deploy handler tests**

The deploy handler constructor changed (new `notifier` and `defaults` params). Update all `NewHandler(...)` calls in test files to pass `nil` for notifier and `storage.SiteConfig{}` for defaults.

**Step 2: Update manage.go tests**

Same — update `NewDeleteHandler`, etc. constructor calls.

**Step 3: Run all tests**

Run: `go test ./... -v`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/deploy/handler_test.go internal/deploy/manage_test.go
git commit -m "test(webhook): update handler tests for new constructor params"
```

---

### Task 6: Add webhook config to admin UI

**Files:**
- Modify: `internal/admin/templates/site.gohtml`

**Step 1: Add webhook config display**

In the site config panel in `site.gohtml`, after the trailing slash section, add:

```html
{{if .Config.WebhookURL}}
    <div class="flex items-center justify-between px-5 py-3">
        <span class="text-sm text-muted">Webhook URL</span>
        <span class="text-sm font-mono truncate max-w-[300px]">{{.Config.WebhookURL}}</span>
    </div>
{{end}}
```

**Step 2: Verify it renders**

Run: `go test ./internal/admin/... -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/admin/templates/site.gohtml
git commit -m "feat(webhook): display webhook config in site detail UI"
```
