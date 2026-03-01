package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

func TestGetSiteConfig_Empty(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateSite("docs")

	h := &SiteConfigHandler{handlerDeps{store: store}}
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

	h := &SiteConfigHandler{handlerDeps{store: store}}
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

	patchHandler := &UpdateSiteConfigHandler{handlerDeps{store: store}}
	pw := httptest.NewRecorder()
	patchHandler.ServeHTTP(pw, patchReq)

	if pw.Code != http.StatusOK {
		t.Fatalf("PATCH: expected 200, got %d: %s", pw.Code, pw.Body.String())
	}

	// GET to verify the config persisted.
	getHandler := &SiteConfigHandler{handlerDeps{store: store}}
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

	h := &UpdateSiteConfigHandler{handlerDeps{store: store}}
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

	h := &UpdateSiteConfigHandler{handlerDeps{store: store}}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}
