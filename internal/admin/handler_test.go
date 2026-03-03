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
	dbpkg "tspages/internal/db"
	"tspages/internal/storage"

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

func openTestDB(t *testing.T, path string, migrations ...[]func(*sql.Tx) error) *sql.DB {
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
	dbPath := filepath.Join(t.TempDir(), "analytics.db")
	db := openTestDB(t, dbPath, analytics.Migrations)
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
	db = openTestDB(t, dbPath, analytics.Migrations)
	t.Cleanup(func() { db.Close() })
	r = analytics.NewRecorder(db)
	t.Cleanup(func() { r.Close() })
	return r
}

// testHandlers groups the handler instances used in tests.
type testHandlers struct {
	Sites           *SitesHandler
	Site            *SiteHandler
	Deployment      *DeploymentHandler
	Deployments     *DeploymentsHandler
	SiteDeployments *SiteDeploymentsHandler
	CreateSite      *CreateSiteHandler
	Feed            *FeedHandler
	SiteFeed        *SiteFeedHandler
}

func newTestHandlers(store *storage.Store, recorder *analytics.Recorder, dnsSuffix string, ensurer SiteEnsurer, checker SiteHealthChecker, defaults storage.SiteConfig) *testHandlers {
	return &testHandlers{
		Sites:           &SitesHandler{store: store, recorder: recorder, dnsSuffix: dnsSuffix, defaults: defaults},
		Site:            &SiteHandler{store: store, recorder: recorder, dnsSuffix: dnsSuffix, defaults: defaults},
		Deployment:      &DeploymentHandler{store: store, dnsSuffix: dnsSuffix},
		Deployments:     &DeploymentsHandler{store: store},
		SiteDeployments: &SiteDeploymentsHandler{store: store},
		CreateSite:      &CreateSiteHandler{store: store, dnsSuffix: dnsSuffix, defaults: defaults, ensurer: ensurer},
		Feed:            &FeedHandler{store: store, dnsSuffix: dnsSuffix},
		SiteFeed:        &SiteFeedHandler{store: store, dnsSuffix: dnsSuffix},
	}
}

func setupHandlers(t *testing.T) (*testHandlers, *storage.Store) {
	t.Helper()
	store := setupStore(t)
	recorder := setupRecorder(t)
	dnsSuffix := "test.ts.net"
	return newTestHandlers(store, recorder, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{}), store
}

var (
	adminID    = auth.Identity{LoginName: "admin@example.com", DisplayName: "Admin"}
	adminCaps  = []auth.Cap{{Access: "admin"}}
	viewerID   = auth.Identity{LoginName: "user@example.com"}
	viewerCaps = []auth.Cap{{Access: "view", Sites: []string{"docs"}}}
)

// region SitesHandler

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

// region SiteHandler

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
	store.WriteDeploymentConfig("docs", "aaa11111", storage.SiteConfig{Analytics: &analytics})

	dnsSuffix := "test.ts.net"
	hs := newTestHandlers(store, recorder, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{})
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

// region DeploymentHandler

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
	hs := newTestHandlers(store, nil, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{})
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
	hs := newTestHandlers(store, nil, dnsSuffix, &mockEnsurer{}, &mockEnsurer{}, storage.SiteConfig{})
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

// region DeploymentsHandler

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

// region CreateSiteHandler

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
	hs := newTestHandlers(store, nil, dnsSuffix, mock, mock, storage.SiteConfig{})
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

// mockChecker implements SiteHealthChecker for testing.
type mockChecker struct {
	running map[string]bool
}

func (m *mockChecker) IsRunning(site string) bool { return m.running[site] }

// region SiteDeploymentsHandler

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

