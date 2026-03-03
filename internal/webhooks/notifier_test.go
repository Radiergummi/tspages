package webhooks

import (
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dbpkg "tspages/internal/db"
	"tspages/internal/storage"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	if err := dbpkg.Apply(db, Migrations); err != nil {
		db.Close()
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
	n := NewNotifier(db)
	n.client = &http.Client{Timeout: 10 * time.Second}
	return n, db
}

func TestNotifier_FiresWebhook(t *testing.T) {
	type result struct {
		body    []byte
		headers http.Header
	}
	ch := make(chan result, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ch <- result{body: body, headers: r.Header.Clone()}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n, _ := testNotifier(t)

	cfg := storage.SiteConfig{WebhookURL: srv.URL}
	n.Fire("deploy.success", "mysite", cfg, map[string]any{"id": "abc123"})

	var got result
	select {
	case got = <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for webhook")
	}

	var payload map[string]any
	if err := json.Unmarshal(got.body, &payload); err != nil {
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

	if got.headers.Get("webhook-id") == "" {
		t.Error("missing webhook-id header")
	}
	if got.headers.Get("webhook-timestamp") == "" {
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
	ch := make(chan string, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ch <- r.Header.Get("webhook-signature")
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

	var gotSig string
	select {
	case gotSig = <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for webhook")
	}

	if gotSig == "" {
		t.Fatal("expected webhook-signature header to be set")
	}
	if !strings.HasPrefix(gotSig, "v1,") {
		t.Errorf("signature = %q, want prefix v1,", gotSig)
	}
}

func TestNotifier_NoSignatureWithoutSecret(t *testing.T) {
	type result struct {
		sig           string
		headerPresent bool
	}
	ch := make(chan result, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sig := r.Header.Get("webhook-signature")
		_, present := r.Header["Webhook-Signature"]
		ch <- result{sig: sig, headerPresent: present}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n, _ := testNotifier(t)

	cfg := storage.SiteConfig{WebhookURL: srv.URL}
	n.Fire("deploy.success", "mysite", cfg, nil)

	var got result
	select {
	case got = <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for webhook")
	}

	if got.headerPresent || got.sig != "" {
		t.Errorf("expected no webhook-signature header, got %q", got.sig)
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

func TestNotifier_Resend(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n, db := testNotifier(t)

	// Insert an original delivery.
	_, err := db.Exec(
		`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"msg_resend", "deploy.success", "docs", srv.URL, `{"v":1}`, 1, 500, "server error", "2025-06-01T10:00:00Z",
	)
	if err != nil {
		t.Fatal(err)
	}

	status, err := n.Resend("msg_resend", "")
	if err != nil {
		t.Fatalf("resend: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}

	// Verify the new attempt was logged.
	var maxAttempt int
	err = db.QueryRow(`SELECT MAX(attempt) FROM webhook_deliveries WHERE webhook_id = ?`, "msg_resend").Scan(&maxAttempt)
	if err != nil {
		t.Fatal(err)
	}
	if maxAttempt != 2 {
		t.Errorf("max attempt = %d, want 2", maxAttempt)
	}
}

func TestNotifier_Resend_NotFound(t *testing.T) {
	n, _ := testNotifier(t)

	_, err := n.Resend("msg_nonexistent", "")
	if err == nil {
		t.Fatal("expected error for nonexistent delivery")
	}
}

func TestNotifier_SemaphoreDrop(t *testing.T) {
	// Create a server that blocks until we release it.
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n, _ := testNotifier(t)
	n.retryDelays = nil // no retries

	cfg := storage.SiteConfig{WebhookURL: srv.URL}

	// Fill the semaphore (capacity 20).
	for i := 0; i < 20; i++ {
		n.Fire("deploy.success", "docs", cfg, nil)
	}

	// Give goroutines time to start and fill the semaphore.
	time.Sleep(100 * time.Millisecond)

	// The 21st event should be dropped (semaphore full inside deliver).
	n.Fire("deploy.success", "docs", cfg, map[string]any{"dropped": true})

	time.Sleep(100 * time.Millisecond)

	// Unblock all pending requests.
	close(block)

	time.Sleep(500 * time.Millisecond)

	// Count total deliveries — should be exactly 20, not 21.
	var count int
	n.db.QueryRow(`SELECT COUNT(DISTINCT webhook_id) FROM webhook_deliveries`).Scan(&count)
	if count > 20 {
		t.Errorf("got %d distinct deliveries, want at most 20 (21st should be dropped)", count)
	}
}

func TestNewSafeClient_DoesNotFollowRedirects(t *testing.T) {
	var targetHit atomic.Int32

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHit.Add(1)
		w.WriteHeader(200)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	// newSafeClient blocks private IPs (including localhost), so we can't
	// hit httptest servers directly. Instead, verify the CheckRedirect policy
	// is set by extracting it and testing the redirect behavior with a
	// localhost-capable client that preserves the same CheckRedirect func.
	safeClient := newSafeClient()
	if safeClient.CheckRedirect == nil {
		t.Fatal("newSafeClient().CheckRedirect is nil; redirects would be followed")
	}

	// Apply the same CheckRedirect to a plain client that can reach localhost.
	client := &http.Client{
		CheckRedirect: safeClient.CheckRedirect,
	}
	resp, err := client.Get(redirector.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected status %d, got %d", http.StatusFound, resp.StatusCode)
	}
	if targetHit.Load() != 0 {
		t.Error("safe client followed redirect to target server")
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		// IPv4 private
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.1.1", true},
		// Tailscale CGNAT — allowed so webhooks can reach tailnet hosts
		{"100.64.0.1", false},
		{"100.127.255.254", false},
		// IPv6 private
		{"::1", true},
		{"fe80::1", true},
		{"fc00::1", true},
		{"fd12::1", true},
		// Public
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2607:f8b0:4004:800::200e", false},
	}
	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		if ip == nil {
			t.Fatalf("failed to parse %q", tt.ip)
		}
		if got := isPrivateIP(ip); got != tt.want {
			t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}

func TestNotifier_RejectsPrivateIP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	db := testDB(t)
	n := NewNotifier(db)
	// Use the safe client (default from NewNotifier) — no override.
	n.retryDelays = nil // no retries for speed

	cfg := storage.SiteConfig{WebhookURL: srv.URL}
	n.Fire("deploy.success", "mysite", cfg, nil)

	time.Sleep(500 * time.Millisecond)

	var errStr string
	if err := db.QueryRow(`SELECT error FROM webhook_deliveries LIMIT 1`).Scan(&errStr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errStr, "private address") {
		t.Errorf("expected error containing 'private address', got %q", errStr)
	}
}

func TestNotifier_GetDelivery(t *testing.T) {
	n, db := testNotifier(t)

	_, err := db.Exec(
		`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at, signed, duration_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"msg_gd1", "deploy.success", "docs", "http://example.com", `{"v":1}`, 1, 200, "", "2025-06-01T10:00:00Z", 1, 42,
	)
	if err != nil {
		t.Fatal(err)
	}

	d, err := n.GetDelivery("msg_gd1")
	if err != nil {
		t.Fatal(err)
	}
	if d.WebhookID != "msg_gd1" {
		t.Errorf("webhook_id = %q", d.WebhookID)
	}
	if d.Event != "deploy.success" {
		t.Errorf("event = %q", d.Event)
	}
	if d.Site != "docs" {
		t.Errorf("site = %q", d.Site)
	}
	if !d.Succeeded {
		t.Error("expected succeeded = true for status 200")
	}
	if !d.Signed {
		t.Error("expected signed = true")
	}
}

func TestNotifier_GetDelivery_NotFound(t *testing.T) {
	n, _ := testNotifier(t)

	_, err := n.GetDelivery("msg_nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent delivery")
	}
}
