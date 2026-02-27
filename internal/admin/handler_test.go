package admin

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/storage"
	"tspages/internal/webhook"

	_ "modernc.org/sqlite"
)

type mockEnsurer struct {
	ensured []string
}

func (m *mockEnsurer) EnsureServer(site string) error {
	m.ensured = append(m.ensured, site)
	return nil
}

func (m *mockEnsurer) IsRunning(site string) bool { return true }

func reqWithAuth(method, path string, caps []auth.Cap, id auth.Identity) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	ctx := auth.ContextWithCaps(r.Context(), caps)
	ctx = auth.ContextWithIdentity(ctx, id)
	return r.WithContext(ctx)
}

func formReqWithAuth(path, body string, caps []auth.Cap, id auth.Identity) *http.Request {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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

	store.CreateDeployment("demo", "bbb22222")
	store.WriteManifest("demo", "bbb22222", storage.Manifest{
		Site: "demo", ID: "bbb22222",
		CreatedBy:       "Bob",
		CreatedByAvatar: "https://example.com/bob.jpg",
		CreatedAt:       time.Date(2025, 2, 1, 14, 0, 0, 0, time.UTC),
		SizeBytes:       2048,
	})
	store.MarkComplete("demo", "bbb22222")
	store.ActivateDeployment("demo", "bbb22222")

	store.CreateDeployment("staging", "ccc33333")
	store.MarkComplete("staging", "ccc33333")

	return store
}

func setupRecorder(t *testing.T) *analytics.Recorder {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "analytics.db")
	r, err := analytics.NewRecorder(dbPath)
	if err != nil {
		t.Fatal(err)
	}
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
	r, err = analytics.NewRecorder(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func setupHandlers(t *testing.T) (*Handlers, *storage.Store) {
	t.Helper()
	store := setupStore(t)
	recorder := setupRecorder(t)
	dnsSuffix := "test.ts.net"
	return NewHandlers(store, recorder, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{}, nil), store
}

var (
	adminID    = auth.Identity{LoginName: "admin@example.com", DisplayName: "Admin"}
	adminCaps  = []auth.Cap{{Access: "admin"}}
	viewerID   = auth.Identity{LoginName: "user@example.com"}
	viewerCaps = []auth.Cap{{Access: "view", Sites: []string{"docs"}}}
)

// --- SitesHandler ---

func TestSitesHandler_AdminJSON(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Sites
	req := reqWithAuth("GET", "/sites", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}

	var resp SitesResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Admin {
		t.Error("admin = false")
	}
	if resp.User.Name != "Admin" {
		t.Errorf("user = %q, want Admin", resp.User.Name)
	}
	if resp.DNSSuffix != "test.ts.net" {
		t.Errorf("dns_suffix = %q", resp.DNSSuffix)
	}
	if len(resp.Sites) != 3 {
		t.Fatalf("got %d sites, want 3", len(resp.Sites))
	}
	for _, s := range resp.Sites {
		switch s.Name {
		case "docs":
			if s.Requests != 3 {
				t.Errorf("docs requests = %d, want 3", s.Requests)
			}
			if s.LastDeployedBy != "Alice" {
				t.Errorf("docs last_deployed_by = %q", s.LastDeployedBy)
			}
			if s.LastDeployedAt == "" {
				t.Error("docs last_deployed_at is empty")
			}
		case "demo":
			if s.LastDeployedBy != "Bob" {
				t.Errorf("demo last_deployed_by = %q", s.LastDeployedBy)
			}
		case "staging":
			if s.LastDeployedBy != "" {
				t.Errorf("staging last_deployed_by = %q, want empty", s.LastDeployedBy)
			}
		}
	}
}

func TestSitesHandler_AdminHTML(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Sites
	req := reqWithAuth("GET", "/sites", adminCaps, adminID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "docs") {
		t.Error("HTML missing site name 'docs'")
	}
	if !strings.Contains(body, "Alice") {
		t.Error("HTML missing deployer 'Alice'")
	}
}

func TestSitesHandler_NonAdminJSON(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Sites
	req := reqWithAuth("GET", "/sites", viewerCaps, viewerID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp SitesResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Admin {
		t.Error("admin = true")
	}
	if resp.User.Name != "user@example.com" {
		t.Errorf("user = %q", resp.User.Name)
	}
	if len(resp.Sites) != 1 {
		t.Fatalf("got %d sites, want 1", len(resp.Sites))
	}
	if resp.Sites[0].Name != "docs" {
		t.Errorf("site = %q, want docs", resp.Sites[0].Name)
	}
	if resp.Sites[0].Requests != 0 {
		t.Errorf("requests = %d, want 0 (non-admin)", resp.Sites[0].Requests)
	}
}

func TestSitesHandler_NonAdminExclusion(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Sites
	req := reqWithAuth("GET", "/sites", []auth.Cap{{Access: "view", Sites: []string{"other"}}}, viewerID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp SitesResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Sites) != 0 {
		t.Errorf("got %d sites, want 0", len(resp.Sites))
	}
}

// --- SiteHandler ---

func TestSiteHandler_AdminJSON(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Site
	req := reqWithAuth("GET", "/sites/docs", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp SiteDetailResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Site.Name != "docs" {
		t.Errorf("site = %q", resp.Site.Name)
	}
	if resp.Site.Requests != 3 {
		t.Errorf("requests = %d, want 3", resp.Site.Requests)
	}
	if len(resp.Deployments) != 1 {
		t.Fatalf("got %d deployments, want 1", len(resp.Deployments))
	}
	if resp.Deployments[0].ID != "aaa11111" {
		t.Errorf("deployment id = %q", resp.Deployments[0].ID)
	}
}

func TestSiteHandler_AdminHTML(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Site
	req := reqWithAuth("GET", "/sites/docs", adminCaps, adminID)
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "aaa11111") {
		t.Error("HTML missing deployment ID")
	}
	if !strings.Contains(body, "Alice") {
		t.Error("HTML missing deployer")
	}
	if !strings.Contains(body, "https://docs.test.ts.net") {
		t.Errorf("HTML missing site URL; body contains 'no DNS suffix': %v", strings.Contains(body, "no DNS suffix"))
	}
}

func TestSiteHandler_JSONSuffix(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Site
	req := reqWithAuth("GET", "/sites/docs.json", adminCaps, adminID)
	req.SetPathValue("site", "docs.json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
}

func TestSiteHandler_HidesAnalyticsWhenDisabled(t *testing.T) {
	store := setupStore(t)
	recorder := setupRecorder(t)

	analytics := false
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{Analytics: &analytics})

	dnsSuffix := "test.ts.net"
	hs := NewHandlers(store, recorder, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{}, nil)
	h := hs.Site
	req := reqWithAuth("GET", "/sites/docs", adminCaps, adminID)
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "/sites/docs/analytics") {
		t.Error("per-site analytics link should be hidden when analytics are disabled")
	}
}

func TestSiteHandler_NotFound(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Site
	req := reqWithAuth("GET", "/sites/nope", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("site", "nope")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestSiteHandler_NonAdminForbidden(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Site
	req := reqWithAuth("GET", "/sites/demo", viewerCaps, viewerID)
	req.SetPathValue("site", "demo")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

// --- DeploymentHandler ---

func TestDeploymentHandler_AdminJSON(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Deployment
	req := reqWithAuth("GET", "/sites/docs/deployments/aaa11111", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("site", "docs")
	req.SetPathValue("id", "aaa11111")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var dep storage.DeploymentInfo
	json.NewDecoder(rec.Body).Decode(&dep)
	if dep.ID != "aaa11111" {
		t.Errorf("id = %q", dep.ID)
	}
	if !dep.Active {
		t.Error("active = false, want true")
	}
	if dep.CreatedBy != "Alice" {
		t.Errorf("created_by = %q", dep.CreatedBy)
	}
}

func TestDeploymentHandler_AdminHTML(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Deployment
	req := reqWithAuth("GET", "/sites/docs/deployments/aaa11111", adminCaps, adminID)
	req.SetPathValue("site", "docs")
	req.SetPathValue("id", "aaa11111")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "aaa11111") {
		t.Error("HTML missing deployment ID")
	}
	if !strings.Contains(body, "active") {
		t.Error("HTML missing active badge")
	}
}

func TestDeploymentHandler_NotFound(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Deployment
	req := reqWithAuth("GET", "/sites/docs/deployments/nope", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("site", "docs")
	req.SetPathValue("id", "nope")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestDeploymentHandler_FileListing(t *testing.T) {
	store := storage.New(t.TempDir())

	dir, err := store.CreateDeployment("docs", "aaa11111")
	if err != nil {
		t.Fatal(err)
	}
	contentDir := filepath.Join(dir, "content")
	if err := os.MkdirAll(filepath.Join(contentDir, "assets"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "index.html"), []byte("<h1>hi</h1>"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "assets", "style.css"), []byte("body{}"), 0644); err != nil {
		t.Fatal(err)
	}
	store.WriteManifest("docs", "aaa11111", storage.Manifest{
		Site: "docs", ID: "aaa11111",
		CreatedAt: time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
		SizeBytes: 1024,
	})
	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")

	dnsSuffix := "test.ts.net"
	hs := NewHandlers(store, nil, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{}, nil)
	h := hs.Deployment

	req := reqWithAuth("GET", "/sites/docs/deployments/aaa11111", adminCaps, adminID)
	req.SetPathValue("site", "docs")
	req.SetPathValue("id", "aaa11111")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "index.html") {
		t.Error("HTML missing file index.html")
	}
	if !strings.Contains(body, "assets/style.css") {
		t.Error("HTML missing file assets/style.css")
	}
	if !strings.Contains(body, "Files") || !strings.Contains(body, ">2</") {
		t.Error("HTML missing file count")
	}
}

func TestDeploymentHandler_DiffAgainstPrevious(t *testing.T) {
	store := storage.New(t.TempDir())

	// First (older) deployment
	dir1, err := store.CreateDeployment("docs", "aaa11111")
	if err != nil {
		t.Fatal(err)
	}
	content1 := filepath.Join(dir1, "content")
	if err := os.MkdirAll(content1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content1, "index.html"), []byte("<h1>v1</h1>"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content1, "old.css"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	store.WriteManifest("docs", "aaa11111", storage.Manifest{
		Site: "docs", ID: "aaa11111",
		CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		SizeBytes: 512,
	})
	store.MarkComplete("docs", "aaa11111")

	// Second (newer) deployment
	dir2, err := store.CreateDeployment("docs", "bbb22222")
	if err != nil {
		t.Fatal(err)
	}
	content2 := filepath.Join(dir2, "content")
	if err := os.MkdirAll(content2, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content2, "index.html"), []byte("<h1>v2 longer</h1>"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content2, "new.js"), []byte("console.log()"), 0644); err != nil {
		t.Fatal(err)
	}
	store.WriteManifest("docs", "bbb22222", storage.Manifest{
		Site: "docs", ID: "bbb22222",
		CreatedAt: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		SizeBytes: 1024,
	})
	store.MarkComplete("docs", "bbb22222")
	store.ActivateDeployment("docs", "bbb22222")

	dnsSuffix := "test.ts.net"
	hs := NewHandlers(store, nil, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{}, nil)
	h := hs.Deployment

	req := reqWithAuth("GET", "/sites/docs/deployments/bbb22222", adminCaps, adminID)
	req.SetPathValue("site", "docs")
	req.SetPathValue("id", "bbb22222")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()

	// Should show diff section referencing previous deployment
	if !strings.Contains(body, "aaa11111") {
		t.Error("HTML missing previous deployment ID in diff")
	}
	// new.js was added
	if !strings.Contains(body, "added") {
		t.Error("HTML missing 'added' indicator")
	}
	// old.css was removed
	if !strings.Contains(body, "removed") {
		t.Error("HTML missing 'removed' indicator")
	}
	// index.html changed size
	if !strings.Contains(body, "modified") {
		t.Error("HTML missing 'modified' indicator")
	}
}

func TestDiffFiles(t *testing.T) {
	current := []storage.FileInfo{
		{Path: "index.html", Size: 200, Hash: "aaa"},
		{Path: "new.js", Size: 50, Hash: "bbb"},
		{Path: "same.css", Size: 100, Hash: "ccc"},
		{Path: "same-size-diff-content.txt", Size: 100, Hash: "ddd"},
	}
	previous := []storage.FileInfo{
		{Path: "index.html", Size: 100, Hash: "xxx"},
		{Path: "old.txt", Size: 30, Hash: "yyy"},
		{Path: "same.css", Size: 100, Hash: "ccc"},
		{Path: "same-size-diff-content.txt", Size: 100, Hash: "zzz"},
	}

	added, removed, changed := diffFiles(current, previous)

	if len(added) != 1 || added[0] != "new.js" {
		t.Errorf("added = %v, want [new.js]", added)
	}
	if len(removed) != 1 || removed[0] != "old.txt" {
		t.Errorf("removed = %v, want [old.txt]", removed)
	}
	if len(changed) != 2 {
		t.Errorf("changed = %v, want [index.html same-size-diff-content.txt]", changed)
	}
}

// --- DeploymentsHandler ---

func TestDeploymentsHandler_AdminJSON(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Deployments
	req := reqWithAuth("GET", "/deployments", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp DeploymentsResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	// setupStore creates 3 sites: docs (1 dep), demo (1 dep), staging (1 dep)
	if len(resp.Deployments) != 3 {
		t.Fatalf("got %d deployments, want 3", len(resp.Deployments))
	}
	if resp.Page != 1 {
		t.Errorf("page = %d, want 1", resp.Page)
	}
	if resp.TotalPages != 1 {
		t.Errorf("total_pages = %d, want 1", resp.TotalPages)
	}
	// Should be sorted by CreatedAt desc — demo (Feb) before docs (Jan)
	if resp.Deployments[0].Site != "demo" {
		t.Errorf("first deployment site = %q, want demo (newest)", resp.Deployments[0].Site)
	}
}

func TestDeploymentsHandler_AdminHTML(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Deployments
	req := reqWithAuth("GET", "/deployments", adminCaps, adminID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "docs") {
		t.Error("HTML missing site 'docs'")
	}
	if !strings.Contains(body, "demo") {
		t.Error("HTML missing site 'demo'")
	}
	if !strings.Contains(body, "aaa11111") {
		t.Error("HTML missing deployment ID")
	}
}

func TestDeploymentsHandler_FilteredByAccess(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Deployments
	// deploy caps only for "docs"
	deployCaps := []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}}
	req := reqWithAuth("GET", "/deployments", deployCaps, viewerID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp DeploymentsResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Deployments) != 1 {
		t.Fatalf("got %d deployments, want 1 (docs only)", len(resp.Deployments))
	}
	if resp.Deployments[0].Site != "docs" {
		t.Errorf("site = %q, want docs", resp.Deployments[0].Site)
	}
}

func TestDeploymentsHandler_ViewOnlyExcluded(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Deployments
	// view-only users should see no deployments
	req := reqWithAuth("GET", "/deployments", viewerCaps, viewerID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp DeploymentsResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Deployments) != 0 {
		t.Errorf("got %d deployments, want 0 (view-only)", len(resp.Deployments))
	}
}

func TestDeploymentsHandler_Pagination(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Deployments
	// Page 2 with only 3 deployments total and page size 50 — should clamp to page 1
	req := reqWithAuth("GET", "/deployments?page=2", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp DeploymentsResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Page != 1 {
		t.Errorf("page = %d, want 1 (clamped)", resp.Page)
	}
}

// --- CreateSiteHandler ---

func TestCreateSiteHandler_Success(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.CreateSite
	req := formReqWithAuth("/sites", "name=newsite", adminCaps, adminID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303, body = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/sites/newsite" {
		t.Errorf("location = %q, want /sites/newsite", loc)
	}
}

func TestCreateSiteHandler_SuccessJSON(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.CreateSite
	req := formReqWithAuth("/sites", "name=newsite", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["name"] != "newsite" {
		t.Errorf("name = %q", resp["name"])
	}
}

func TestCreateSiteHandler_InvalidName(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.CreateSite
	req := formReqWithAuth("/sites", "name=BAD!", adminCaps, adminID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCreateSiteHandler_Forbidden(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.CreateSite
	req := formReqWithAuth("/sites", "name=newsite", viewerCaps, viewerID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestCreateSiteHandler_Duplicate(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.CreateSite
	// "docs" already exists from setupStore
	req := formReqWithAuth("/sites", "name=docs", adminCaps, adminID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestCreateSiteHandler_DeployCannotCreate(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.CreateSite
	// Deploy access does NOT grant site creation — that requires admin
	deployCaps := []auth.Cap{{Access: "deploy"}}
	req := formReqWithAuth("/sites", "name=newsite2", deployCaps, viewerID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestCreateSiteHandler_ScopedAdmin(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.CreateSite
	// Scoped admin can create sites within their scope
	scopedCaps := []auth.Cap{{Access: "admin", Sites: []string{"newsite3"}}}
	req := formReqWithAuth("/sites", "name=newsite3", scopedCaps, viewerID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
}

func TestCreateSiteHandler_ScopedAdminOutOfScope(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.CreateSite
	// Scoped admin cannot create sites outside their scope
	scopedCaps := []auth.Cap{{Access: "admin", Sites: []string{"other"}}}
	req := formReqWithAuth("/sites", "name=newsite4", scopedCaps, viewerID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestCreateSiteHandler_CallsEnsureServer(t *testing.T) {
	store := setupStore(t)
	dnsSuffix := "test.ts.net"
	mock := &mockEnsurer{}
	hs := NewHandlers(store, nil, dnsSuffix, mock, mock, storage.SiteConfig{}, nil)
	h := hs.CreateSite

	req := formReqWithAuth("/sites", "name=newsite5", adminCaps, adminID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if len(mock.ensured) != 1 || mock.ensured[0] != "newsite5" {
		t.Errorf("EnsureServer calls = %v, want [newsite5]", mock.ensured)
	}
}

// --- AnalyticsHandler ---

func TestAnalyticsHandler_HTML(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.Analytics
	req := reqWithAuth("GET", "/sites/docs/analytics", adminCaps, adminID)
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

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
	hs, _ := setupHandlers(t)
	h := hs.Analytics
	req := reqWithAuth("GET", "/sites/docs/analytics?range=all", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

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
	hs, _ := setupHandlers(t)
	h := hs.Analytics
	req := reqWithAuth("GET", "/sites/demo/analytics", viewerCaps, viewerID)
	req.SetPathValue("site", "demo")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestAnalyticsHandler_Disabled(t *testing.T) {
	store := setupStore(t)
	recorder := setupRecorder(t)

	// Disable analytics for docs via deployment config.
	analytics := false
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{Analytics: &analytics})

	dnsSuffix := "test.ts.net"
	hs := NewHandlers(store, recorder, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{}, nil)

	req := reqWithAuth("GET", "/sites/docs/analytics", adminCaps, adminID)
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	hs.Analytics.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (analytics disabled)", rec.Code)
	}
}

func TestAnalyticsHandler_DisabledViaDefaults(t *testing.T) {
	store := setupStore(t)
	recorder := setupRecorder(t)

	analytics := false
	defaults := storage.SiteConfig{Analytics: &analytics}
	dnsSuffix := "test.ts.net"
	hs := NewHandlers(store, recorder, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, defaults, nil)

	req := reqWithAuth("GET", "/sites/docs/analytics", adminCaps, adminID)
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	hs.Analytics.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (analytics disabled via defaults)", rec.Code)
	}
}

// --- AllAnalyticsHandler ---

func setupMultiSiteRecorder(t *testing.T) *analytics.Recorder {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "analytics.db")
	r, err := analytics.NewRecorder(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		r.Record(analytics.Event{
			Timestamp: time.Now(), Site: "docs", Path: "/",
			Status: 200, UserLogin: "alice@example.com", UserName: "Alice",
		})
	}
	for i := 0; i < 2; i++ {
		r.Record(analytics.Event{
			Timestamp: time.Now(), Site: "demo", Path: "/hello",
			Status: 200, UserLogin: "bob@example.com", UserName: "Bob",
		})
	}
	r.Close()
	r, err = analytics.NewRecorder(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestAllAnalyticsHandler_AdminJSON(t *testing.T) {
	store := setupStore(t)
	recorder := setupMultiSiteRecorder(t)
	dnsSuffix := "test.ts.net"
	hs := NewHandlers(store, recorder, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{}, nil)

	req := reqWithAuth("GET", "/analytics?range=all", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	hs.AllAnalytics.ServeHTTP(rec, req)

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
	dnsSuffix := "test.ts.net"
	hs := NewHandlers(store, recorder, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{}, nil)

	req := reqWithAuth("GET", "/analytics?range=all", adminCaps, adminID)

	rec := httptest.NewRecorder()
	hs.AllAnalytics.ServeHTTP(rec, req)

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
	dnsSuffix := "test.ts.net"
	hs := NewHandlers(store, recorder, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{}, nil)

	// viewerCaps only grants view — analytics requires deploy
	req := reqWithAuth("GET", "/analytics?range=all", viewerCaps, viewerID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	hs.AllAnalytics.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestAllAnalyticsHandler_FilteredByAccess(t *testing.T) {
	store := setupStore(t)
	recorder := setupMultiSiteRecorder(t)
	dnsSuffix := "test.ts.net"
	hs := NewHandlers(store, recorder, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{}, nil)

	// Deploy caps for "docs" only — should see docs data but not demo
	deployCaps := []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}}
	req := reqWithAuth("GET", "/analytics?range=all", deployCaps, viewerID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	hs.AllAnalytics.ServeHTTP(rec, req)

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

	// Disable analytics for demo.
	analytics := false
	store.WriteSiteConfig("demo", "bbb22222", storage.SiteConfig{Analytics: &analytics})

	dnsSuffix := "test.ts.net"
	hs := NewHandlers(store, recorder, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{}, nil)

	req := reqWithAuth("GET", "/analytics?range=all", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	hs.AllAnalytics.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	// Only docs events (3), demo excluded.
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
	store := setupStore(t)
	dnsSuffix := "test.ts.net"
	hs := NewHandlers(store, nil, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{}, nil)

	req := reqWithAuth("GET", "/analytics", adminCaps, adminID)

	rec := httptest.NewRecorder()
	hs.AllAnalytics.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// --- PurgeAnalyticsHandler ---

func TestPurgeAnalyticsHandler(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.PurgeAnalytics
	req := reqWithAuth("POST", "/sites/docs/analytics/purge", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

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
	store := setupStore(t)
	dnsSuffix := "test.ts.net"
	hs := NewHandlers(store, nil, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{}, nil)

	req := reqWithAuth("POST", "/sites/docs/analytics/purge", adminCaps, adminID)
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	hs.PurgeAnalytics.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestPurgeAnalyticsHandler_Forbidden(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.PurgeAnalytics
	req := reqWithAuth("POST", "/sites/docs/analytics/purge", viewerCaps, viewerID)
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestJSONResponses_LinkHeaders(t *testing.T) {
	hs, _ := setupHandlers(t)

	tests := []struct {
		name    string
		handler http.Handler
		path    string
		pathVal map[string]string
		wantRel []string // substrings expected in Link header
	}{
		{"sites", hs.Sites, "/sites", nil, []string{`rel="alternate"`, "/sites", "/feed.atom"}},
		{"site", hs.Site, "/sites/docs", map[string]string{"site": "docs"}, []string{"/sites/docs", "/sites/docs/feed.atom"}},
		{"deployment", hs.Deployment, "/sites/docs/deployments/aaa11111", map[string]string{"site": "docs", "id": "aaa11111"}, []string{"/sites/docs/deployments/aaa11111"}},
		{"deployments", hs.Deployments, "/deployments", nil, []string{"/deployments", "/feed.atom"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := reqWithAuth("GET", tt.path, adminCaps, adminID)
			req.Header.Set("Accept", "application/json")
			for k, v := range tt.pathVal {
				req.SetPathValue(k, v)
			}
			rec := httptest.NewRecorder()
			tt.handler.ServeHTTP(rec, req)

			link := rec.Header().Get("Link")
			if link == "" {
				t.Fatal("missing Link header")
			}
			for _, want := range tt.wantRel {
				if !strings.Contains(link, want) {
					t.Errorf("Link header %q missing %q", link, want)
				}
			}
		})
	}
}

// --- HealthHandler ---

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

// --- SiteHealthHandler ---

func TestSiteHealthHandler_Running(t *testing.T) {
	store := setupStore(t)
	dnsSuffix := "test.ts.net"
	checker := &mockChecker{running: map[string]bool{"docs": true}}
	d := handlerDeps{store: store, dnsSuffix: dnsSuffix}
	h := &SiteHealthHandler{handlerDeps: d, checker: checker}

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
	dnsSuffix := "test.ts.net"
	checker := &mockChecker{running: map[string]bool{}}
	d := handlerDeps{store: store, dnsSuffix: dnsSuffix}
	h := &SiteHealthHandler{handlerDeps: d, checker: checker}

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
	dnsSuffix := "test.ts.net"
	checker := &mockChecker{}
	d := handlerDeps{store: store, dnsSuffix: dnsSuffix}
	h := &SiteHealthHandler{handlerDeps: d, checker: checker}

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
	dnsSuffix := "test.ts.net"
	checker := &mockChecker{}
	d := handlerDeps{store: store, dnsSuffix: dnsSuffix}
	h := &SiteHealthHandler{handlerDeps: d, checker: checker}

	req := reqWithAuth("GET", "/sites/nonexistent/healthz", adminCaps, adminID)
	req.SetPathValue("site", "nonexistent")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// mockChecker implements SiteHealthChecker for testing.
type mockChecker struct {
	running map[string]bool
}

func (m *mockChecker) IsRunning(site string) bool { return m.running[site] }

func TestSubtractISO8601(t *testing.T) {
	now := time.Date(2025, 3, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		input string
		want  time.Time
		ok    bool
	}{
		{"PT24H", now.Add(-24 * time.Hour), true},
		{"PT1H", now.Add(-time.Hour), true},
		{"P7D", now.AddDate(0, 0, -7), true},
		{"P30D", now.AddDate(0, 0, -30), true},
		{"P1M", now.AddDate(0, -1, 0), true},
		{"P1Y", now.AddDate(-1, 0, 0), true},
		{"P1Y2M3DT4H5M6S", now.AddDate(-1, -2, -3).Add(-4*time.Hour - 5*time.Minute - 6*time.Second), true},
		{"P2W", now.AddDate(0, 0, -14), true},
		{"", time.Time{}, false},
		{"24h", time.Time{}, false},
		{"P", time.Time{}, false},
		{"PT", time.Time{}, false},
		{"P0D", time.Time{}, false},
		{"PXD", time.Time{}, false},
	}
	for _, tt := range tests {
		got, ok := subtractISO8601(now, tt.input)
		if ok != tt.ok {
			t.Errorf("subtractISO8601(%q): ok = %v, want %v", tt.input, ok, tt.ok)
			continue
		}
		if ok && !got.Equal(tt.want) {
			t.Errorf("subtractISO8601(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- helpers for webhook handler tests ---

func testNotifierDB(t *testing.T) (*webhook.Notifier, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "webhook.db")
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	n, err := webhook.NewNotifier(db)
	if err != nil {
		t.Fatal(err)
	}
	return n, db
}

// insertDelivery inserts a test delivery row and returns the webhook_id.
func insertDelivery(t *testing.T, db *sql.DB, site string, status int, url ...string) string {
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

func setupHandlersWithNotifier(t *testing.T) (*Handlers, *storage.Store, *webhook.Notifier, *sql.DB) {
	t.Helper()
	store := setupStore(t)
	recorder := setupRecorder(t)
	notifier, db := testNotifierDB(t)
	dnsSuffix := "test.ts.net"
	return NewHandlers(store, recorder, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{}, notifier), store, notifier, db
}

// --- SiteDeploymentsHandler ---

func TestSiteDeploymentsHandler_AdminJSON(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.SiteDeployments
	req := reqWithAuth("GET", "/sites/docs/deployments", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	deps := resp["deployments"].([]any)
	if len(deps) != 1 {
		t.Fatalf("got %d deployments, want 1", len(deps))
	}
	if resp["page"].(float64) != 1 {
		t.Errorf("page = %v, want 1", resp["page"])
	}
}

func TestSiteDeploymentsHandler_AdminHTML(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.SiteDeployments
	req := reqWithAuth("GET", "/sites/docs/deployments", adminCaps, adminID)
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "aaa11111") {
		t.Error("HTML missing deployment ID")
	}
}

func TestSiteDeploymentsHandler_Forbidden(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.SiteDeployments
	req := reqWithAuth("GET", "/sites/demo/deployments", viewerCaps, viewerID)
	req.SetPathValue("site", "demo")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestSiteDeploymentsHandler_InvalidSite(t *testing.T) {
	hs, _ := setupHandlers(t)
	h := hs.SiteDeployments
	req := reqWithAuth("GET", "/sites/BAD!/deployments", adminCaps, adminID)
	req.SetPathValue("site", "BAD!")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// --- WebhooksHandler ---

func TestWebhooksHandler_AdminJSON(t *testing.T) {
	hs, _, _, _ := setupHandlersWithNotifier(t)
	h := hs.Webhooks
	req := reqWithAuth("GET", "/webhooks", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

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
	hs, _, _, _ := setupHandlersWithNotifier(t)
	h := hs.Webhooks
	req := reqWithAuth("GET", "/webhooks", viewerCaps, viewerID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestWebhooksHandler_NilNotifier(t *testing.T) {
	hs, _ := setupHandlers(t) // nil notifier
	h := hs.Webhooks
	req := reqWithAuth("GET", "/webhooks", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Should still return 200 with empty deliveries, not panic.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// --- SiteWebhooksHandler ---

func TestSiteWebhooksHandler_AdminJSON(t *testing.T) {
	hs, _, _, _ := setupHandlersWithNotifier(t)
	h := hs.SiteWebhooks
	req := reqWithAuth("GET", "/sites/docs/webhooks", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSiteWebhooksHandler_Forbidden(t *testing.T) {
	hs, _, _, _ := setupHandlersWithNotifier(t)
	h := hs.SiteWebhooks
	req := reqWithAuth("GET", "/sites/demo/webhooks", viewerCaps, viewerID)
	req.SetPathValue("site", "demo")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestSiteWebhooksHandler_InvalidSite(t *testing.T) {
	hs, _, _, _ := setupHandlersWithNotifier(t)
	h := hs.SiteWebhooks
	req := reqWithAuth("GET", "/sites/BAD!/webhooks", adminCaps, adminID)
	req.SetPathValue("site", "BAD!")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// --- WebhookDetailHandler ---

func TestWebhookDetailHandler_JSON(t *testing.T) {
	hs, _, _, db := setupHandlersWithNotifier(t)
	webhookID := insertDelivery(t, db, "docs", 200)

	h := hs.WebhookDetail
	req := reqWithAuth("GET", "/webhooks/"+webhookID, adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("id", webhookID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

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
	hs, _, _, _ := setupHandlersWithNotifier(t)
	h := hs.WebhookDetail
	req := reqWithAuth("GET", "/webhooks/msg_123", viewerCaps, viewerID)
	req.SetPathValue("id", "msg_123")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestWebhookDetailHandler_NotFound(t *testing.T) {
	hs, _, _, _ := setupHandlersWithNotifier(t)
	h := hs.WebhookDetail
	req := reqWithAuth("GET", "/webhooks/msg_nonexistent", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("id", "msg_nonexistent")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWebhookDetailHandler_NilNotifier(t *testing.T) {
	hs, _ := setupHandlers(t) // nil notifier
	h := hs.WebhookDetail
	req := reqWithAuth("GET", "/webhooks/msg_123", adminCaps, adminID)
	req.SetPathValue("id", "msg_123")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (webhooks not configured)", rec.Code)
	}
}

func TestWebhookDetailHandler_MissingID(t *testing.T) {
	hs, _, _, _ := setupHandlersWithNotifier(t)
	h := hs.WebhookDetail
	req := reqWithAuth("GET", "/webhooks/", adminCaps, adminID)
	req.SetPathValue("id", "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// --- WebhookRetryHandler ---

func TestWebhookRetryHandler_Forbidden(t *testing.T) {
	hs, _, _, db := setupHandlersWithNotifier(t)
	webhookID := insertDelivery(t, db, "docs", 500)

	h := hs.WebhookRetry
	// Deploy caps for "docs" should NOT be enough — retry requires IsAdmin for the site.
	deployCaps := []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}}
	req := reqWithAuth("POST", "/webhooks/"+webhookID+"/retry", deployCaps, viewerID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("id", webhookID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
}

func TestWebhookRetryHandler_ForbiddenNoAdminCap(t *testing.T) {
	hs, _, _, db := setupHandlersWithNotifier(t)
	webhookID := insertDelivery(t, db, "docs", 500)

	h := hs.WebhookRetry
	// View-only caps should be rejected before the DB lookup.
	req := reqWithAuth("POST", "/webhooks/"+webhookID+"/retry", viewerCaps, viewerID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("id", webhookID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
}

func TestWebhookRetryHandler_NilNotifier(t *testing.T) {
	hs, _ := setupHandlers(t) // nil notifier
	h := hs.WebhookRetry
	req := reqWithAuth("POST", "/webhooks/msg_123/retry", adminCaps, adminID)
	req.SetPathValue("id", "msg_123")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (webhooks not configured)", rec.Code)
	}
}

func TestWebhookRetryHandler_NotFound(t *testing.T) {
	hs, _, _, _ := setupHandlersWithNotifier(t)
	h := hs.WebhookRetry
	req := reqWithAuth("POST", "/webhooks/msg_nonexistent/retry", adminCaps, adminID)
	req.SetPathValue("id", "msg_nonexistent")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

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

	hs, _, notifier, db := setupHandlersWithNotifier(t)
	notifier.SetClient(&http.Client{Timeout: 5 * time.Second})
	webhookID := insertDelivery(t, db, "docs", 500, srv.URL)

	h := hs.WebhookRetry
	req := reqWithAuth("POST", "/webhooks/"+webhookID+"/retry", adminCaps, adminID)
	req.Header.Set("Accept", "application/json")
	req.SetPathValue("id", webhookID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["status"] == nil {
		t.Error("response missing 'status' field")
	}
}
