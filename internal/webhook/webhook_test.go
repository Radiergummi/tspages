package webhook

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tspages/internal/storage"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// testNotifier creates a Notifier with a plain HTTP client (no private-IP
// restriction) so that tests using httptest servers on localhost work.
func testNotifier(t *testing.T) (*Notifier, *sql.DB) {
	t.Helper()
	db := testDB(t)
	n, err := NewNotifier(db)
	if err != nil {
		t.Fatal(err)
	}
	n.client = &http.Client{Timeout: 10 * time.Second}
	return n, db
}

func TestNotifier_FiresWebhook(t *testing.T) {
	var called atomic.Int32
	var gotBody []byte
	var gotHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n, _ := testNotifier(t)

	cfg := storage.SiteConfig{WebhookURL: srv.URL}
	n.Fire("deploy.success", "mysite", cfg, map[string]any{"id": "abc123"})

	time.Sleep(500 * time.Millisecond)

	if called.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", called.Load())
	}

	var payload map[string]any
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["type"] != "deploy.success" {
		t.Errorf("type = %v, want deploy.success", payload["type"])
	}
	if payload["timestamp"] == nil {
		t.Error("missing timestamp")
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatal("data is not a map")
	}
	if data["id"] != "abc123" {
		t.Errorf("data.id = %v, want abc123", data["id"])
	}

	if gotHeaders.Get("webhook-id") == "" {
		t.Error("missing webhook-id header")
	}
	if gotHeaders.Get("webhook-timestamp") == "" {
		t.Error("missing webhook-timestamp header")
	}
}

func TestNotifier_RespectsEventFilter(t *testing.T) {
	var called atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n, _ := testNotifier(t)

	cfg := storage.SiteConfig{
		WebhookURL:    srv.URL,
		WebhookEvents: []string{"site.created"},
	}
	n.Fire("deploy.success", "mysite", cfg, nil)

	time.Sleep(500 * time.Millisecond)

	if called.Load() != 0 {
		t.Fatalf("expected 0 calls, got %d", called.Load())
	}
}

func TestNotifier_AllEventsWhenEmpty(t *testing.T) {
	var called atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n, _ := testNotifier(t)

	cfg := storage.SiteConfig{WebhookURL: srv.URL}
	n.Fire("site.deleted", "mysite", cfg, nil)

	time.Sleep(500 * time.Millisecond)

	if called.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", called.Load())
	}
}

func TestNotifier_NoURL_Noop(t *testing.T) {
	n, _ := testNotifier(t)

	// Should not panic.
	n.Fire("deploy.success", "mysite", storage.SiteConfig{}, nil)
}

func TestNotifier_SignsWithSecret(t *testing.T) {
	var gotSig string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("webhook-signature")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n, _ := testNotifier(t)

	// Use a valid base64 secret.
	cfg := storage.SiteConfig{
		WebhookURL:    srv.URL,
		WebhookSecret: "dGVzdHNlY3JldA==", // base64("testsecret")
	}
	n.Fire("deploy.success", "mysite", cfg, nil)

	time.Sleep(500 * time.Millisecond)

	if gotSig == "" {
		t.Fatal("expected webhook-signature header to be set")
	}
	if !strings.HasPrefix(gotSig, "v1,") {
		t.Errorf("signature = %q, want prefix v1,", gotSig)
	}
}

func TestNotifier_NoSignatureWithoutSecret(t *testing.T) {
	var gotSig string
	var headerPresent bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("webhook-signature")
		_, headerPresent = r.Header["Webhook-Signature"]
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n, _ := testNotifier(t)

	cfg := storage.SiteConfig{WebhookURL: srv.URL}
	n.Fire("deploy.success", "mysite", cfg, nil)

	time.Sleep(500 * time.Millisecond)

	if headerPresent || gotSig != "" {
		t.Errorf("expected no webhook-signature header, got %q", gotSig)
	}
}

func TestNotifier_RetriesOnFailure(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n, _ := testNotifier(t)
	n.retryDelays = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}

	cfg := storage.SiteConfig{WebhookURL: srv.URL}
	n.Fire("deploy.success", "mysite", cfg, nil)

	time.Sleep(500 * time.Millisecond)

	if calls.Load() != 3 {
		t.Fatalf("expected 3 calls, got %d", calls.Load())
	}
}

func TestNotifier_NoRetryOn406(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(406)
	}))
	defer srv.Close()

	n, _ := testNotifier(t)
	n.retryDelays = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}

	cfg := storage.SiteConfig{WebhookURL: srv.URL}
	n.Fire("deploy.success", "mysite", cfg, nil)

	time.Sleep(500 * time.Millisecond)

	if calls.Load() != 1 {
		t.Fatalf("expected 1 call (no retries on 406), got %d", calls.Load())
	}
}

func TestNotifier_LogsDeliveries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n, db := testNotifier(t)

	cfg := storage.SiteConfig{WebhookURL: srv.URL}
	n.Fire("deploy.success", "mysite", cfg, map[string]any{"v": 1})

	time.Sleep(500 * time.Millisecond)

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count < 1 {
		t.Fatalf("expected at least 1 delivery row, got %d", count)
	}

	var event, site, url string
	var status int
	if err := db.QueryRow(`SELECT event, site, url, status FROM webhook_deliveries LIMIT 1`).Scan(&event, &site, &url, &status); err != nil {
		t.Fatal(err)
	}
	if event != "deploy.success" {
		t.Errorf("event = %q, want deploy.success", event)
	}
	if site != "mysite" {
		t.Errorf("site = %q, want mysite", site)
	}
	if url != srv.URL {
		t.Errorf("url = %q, want %q", url, srv.URL)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
}

func TestNotifier_ListDeliveries(t *testing.T) {
	n, _ := testNotifier(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Fire two webhooks to different sites.
	n.Fire("deploy.success", "docs", storage.SiteConfig{WebhookURL: srv.URL}, map[string]any{"id": "1"})
	n.Fire("site.created", "blog", storage.SiteConfig{WebhookURL: srv.URL}, map[string]any{"id": "2"})

	time.Sleep(500 * time.Millisecond)

	// List all deliveries.
	deliveries, total, err := n.ListDeliveries("", "", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = deliveries
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}

	// Filter by site "docs".
	deliveries, total, err = n.ListDeliveries("docs", "", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = deliveries
	if total != 1 {
		t.Fatalf("total by site = %d, want 1", total)
	}

	// Filter by event "site.created".
	deliveries, total, err = n.ListDeliveries("", "site.created", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = deliveries
	if total != 1 {
		t.Fatalf("total by event = %d, want 1", total)
	}
}

func TestNotifier_ListDeliveries_StatusFilter(t *testing.T) {
	n, db := testNotifier(t)

	// msg_1: 1 attempt, status 200 (succeeded).
	_, err := db.Exec(
		`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"msg_1", "deploy.success", "docs", "http://example.com", "{}", 1, 200, "", "2025-01-01T00:00:00Z",
	)
	if err != nil {
		t.Fatal(err)
	}

	// msg_2: 2 attempts, both status 500 (failed).
	for attempt := 1; attempt <= 2; attempt++ {
		_, err := db.Exec(
			`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"msg_2", "deploy.success", "docs", "http://example.com", "{}", attempt, 500, "server error", "2025-01-01T00:00:01Z",
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Filter succeeded.
	deliveries, total, err := n.ListDeliveries("", "", "succeeded", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = deliveries
	if total != 1 {
		t.Fatalf("succeeded total = %d, want 1", total)
	}

	// Filter failed.
	deliveries, total, err = n.ListDeliveries("", "", "failed", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = deliveries
	if total != 1 {
		t.Fatalf("failed total = %d, want 1", total)
	}
}

func TestNotifier_GetDeliveryAttempts(t *testing.T) {
	n, db := testNotifier(t)

	// Insert 2 rows for the same webhook_id.
	for attempt := 1; attempt <= 2; attempt++ {
		_, err := db.Exec(
			`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"msg_test", "deploy.success", "docs", "http://example.com", `{"v":1}`, attempt, 500+attempt, "", "2025-01-01T00:00:00Z",
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	attempts, err := n.GetDeliveryAttempts("msg_test")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 {
		t.Fatalf("got %d attempts, want 2", len(attempts))
	}
	if attempts[0].Attempt != 1 {
		t.Errorf("first attempt = %d, want 1", attempts[0].Attempt)
	}
	if attempts[1].Attempt != 2 {
		t.Errorf("second attempt = %d, want 2", attempts[1].Attempt)
	}
	if attempts[0].Status != 501 {
		t.Errorf("first status = %d, want 501", attempts[0].Status)
	}
	if attempts[1].Status != 502 {
		t.Errorf("second status = %d, want 502", attempts[1].Status)
	}
	if attempts[0].Payload != `{"v":1}` {
		t.Errorf("payload = %q, want {\"v\":1}", attempts[0].Payload)
	}
}

func TestDeliveryStats(t *testing.T) {
	n, db := testNotifier(t)

	// Insert test data: 2 succeeded (webhook_id msg_1, msg_2), 1 failed (msg_3).
	rows := []struct {
		id      string
		event   string
		site    string
		attempt int
		status  int
		ts      string
	}{
		{"msg_1", "deploy.success", "docs", 1, 200, "2025-06-01T10:00:00Z"},
		{"msg_2", "deploy.success", "blog", 1, 500, "2025-06-01T11:00:00Z"},
		{"msg_2", "deploy.success", "blog", 2, 200, "2025-06-01T11:01:00Z"},
		{"msg_3", "deploy.failed", "docs", 1, 500, "2025-06-01T12:00:00Z"},
		{"msg_3", "deploy.failed", "docs", 2, 500, "2025-06-01T12:01:00Z"},
	}
	for _, r := range rows {
		_, err := db.Exec(
			`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.id, r.event, r.site, "http://example.com", "{}", r.attempt, r.status, "", r.ts,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	from, _ := time.Parse(time.RFC3339, "2025-06-01T00:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2025-06-02T00:00:00Z")

	// All deliveries.
	total, succeeded, failed, err := n.DeliveryStats("", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if succeeded != 2 {
		t.Errorf("succeeded = %d, want 2", succeeded)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}

	// Filter by site.
	total, succeeded, failed, err = n.DeliveryStats("docs", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("docs total = %d, want 2", total)
	}
	if succeeded != 1 {
		t.Errorf("docs succeeded = %d, want 1", succeeded)
	}
	if failed != 1 {
		t.Errorf("docs failed = %d, want 1", failed)
	}

	// Time filter: only first hour.
	earlyTo, _ := time.Parse(time.RFC3339, "2025-06-01T10:30:00Z")
	total, _, _, err = n.DeliveryStats("", from, earlyTo)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("time-filtered total = %d, want 1", total)
	}
}

func TestDeliveriesOverTime(t *testing.T) {
	n, db := testNotifier(t)

	// Insert deliveries at known times.
	rows := []struct {
		id     string
		status int
		ts     string
	}{
		{"msg_1", 200, "2025-06-01T10:00:00Z"},
		{"msg_2", 500, "2025-06-01T10:05:00Z"},
		{"msg_3", 200, "2025-06-01T10:20:00Z"},
	}
	for _, r := range rows {
		_, err := db.Exec(
			`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.id, "deploy.success", "docs", "http://example.com", "{}", 1, r.status, "", r.ts,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	from, _ := time.Parse(time.RFC3339, "2025-06-01T10:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2025-06-01T12:00:00Z")

	buckets, err := n.DeliveriesOverTime("", from, to)
	if err != nil {
		t.Fatal(err)
	}

	// Should have gap-filled buckets (15-min step for 2h range).
	if len(buckets) == 0 {
		t.Fatal("expected non-empty buckets")
	}

	// Verify the first bucket has data.
	first := buckets[0]
	if first.Time != "2025-06-01T10:00:00Z" {
		t.Errorf("first bucket time = %s, want 2025-06-01T10:00:00Z", first.Time)
	}
	// msg_1 (succeeded) and msg_2 (failed) are in the first 15-min bucket.
	if first.Succeeded != 1 {
		t.Errorf("first bucket succeeded = %d, want 1", first.Succeeded)
	}
	if first.Failed != 1 {
		t.Errorf("first bucket failed = %d, want 1", first.Failed)
	}

	// Second bucket (10:15) should have msg_3 (succeeded).
	if len(buckets) > 1 && buckets[1].Succeeded != 1 {
		t.Errorf("second bucket succeeded = %d, want 1", buckets[1].Succeeded)
	}
}

func TestEventBreakdown(t *testing.T) {
	n, db := testNotifier(t)

	rows := []struct {
		id    string
		event string
	}{
		{"msg_1", "deploy.success"},
		{"msg_2", "deploy.success"},
		{"msg_3", "deploy.failed"},
		{"msg_4", "site.created"},
	}
	for _, r := range rows {
		_, err := db.Exec(
			`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.id, r.event, "docs", "http://example.com", "{}", 1, 200, "", "2025-06-01T10:00:00Z",
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	from, _ := time.Parse(time.RFC3339, "2025-06-01T00:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2025-06-02T00:00:00Z")

	events, err := n.EventBreakdown("", from, to)
	if err != nil {
		t.Fatal(err)
	}

	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}

	// Should be ordered by count DESC: deploy.success (2), then the rest (1 each).
	if events[0].Event != "deploy.success" || events[0].Count != 2 {
		t.Errorf("first event = %v, want deploy.success with count 2", events[0])
	}
}

func TestNotifier_RejectsPrivateIP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	db := testDB(t)
	n, err := NewNotifier(db)
	if err != nil {
		t.Fatal(err)
	}
	// Use the safe client (default from NewNotifier) — no override.
	n.retryDelays = nil // no retries for speed

	cfg := storage.SiteConfig{WebhookURL: srv.URL}
	n.Fire("deploy.success", "mysite", cfg, nil)

	time.Sleep(500 * time.Millisecond)

	var errStr string
	err = db.QueryRow(`SELECT error FROM webhook_deliveries LIMIT 1`).Scan(&errStr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errStr, "private address") {
		t.Errorf("expected error containing 'private address', got %q", errStr)
	}
}
