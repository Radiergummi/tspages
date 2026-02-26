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
