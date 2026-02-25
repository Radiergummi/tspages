package serve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

func setupSite(t *testing.T, store *storage.Store, site, id string, files map[string]string) {
	t.Helper()
	dir, err := store.CreateDeployment(site, id)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		path := filepath.Join(dir, "content", name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	store.MarkComplete(site, id)
	store.ActivateDeployment(site, id)
}

func withCaps(r *http.Request, caps []auth.Cap) *http.Request {
	return r.WithContext(auth.ContextWithCaps(r.Context(), caps))
}

func TestHandler_ServesFile(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Docs</h1>",
		"style.css":  "body{}",
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/style.css", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "style.css")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "body{}" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestHandler_IndexFallback(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Index</h1>",
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Forbidden(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "hi",
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/index.html", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"other"}}})
	req.SetPathValue("path", "index.html")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestHandler_ETag(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"style.css": "body{}",
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/style.css", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "style.css")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag header")
	}
	if etag != `"aaa11111:style.css"` {
		t.Errorf("ETag = %q, want %q", etag, `"aaa11111:style.css"`)
	}
}

func TestHandler_ETag_NotModified(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"style.css": "body{}",
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})

	// First request to get the ETag
	req := httptest.NewRequest("GET", "/style.css", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "style.css")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	etag := rec.Header().Get("ETag")

	// Second request with If-None-Match
	req2 := httptest.NewRequest("GET", "/style.css", nil)
	req2 = withCaps(req2, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req2.SetPathValue("path", "style.css")
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", rec2.Code)
	}
}

func TestHandler_ETag_ChangesOnNewDeployment(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"style.css": "body{}",
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/style.css", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "style.css")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	etag1 := rec.Header().Get("ETag")

	// Deploy a new version
	setupSite(t, store, "docs", "bbb22222", map[string]string{
		"style.css": "body{color:red}",
	})

	req2 := httptest.NewRequest("GET", "/style.css", nil)
	req2 = withCaps(req2, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req2.SetPathValue("path", "style.css")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	etag2 := rec2.Header().Get("ETag")

	if etag1 == etag2 {
		t.Errorf("ETag should change after new deployment, both = %q", etag1)
	}

	// Old ETag should not produce 304
	req3 := httptest.NewRequest("GET", "/style.css", nil)
	req3 = withCaps(req3, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req3.SetPathValue("path", "style.css")
	req3.Header.Set("If-None-Match", etag1)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusOK {
		t.Errorf("stale ETag should get 200, got %d", rec3.Code)
	}
}

func TestHandler_404_Custom(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Docs</h1>",
		"404.html":   "<h1>Custom Not Found</h1>",
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/nope", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "nope")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Custom Not Found") {
		t.Errorf("expected custom 404 content, got: %s", rec.Body.String())
	}
}

func TestHandler_404_Default(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Docs</h1>",
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/nope", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "nope")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "404") {
		t.Error("default 404 should contain '404'")
	}
	if !strings.Contains(body, "doesn't exist") {
		t.Error("default 404 should explain page doesn't exist")
	}
}

func TestHandler_NoDeployment_Placeholder(t *testing.T) {
	store := storage.New(t.TempDir())

	h := NewHandler(store, "mysite", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/index.html", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "index.html")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "mysite") {
		t.Error("placeholder should mention the site name")
	}
	if !strings.Contains(body, "no deployment yet") {
		t.Error("placeholder should explain there is no deployment")
	}
	if !strings.Contains(body, "/deploy/mysite") {
		t.Error("placeholder should show the deploy command")
	}
}

func TestHandler_SPA_FallbackToIndex(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("app", "aaa11111")
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "index.html"), []byte("<h1>SPA App</h1>"), 0644)
	os.WriteFile(filepath.Join(contentDir, "style.css"), []byte("body{}"), 0644)
	store.MarkComplete("app", "aaa11111")
	store.ActivateDeployment("app", "aaa11111")

	spa := true
	store.WriteSiteConfig("app", "aaa11111", storage.SiteConfig{SPA: &spa})

	h := NewHandler(store, "app", nil, storage.SiteConfig{})

	req := httptest.NewRequest("GET", "/dashboard/settings", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "dashboard/settings")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "SPA App") {
		t.Errorf("expected index.html content, got: %s", rec.Body.String())
	}
}

func TestHandler_SPA_RealFileStillServed(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("app", "aaa11111")
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "index.html"), []byte("<h1>SPA</h1>"), 0644)
	os.WriteFile(filepath.Join(contentDir, "style.css"), []byte("body{}"), 0644)
	store.MarkComplete("app", "aaa11111")
	store.ActivateDeployment("app", "aaa11111")

	spa := true
	store.WriteSiteConfig("app", "aaa11111", storage.SiteConfig{SPA: &spa})

	h := NewHandler(store, "app", nil, storage.SiteConfig{})

	req := httptest.NewRequest("GET", "/style.css", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "style.css")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != "body{}" {
		t.Errorf("body = %q, want body{}", rec.Body.String())
	}
}

func TestHandler_SPA_CustomIndexPage(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("app", "aaa11111")
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "app.html"), []byte("<h1>Custom Index</h1>"), 0644)
	store.MarkComplete("app", "aaa11111")
	store.ActivateDeployment("app", "aaa11111")

	spa := true
	store.WriteSiteConfig("app", "aaa11111", storage.SiteConfig{SPA: &spa, IndexPage: "app.html"})

	h := NewHandler(store, "app", nil, storage.SiteConfig{})

	req := httptest.NewRequest("GET", "/any/route", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "any/route")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Custom Index") {
		t.Errorf("expected custom index content, got: %s", rec.Body.String())
	}
}

func TestHandler_SPA_Disabled(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Docs</h1>",
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})

	req := httptest.NewRequest("GET", "/nonexistent", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "nonexistent")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when SPA is disabled", rec.Code)
	}
}

func TestHandler_SPA_FromDefaults(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("app", "aaa11111")
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "index.html"), []byte("<h1>SPA from defaults</h1>"), 0644)
	store.MarkComplete("app", "aaa11111")
	store.ActivateDeployment("app", "aaa11111")

	spa := true
	defaults := storage.SiteConfig{SPA: &spa}
	h := NewHandler(store, "app", nil, defaults)

	req := httptest.NewRequest("GET", "/some/route", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "some/route")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SPA from defaults)", rec.Code)
	}
}

func TestHandler_CustomNotFoundPage(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("docs", "aaa11111")
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(filepath.Join(contentDir, "errors"), 0755)
	os.WriteFile(filepath.Join(contentDir, "index.html"), []byte("<h1>Docs</h1>"), 0644)
	os.WriteFile(filepath.Join(contentDir, "errors", "404.html"), []byte("<h1>Custom Error</h1>"), 0644)
	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")

	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{NotFoundPage: "errors/404.html"})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})

	req := httptest.NewRequest("GET", "/nonexistent", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "nonexistent")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Custom Error") {
		t.Errorf("expected custom error content, got: %s", rec.Body.String())
	}
}

func TestHandler_CustomHeaders(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("docs", "aaa11111")
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "index.html"), []byte("<h1>Docs</h1>"), 0644)
	os.WriteFile(filepath.Join(contentDir, "style.css"), []byte("body{}"), 0644)
	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")

	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{
		Headers: map[string]map[string]string{
			"/*":     {"X-Frame-Options": "DENY"},
			"/*.css": {"Cache-Control": "public, max-age=86400"},
		},
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})

	// HTML file should get X-Frame-Options from /*
	req := httptest.NewRequest("GET", "/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", rec.Header().Get("X-Frame-Options"))
	}

	// CSS file should get both /* and /*.css headers
	req2 := httptest.NewRequest("GET", "/style.css", nil)
	req2 = withCaps(req2, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req2.SetPathValue("path", "style.css")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)

	if rec2.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("CSS X-Frame-Options = %q, want DENY", rec2.Header().Get("X-Frame-Options"))
	}
	if rec2.Header().Get("Cache-Control") != "public, max-age=86400" {
		t.Errorf("CSS Cache-Control = %q", rec2.Header().Get("Cache-Control"))
	}
}

func TestHandler_CustomHeaders_FromDefaults(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Docs</h1>",
	})

	defaults := storage.SiteConfig{
		Headers: map[string]map[string]string{
			"/*": {"X-Frame-Options": "SAMEORIGIN"},
		},
	}
	h := NewHandler(store, "docs", nil, defaults)

	req := httptest.NewRequest("GET", "/index.html", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "index.html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q, want SAMEORIGIN", rec.Header().Get("X-Frame-Options"))
	}
}

func TestMatchHeaderPath(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"/*", "/index.html", true},
		{"/*", "/foo/bar", true},
		{"/*.css", "/style.css", true},
		{"/*.css", "/assets/style.css", true},
		{"/*.css", "/index.html", false},
		{"/assets/*", "/assets/style.css", true},
		{"/assets/*", "/index.html", false},
		{"/index.html", "/index.html", true},
		{"/index.html", "/other.html", false},
	}
	for _, tt := range tests {
		got := matchHeaderPath(tt.pattern, tt.path)
		if got != tt.want {
			t.Errorf("matchHeaderPath(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestHandler_FullConfig_Integration(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("app", "int11111")
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(filepath.Join(contentDir, "errors"), 0755)
	os.WriteFile(filepath.Join(contentDir, "index.html"), []byte("<h1>My SPA</h1>"), 0644)
	os.WriteFile(filepath.Join(contentDir, "style.css"), []byte("body{}"), 0644)
	os.WriteFile(filepath.Join(contentDir, "errors", "404.html"), []byte("<h1>Custom 404</h1>"), 0644)
	store.MarkComplete("app", "int11111")
	store.ActivateDeployment("app", "int11111")

	spa := true
	store.WriteSiteConfig("app", "int11111", storage.SiteConfig{
		SPA:          &spa,
		NotFoundPage: "errors/404.html",
		Headers: map[string]map[string]string{
			"/*":     {"X-Content-Type-Options": "nosniff"},
			"/*.css": {"Cache-Control": "public, max-age=86400"},
		},
	})

	h := NewHandler(store, "app", nil, storage.SiteConfig{})

	// 1. Normal file serves correctly with headers
	req := httptest.NewRequest("GET", "/style.css", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "style.css")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("css: status = %d", rec.Code)
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=86400" {
		t.Errorf("css: Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("css: X-Content-Type-Options = %q", rec.Header().Get("X-Content-Type-Options"))
	}

	// 2. SPA fallback for unknown path
	req2 := httptest.NewRequest("GET", "/dashboard", nil)
	req2 = withCaps(req2, []auth.Cap{{Access: "view"}})
	req2.SetPathValue("path", "dashboard")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("spa: status = %d", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "My SPA") {
		t.Error("spa: expected index.html content")
	}
	if rec2.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("spa: X-Content-Type-Options = %q", rec2.Header().Get("X-Content-Type-Options"))
	}
}

func TestHandler_Placeholder_WithDNSSuffix(t *testing.T) {
	store := storage.New(t.TempDir())
	suffix := "example.ts.net"

	h := NewHandler(store, "docs", &suffix, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "https://pages.example.ts.net") {
		t.Errorf("placeholder should use FQDN control plane URL, got:\n%s", body)
	}
}

func TestHandler_AnalyticsEnabled_Default(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{"index.html": "hi"})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})

	// Trigger config load by serving a request.
	req := httptest.NewRequest("GET", "/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !h.AnalyticsEnabled() {
		t.Error("analytics should be enabled by default (nil)")
	}
}

func TestHandler_AnalyticsEnabled_Disabled(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{"index.html": "hi"})
	analytics := false
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{Analytics: &analytics})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})

	req := httptest.NewRequest("GET", "/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if h.AnalyticsEnabled() {
		t.Error("analytics should be disabled")
	}
}

func TestHandler_AnalyticsEnabled_FromDefaults(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{"index.html": "hi"})

	analytics := false
	defaults := storage.SiteConfig{Analytics: &analytics}
	h := NewHandler(store, "docs", nil, defaults)

	req := httptest.NewRequest("GET", "/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if h.AnalyticsEnabled() {
		t.Error("analytics should be disabled via defaults")
	}
}

func TestHandler_AnalyticsEnabled_NoDeployment(t *testing.T) {
	store := storage.New(t.TempDir())
	// Site exists but has no deployment — serve handler shows placeholder.
	store.CreateSite("docs")

	analytics := false
	defaults := storage.SiteConfig{Analytics: &analytics}
	h := NewHandler(store, "docs", nil, defaults)

	// AnalyticsEnabled should respect defaults even before any request.
	if h.AnalyticsEnabled() {
		t.Error("analytics should be disabled via defaults even with no deployment")
	}
}

func TestMatchRedirect(t *testing.T) {
	tests := []struct {
		name   string
		rule   storage.RedirectRule
		path   string
		want   string
		wantOK bool
	}{
		{"exact match", storage.RedirectRule{From: "/old", To: "/new"}, "/old", "/new", true},
		{"exact no match", storage.RedirectRule{From: "/old", To: "/new"}, "/other", "", false},
		{"named param", storage.RedirectRule{From: "/blog/:slug", To: "/posts/:slug"}, "/blog/hello", "/posts/hello", true},
		{"named param no match", storage.RedirectRule{From: "/blog/:slug", To: "/posts/:slug"}, "/other/hello", "", false},
		{"named param too few segments", storage.RedirectRule{From: "/blog/:slug", To: "/posts/:slug"}, "/blog", "", false},
		{"named param too many segments", storage.RedirectRule{From: "/blog/:slug", To: "/posts/:slug"}, "/blog/a/b", "", false},
		{"multiple params", storage.RedirectRule{From: "/a/:x/b/:y", To: "/c/:y/:x"}, "/a/1/b/2", "/c/2/1", true},
		{"splat", storage.RedirectRule{From: "/docs/*", To: "/v2/docs/*"}, "/docs/getting-started", "/v2/docs/getting-started", true},
		{"splat deep", storage.RedirectRule{From: "/docs/*", To: "/v2/*"}, "/docs/a/b/c", "/v2/a/b/c", true},
		{"splat root", storage.RedirectRule{From: "/docs/*", To: "/v2/*"}, "/docs/", "/v2/", true},
		{"external", storage.RedirectRule{From: "/ext", To: "https://example.com"}, "/ext", "https://example.com", true},
		{"value contains colon param", storage.RedirectRule{From: "/a/:x/:y", To: "/b/:x/:y"}, "/a/:y/hello", "/b/:y/hello", true},
		{"splat no trailing slash", storage.RedirectRule{From: "/docs/*", To: "/v2/*"}, "/docs", "/v2/", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := matchRedirect(tt.rule, tt.path)
			if ok != tt.wantOK {
				t.Errorf("matched = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("target = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandler_Redirect_Exact(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Docs</h1>",
	})
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{
		Redirects: []storage.RedirectRule{
			{From: "/old", To: "/new", Status: 302},
		},
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/old", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "old")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/new" {
		t.Errorf("Location = %q, want /new", loc)
	}
}

func TestHandler_Redirect_NamedParam(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "hi",
	})
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{
		Redirects: []storage.RedirectRule{
			{From: "/blog/:slug", To: "/posts/:slug"},
		},
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/blog/hello-world", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "blog/hello-world")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/posts/hello-world" {
		t.Errorf("Location = %q, want /posts/hello-world", loc)
	}
}

func TestHandler_Redirect_Splat(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "hi",
	})
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{
		Redirects: []storage.RedirectRule{
			{From: "/docs/*", To: "/v2/docs/*"},
		},
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/docs/getting-started", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "docs/getting-started")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/v2/docs/getting-started" {
		t.Errorf("Location = %q, want /v2/docs/getting-started", loc)
	}
}

func TestHandler_Redirect_FirstMatchWins(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "hi",
	})
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{
		Redirects: []storage.RedirectRule{
			{From: "/a", To: "/first", Status: 302},
			{From: "/a", To: "/second", Status: 302},
		},
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/a", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "a")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if loc := rec.Header().Get("Location"); loc != "/first" {
		t.Errorf("Location = %q, want /first (first match wins)", loc)
	}
}

func TestHandler_Redirect_NoMatchServesFile(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Docs</h1>",
		"about.html": "<h1>About</h1>",
	})
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{
		Redirects: []storage.RedirectRule{
			{From: "/old", To: "/new"},
		},
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/about.html", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "about.html")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no redirect match)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "About") {
		t.Error("should serve the file normally when no redirect matches")
	}
}

func TestHandler_Redirect_FromDefaults(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "hi",
	})

	defaults := storage.SiteConfig{
		Redirects: []storage.RedirectRule{
			{From: "/old", To: "/new", Status: 301},
		},
	}
	h := NewHandler(store, "docs", nil, defaults)

	req := httptest.NewRequest("GET", "/old", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "old")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301 (redirect from defaults)", rec.Code)
	}
}
