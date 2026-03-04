package pushnotify

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dbpkg "tspages/internal/db"
	"tspages/internal/storage"

	_ "modernc.org/sqlite"
)

func testNotifier(t *testing.T) *Notifier {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := dbpkg.Apply(db, Migrations); err != nil {
		db.Close()
		t.Fatalf("migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewNotifier(db, "test-pub", "test-priv", "")
}

// mockSender returns a sender function that records calls and responds with
// the given status code. The done WaitGroup is decremented on each call.
func mockSender(status int, calls *atomic.Int32, done *sync.WaitGroup) func([]byte, dbSubscription) (*http.Response, error) {
	return func(payload []byte, sub dbSubscription) (*http.Response, error) {
		if calls != nil {
			calls.Add(1)
		}
		if done != nil {
			done.Done()
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}
}

// region ListSubscriptions

func TestListSubscriptions(t *testing.T) {
	n := testNotifier(t)

	n.Subscribe("docs", "/contact", "alice@example.com", Subscription{Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a"})
	n.Subscribe("docs", "/feedback", "bob@example.com", Subscription{Endpoint: "https://e.com/2", KeyP256dh: "k", KeyAuth: "a"})
	n.Subscribe("demo", "/contact", "alice@example.com", Subscription{Endpoint: "https://e.com/3", KeyP256dh: "k", KeyAuth: "a"})

	subs, err := n.ListSubscriptions("docs")
	if err != nil {
		t.Fatalf("ListSubscriptions: %v", err)
	}
	if len(subs) != 2 {
		t.Fatalf("expected 2 subscriptions for docs, got %d", len(subs))
	}
	// Verify both entries are present (order depends on created_at which may be identical).
	paths := map[string]bool{}
	for _, s := range subs {
		paths[s.Path] = true
	}
	if !paths["/contact"] || !paths["/feedback"] {
		t.Errorf("expected /contact and /feedback, got %v", paths)
	}

	subs, err = n.ListSubscriptions("unknown")
	if err != nil {
		t.Fatalf("ListSubscriptions: %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("expected 0 subscriptions for unknown site, got %d", len(subs))
	}
}

// region Subscribe / IsSubscribed / Unsubscribe

func TestSubscribeAndIsSubscribed(t *testing.T) {
	n := testNotifier(t)

	sub := Subscription{
		Endpoint:  "https://push.example.com/sub1",
		KeyP256dh: "p256dh-key",
		KeyAuth:   "auth-key",
	}

	if err := n.Subscribe("docs", "/contact", "alice@example.com", sub); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ok, err := n.IsSubscribed("docs", "/contact", "https://push.example.com/sub1")
	if err != nil {
		t.Fatalf("IsSubscribed: %v", err)
	}
	if !ok {
		t.Error("expected subscribed")
	}

	// Different site should not match.
	ok, err = n.IsSubscribed("demo", "/contact", "https://push.example.com/sub1")
	if err != nil {
		t.Fatalf("IsSubscribed: %v", err)
	}
	if ok {
		t.Error("expected not subscribed for different site")
	}

	// Different path should not match.
	ok, err = n.IsSubscribed("docs", "/feedback", "https://push.example.com/sub1")
	if err != nil {
		t.Fatalf("IsSubscribed: %v", err)
	}
	if ok {
		t.Error("expected not subscribed for different path")
	}

	// Different endpoint should not match.
	ok, err = n.IsSubscribed("docs", "/contact", "https://push.example.com/other")
	if err != nil {
		t.Fatalf("IsSubscribed: %v", err)
	}
	if ok {
		t.Error("expected not subscribed for different endpoint")
	}
}

func TestSubscribeUpsert(t *testing.T) {
	n := testNotifier(t)

	sub := Subscription{
		Endpoint:  "https://push.example.com/sub1",
		KeyP256dh: "old-key",
		KeyAuth:   "old-auth",
	}
	if err := n.Subscribe("docs", "/contact", "alice@example.com", sub); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Re-subscribing with same endpoint+site+path should update keys, not create a duplicate.
	sub.KeyP256dh = "new-key"
	sub.KeyAuth = "new-auth"
	if err := n.Subscribe("docs", "/contact", "bob@example.com", sub); err != nil {
		t.Fatalf("Subscribe upsert: %v", err)
	}

	ok, _ := n.IsSubscribed("docs", "/contact", "https://push.example.com/sub1")
	if !ok {
		t.Error("expected still subscribed after upsert")
	}

	// Verify only one row exists (upsert, not duplicate).
	var count int
	n.db.QueryRow(`SELECT COUNT(*) FROM push_subscriptions WHERE site = 'docs' AND path = '/contact'`).Scan(&count)
	if count != 1 {
		t.Errorf("got %d rows after upsert, want 1", count)
	}
}

func TestSubscribeMultipleForms(t *testing.T) {
	n := testNotifier(t)

	sub := Subscription{
		Endpoint:  "https://push.example.com/sub1",
		KeyP256dh: "key",
		KeyAuth:   "auth",
	}

	// Subscribe to two different forms on the same site.
	n.Subscribe("docs", "/contact", "alice", sub)
	n.Subscribe("docs", "/feedback", "alice", sub)

	ok, _ := n.IsSubscribed("docs", "/contact", sub.Endpoint)
	if !ok {
		t.Error("expected subscribed to /contact")
	}
	ok, _ = n.IsSubscribed("docs", "/feedback", sub.Endpoint)
	if !ok {
		t.Error("expected subscribed to /feedback")
	}

	// Unsubscribe from /contact should not affect /feedback.
	n.Unsubscribe(sub.Endpoint, "docs", "/contact", "alice")

	ok, _ = n.IsSubscribed("docs", "/contact", sub.Endpoint)
	if ok {
		t.Error("expected not subscribed to /contact after unsubscribe")
	}
	ok, _ = n.IsSubscribed("docs", "/feedback", sub.Endpoint)
	if !ok {
		t.Error("expected still subscribed to /feedback")
	}
}

func TestUnsubscribe(t *testing.T) {
	n := testNotifier(t)

	sub := Subscription{
		Endpoint:  "https://push.example.com/sub1",
		KeyP256dh: "key",
		KeyAuth:   "auth",
	}
	n.Subscribe("docs", "/contact", "alice@example.com", sub)

	if err := n.Unsubscribe("https://push.example.com/sub1", "docs", "/contact", "alice@example.com"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	ok, _ := n.IsSubscribed("docs", "/contact", "https://push.example.com/sub1")
	if ok {
		t.Error("expected not subscribed after unsubscribe")
	}
}

func TestUnsubscribeNonexistent(t *testing.T) {
	n := testNotifier(t)

	if err := n.Unsubscribe("https://push.example.com/nonexistent", "docs", "/contact", ""); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
}

func TestUnsubscribeWrongUser(t *testing.T) {
	n := testNotifier(t)

	sub := Subscription{
		Endpoint:  "https://push.example.com/sub1",
		KeyP256dh: "key",
		KeyAuth:   "auth",
	}
	n.Subscribe("docs", "/contact", "alice@example.com", sub)

	// bob tries to unsubscribe alice's subscription — should be a no-op.
	if err := n.Unsubscribe("https://push.example.com/sub1", "docs", "/contact", "bob@example.com"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	ok, _ := n.IsSubscribed("docs", "/contact", "https://push.example.com/sub1")
	if !ok {
		t.Error("subscription was removed by wrong user")
	}
}

// region subscriptionsForEvent

func TestSubscriptionsForEvent(t *testing.T) {
	n := testNotifier(t)

	n.Subscribe("docs", "/contact", "alice", Subscription{Endpoint: "https://push.example.com/1", KeyP256dh: "k1", KeyAuth: "a1"})
	n.Subscribe("docs", "/contact", "bob", Subscription{Endpoint: "https://push.example.com/2", KeyP256dh: "k2", KeyAuth: "a2"})
	n.Subscribe("docs", "/feedback", "alice", Subscription{Endpoint: "https://push.example.com/1", KeyP256dh: "k1", KeyAuth: "a1"})
	n.Subscribe("demo", "/contact", "alice", Subscription{Endpoint: "https://push.example.com/3", KeyP256dh: "k3", KeyAuth: "a3"})

	subs, err := n.subscriptionsForEvent("docs", "/contact")
	if err != nil {
		t.Fatalf("subscriptionsForEvent: %v", err)
	}
	if len(subs) != 2 {
		t.Errorf("got %d subscriptions for docs /contact, want 2", len(subs))
	}

	subs, err = n.subscriptionsForEvent("docs", "/feedback")
	if err != nil {
		t.Fatalf("subscriptionsForEvent: %v", err)
	}
	if len(subs) != 1 {
		t.Errorf("got %d subscriptions for docs /feedback, want 1", len(subs))
	}

	subs, err = n.subscriptionsForEvent("demo", "/contact")
	if err != nil {
		t.Fatalf("subscriptionsForEvent: %v", err)
	}
	if len(subs) != 1 {
		t.Errorf("got %d subscriptions for demo /contact, want 1", len(subs))
	}

	// No subscriptions for unknown site.
	subs, err = n.subscriptionsForEvent("unknown", "/contact")
	if err != nil {
		t.Fatalf("subscriptionsForEvent: %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("got %d subscriptions for unknown site, want 0", len(subs))
	}
}

// region Fire

func TestFireIgnoresNonFormEvents(t *testing.T) {
	n := testNotifier(t)

	n.Subscribe("docs", "/contact", "alice", Subscription{Endpoint: "https://push.example.com/1", KeyP256dh: "k", KeyAuth: "a"})
	n.Fire("deploy.success", "docs", storage.SiteConfig{}, map[string]any{
		"id": "dep_123",
	})
}

func TestFireNoSubscriptions(t *testing.T) {
	n := testNotifier(t)

	// Fire with no subscriptions — should not panic.
	n.Fire("form.submission", "docs", storage.SiteConfig{}, map[string]any{
		"id":        "sub_123",
		"path":      "/contact",
		"submitter": "alice@example.com",
	})
}

func TestFireFormSubmission(t *testing.T) {
	n := testNotifier(t)

	var calls atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)
	n.sender = mockSender(http.StatusCreated, &calls, &wg)

	n.Subscribe("docs", "/contact", "alice", Subscription{Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a"})
	n.Subscribe("docs", "/contact", "bob", Subscription{Endpoint: "https://e.com/2", KeyP256dh: "k", KeyAuth: "a"})

	n.Fire("form.submission", "docs", storage.SiteConfig{}, map[string]any{
		"id":        "sub_abc123",
		"path":      "/contact",
		"submitter": "alice@example.com",
	})

	wg.Wait()
	if got := calls.Load(); got != 2 {
		t.Errorf("got %d push calls, want 2", got)
	}
}

func TestFirePayloadContents(t *testing.T) {
	n := testNotifier(t)

	var captured []byte
	var wg sync.WaitGroup
	wg.Add(1)
	n.sender = func(payload []byte, sub dbSubscription) (*http.Response, error) {
		captured = append([]byte{}, payload...)
		wg.Done()
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}

	n.Subscribe("docs", "/contact", "alice", Subscription{Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a"})

	n.Fire("form.submission", "docs", storage.SiteConfig{}, map[string]any{
		"id":        "sub_abc123",
		"path":      "/contact",
		"submitter": "alice@example.com",
	})

	wg.Wait()

	var payload map[string]string
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if payload["title"] != "New submission on docs" {
		t.Errorf("title = %q", payload["title"])
	}
	if payload["body"] != "/contact \u2014 alice@example.com" {
		t.Errorf("body = %q", payload["body"])
	}
	if payload["url"] != "/sites/docs/submissions/sub_abc123" {
		t.Errorf("url = %q", payload["url"])
	}
	if payload["site"] != "docs" {
		t.Errorf("site = %q", payload["site"])
	}
	if payload["path"] != "/contact" {
		t.Errorf("path = %q", payload["path"])
	}
}

func TestFireMissingDataFields(t *testing.T) {
	n := testNotifier(t)

	var wg sync.WaitGroup
	wg.Add(1)
	n.sender = mockSender(http.StatusCreated, nil, &wg)

	n.Subscribe("docs", "/contact", "alice", Subscription{Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a"})

	// Fire with empty data except path — should not panic.
	n.Fire("form.submission", "docs", storage.SiteConfig{}, map[string]any{
		"path": "/contact",
		// missing id and submitter
	})

	wg.Wait() // confirm it actually sent
}

func TestFireOnlyTargetsMatchingPath(t *testing.T) {
	n := testNotifier(t)

	var contactCalls, feedbackCalls atomic.Int32
	n.sender = func(payload []byte, sub dbSubscription) (*http.Response, error) {
		if strings.Contains(sub.endpoint, "contact") {
			contactCalls.Add(1)
		} else {
			feedbackCalls.Add(1)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}

	n.Subscribe("docs", "/contact", "alice", Subscription{Endpoint: "https://e.com/contact", KeyP256dh: "k", KeyAuth: "a"})
	n.Subscribe("docs", "/feedback", "bob", Subscription{Endpoint: "https://e.com/feedback", KeyP256dh: "k", KeyAuth: "a"})

	// Fire for /contact only.
	n.Fire("form.submission", "docs", storage.SiteConfig{}, map[string]any{
		"id":   "sub_1",
		"path": "/contact",
	})

	// Wait for async goroutine.
	time.Sleep(100 * time.Millisecond)

	if got := contactCalls.Load(); got != 1 {
		t.Errorf("contact calls = %d, want 1", got)
	}
	if got := feedbackCalls.Load(); got != 0 {
		t.Errorf("feedback calls = %d, want 0 (wrong path)", got)
	}
}

// region sendPush expired subscription cleanup

func TestSendPush_ExpiredSubscription_410(t *testing.T) {
	n := testNotifier(t)

	var wg sync.WaitGroup
	wg.Add(1)
	n.sender = mockSender(http.StatusGone, nil, &wg)

	n.Subscribe("docs", "/contact", "alice", Subscription{Endpoint: "https://e.com/expired", KeyP256dh: "k", KeyAuth: "a"})

	ok, _ := n.IsSubscribed("docs", "/contact", "https://e.com/expired")
	if !ok {
		t.Fatal("expected subscribed before fire")
	}

	n.Fire("form.submission", "docs", storage.SiteConfig{}, map[string]any{
		"id":   "sub_1",
		"path": "/contact",
	})

	wg.Wait()
	// Small sleep to let deleteByID complete after the sender returns.
	time.Sleep(50 * time.Millisecond)

	ok, _ = n.IsSubscribed("docs", "/contact", "https://e.com/expired")
	if ok {
		t.Error("expired subscription (410) was not cleaned up")
	}
}

func TestSendPush_ExpiredSubscription_404(t *testing.T) {
	n := testNotifier(t)

	var wg sync.WaitGroup
	wg.Add(1)
	n.sender = mockSender(http.StatusNotFound, nil, &wg)

	n.Subscribe("docs", "/contact", "alice", Subscription{Endpoint: "https://e.com/gone", KeyP256dh: "k", KeyAuth: "a"})

	n.Fire("form.submission", "docs", storage.SiteConfig{}, map[string]any{
		"id":   "sub_1",
		"path": "/contact",
	})

	wg.Wait()
	time.Sleep(50 * time.Millisecond)

	ok, _ := n.IsSubscribed("docs", "/contact", "https://e.com/gone")
	if ok {
		t.Error("expired subscription (404) was not cleaned up")
	}
}

func TestSendPush_SuccessPreservesSubscription(t *testing.T) {
	n := testNotifier(t)

	var wg sync.WaitGroup
	wg.Add(1)
	n.sender = mockSender(http.StatusCreated, nil, &wg)

	endpoint := "https://e.com/ok"
	n.Subscribe("docs", "/contact", "alice", Subscription{Endpoint: endpoint, KeyP256dh: "k", KeyAuth: "a"})

	n.Fire("form.submission", "docs", storage.SiteConfig{}, map[string]any{
		"id":   "sub_1",
		"path": "/contact",
	})

	wg.Wait()

	ok, _ := n.IsSubscribed("docs", "/contact", endpoint)
	if !ok {
		t.Error("subscription was removed after successful delivery")
	}
}

func TestSendPush_ServerError_PreservesSubscription(t *testing.T) {
	n := testNotifier(t)

	var wg sync.WaitGroup
	wg.Add(1)
	n.sender = mockSender(http.StatusInternalServerError, nil, &wg)

	endpoint := "https://e.com/error"
	n.Subscribe("docs", "/contact", "alice", Subscription{Endpoint: endpoint, KeyP256dh: "k", KeyAuth: "a"})

	n.Fire("form.submission", "docs", storage.SiteConfig{}, map[string]any{
		"id":   "sub_1",
		"path": "/contact",
	})

	wg.Wait()

	// 500 is not 404/410, so subscription should be preserved.
	ok, _ := n.IsSubscribed("docs", "/contact", endpoint)
	if !ok {
		t.Error("subscription was removed after 500 error (should only remove on 404/410)")
	}
}

func TestSendPush_NetworkError_PreservesSubscription(t *testing.T) {
	n := testNotifier(t)

	var wg sync.WaitGroup
	wg.Add(1)
	n.sender = func(payload []byte, sub dbSubscription) (*http.Response, error) {
		wg.Done()
		return nil, fmt.Errorf("connection refused")
	}

	endpoint := "https://e.com/unreachable"
	n.Subscribe("docs", "/contact", "alice", Subscription{Endpoint: endpoint, KeyP256dh: "k", KeyAuth: "a"})

	n.Fire("form.submission", "docs", storage.SiteConfig{}, map[string]any{
		"id":   "sub_1",
		"path": "/contact",
	})

	wg.Wait()
	time.Sleep(50 * time.Millisecond)

	// Network errors don't mean expired — subscription should remain.
	ok, _ := n.IsSubscribed("docs", "/contact", endpoint)
	if !ok {
		t.Error("subscription was removed after network error")
	}
}

// region formattedBody

func TestFormattedBody(t *testing.T) {
	tests := []struct {
		path, submitter, want string
	}{
		{"/contact", "alice@example.com", "/contact \u2014 alice@example.com"},
		{"/feedback", "", "/feedback"},
		{"", "", ""},
	}
	for _, tt := range tests {
		got := formattedBody(tt.path, tt.submitter)
		if got != tt.want {
			t.Errorf("formattedBody(%q, %q) = %q, want %q", tt.path, tt.submitter, got, tt.want)
		}
	}
}

// region LoadOrCreateVAPIDKeys

func TestLoadOrCreateVAPIDKeys(t *testing.T) {
	dir := t.TempDir()

	// First call should generate keys.
	pub, priv, err := LoadOrCreateVAPIDKeys(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateVAPIDKeys: %v", err)
	}
	if pub == "" || priv == "" {
		t.Fatal("expected non-empty keys")
	}

	// Second call should return the same keys.
	pub2, priv2, err := LoadOrCreateVAPIDKeys(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateVAPIDKeys (2nd): %v", err)
	}
	if pub2 != pub || priv2 != priv {
		t.Error("expected same keys on second call")
	}

	// File should exist.
	if _, err := os.Stat(filepath.Join(dir, "vapid.json")); err != nil {
		t.Errorf("vapid.json not found: %v", err)
	}
}

func TestLoadOrCreateVAPIDKeys_CorruptedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vapid.json"), []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}

	_, _, err := LoadOrCreateVAPIDKeys(dir)
	if err == nil {
		t.Error("expected error for corrupted vapid.json")
	}
}

func TestLoadOrCreateVAPIDKeys_ReadOnlyDirectory(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0555)
	t.Cleanup(func() { os.Chmod(dir, 0755) })

	_, _, err := LoadOrCreateVAPIDKeys(dir)
	if err == nil {
		t.Error("expected error when directory is read-only")
	}
}

func TestLoadOrCreateVAPIDKeys_ReadPermissionError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vapid.json")
	if err := os.WriteFile(path, []byte(`{"public_key":"a","private_key":"b"}`), 0600); err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, 0000)
	t.Cleanup(func() { os.Chmod(path, 0600) })

	_, _, err := LoadOrCreateVAPIDKeys(dir)
	if err == nil {
		t.Error("expected error for unreadable vapid.json")
	}
}

// region cross-site isolation

func TestCrossSiteIsolation(t *testing.T) {
	n := testNotifier(t)

	// Subscribe same endpoint to different site+path combos.
	n.Subscribe("docs", "/contact", "alice", Subscription{Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a"})
	n.Subscribe("docs", "/feedback", "alice", Subscription{Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a"})
	n.Subscribe("demo", "/contact", "alice", Subscription{Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a"})

	// All three should be independently subscribed.
	for _, tc := range []struct{ site, path string }{
		{"docs", "/contact"},
		{"docs", "/feedback"},
		{"demo", "/contact"},
	} {
		ok, err := n.IsSubscribed(tc.site, tc.path, "https://e.com/1")
		if err != nil {
			t.Fatalf("IsSubscribed(%s, %s): %v", tc.site, tc.path, err)
		}
		if !ok {
			t.Errorf("expected subscribed for %s %s", tc.site, tc.path)
		}
	}

	// Unsubscribe from one should not affect others.
	n.Unsubscribe("https://e.com/1", "docs", "/contact", "alice")
	ok, _ := n.IsSubscribed("docs", "/contact", "https://e.com/1")
	if ok {
		t.Error("still subscribed after unsubscribe")
	}
	ok, _ = n.IsSubscribed("docs", "/feedback", "https://e.com/1")
	if !ok {
		t.Error("feedback subscription was incorrectly removed")
	}
	ok, _ = n.IsSubscribed("demo", "/contact", "https://e.com/1")
	if !ok {
		t.Error("demo subscription was incorrectly removed")
	}
}
