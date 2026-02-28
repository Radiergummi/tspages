package serve

import (
	"compress/gzip"
	"io"
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

func TestHandler_PathTraversal_Blocked(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Docs</h1>",
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})

	for _, p := range []string{"../secret", "foo/../../etc/passwd", "..", "....//....//etc/passwd"} {
		req := httptest.NewRequest("GET", "/"+p, nil)
		req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
		req.SetPathValue("path", p)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("path %q: got %d, want 404", p, rec.Code)
		}
	}
}

func TestHandler_ServesFile(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Docs</h1>",
		"style.css":  "body{}",
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})

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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
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

	h := NewHandler(store, "mysite", "", storage.SiteConfig{})
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
	store.WriteSiteConfig("app", "aaa11111", storage.SiteConfig{SPARouting: &spa})

	h := NewHandler(store, "app", "", storage.SiteConfig{})

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
	store.WriteSiteConfig("app", "aaa11111", storage.SiteConfig{SPARouting: &spa})

	h := NewHandler(store, "app", "", storage.SiteConfig{})

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
	store.WriteSiteConfig("app", "aaa11111", storage.SiteConfig{SPARouting: &spa, IndexPage: "app.html"})

	h := NewHandler(store, "app", "", storage.SiteConfig{})

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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})

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
	defaults := storage.SiteConfig{SPARouting: &spa}
	h := NewHandler(store, "app", "", defaults)

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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})

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

func TestHandler_NotFoundPage_SymlinkEscape(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("docs", "aaa11111")
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "index.html"), []byte("<h1>Docs</h1>"), 0644)

	// Create a file outside the site root.
	externalDir := t.TempDir()
	os.WriteFile(filepath.Join(externalDir, "secret.html"), []byte("<h1>Secret</h1>"), 0644)

	// Symlink inside content pointing outside.
	os.Symlink(filepath.Join(externalDir, "secret.html"), filepath.Join(contentDir, "evil404.html"))

	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{NotFoundPage: "evil404.html"})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})

	req := httptest.NewRequest("GET", "/nonexistent", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "nonexistent")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "Secret") {
		t.Error("served external file via symlink escape in NotFoundPage — containment check failed")
	}
}

func TestHandler_CleanURL_SymlinkEscape(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("docs", "aaa11111")
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "index.html"), []byte("<h1>Docs</h1>"), 0644)

	// Create a file outside the site root.
	externalDir := t.TempDir()
	os.WriteFile(filepath.Join(externalDir, "secret.html"), []byte("SECRET DATA"), 0644)

	// Symlink: content/secret.html → external secret.html
	os.Symlink(filepath.Join(externalDir, "secret.html"), filepath.Join(contentDir, "secret.html"))

	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")

	h := NewHandler(store, "docs", "", storage.SiteConfig{})

	// Request /secret — clean URL fallback should try secret.html (the symlink)
	// but the containment check must block it.
	req := httptest.NewRequest("GET", "/secret", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "secret")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "SECRET DATA") {
		t.Error("served external file via symlink escape in clean URL .html fallback")
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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})

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
	h := NewHandler(store, "docs", "", defaults)

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
		SPARouting:   &spa,
		NotFoundPage: "errors/404.html",
		Headers: map[string]map[string]string{
			"/*":     {"X-Content-Type-Options": "nosniff"},
			"/*.css": {"Cache-Control": "public, max-age=86400"},
		},
	})

	h := NewHandler(store, "app", "", storage.SiteConfig{})

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
	h := NewHandler(store, "docs", "example.ts.net", storage.SiteConfig{})
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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})

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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})

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
	h := NewHandler(store, "docs", "", defaults)

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
	h := NewHandler(store, "docs", "", defaults)

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
			got, ok := matchRedirect(tt.rule, strings.Split(tt.path, "/"))
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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
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

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
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
		"style.css":  "body{}",
	})
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{
		Redirects: []storage.RedirectRule{
			{From: "/old", To: "/new"},
		},
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/style.css", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "style.css")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no redirect match)", rec.Code)
	}
	if rec.Body.String() != "body{}" {
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
	h := NewHandler(store, "docs", "", defaults)

	req := httptest.NewRequest("GET", "/old", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "old")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301 (redirect from defaults)", rec.Code)
	}
}

// --- Cache-Control ---

func TestHandler_CacheControl_HTML(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Docs</h1>",
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, no-cache, stale-while-revalidate=60" {
		t.Errorf("Cache-Control = %q, want %q", cc, "public, no-cache, stale-while-revalidate=60")
	}
}

func TestHandler_CacheControl_RegularAsset(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"style.css": "body{}",
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/style.css", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "style.css")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=3600, stale-while-revalidate=120" {
		t.Errorf("Cache-Control = %q, want %q", cc, "public, max-age=3600, stale-while-revalidate=120")
	}
}

func TestHandler_CacheControl_HashedAsset(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"main.a1b2c3d4.js": "console.log('hi')",
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/main.a1b2c3d4.js", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "main.a1b2c3d4.js")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	want := "public, max-age=31536000, immutable"
	if cc := rec.Header().Get("Cache-Control"); cc != want {
		t.Errorf("Cache-Control = %q, want %q", cc, want)
	}
}

func TestHandler_CacheControl_UserOverride(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("docs", "aaa11111")
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "style.css"), []byte("body{}"), 0644)
	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")

	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{
		Headers: map[string]map[string]string{
			"/*.css": {"Cache-Control": "public, max-age=86400"},
		},
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/style.css", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "style.css")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// User config should override the default "public, max-age=3600".
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Errorf("Cache-Control = %q, want user override %q", cc, "public, max-age=86400")
	}
}

func TestHasContentHash(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"main.a1b2c3d4.js", true},
		{"index-BdH3bPq2.js", true},
		{"style-abc12345.css", true},
		{"app.DfG4h5J6k7.css", true},
		{"assets/main.a1b2c3d4.js", true},
		{"style.css", false},
		{"index.html", false},
		{"dashboard.js", false},      // all letters, no digits
		{"12345678.js", false},       // all digits, no letters
		{"ab12.js", false},           // too short
		{"readme.txt", false},        // no hash segment
		{"some-file-name.js", false}, // segments too short
	}
	for _, tt := range tests {
		if got := hasContentHash(tt.name); got != tt.want {
			t.Errorf("hasContentHash(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// --- Compression ---

func TestHandler_Gzip_CompressesHTML(t *testing.T) {
	store := storage.New(t.TempDir())
	// Need enough content to exceed compressMinBytes (256).
	content := strings.Repeat("<p>Hello world</p>\n", 30)
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": content,
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "")
	req.Header.Set("Accept-Encoding", "gzip, deflate")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", ce)
	}
	if rec.Header().Get("Content-Length") != "" {
		t.Error("Content-Length should be removed when compressing")
	}
	if rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("Vary = %q, want Accept-Encoding", rec.Header().Get("Vary"))
	}

	// Verify the body is valid gzip that decompresses to the original content.
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gr.Close()
	body, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("reading gzip body: %v", err)
	}
	if string(body) != content {
		t.Errorf("decompressed body length = %d, want %d", len(body), len(content))
	}
}

func TestHandler_Gzip_SkipsImages(t *testing.T) {
	store := storage.New(t.TempDir())
	// Fake PNG content (doesn't need to be valid, just large enough).
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"image.png": strings.Repeat("x", 1000),
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/image.png", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "image.png")
	req.Header.Set("Accept-Encoding", "gzip")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("images should not be compressed")
	}
}

func TestHandler_Gzip_SkipsSmallFiles(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"tiny.txt": "<p>hi</p>",
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/tiny.txt", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "tiny.txt")
	req.Header.Set("Accept-Encoding", "gzip")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("files < 256 bytes should not be compressed")
	}
	// Vary should still be set for compressible types.
	if rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("Vary = %q, want Accept-Encoding even for small compressible files", rec.Header().Get("Vary"))
	}
}

func TestHandler_Gzip_SkipsWithoutAcceptEncoding(t *testing.T) {
	store := storage.New(t.TempDir())
	content := strings.Repeat("<p>Hello</p>\n", 50)
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": content,
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "")
	// No Accept-Encoding header.

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("should not compress when client doesn't accept gzip")
	}
	if rec.Body.String() != content {
		t.Error("body should be uncompressed original content")
	}
}

func TestHandler_Gzip_304NotCompressed(t *testing.T) {
	store := storage.New(t.TempDir())
	content := strings.Repeat("<p>Hello</p>\n", 50)
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": content,
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})

	// First request to get ETag.
	req := httptest.NewRequest("GET", "/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "")
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	etag := rec.Header().Get("ETag")

	// Second request with If-None-Match — should get 304, no body.
	req2 := httptest.NewRequest("GET", "/", nil)
	req2 = withCaps(req2, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req2.SetPathValue("path", "")
	req2.Header.Set("Accept-Encoding", "gzip")
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec2.Code)
	}
	if rec2.Header().Get("Content-Encoding") == "gzip" {
		t.Error("304 response should not have Content-Encoding")
	}
}

func TestHandler_Gzip_CompressesCSS(t *testing.T) {
	store := storage.New(t.TempDir())
	content := strings.Repeat("body { color: red; }\n", 30)
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"style.css": content,
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/style.css", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "style.css")
	req.Header.Set("Accept-Encoding", "gzip")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Error("CSS should be compressed")
	}

	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gr.Close()
	body, _ := io.ReadAll(gr)
	if string(body) != content {
		t.Errorf("decompressed length = %d, want %d", len(body), len(content))
	}
}

// --- Precompressed Assets ---

func TestHandler_Precompressed_PrefersBrotli(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"style.css":    "original-css",
		"style.css.br": "brotli-compressed",
		"style.css.gz": "gzip-compressed",
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/style.css", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "style.css")
	req.Header.Set("Accept-Encoding", "gzip, br")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "br" {
		t.Errorf("Content-Encoding = %q, want br", ce)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", ct)
	}
	if rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("Vary = %q, want Accept-Encoding", rec.Header().Get("Vary"))
	}
	if body := rec.Body.String(); body != "brotli-compressed" {
		t.Errorf("body = %q, want brotli-compressed", body)
	}
}

func TestHandler_Precompressed_GzipFallback(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"style.css":    "original-css",
		"style.css.gz": "gzip-compressed",
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/style.css", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "style.css")
	req.Header.Set("Accept-Encoding", "gzip, br")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", ce)
	}
	if body := rec.Body.String(); body != "gzip-compressed" {
		t.Errorf("body = %q, want gzip-compressed", body)
	}
}

func TestHandler_Precompressed_OnTheFlyFallback(t *testing.T) {
	store := storage.New(t.TempDir())
	content := strings.Repeat("body { color: red; }\n", 30)
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"style.css": content,
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/style.css", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "style.css")
	req.Header.Set("Accept-Encoding", "gzip")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip (on-the-fly)", ce)
	}
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gr.Close()
	body, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("reading gzip body: %v", err)
	}
	if string(body) != content {
		t.Errorf("decompressed length = %d, want %d", len(body), len(content))
	}
}

func TestHandler_Precompressed_NoEncoding(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"style.css":    "original-css",
		"style.css.br": "brotli-compressed",
		"style.css.gz": "gzip-compressed",
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/style.css", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "style.css")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("Content-Encoding = %q, want empty", ce)
	}
	if body := rec.Body.String(); body != "original-css" {
		t.Errorf("body = %q, want original-css", body)
	}
}

// --- Directory Listing ---

func TestHandler_DirectoryListing_Enabled(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("files", "aaa11111")
	contentDir := filepath.Join(dir, "content", "docs")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "readme.txt"), []byte("hello"), 0644)
	os.WriteFile(filepath.Join(contentDir, "guide.pdf"), []byte("pdf"), 0644)
	store.MarkComplete("files", "aaa11111")
	store.ActivateDeployment("files", "aaa11111")

	dl := true
	store.WriteSiteConfig("files", "aaa11111", storage.SiteConfig{DirectoryListing: &dl})

	h := NewHandler(store, "files", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/docs/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "readme.txt") {
		t.Error("listing should contain readme.txt")
	}
	if !strings.Contains(body, "guide.pdf") {
		t.Error("listing should contain guide.pdf")
	}
	if !strings.Contains(body, "Index of") {
		t.Error("listing should contain 'Index of' heading")
	}
}

func TestHandler_DirectoryListing_Disabled(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("files", "aaa11111")
	contentDir := filepath.Join(dir, "content", "docs")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "readme.txt"), []byte("hello"), 0644)
	store.MarkComplete("files", "aaa11111")
	store.ActivateDeployment("files", "aaa11111")

	// directory_listing defaults to nil (off)
	h := NewHandler(store, "files", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/docs/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when directory listing disabled", rec.Code)
	}
}

func TestHandler_DirectoryListing_IndexTakesPrecedence(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("files", "aaa11111")
	contentDir := filepath.Join(dir, "content", "docs")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "index.html"), []byte("<h1>Docs Index</h1>"), 0644)
	os.WriteFile(filepath.Join(contentDir, "readme.txt"), []byte("hello"), 0644)
	store.MarkComplete("files", "aaa11111")
	store.ActivateDeployment("files", "aaa11111")

	dl := true
	store.WriteSiteConfig("files", "aaa11111", storage.SiteConfig{DirectoryListing: &dl})

	h := NewHandler(store, "files", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/docs/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Docs Index") {
		t.Error("should serve index.html, not directory listing")
	}
	if strings.Contains(body, "Index of") {
		t.Error("should not render directory listing when index exists")
	}
}

func TestHandler_DirectoryListing_FromDefaults(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("files", "aaa11111")
	contentDir := filepath.Join(dir, "content", "docs")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "file.txt"), []byte("hi"), 0644)
	store.MarkComplete("files", "aaa11111")
	store.ActivateDeployment("files", "aaa11111")

	dl := true
	defaults := storage.SiteConfig{DirectoryListing: &dl}
	h := NewHandler(store, "files", "", defaults)

	req := httptest.NewRequest("GET", "/docs/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (directory listing from defaults)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "file.txt") {
		t.Error("listing should show file.txt from defaults config")
	}
}

func TestHandler_DirectoryListing_ParentLink(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("files", "aaa11111")
	contentDir := filepath.Join(dir, "content", "a", "b")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "file.txt"), []byte("hi"), 0644)
	store.MarkComplete("files", "aaa11111")
	store.ActivateDeployment("files", "aaa11111")

	dl := true
	store.WriteSiteConfig("files", "aaa11111", storage.SiteConfig{DirectoryListing: &dl})

	h := NewHandler(store, "files", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/a/b/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "a/b")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "..") {
		t.Error("nested directory should show parent link")
	}
}

// --- Trailing Slash ---

func TestCheckTrailingSlash(t *testing.T) {
	tests := []struct {
		path string
		mode string
		want string
		ok   bool
	}{
		{"/about", "add", "/about/", true},
		{"/about/", "add", "", false},    // already has slash
		{"/style.css", "add", "", false}, // file extension
		{"/", "add", "", false},          // root
		{"/about/", "remove", "/about", true},
		{"/about", "remove", "", false}, // no trailing slash
		{"/", "remove", "", false},      // root
		{"/about", "", "", false},       // disabled
		{"/about/", "", "", false},      // disabled
		{"/docs/api", "add", "/docs/api/", true},
		{"/docs/api/", "remove", "/docs/api", true},
	}
	for _, tt := range tests {
		got, ok := checkTrailingSlash(tt.path, tt.mode)
		if ok != tt.ok || got != tt.want {
			t.Errorf("checkTrailingSlash(%q, %q) = (%q, %v), want (%q, %v)",
				tt.path, tt.mode, got, ok, tt.want, tt.ok)
		}
	}
}

func TestHandler_TrailingSlash_Add(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("docs", "aaa11111")
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(filepath.Join(contentDir, "about"), 0755)
	os.WriteFile(filepath.Join(contentDir, "about", "index.html"), []byte("<h1>About</h1>"), 0644)
	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")

	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{TrailingSlash: "add"})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/about", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "about")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/about/" {
		t.Errorf("Location = %q, want /about/", loc)
	}
}

func TestHandler_TrailingSlash_Add_SkipsFileExtensions(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"style.css": "body{}",
	})
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{TrailingSlash: "add"})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/style.css", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "style.css")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (file extension should not be redirected)", rec.Code)
	}
}

func TestHandler_TrailingSlash_Remove(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"about.html": "<h1>About</h1>",
	})
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{TrailingSlash: "remove"})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/about/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "about/")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/about" {
		t.Errorf("Location = %q, want /about", loc)
	}
}

func TestHandler_TrailingSlash_Remove_RootUnchanged(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Docs</h1>",
	})
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{TrailingSlash: "remove"})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (root should not be redirected)", rec.Code)
	}
}

func TestHandler_TrailingSlash_FromDefaults(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "hi",
	})

	defaults := storage.SiteConfig{TrailingSlash: "add"}
	h := NewHandler(store, "docs", "", defaults)

	req := httptest.NewRequest("GET", "/about", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "about")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301 (trailing slash from defaults)", rec.Code)
	}
}

// --- Clean URLs ---

func TestHandler_CleanURL_Fallback(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Home</h1>",
		"about.html": "<h1>About</h1>",
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/about", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "about")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "About") {
		t.Errorf("expected about.html content, got: %s", rec.Body.String())
	}
}

func TestHandler_CleanURL_CanonicalRedirect(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Home</h1>",
		"about.html": "<h1>About</h1>",
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/about.html", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "about.html")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/about" {
		t.Errorf("Location = %q, want /about", loc)
	}
}

func TestHandler_CleanURL_ExactFileWins(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Home</h1>",
		"about":      "exact file",
		"about.html": "<h1>About HTML</h1>",
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/about", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "about")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "exact file" {
		t.Errorf("expected exact file content, got: %s", rec.Body.String())
	}
}

func TestHandler_CleanURL_DirectoryWins(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("docs", "aaa11111")
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(filepath.Join(contentDir, "about"), 0755)
	os.WriteFile(filepath.Join(contentDir, "about", "index.html"), []byte("<h1>About Index</h1>"), 0644)
	os.WriteFile(filepath.Join(contentDir, "about.html"), []byte("<h1>About HTML</h1>"), 0644)
	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/about", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "about")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "About Index") {
		t.Errorf("directory index should win over .html fallback, got: %s", rec.Body.String())
	}
}

func TestHandler_CleanURL_Disabled(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Home</h1>",
		"about.html": "<h1>About</h1>",
	})
	htmlExt := true
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{HTMLExtensions: &htmlExt})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})

	// With html_extensions = true, /about should 404 (no clean URL fallback)
	req := httptest.NewRequest("GET", "/about", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "about")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when html_extensions=true", rec.Code)
	}

	// /about.html should serve normally (no redirect)
	req2 := httptest.NewRequest("GET", "/about.html", nil)
	req2 = withCaps(req2, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req2.SetPathValue("path", "about.html")

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when html_extensions=true", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "About") {
		t.Error("should serve about.html directly when html_extensions=true")
	}
}

func TestHandler_CleanURL_NestedPath(t *testing.T) {
	store := storage.New(t.TempDir())
	dir, _ := store.CreateDeployment("docs", "aaa11111")
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(filepath.Join(contentDir, "docs"), 0755)
	os.WriteFile(filepath.Join(contentDir, "docs", "setup.html"), []byte("<h1>Setup Guide</h1>"), 0644)
	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")

	h := NewHandler(store, "docs", "", storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/docs/setup", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "docs/setup")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Setup Guide") {
		t.Errorf("expected setup.html content, got: %s", rec.Body.String())
	}
}

func TestCleanURLRedirect(t *testing.T) {
	tests := []struct {
		path string
		want string
		ok   bool
	}{
		{"/about.html", "/about", true},
		{"/about.htm", "/about", true},
		{"/docs/setup.html", "/docs/setup", true},
		{"/About.HTML", "/About", true},
		{"/index.html", "", false}, // index files excluded
		{"/index.htm", "", false},  // index files excluded
		{"/Index.HTML", "", false}, // case insensitive
		{"/style.css", "", false},  // non-HTML
		{"/about", "", false},      // no extension
		{"/", "", false},           // root
	}
	for _, tt := range tests {
		got, ok := cleanURLRedirect(tt.path)
		if ok != tt.ok || got != tt.want {
			t.Errorf("cleanURLRedirect(%q) = (%q, %v), want (%q, %v)",
				tt.path, got, ok, tt.want, tt.ok)
		}
	}
}

func TestIsCompressible(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"text/html", true},
		{"text/css", true},
		{"text/plain", true},
		{"text/html; charset=utf-8", true},
		{"application/javascript", true},
		{"application/json", true},
		{"application/xml", true},
		{"image/svg+xml", true},
		{"application/wasm", true},
		{"image/png", false},
		{"image/jpeg", false},
		{"application/octet-stream", false},
		{"font/woff2", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isCompressible(tt.ct); got != tt.want {
			t.Errorf("isCompressible(%q) = %v, want %v", tt.ct, got, tt.want)
		}
	}
}

func TestAcceptsEncoding_QValueZero(t *testing.T) {
	tests := []struct {
		name           string
		acceptEncoding string
		wantGzip       bool
		wantBrotli     bool
	}{
		{"gzip q=0 refused", "gzip;q=0, br", false, true},
		{"br q=0 refused", "gzip, br;q=0", true, false},
		{"gzip q=0.0 refused", "gzip;q=0.0", false, false},
		{"gzip q=0.00 refused", "gzip;q=0.00", false, false},
		{"gzip q=0.000 refused", "gzip;q=0.000", false, false},
		{"gzip q=0.001 accepted", "gzip;q=0.001", true, false},
		{"normal accept", "gzip, br", true, true},
		{"only gzip", "gzip", true, false},
		{"empty header", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.acceptEncoding != "" {
				req.Header.Set("Accept-Encoding", tt.acceptEncoding)
			}
			if got := acceptsGzip(req); got != tt.wantGzip {
				t.Errorf("acceptsGzip() = %v, want %v", got, tt.wantGzip)
			}
			if got := acceptsBrotli(req); got != tt.wantBrotli {
				t.Errorf("acceptsBrotli() = %v, want %v", got, tt.wantBrotli)
			}
		})
	}
}

func TestCompressWriter_NonOK_PassesThrough(t *testing.T) {
	// WriteHeader with non-200 should pass through immediately
	// and a second WriteHeader call should be a no-op.
	rec := httptest.NewRecorder()
	cw := &compressWriter{ResponseWriter: rec}

	cw.WriteHeader(http.StatusPartialContent) // 206
	cw.WriteHeader(http.StatusOK)             // should be ignored (headerWritten)

	if rec.Code != http.StatusPartialContent {
		t.Errorf("status = %d, want 206", rec.Code)
	}

	// Close should also be a no-op since header was already written.
	if err := cw.Close(); err != nil {
		t.Errorf("Close error: %v", err)
	}
}

func TestHandler_Gzip_RefusedWithQZero(t *testing.T) {
	store := storage.New(t.TempDir())
	body := strings.Repeat("hello world ", 100)
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": body,
	})

	h := NewHandler(store, "docs", "", storage.SiteConfig{})

	req := httptest.NewRequest("GET", "/index.html", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "index.html")
	req.Header.Set("Accept-Encoding", "gzip;q=0")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce == "gzip" {
		t.Error("response should not be gzip-encoded when q=0")
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{1024*1024*1024 + 512*1024*1024, "1.5 GB"},
	}
	for _, tt := range tests {
		if got := formatBytes(tt.input); got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
