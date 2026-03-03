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
	return pushnotify.NewNotifier(db, "test-pub", "test-priv")
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

// region POST /push/subscribe

func TestPushSubscribe_Success(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	body := `{"site":"docs","path":"/contact","endpoint":"https://push.example.com/1","key_p256dh":"k","key_auth":"a"}`
	req := pushReq("POST", "/push/subscribe", body, pushAdminCaps, pushAdminID)
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
		{"missing site", `{"path":"/contact","endpoint":"https://e.com/1","key_p256dh":"k","key_auth":"a"}`},
		{"missing path", `{"site":"docs","endpoint":"https://e.com/1","key_p256dh":"k","key_auth":"a"}`},
		{"missing endpoint", `{"site":"docs","path":"/contact","key_p256dh":"k","key_auth":"a"}`},
		{"missing p256dh", `{"site":"docs","path":"/contact","endpoint":"https://e.com/1","key_auth":"a"}`},
		{"missing auth", `{"site":"docs","path":"/contact","endpoint":"https://e.com/1","key_p256dh":"k"}`},
		{"empty body", `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := pushReq("POST", "/push/subscribe", tt.body, pushAdminCaps, pushAdminID)
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

	req := pushReq("POST", "/push/subscribe", "not json", pushAdminCaps, pushAdminID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPushSubscribe_Forbidden(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	// Viewer with no deploy caps should be forbidden.
	body := `{"site":"docs","path":"/contact","endpoint":"https://e.com/1","key_p256dh":"k","key_auth":"a"}`
	req := pushReq("POST", "/push/subscribe", body, pushViewerCaps, pushViewerID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestPushSubscribe_DeployCapForMatchingSite(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	// Deploy caps for "docs" subscribing to "docs" — should succeed.
	body := `{"site":"docs","path":"/contact","endpoint":"https://e.com/1","key_p256dh":"k","key_auth":"a"}`
	req := pushReq("POST", "/push/subscribe", body, pushDeployCaps, pushAdminID)
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

	// Deploy caps for "docs" but trying to subscribe to "demo".
	body := `{"site":"demo","path":"/contact","endpoint":"https://e.com/1","key_p256dh":"k","key_auth":"a"}`
	req := pushReq("POST", "/push/subscribe", body, pushDeployCaps, pushAdminID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (deploy cap is for docs, not demo)", rec.Code)
	}
}

// region DELETE /push/subscribe

func TestPushUnsubscribe_Success(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	// First subscribe.
	push.Subscribe("docs", "/contact", "admin@example.com", pushnotify.Subscription{
		Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a",
	})

	body := `{"endpoint":"https://e.com/1","site":"docs","path":"/contact"}`
	req := pushReq("DELETE", "/push/subscribe", body, pushAdminCaps, pushAdminID)
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
		{"missing endpoint", `{"site":"docs","path":"/contact"}`},
		{"missing site", `{"endpoint":"https://e.com/1","path":"/contact"}`},
		{"missing path", `{"endpoint":"https://e.com/1","site":"docs"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := pushReq("DELETE", "/push/subscribe", tt.body, pushAdminCaps, pushAdminID)
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

	req := pushReq("DELETE", "/push/subscribe", "broken", pushAdminCaps, pushAdminID)
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

	body := `{"endpoint":"https://e.com/1","site":"docs","path":"/contact"}`
	req := pushReq("DELETE", "/push/subscribe", body, pushViewerCaps, pushViewerID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}

	// Subscription should still exist.
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
	body := `{"endpoint":"https://e.com/1","site":"demo","path":"/contact"}`
	req := pushReq("DELETE", "/push/subscribe", body, pushDeployCaps, pushAdminID)
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

	// admin (different user) tries to unsubscribe — has deploy cap but doesn't own it.
	body := `{"endpoint":"https://e.com/1","site":"docs","path":"/contact"}`
	req := pushReq("DELETE", "/push/subscribe", body, pushAdminCaps, pushAdminID)
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

// region GET /push/subscribe (status check)

func TestPushStatus_Subscribed(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	push.Subscribe("docs", "/contact", "admin@example.com", pushnotify.Subscription{
		Endpoint: "https://e.com/1", KeyP256dh: "k", KeyAuth: "a",
	})

	req := pushReq("GET", "/push/subscribe?site=docs&path=%2Fcontact&endpoint=https%3A%2F%2Fe.com%2F1", "", pushAdminCaps, pushAdminID)
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

	req := pushReq("GET", "/push/subscribe?site=docs&path=%2Fcontact&endpoint=https%3A%2F%2Fe.com%2F1", "", pushAdminCaps, pushAdminID)
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

	tests := []struct {
		name, query string
	}{
		{"no params", "/push/subscribe"},
		{"missing site", "/push/subscribe?path=/contact&endpoint=https://e.com/1"},
		{"missing path", "/push/subscribe?site=docs&endpoint=https://e.com/1"},
		{"missing endpoint", "/push/subscribe?site=docs&path=/contact"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := pushReq("GET", tt.query, "", pushAdminCaps, pushAdminID)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

// region method not allowed

func TestPushSubscribeHandler_MethodNotAllowed(t *testing.T) {
	push := testPushNotifier(t)
	h := &PushSubscribeHandler{push: push}

	req := pushReq("PUT", "/push/subscribe", "", pushAdminCaps, pushAdminID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
