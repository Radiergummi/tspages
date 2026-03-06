package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

func TestGetSiteConfig_Empty(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateSite("docs")

	h := &SiteConfigHandler{store: store}
	r := reqWithAuth("GET", "/sites/docs/config", []auth.Cap{{Access: "admin"}}, auth.Identity{LoginName: "admin@test"})
	r.SetPathValue("site", "docs")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var cfg storage.SiteConfig
	if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	// All fields should be zero/nil for an empty config.
	if cfg.IndexPage != "" || cfg.SPARouting != nil || cfg.Analytics != nil {
		t.Fatalf("expected empty config, got %+v", cfg)
	}
}

func TestGetSiteConfig_RequiresAdmin(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateSite("docs")

	h := &SiteConfigHandler{store: store}
	// Only "view" access, not admin.
	r := reqWithAuth("GET", "/sites/docs/config", []auth.Cap{{Access: "view"}}, auth.Identity{LoginName: "viewer@test"})
	r.SetPathValue("site", "docs")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestPatchSiteConfig(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateSite("docs")

	caps := []auth.Cap{{Access: "admin"}}
	id := auth.Identity{LoginName: "admin@test"}

	// PATCH to set some config.
	patchBody := `{"analytics": true, "index_page": "home.html"}`
	patchReq := httptest.NewRequest("PATCH", "/sites/docs/config", strings.NewReader(patchBody))
	patchReq.Header.Set("Content-Type", "application/json")
	ctx := auth.ContextWithCaps(patchReq.Context(), caps)
	ctx = auth.ContextWithIdentity(ctx, id)
	patchReq = patchReq.WithContext(ctx)
	patchReq.SetPathValue("site", "docs")

	patchHandler := &UpdateSiteConfigHandler{store: store}
	pw := httptest.NewRecorder()
	patchHandler.ServeHTTP(pw, patchReq)

	if pw.Code != http.StatusOK {
		t.Fatalf("PATCH: expected 200, got %d: %s", pw.Code, pw.Body.String())
	}

	// GET to verify the config persisted.
	getHandler := &SiteConfigHandler{store: store}
	getReq := reqWithAuth("GET", "/sites/docs/config", caps, id)
	getReq.SetPathValue("site", "docs")
	gw := httptest.NewRecorder()
	getHandler.ServeHTTP(gw, getReq)

	if gw.Code != http.StatusOK {
		t.Fatalf("GET: expected 200, got %d", gw.Code)
	}

	var cfg storage.SiteConfig
	if err := json.NewDecoder(gw.Body).Decode(&cfg); err != nil {
		t.Fatalf("decoding GET response: %v", err)
	}
	if cfg.Analytics == nil || !*cfg.Analytics {
		t.Fatal("expected analytics=true")
	}
	if cfg.IndexPage != "home.html" {
		t.Fatalf("expected index_page=home.html, got %q", cfg.IndexPage)
	}
}

func TestPatchSiteConfig_InvalidatesHandler(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateSite("docs")

	mock := &mockEnsurer{}
	h := &UpdateSiteConfigHandler{store: store, ensurer: mock}

	body := `{"directory_listing": true}`
	r := httptest.NewRequest("PATCH", "/sites/docs/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := auth.ContextWithCaps(r.Context(), []auth.Cap{{Access: "admin"}})
	ctx = auth.ContextWithIdentity(ctx, auth.Identity{LoginName: "admin@test"})
	r = r.WithContext(ctx)
	r.SetPathValue("site", "docs")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(mock.ensured) != 1 || mock.ensured[0] != "docs" {
		t.Fatalf("expected EnsureServer(docs), got %v", mock.ensured)
	}
}

func TestPostSiteConfig_InvalidatesHandler(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateSite("docs")

	mock := &mockEnsurer{}
	h := &PostSiteConfigHandler{store: store, ensurer: mock}

	body := "public=false&analytics=false&clean_urls=false&spa_routing=false&directory_listing=false&directory_listing=true&index_page=&not_found_page=&trailing_slash=&webhook_url=&webhook_secret="
	r := formReqWithAuth("/sites/docs/config", body, []auth.Cap{{Access: "admin"}}, auth.Identity{LoginName: "admin@test"})
	r.SetPathValue("site", "docs")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	if len(mock.ensured) != 1 || mock.ensured[0] != "docs" {
		t.Fatalf("expected EnsureServer(docs), got %v", mock.ensured)
	}
}

func TestPatchSiteConfig_ValidationError(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateSite("docs")

	// SPA + directory listing are mutually exclusive.
	body := `{"spa_routing": true, "directory_listing": true}`
	r := httptest.NewRequest("PATCH", "/sites/docs/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := auth.ContextWithCaps(r.Context(), []auth.Cap{{Access: "admin"}})
	ctx = auth.ContextWithIdentity(ctx, auth.Identity{LoginName: "admin@test"})
	r = r.WithContext(ctx)
	r.SetPathValue("site", "docs")

	h := &UpdateSiteConfigHandler{store: store}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPatchSiteConfig_RequiresAdmin(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateSite("docs")

	body := `{"index_page": "home.html"}`
	r := httptest.NewRequest("PATCH", "/sites/docs/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	// Only "deploy" access, not admin.
	ctx := auth.ContextWithCaps(r.Context(), []auth.Cap{{Access: "deploy"}})
	ctx = auth.ContextWithIdentity(ctx, auth.Identity{LoginName: "deployer@test"})
	r = r.WithContext(ctx)
	r.SetPathValue("site", "docs")

	h := &UpdateSiteConfigHandler{store: store}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestFormBool(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   bool
	}{
		{"unchecked (hidden only)", []string{"false"}, false},
		{"checked (hidden+checkbox)", []string{"false", "true"}, true},
		{"missing", nil, false},
		{"single true", []string{"true"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := map[string][]string{"field": tt.values}
			got := formBool(form, "field")
			if got != tt.want {
				t.Fatalf("formBool(%v) = %v, want %v", tt.values, got, tt.want)
			}
		})
	}
}

func TestPostSiteConfig_FormSubmit(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateSite("docs")

	caps := []auth.Cap{{Access: "admin"}}
	id := auth.Identity{LoginName: "admin@test"}

	// Simulate form: analytics on, clean_urls on (→ html_extensions=false), index_page set.
	body := "public=false&analytics=false&analytics=true&clean_urls=false&clean_urls=true&spa_routing=false&directory_listing=false&index_page=home.html&not_found_page=&trailing_slash=&webhook_url=&webhook_secret="
	r := formReqWithAuth("/sites/docs/config", body, caps, id)
	r.SetPathValue("site", "docs")

	h := &PostSiteConfigHandler{store: store}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/sites/docs" {
		t.Fatalf("expected redirect to /sites/docs, got %q", loc)
	}

	// Verify persisted config.
	cfg, err := store.ReadSiteConfig("docs")
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if cfg.Analytics == nil || !*cfg.Analytics {
		t.Fatal("expected analytics=true")
	}
	if cfg.HTMLExtensions == nil || *cfg.HTMLExtensions {
		t.Fatal("expected html_extensions=false (clean_urls=true)")
	}
	if cfg.IndexPage != "home.html" {
		t.Fatalf("expected index_page=home.html, got %q", cfg.IndexPage)
	}
}

func TestPostSiteConfig_PreservesEndpoints(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateSite("docs")

	// Pre-configure form endpoints.
	enabled := true
	store.WriteSiteConfig("docs", storage.SiteConfig{
		Forms: &storage.FormsConfig{
			Enabled:   &enabled,
			Endpoints: []storage.FormEndpoint{{Path: "/contact", Name: "Contact"}},
		},
	})

	caps := []auth.Cap{{Access: "admin"}}
	id := auth.Identity{LoginName: "admin@test"}
	body := "forms=false&forms=true&public=false&analytics=false&spa_routing=false&directory_listing=false&clean_urls=false&index_page=&not_found_page=&trailing_slash=&webhook_url=&webhook_secret="
	r := formReqWithAuth("/sites/docs/config", body, caps, id)
	r.SetPathValue("site", "docs")

	h := &PostSiteConfigHandler{store: store}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}

	cfg, err := store.ReadSiteConfig("docs")
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if cfg.Forms == nil || !*cfg.Forms.Enabled {
		t.Fatal("expected forms enabled")
	}
	if len(cfg.Forms.Endpoints) != 1 || cfg.Forms.Endpoints[0].Path != "/contact" {
		t.Fatalf("expected endpoints preserved, got %+v", cfg.Forms.Endpoints)
	}
}

func TestPostSiteConfig_RequiresAdmin(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateSite("docs")

	body := "index_page=home.html"
	r := formReqWithAuth("/sites/docs/config", body, []auth.Cap{{Access: "deploy"}}, auth.Identity{LoginName: "deployer@test"})
	r.SetPathValue("site", "docs")

	h := &PostSiteConfigHandler{store: store}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestPatchSiteConfig_EnsureServerError(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateSite("docs")

	mock := &mockEnsurer{err: errors.New("restart failed")}
	h := &UpdateSiteConfigHandler{store: store, ensurer: mock}

	body := `{"directory_listing": true}`
	r := httptest.NewRequest("PATCH", "/sites/docs/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := auth.ContextWithCaps(r.Context(), []auth.Cap{{Access: "admin"}})
	ctx = auth.ContextWithIdentity(ctx, auth.Identity{LoginName: "admin@test"})
	r = r.WithContext(ctx)
	r.SetPathValue("site", "docs")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	// Config update should still succeed even if EnsureServer fails.
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(mock.ensured) != 1 {
		t.Fatalf("expected EnsureServer called once, got %d", len(mock.ensured))
	}
}

func TestPostSiteConfig_EnsureServerError(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateSite("docs")

	mock := &mockEnsurer{err: errors.New("restart failed")}
	h := &PostSiteConfigHandler{store: store, ensurer: mock}

	body := "public=false&analytics=false&clean_urls=false&spa_routing=false&directory_listing=false&index_page=&not_found_page=&trailing_slash=&webhook_url=&webhook_secret="
	r := formReqWithAuth("/sites/docs/config", body, []auth.Cap{{Access: "admin"}}, auth.Identity{LoginName: "admin@test"})
	r.SetPathValue("site", "docs")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	// Config update should still succeed with redirect even if EnsureServer fails.
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPatchSiteConfig_WriteError(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)
	store.CreateSite("docs")

	// Make the site config file unwritable by removing write permission on the site dir.
	siteDir := filepath.Join(dir, "sites", "docs")
	os.Chmod(siteDir, 0555)
	t.Cleanup(func() { os.Chmod(siteDir, 0755) })

	h := &UpdateSiteConfigHandler{store: store}
	body := `{"index_page": "home.html"}`
	r := httptest.NewRequest("PATCH", "/sites/docs/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := auth.ContextWithCaps(r.Context(), []auth.Cap{{Access: "admin"}})
	ctx = auth.ContextWithIdentity(ctx, auth.Identity{LoginName: "admin@test"})
	r = r.WithContext(ctx)
	r.SetPathValue("site", "docs")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for write failure, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPostSiteConfig_WriteError(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)
	store.CreateSite("docs")

	// Make the site config file unwritable.
	siteDir := filepath.Join(dir, "sites", "docs")
	os.Chmod(siteDir, 0555)
	t.Cleanup(func() { os.Chmod(siteDir, 0755) })

	h := &PostSiteConfigHandler{store: store}
	body := "public=false&analytics=false&clean_urls=false&spa_routing=false&directory_listing=false&index_page=&not_found_page=&trailing_slash=&webhook_url=&webhook_secret="
	r := formReqWithAuth("/sites/docs/config", body, []auth.Cap{{Access: "admin"}}, auth.Identity{LoginName: "admin@test"})
	r.SetPathValue("site", "docs")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for write failure, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPostSiteConfig_ValidationError(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateSite("docs")

	// SPA + directory listing both true = mutually exclusive.
	body := "spa_routing=false&spa_routing=true&directory_listing=false&directory_listing=true&public=false&analytics=false&clean_urls=false&index_page=&not_found_page=&trailing_slash=&webhook_url=&webhook_secret="
	r := formReqWithAuth("/sites/docs/config", body, []auth.Cap{{Access: "admin"}}, auth.Identity{LoginName: "admin@test"})
	r.SetPathValue("site", "docs")

	h := &PostSiteConfigHandler{store: store}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
