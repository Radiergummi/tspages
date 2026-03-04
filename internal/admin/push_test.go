package admin

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tspages/internal/auth"
	dbpkg "tspages/internal/db"
	"tspages/internal/pushnotify"

	_ "modernc.org/sqlite"
)

func testPushNotifier(t *testing.T) *pushnotify.Notifier {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	if err := dbpkg.Apply(db, pushnotify.Migrations); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return pushnotify.NewNotifier(db, "test-pub", "test-priv", "")
}

func pushReq(method, path, body string, caps []auth.Cap, id auth.Identity) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	ctx := auth.ContextWithCaps(r.Context(), caps)
	ctx = auth.ContextWithIdentity(ctx, id)
	return r.WithContext(ctx)
}

var (
	pushAdminID    = auth.Identity{LoginName: "admin@example.com", DisplayName: "Admin"}
	pushAdminCaps  = []auth.Cap{{Access: "admin"}}
	pushDeployCaps = []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}}
	pushViewerID   = auth.Identity{LoginName: "viewer@example.com"}
	pushViewerCaps = []auth.Cap{{Access: "view"}}
)

// region GET /push/vapidkey

func TestVAPIDKeyHandler(t *testing.T) {
	h := &VAPIDKeyHandler{vapidPub: "BN-test-public-key"}
	rec := httptest.NewRecorder()
	req := pushReq("GET", "/push/vapidkey", "", pushAdminCaps, pushAdminID)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["public_key"] != "BN-test-public-key" {
		t.Errorf("public_key = %q, want BN-test-public-key", resp["public_key"])
	}
}

// region POST /sites/{site}/push/subscriptions

func TestPushSubscribe_Success(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	body := `{"path":"/contact","endpoint":"https://push.example.com/1","key_p256dh":"k","key_auth":"a"}`
	req := pushReq("POST", "/sites/docs/push/subscriptions", body, pushAdminCaps, pushAdminID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	ok, _ := push.IsSubscribed("docs", "/contact", "https://push.example.com/1")
	if !ok {
		t.Error("subscription was not saved")
	}
}

func TestPushSubscribe_MissingFields(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	tests := []struct {
		name, body string
	}{
		{"missing path", `{"endpoint":"https://e.com/1","key_p256dh":"k","key_auth":"a"}`},
		{"missing endpoint", `{"path":"/contact","key_p256dh":"k","key_auth":"a"}`},
		{"missing p256dh", `{"path":"/contact","endpoint":"https://e.com/1","key_auth":"a"}`},
		{"missing auth", `{"path":"/contact","endpoint":"https://e.com/1","key_p256dh":"k"}`},
		{"empty body", `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := pushReq("POST", "/sites/docs/push/subscriptions", tt.body, pushAdminCaps, pushAdminID)
			req.SetPathValue("site", "docs")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestPushSubscribe_InvalidJSON(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	req := pushReq("POST", "/sites/docs/push/subscriptions", "not json", pushAdminCaps, pushAdminID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPushSubscribe_Forbidden(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	body := `{"path":"/contact","endpoint":"https://e.com/1","key_p256dh":"k","key_auth":"a"}`
	req := pushReq("POST", "/sites/docs/push/subscriptions", body, pushViewerCaps, pushViewerID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestPushSubscribe_DeployCapForMatchingSite(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	body := `{"path":"/contact","endpoint":"https://e.com/1","key_p256dh":"k","key_auth":"a"}`
	req := pushReq("POST", "/sites/docs/push/subscriptions", body, pushDeployCaps, pushAdminID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (deploy cap matches site)", rec.Code)
	}

	ok, _ := push.IsSubscribed("docs", "/contact", "https://e.com/1")
	if !ok {
		t.Error("subscription was not saved")
	}
}

func TestPushSubscribe_DeployCapForDifferentSite(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	// Deploy caps for "docs" but URL is for "demo".
	body := `{"path":"/contact","endpoint":"https://e.com/1","key_p256dh":"k","key_auth":"a"}`
	req := pushReq("POST", "/sites/demo/push/subscriptions", body, pushDeployCaps, pushAdminID)
	req.SetPathValue("site", "demo")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (deploy cap is for docs, not demo)", rec.Code)
	}
}

// region DELETE /sites/{site}/push/subscriptions

func TestPushUnsubscribe_Success(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	push.Subscribe("docs", "/contact", "admin@example.com", pushnotify.Subscription{
		Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a",
	})

	body := `{"endpoint":"https://e.com/1","path":"/contact"}`
	req := pushReq("DELETE", "/sites/docs/push/subscriptions", body, pushAdminCaps, pushAdminID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	ok, _ := push.IsSubscribed("docs", "/contact", "https://e.com/1")
	if ok {
		t.Error("subscription still exists after DELETE")
	}
}

func TestPushUnsubscribe_MissingFields(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	tests := []struct {
		name, body string
	}{
		{"missing endpoint", `{"path":"/contact"}`},
		{"missing path", `{"endpoint":"https://e.com/1"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := pushReq("DELETE", "/sites/docs/push/subscriptions", tt.body, pushAdminCaps, pushAdminID)
			req.SetPathValue("site", "docs")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestPushUnsubscribe_InvalidJSON(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	req := pushReq("DELETE", "/sites/docs/push/subscriptions", "broken", pushAdminCaps, pushAdminID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPushUnsubscribe_Forbidden(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	push.Subscribe("docs", "/contact", "admin@example.com", pushnotify.Subscription{
		Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a",
	})

	body := `{"endpoint":"https://e.com/1","path":"/contact"}`
	req := pushReq("DELETE", "/sites/docs/push/subscriptions", body, pushViewerCaps, pushViewerID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}

	ok, _ := push.IsSubscribed("docs", "/contact", "https://e.com/1")
	if !ok {
		t.Error("subscription was removed despite forbidden response")
	}
}

func TestPushUnsubscribe_DeployCapForDifferentSite(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	push.Subscribe("demo", "/contact", "admin@example.com", pushnotify.Subscription{
		Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a",
	})

	// Deploy caps for "docs" but trying to unsubscribe from "demo".
	body := `{"endpoint":"https://e.com/1","path":"/contact"}`
	req := pushReq("DELETE", "/sites/demo/push/subscriptions", body, pushDeployCaps, pushAdminID)
	req.SetPathValue("site", "demo")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (deploy cap is for docs, not demo)", rec.Code)
	}

	ok, _ := push.IsSubscribed("demo", "/contact", "https://e.com/1")
	if !ok {
		t.Error("subscription was removed despite forbidden response")
	}
}

func TestPushUnsubscribe_WrongOwner(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	// alice subscribes.
	push.Subscribe("docs", "/contact", "alice@example.com", pushnotify.Subscription{
		Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a",
	})

	// admin (different user) tries to unsubscribe — has admin cap but doesn't own it.
	body := `{"endpoint":"https://e.com/1","path":"/contact"}`
	req := pushReq("DELETE", "/sites/docs/push/subscriptions", body, pushAdminCaps, pushAdminID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Subscription should still exist — DELETE was scoped to admin's login, not alice's.
	ok, _ := push.IsSubscribed("docs", "/contact", "https://e.com/1")
	if !ok {
		t.Error("subscription was removed by a different user")
	}
}

// region GET /sites/{site}/push/subscriptions (status check)

func TestPushStatus_Subscribed(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	push.Subscribe("docs", "/contact", "admin@example.com", pushnotify.Subscription{
		Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a",
	})

	req := pushReq("GET", "/sites/docs/push/subscriptions?path=%2Fcontact&endpoint=https%3A%2F%2Fe.com%2F1", "", pushAdminCaps, pushAdminID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp map[string]bool
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp["subscribed"] {
		t.Error("expected subscribed = true")
	}
}

func TestPushStatus_NotSubscribed(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	req := pushReq("GET", "/sites/docs/push/subscriptions?path=%2Fcontact&endpoint=https%3A%2F%2Fe.com%2F1", "", pushAdminCaps, pushAdminID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp map[string]bool
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["subscribed"] {
		t.Error("expected subscribed = false")
	}
}

func TestPushStatus_MissingParams(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	// endpoint is present but path is missing → should 400.
	req := pushReq("GET", "/sites/docs/push/subscriptions?endpoint=https://e.com/1", "", pushAdminCaps, pushAdminID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// region GET /sites/{site}/push/subscriptions (list)

func TestPushList_HTML(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	push.Subscribe("docs", "/contact", "alice@example.com", pushnotify.Subscription{
		Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a",
	})

	req := pushReq("GET", "/sites/docs/push/subscriptions", "", pushAdminCaps, pushAdminID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "alice@example.com") {
		t.Error("HTML response missing subscriber")
	}
	if !strings.Contains(body, "/contact") {
		t.Error("HTML response missing path")
	}
}

func TestPushList_JSON(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	push.Subscribe("docs", "/contact", "alice@example.com", pushnotify.Subscription{
		Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a",
	})
	push.Subscribe("docs", "/feedback", "bob@example.com", pushnotify.Subscription{
		Endpoint: "https://e.com/2", KeyP256dh: "k", KeyAuth: "a",
	})

	req := pushReq("GET", "/sites/docs/push/subscriptions", "", pushAdminCaps, pushAdminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var subs []pushnotify.SubscriptionInfo
	json.NewDecoder(rec.Body).Decode(&subs)
	if len(subs) != 2 {
		t.Fatalf("expected 2 subscriptions, got %d", len(subs))
	}
}

func TestPushList_Empty(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	req := pushReq("GET", "/sites/docs/push/subscriptions", "", pushAdminCaps, pushAdminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var subs []pushnotify.SubscriptionInfo
	json.NewDecoder(rec.Body).Decode(&subs)
	if len(subs) != 0 {
		t.Errorf("expected 0 subscriptions, got %d", len(subs))
	}
}

func TestPushList_Forbidden(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	req := pushReq("GET", "/sites/docs/push/subscriptions", "", pushViewerCaps, pushViewerID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

// region method not allowed

func TestPushSubscribeHandler_MethodNotAllowed(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	req := pushReq("PUT", "/sites/docs/push/subscriptions", "", pushAdminCaps, pushAdminID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
