package analytics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func setupStore(t *testing.T) *storage.Store {
	t.Helper()
	store := storage.New(t.TempDir())
	store.CreateDeployment("docs", "aaa11111")
	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")
	store.CreateDeployment("demo", "bbb22222")
	store.MarkComplete("demo", "bbb22222")
	store.ActivateDeployment("demo", "bbb22222")
	return store
}

func setupRecorder(t *testing.T) *Recorder {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "analytics.db")
	db := openTestDB(t, dbPath)
	r := NewRecorder(db)
	for i := 0; i < 3; i++ {
		r.Record(Event{
			Timestamp: time.Now(), Site: "docs", Path: "/",
			Status: 200, UserLogin: "alice@example.com", UserName: "Alice",
		})
	}
	r.Close()
	db.Close()
	db = openTestDB(t, dbPath)
	t.Cleanup(func() { db.Close() })
	r = NewRecorder(db)
	t.Cleanup(func() { r.Close() })
	return r
}

func setupMultiSiteRecorder(t *testing.T) *Recorder {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "analytics.db")
	db := openTestDB(t, dbPath)
	r := NewRecorder(db)
	for i := 0; i < 3; i++ {
		r.Record(Event{
			Timestamp: time.Now(), Site: "docs", Path: "/",
			Status: 200, UserLogin: "alice@example.com", UserName: "Alice",
		})
	}
	for i := 0; i < 2; i++ {
		r.Record(Event{
			Timestamp: time.Now(), Site: "demo", Path: "/hello",
			Status: 200, UserLogin: "bob@example.com", UserName: "Bob",
		})
	}
	r.Close()
	db.Close()
	db = openTestDB(t, dbPath)
	t.Cleanup(func() { db.Close() })
	r = NewRecorder(db)
	t.Cleanup(func() { r.Close() })
	return r
}

func setupAnalyticsHandlers(t *testing.T) (*AnalyticsHandler, *AllAnalyticsHandler, *PurgeAnalyticsHandler, *storage.Store) {
	t.Helper()
	store := setupStore(t)
	recorder := setupRecorder(t)
	defaults := storage.SiteConfig{}
	ah := &AnalyticsHandler{store: store, recorder: recorder, defaults: defaults}
	allAh := &AllAnalyticsHandler{store: store, recorder: recorder, defaults: defaults}
	purge := &PurgeAnalyticsHandler{recorder: recorder}
	return ah, allAh, purge, store
}

// region AnalyticsHandler

func TestAnalyticsHandler_HTML(t *testing.T) {
	ah, _, _, _ := setupAnalyticsHandlers(t)
	req := reqWithAuth("GET", "/sites/docs/analytics", adminCaps, adminID)
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	ah.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Analytics") {
		t.Error("HTML missing Analytics title")
	}
	if !strings.Contains(body, "Requests") {
		t.Error("HTML missing Requests metric")
	}
}

func TestAnalyticsHandler_JSON(t *testing.T) {
	ah, _, _, _ := setupAnalyticsHandlers(t)
	req := reqWithAuth("GET", "/sites/docs/analytics?range=all", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	ah.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["site"] != "docs" {
		t.Errorf("site = %v", resp["site"])
	}
	if resp["total"].(float64) != 3 {
		t.Errorf("total = %v, want 3", resp["total"])
	}
}

func TestAnalyticsHandler_Forbidden(t *testing.T) {
	ah, _, _, _ := setupAnalyticsHandlers(t)
	req := reqWithAuth("GET", "/sites/demo/analytics", viewerCaps, viewerID)
	req.SetPathValue("site", "demo")

	rec := httptest.NewRecorder()
	ah.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestAnalyticsHandler_Disabled(t *testing.T) {
	store := setupStore(t)
	recorder := setupRecorder(t)

	// Disable analytics for docs via deployment config.
	analyticsOff := false
	store.WriteDeploymentConfig("docs", "aaa11111", storage.SiteConfig{Analytics: &analyticsOff})

	ah := &AnalyticsHandler{store: store, recorder: recorder, defaults: storage.SiteConfig{}}
	req := reqWithAuth("GET", "/sites/docs/analytics", adminCaps, adminID)
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	ah.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (analytics disabled)", rec.Code)
	}
}

func TestAnalyticsHandler_DisabledViaDefaults(t *testing.T) {
	store := setupStore(t)
	recorder := setupRecorder(t)

	analyticsOff := false
	defaults := storage.SiteConfig{Analytics: &analyticsOff}
	ah := &AnalyticsHandler{store: store, recorder: recorder, defaults: defaults}

	req := reqWithAuth("GET", "/sites/docs/analytics", adminCaps, adminID)
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	ah.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (analytics disabled via defaults)", rec.Code)
	}
}

// region AllAnalyticsHandler

func TestAllAnalyticsHandler_AdminJSON(t *testing.T) {
	store := setupStore(t)
	recorder := setupMultiSiteRecorder(t)
	allAh := &AllAnalyticsHandler{store: store, recorder: recorder, defaults: storage.SiteConfig{}}

	req := reqWithAuth("GET", "/analytics?range=all", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	allAh.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["total"].(float64) != 5 {
		t.Errorf("total = %v, want 5", resp["total"])
	}
	if resp["unique_visitors"].(float64) != 2 {
		t.Errorf("unique_visitors = %v, want 2", resp["unique_visitors"])
	}
	sites := resp["sites"].([]any)
	if len(sites) != 2 {
		t.Fatalf("got %d sites, want 2", len(sites))
	}
}

func TestAllAnalyticsHandler_AdminHTML(t *testing.T) {
	store := setupStore(t)
	recorder := setupMultiSiteRecorder(t)
	allAh := &AllAnalyticsHandler{store: store, recorder: recorder, defaults: storage.SiteConfig{}}

	req := reqWithAuth("GET", "/analytics?range=all", adminCaps, adminID)

	rec := httptest.NewRecorder()
	allAh.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Analytics") {
		t.Error("HTML missing Analytics title")
	}
	if !strings.Contains(body, "docs") {
		t.Error("HTML missing site 'docs'")
	}
	if !strings.Contains(body, "demo") {
		t.Error("HTML missing site 'demo'")
	}
}

func TestAllAnalyticsHandler_ViewOnlyForbidden(t *testing.T) {
	store := setupStore(t)
	recorder := setupMultiSiteRecorder(t)
	allAh := &AllAnalyticsHandler{store: store, recorder: recorder, defaults: storage.SiteConfig{}}

	req := reqWithAuth("GET", "/analytics?range=all", viewerCaps, viewerID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	allAh.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestAllAnalyticsHandler_FilteredByAccess(t *testing.T) {
	store := setupStore(t)
	recorder := setupMultiSiteRecorder(t)
	allAh := &AllAnalyticsHandler{store: store, recorder: recorder, defaults: storage.SiteConfig{}}

	deployCaps := []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}}
	req := reqWithAuth("GET", "/analytics?range=all", deployCaps, viewerID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	allAh.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["total"].(float64) != 3 {
		t.Errorf("total = %v, want 3 (docs only)", resp["total"])
	}
	sites := resp["sites"].([]any)
	if len(sites) != 1 {
		t.Fatalf("got %d sites, want 1", len(sites))
	}
	first := sites[0].(map[string]any)
	if first["site"] != "docs" {
		t.Errorf("site = %v, want docs", first["site"])
	}
}

func TestAllAnalyticsHandler_ExcludesDisabledSites(t *testing.T) {
	store := setupStore(t)
	recorder := setupMultiSiteRecorder(t)

	analyticsOff := false
	store.WriteDeploymentConfig("demo", "bbb22222", storage.SiteConfig{Analytics: &analyticsOff})

	allAh := &AllAnalyticsHandler{store: store, recorder: recorder, defaults: storage.SiteConfig{}}

	req := reqWithAuth("GET", "/analytics?range=all", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	allAh.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["total"].(float64) != 3 {
		t.Errorf("total = %v, want 3 (demo excluded)", resp["total"])
	}
	sites := resp["sites"].([]any)
	if len(sites) != 1 {
		t.Fatalf("got %d sites, want 1", len(sites))
	}
	first := sites[0].(map[string]any)
	if first["site"] != "docs" {
		t.Errorf("site = %v, want docs", first["site"])
	}
}

func TestAllAnalyticsHandler_NoRecorder(t *testing.T) {
	allAh := &AllAnalyticsHandler{recorder: nil}
	req := reqWithAuth("GET", "/analytics", adminCaps, adminID)

	rec := httptest.NewRecorder()
	allAh.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// region PurgeAnalyticsHandler

func TestPurgeAnalyticsHandler(t *testing.T) {
	_, _, purge, _ := setupAnalyticsHandlers(t)
	req := reqWithAuth("POST", "/sites/docs/analytics/purge", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	purge.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["deleted"].(float64) != 3 {
		t.Errorf("deleted = %v, want 3", resp["deleted"])
	}
}

func TestPurgeAnalyticsHandler_NoRecorder(t *testing.T) {
	purge := &PurgeAnalyticsHandler{recorder: nil}
	req := reqWithAuth("POST", "/sites/docs/analytics/purge", adminCaps, adminID)
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	purge.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestPurgeAnalyticsHandler_Forbidden(t *testing.T) {
	_, _, purge, _ := setupAnalyticsHandlers(t)
	req := reqWithAuth("POST", "/sites/docs/analytics/purge", viewerCaps, viewerID)
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	purge.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}
