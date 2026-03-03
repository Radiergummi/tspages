package webhooks

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

var (
	adminID    = auth.Identity{LoginName: "admin@example.com", DisplayName: "Admin"}
	adminCaps  = []auth.Cap{{Access: "admin"}}
	viewerID   = auth.Identity{LoginName: "user@example.com"}
	viewerCaps = []auth.Cap{{Access: "view", Sites: []string{"docs"}}}
)

func reqWithAuth(method, path string, caps []auth.Cap, id auth.Identity) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	ctx := auth.ContextWithCaps(r.Context(), caps)
	ctx = auth.ContextWithIdentity(ctx, id)
	return r.WithContext(ctx)
}

func setupWebhookHandlers(t *testing.T) (*WebhooksHandler, *SiteWebhooksHandler, *WebhookDetailHandler, *WebhookRetryHandler, *storage.Store, *Notifier, *sql.DB) {
	t.Helper()
	store := storage.New(t.TempDir())
	store.CreateDeployment("docs", "aaa11111")
	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")

	n, db := testNotifier(t)
	defaults := storage.SiteConfig{}

	wh := &WebhooksHandler{store: store, defaults: defaults, notifier: n}
	swh := &SiteWebhooksHandler{WebhooksHandler: wh}
	det := &WebhookDetailHandler{store: store, defaults: defaults, notifier: n}
	retry := &WebhookRetryHandler{store: store, defaults: defaults, notifier: n}
	return wh, swh, det, retry, store, n, db
}

func insertTestDelivery(t *testing.T, db *sql.DB, site string, status int, url ...string) string {
	t.Helper()
	webhookID := "msg_test_" + site
	u := "http://example.com/hook"
	if len(url) > 0 {
		u = url[0]
	}
	_, err := db.Exec(
		`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		webhookID, "deploy.success", site, u, `{"v":1}`, 1, status, "", "2025-06-01T10:00:00Z",
	)
	if err != nil {
		t.Fatal(err)
	}
	return webhookID
}

// region WebhooksHandler

func TestWebhooksHandler_AdminJSON(t *testing.T) {
	wh, _, _, _, _, _, _ := setupWebhookHandlers(t)
	req := reqWithAuth("GET", "/webhooks", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	wh.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["page"].(float64) != 1 {
		t.Errorf("page = %v, want 1", resp["page"])
	}
	if resp["total_pages"].(float64) != 1 {
		t.Errorf("total_pages = %v, want 1", resp["total_pages"])
	}
}

func TestWebhooksHandler_Forbidden(t *testing.T) {
	wh, _, _, _, _, _, _ := setupWebhookHandlers(t)
	req := reqWithAuth("GET", "/webhooks", viewerCaps, viewerID)

	rec := httptest.NewRecorder()
	wh.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestWebhooksHandler_NilNotifier(t *testing.T) {
	wh := &WebhooksHandler{notifier: nil}
	req := reqWithAuth("GET", "/webhooks", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	wh.ServeHTTP(rec, req)

	// Should still return 200 with empty deliveries, not panic.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// region SiteWebhooksHandler

func TestSiteWebhooksHandler_AdminJSON(t *testing.T) {
	_, swh, _, _, _, _, _ := setupWebhookHandlers(t)
	req := reqWithAuth("GET", "/sites/docs/webhooks", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	swh.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSiteWebhooksHandler_Forbidden(t *testing.T) {
	_, swh, _, _, _, _, _ := setupWebhookHandlers(t)
	req := reqWithAuth("GET", "/sites/demo/webhooks", viewerCaps, viewerID)
	req.SetPathValue("site", "demo")

	rec := httptest.NewRecorder()
	swh.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestSiteWebhooksHandler_InvalidSite(t *testing.T) {
	_, swh, _, _, _, _, _ := setupWebhookHandlers(t)
	req := reqWithAuth("GET", "/sites/BAD!/webhooks", adminCaps, adminID)
	req.SetPathValue("site", "BAD!")

	rec := httptest.NewRecorder()
	swh.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// region WebhookDetailHandler

func TestWebhookDetailHandler_JSON(t *testing.T) {
	_, _, det, _, _, _, db := setupWebhookHandlers(t)
	webhookID := insertTestDelivery(t, db, "docs", 200)

	req := reqWithAuth("GET", "/webhooks/"+webhookID, adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("id", webhookID)

	rec := httptest.NewRecorder()
	det.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	delivery := resp["delivery"].(map[string]any)
	if delivery["webhook_id"] != webhookID {
		t.Errorf("webhook_id = %v, want %q", delivery["webhook_id"], webhookID)
	}
}

func TestWebhookDetailHandler_Forbidden(t *testing.T) {
	_, _, det, _, _, _, _ := setupWebhookHandlers(t)
	req := reqWithAuth("GET", "/webhooks/msg_123", viewerCaps, viewerID)
	req.SetPathValue("id", "msg_123")

	rec := httptest.NewRecorder()
	det.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestWebhookDetailHandler_ForbiddenWrongSite(t *testing.T) {
	_, _, det, _, _, _, db := setupWebhookHandlers(t)
	// Delivery belongs to "demo", but caller only has deploy caps for "docs".
	webhookID := insertTestDelivery(t, db, "demo", 200)

	deployCaps := []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}}
	req := reqWithAuth("GET", "/webhooks/"+webhookID, deployCaps, viewerID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("id", webhookID)

	rec := httptest.NewRecorder()
	det.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
}

func TestWebhookDetailHandler_NotFound(t *testing.T) {
	_, _, det, _, _, _, _ := setupWebhookHandlers(t)
	req := reqWithAuth("GET", "/webhooks/msg_nonexistent", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("id", "msg_nonexistent")

	rec := httptest.NewRecorder()
	det.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWebhookDetailHandler_NilNotifier(t *testing.T) {
	det := &WebhookDetailHandler{notifier: nil}
	req := reqWithAuth("GET", "/webhooks/msg_123", adminCaps, adminID)
	req.SetPathValue("id", "msg_123")

	rec := httptest.NewRecorder()
	det.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (webhooks not configured)", rec.Code)
	}
}

func TestWebhookDetailHandler_MissingID(t *testing.T) {
	_, _, det, _, _, _, _ := setupWebhookHandlers(t)
	req := reqWithAuth("GET", "/webhooks/", adminCaps, adminID)
	req.SetPathValue("id", "")

	rec := httptest.NewRecorder()
	det.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// region WebhookRetryHandler

func TestWebhookRetryHandler_Forbidden(t *testing.T) {
	_, _, _, retry, _, _, db := setupWebhookHandlers(t)
	webhookID := insertTestDelivery(t, db, "docs", 500)

	// Deploy caps for "docs" should NOT be enough — retry requires IsAdmin for the site.
	deployCaps := []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}}
	req := reqWithAuth("POST", "/webhooks/"+webhookID+"/retry", deployCaps, viewerID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("id", webhookID)

	rec := httptest.NewRecorder()
	retry.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
}

func TestWebhookRetryHandler_ForbiddenNoAdminCap(t *testing.T) {
	_, _, _, retry, _, _, db := setupWebhookHandlers(t)
	webhookID := insertTestDelivery(t, db, "docs", 500)

	// View-only caps should be rejected before the DB lookup.
	req := reqWithAuth("POST", "/webhooks/"+webhookID+"/retry", viewerCaps, viewerID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("id", webhookID)

	rec := httptest.NewRecorder()
	retry.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
}

func TestWebhookRetryHandler_NilNotifier(t *testing.T) {
	retry := &WebhookRetryHandler{notifier: nil}
	req := reqWithAuth("POST", "/webhooks/msg_123/retry", adminCaps, adminID)
	req.SetPathValue("id", "msg_123")

	rec := httptest.NewRecorder()
	retry.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (webhooks not configured)", rec.Code)
	}
}

func TestWebhookRetryHandler_NotFound(t *testing.T) {
	_, _, _, retry, _, _, _ := setupWebhookHandlers(t)
	req := reqWithAuth("POST", "/webhooks/msg_nonexistent/retry", adminCaps, adminID)
	req.SetPathValue("id", "msg_nonexistent")

	rec := httptest.NewRecorder()
	retry.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWebhookRetryHandler_ReachesResend(t *testing.T) {
	// Use a local server instead of hitting the real internet.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	_, _, _, retry, _, n, db := setupWebhookHandlers(t)
	n.SetClient(&http.Client{Timeout: 5 * time.Second})
	webhookID := insertTestDelivery(t, db, "docs", 500, srv.URL)

	req := reqWithAuth("POST", "/webhooks/"+webhookID+"/retry", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("id", webhookID)

	rec := httptest.NewRecorder()
	retry.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["status"] == nil {
		t.Error("response missing 'status' field")
	}
}
