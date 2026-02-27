package deploy

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

type mockManager struct {
	ensured map[string]int
	stopped map[string]int
}

func newMockManager() *mockManager {
	return &mockManager{ensured: make(map[string]int), stopped: make(map[string]int)}
}

func (m *mockManager) EnsureServer(site string) error {
	m.ensured[site]++
	return nil
}

func (m *mockManager) StopServer(site string) error {
	m.stopped[site]++
	return nil
}

var testDNSSuffix = "test.ts.net"

func withCaps(r *http.Request, caps []auth.Cap) *http.Request {
	ctx := auth.ContextWithCaps(r.Context(), caps)
	return r.WithContext(ctx)
}

func withIdentity(r *http.Request, id auth.Identity) *http.Request {
	ctx := auth.ContextWithIdentity(r.Context(), id)
	return r.WithContext(ctx)
}

func TestHandler_Success(t *testing.T) {
	store := storage.New(t.TempDir())
	mgr := newMockManager()
	h := NewHandler(store, mgr, 10, 10, &testDNSSuffix, nil, storage.SiteConfig{})

	body := makeZip(t, map[string]string{"index.html": "<h1>Hi</h1>"})
	req := httptest.NewRequest("POST", "/deploy/docs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/zip")
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp DeployResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Site != "docs" {
		t.Errorf("site = %q, want %q", resp.Site, "docs")
	}
	if resp.DeploymentID == "" {
		t.Error("expected deployment_id")
	}
	if resp.URL != "https://docs.test.ts.net/" {
		t.Errorf("url = %q, want %q", resp.URL, "https://docs.test.ts.net/")
	}
	if mgr.ensured["docs"] != 1 {
		t.Errorf("EnsureServer called %d times, want 1", mgr.ensured["docs"])
	}
}

func TestHandler_Forbidden(t *testing.T) {
	store := storage.New(t.TempDir())
	h := NewHandler(store, newMockManager(), 10, 10, &testDNSSuffix, nil, storage.SiteConfig{})

	body := makeZip(t, map[string]string{"index.html": "hi"})
	req := httptest.NewRequest("POST", "/deploy/docs", bytes.NewReader(body))
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"other"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestHandler_WritesManifest(t *testing.T) {
	store := storage.New(t.TempDir())
	mgr := newMockManager()
	h := NewHandler(store, mgr, 10, 10, &testDNSSuffix, nil, storage.SiteConfig{})

	body := makeZip(t, map[string]string{"index.html": "<h1>Hi</h1>"})
	req := httptest.NewRequest("POST", "/deploy/docs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/zip")
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req = withIdentity(req, auth.Identity{LoginName: "alice@example.com", DisplayName: "Alice"})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp DeployResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	m, err := store.ReadManifest("docs", resp.DeploymentID)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if m.Site != "docs" {
		t.Errorf("manifest site = %q", m.Site)
	}
	if m.ID != resp.DeploymentID {
		t.Errorf("manifest id = %q, want %q", m.ID, resp.DeploymentID)
	}
	if m.CreatedBy != "Alice" {
		t.Errorf("manifest created_by = %q, want %q", m.CreatedBy, "Alice")
	}
	// SizeBytes should reflect extracted content size, not compressed upload size.
	if m.SizeBytes != int64(len("<h1>Hi</h1>")) {
		t.Errorf("manifest size_bytes = %d, want %d", m.SizeBytes, len("<h1>Hi</h1>"))
	}
	if m.CreatedAt.IsZero() {
		t.Error("manifest created_at is zero")
	}
}

func TestHandler_UploadTooLarge(t *testing.T) {
	store := storage.New(t.TempDir())
	// maxUploadMB=1 means 1 MiB limit
	h := NewHandler(store, newMockManager(), 1, 10, &testDNSSuffix, nil, storage.SiteConfig{})

	// Random data doesn't compress — zip body will exceed 1 MiB
	randomData := make([]byte, 2<<20)
	rand.Read(randomData)
	body := makeZip(t, map[string]string{"big.bin": string(randomData)})

	req := httptest.NewRequest("POST", "/deploy/docs", bytes.NewReader(body))
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
}

func TestHandler_ActivateFalse(t *testing.T) {
	store := storage.New(t.TempDir())
	mgr := newMockManager()
	h := NewHandler(store, mgr, 10, 10, &testDNSSuffix, nil, storage.SiteConfig{})

	body := makeZip(t, map[string]string{"index.html": "hi"})
	req := httptest.NewRequest("POST", "/deploy/docs?activate=false", bytes.NewReader(body))
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	_, err := store.CurrentDeployment("docs")
	if err == nil {
		t.Error("expected no active deployment when activate=false")
	}
	if mgr.ensured["docs"] != 0 {
		t.Errorf("EnsureServer should not be called when activate=false, called %d times", mgr.ensured["docs"])
	}
}

func TestHandler_ParsesSiteConfig(t *testing.T) {
	store := storage.New(t.TempDir())
	mgr := newMockManager()
	h := NewHandler(store, mgr, 10, 10, &testDNSSuffix, nil, storage.SiteConfig{})

	body := makeZip(t, map[string]string{
		"index.html":   "<h1>SPA</h1>",
		"tspages.toml": "spa_routing = true\nnot_found_page = \"errors/404.html\"\n",
	})
	req := httptest.NewRequest("POST", "/deploy/docs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/zip")
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp DeployResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	// Config should be stored at deployment level
	cfg, err := store.ReadSiteConfig("docs", resp.DeploymentID)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if cfg.SPARouting == nil || *cfg.SPARouting != true {
		t.Error("spa should be true")
	}
	if cfg.NotFoundPage != "errors/404.html" {
		t.Errorf("not_found_page = %q", cfg.NotFoundPage)
	}

	// tspages.toml should NOT be in content dir
	configInContent := filepath.Join(store.ContentDir("docs", resp.DeploymentID), "tspages.toml")
	if _, err := os.Stat(configInContent); !os.IsNotExist(err) {
		t.Error("tspages.toml should be removed from content dir")
	}
}

func TestHandler_ParsesRedirectsFile(t *testing.T) {
	store := storage.New(t.TempDir())
	mgr := newMockManager()
	h := NewHandler(store, mgr, 10, 10, &testDNSSuffix, nil, storage.SiteConfig{})

	body := makeZip(t, map[string]string{
		"index.html": "<h1>Hi</h1>",
		"_redirects": "/old /new 301\n/blog/:slug /posts/:slug\n",
	})
	req := httptest.NewRequest("POST", "/deploy/docs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/zip")
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp DeployResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	cfg, err := store.ReadSiteConfig("docs", resp.DeploymentID)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if len(cfg.Redirects) != 2 {
		t.Fatalf("redirects = %d, want 2", len(cfg.Redirects))
	}
	if cfg.Redirects[0].From != "/old" || cfg.Redirects[0].To != "/new" || cfg.Redirects[0].Status != 301 {
		t.Errorf("redirect 0 = %+v", cfg.Redirects[0])
	}

	// _redirects should NOT be in content dir
	if _, err := os.Stat(filepath.Join(store.ContentDir("docs", resp.DeploymentID), "_redirects")); !os.IsNotExist(err) {
		t.Error("_redirects should be removed from content dir")
	}
}

func TestHandler_ParsesHeadersFile(t *testing.T) {
	store := storage.New(t.TempDir())
	mgr := newMockManager()
	h := NewHandler(store, mgr, 10, 10, &testDNSSuffix, nil, storage.SiteConfig{})

	body := makeZip(t, map[string]string{
		"index.html": "<h1>Hi</h1>",
		"_headers":   "/*\n  X-Frame-Options: DENY\n",
	})
	req := httptest.NewRequest("POST", "/deploy/docs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/zip")
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp DeployResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	cfg, err := store.ReadSiteConfig("docs", resp.DeploymentID)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if cfg.Headers == nil || cfg.Headers["/*"]["X-Frame-Options"] != "DENY" {
		t.Errorf("headers = %+v", cfg.Headers)
	}

	// _headers should NOT be in content dir
	if _, err := os.Stat(filepath.Join(store.ContentDir("docs", resp.DeploymentID), "_headers")); !os.IsNotExist(err) {
		t.Error("_headers should be removed from content dir")
	}
}

func TestHandler_TomlOverridesNetlifyFiles(t *testing.T) {
	store := storage.New(t.TempDir())
	mgr := newMockManager()
	h := NewHandler(store, mgr, 10, 10, &testDNSSuffix, nil, storage.SiteConfig{})

	body := makeZip(t, map[string]string{
		"index.html":   "<h1>Hi</h1>",
		"_redirects":   "/old /new 301\n",
		"_headers":     "/*\n  X-Frame-Options: DENY\n",
		"tspages.toml": "[[redirects]]\nfrom = \"/old\"\nto = \"/replaced\"\nstatus = 302\n",
	})
	req := httptest.NewRequest("POST", "/deploy/docs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/zip")
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp DeployResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	cfg, err := store.ReadSiteConfig("docs", resp.DeploymentID)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	// toml redirects should completely replace _redirects
	if len(cfg.Redirects) != 1 || cfg.Redirects[0].To != "/replaced" {
		t.Errorf("toml should override _redirects, got %+v", cfg.Redirects)
	}
	// _headers should still apply since toml had no headers
	if cfg.Headers == nil || cfg.Headers["/*"]["X-Frame-Options"] != "DENY" {
		t.Errorf("_headers should still apply, got %+v", cfg.Headers)
	}
}

func TestHandler_InvalidSiteConfig(t *testing.T) {
	store := storage.New(t.TempDir())
	mgr := newMockManager()
	h := NewHandler(store, mgr, 10, 10, &testDNSSuffix, nil, storage.SiteConfig{})

	body := makeZip(t, map[string]string{
		"index.html":   "<h1>Hi</h1>",
		"tspages.toml": `index_page = "../../../etc/passwd"`,
	})
	req := httptest.NewRequest("POST", "/deploy/docs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/zip")
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for invalid site config", rec.Code)
	}
}

func TestHandler_NoSiteConfig(t *testing.T) {
	store := storage.New(t.TempDir())
	mgr := newMockManager()
	h := NewHandler(store, mgr, 10, 10, &testDNSSuffix, nil, storage.SiteConfig{})

	body := makeZip(t, map[string]string{
		"index.html": "<h1>Hi</h1>",
	})
	req := httptest.NewRequest("POST", "/deploy/docs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/zip")
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var resp DeployResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	// No config file — should return zero-value config
	cfg, err := store.ReadSiteConfig("docs", resp.DeploymentID)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if cfg.SPARouting != nil {
		t.Error("spa should be nil when no config")
	}
}
