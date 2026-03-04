package telemetry

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	dbpkg "tspages/internal/db"
	"tspages/internal/storage"

	_ "modernc.org/sqlite"
)

func reqWithAuth(method, path string, caps []auth.Cap, id auth.Identity) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	ctx := auth.ContextWithCaps(r.Context(), caps)
	ctx = auth.ContextWithIdentity(ctx, id)
	return r.WithContext(ctx)
}

func setupStore(t *testing.T) *storage.Store {
	t.Helper()
	store := storage.New(t.TempDir())

	store.CreateDeployment("docs", "aaa11111")
	store.WriteManifest("docs", "aaa11111", storage.Manifest{
		Site: "docs", ID: "aaa11111",
		CreatedBy:       "Alice",
		CreatedByAvatar: "https://example.com/alice.jpg",
		CreatedAt:       time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		SizeBytes:       1024,
	})
	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")

	return store
}

func openTestDBAt(t *testing.T, path string, migrations ...[]func(*sql.Tx) error) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	var all []func(*sql.Tx) error
	for _, m := range migrations {
		all = append(all, m...)
	}
	if err := dbpkg.Apply(db, all); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

func setupRecorder(t *testing.T) *analytics.Recorder {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db := openTestDBAt(t, dbPath, analytics.Migrations)
	r := analytics.NewRecorder(db)
	for i := 0; i < 3; i++ {
		r.Record(analytics.Event{
			Timestamp: time.Now(),
			Site:      "docs",
			Path:      "/",
			Status:    200,
		})
	}
	// Close and reopen to flush events.
	r.Close()
	db.Close()
	db = openTestDBAt(t, dbPath, analytics.Migrations)
	t.Cleanup(func() { db.Close() })
	r = analytics.NewRecorder(db)
	t.Cleanup(func() { r.Close() })
	return r
}

var (
	adminID   = auth.Identity{LoginName: "admin@example.com", DisplayName: "Admin"}
	adminCaps = []auth.Cap{{Access: "admin"}}
	viewerID  = auth.Identity{LoginName: "user@example.com"}
	viewerCaps = []auth.Cap{{Access: "view", Sites: []string{"docs"}}}
)

// region HealthHandler

func TestHealthHandler_OK(t *testing.T) {
	store := setupStore(t)
	recorder := setupRecorder(t)
	h := NewHealthHandler(store, recorder)

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
	checks := resp["checks"].(map[string]any)
	if checks["storage"] != "ok" {
		t.Errorf("storage = %v, want ok", checks["storage"])
	}
	if checks["analytics"] != "ok" {
		t.Errorf("analytics = %v, want ok", checks["analytics"])
	}
}

func TestHealthHandler_NoAnalytics(t *testing.T) {
	store := setupStore(t)
	h := NewHealthHandler(store, nil)

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	checks := resp["checks"].(map[string]any)
	if checks["analytics"] != "disabled" {
		t.Errorf("analytics = %v, want disabled", checks["analytics"])
	}
}

// region SiteHealthHandler

// mockChecker implements SiteHealthChecker for testing.
type mockChecker struct {
	running map[string]bool
}

func (m *mockChecker) IsRunning(site string) bool { return m.running[site] }

func TestSiteHealthHandler_Running(t *testing.T) {
	store := setupStore(t)
	checker := &mockChecker{running: map[string]bool{"docs": true}}
	h := &SiteHealthHandler{store: store, checker: checker}

	req := reqWithAuth("GET", "/sites/docs/healthz", adminCaps, adminID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
	if resp["server"] != "running" {
		t.Errorf("server = %v, want running", resp["server"])
	}
	if resp["active_deployment"] != "aaa11111" {
		t.Errorf("active_deployment = %v, want aaa11111", resp["active_deployment"])
	}
}

func TestSiteHealthHandler_Stopped(t *testing.T) {
	store := setupStore(t)
	checker := &mockChecker{running: map[string]bool{}}
	h := &SiteHealthHandler{store: store, checker: checker}

	req := reqWithAuth("GET", "/sites/docs/healthz", adminCaps, adminID)
	req.SetPathValue("site", "docs")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["status"] != "error" {
		t.Errorf("status = %v, want error", resp["status"])
	}
	if resp["server"] != "stopped" {
		t.Errorf("server = %v, want stopped", resp["server"])
	}
}

func TestSiteHealthHandler_Forbidden(t *testing.T) {
	store := setupStore(t)
	checker := &mockChecker{}
	h := &SiteHealthHandler{store: store, checker: checker}

	// viewerCaps only has view access to "docs", not "demo"
	req := reqWithAuth("GET", "/sites/demo/healthz", viewerCaps, viewerID)
	req.SetPathValue("site", "demo")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestSiteHealthHandler_NotFound(t *testing.T) {
	store := setupStore(t)
	checker := &mockChecker{}
	h := &SiteHealthHandler{store: store, checker: checker}

	req := reqWithAuth("GET", "/sites/nonexistent/healthz", adminCaps, adminID)
	req.SetPathValue("site", "nonexistent")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
