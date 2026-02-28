package admin

import (
	"encoding/json"
	"net/http"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

// --- GET /sites/{site}/config ---

type SiteConfigHandler struct{ handlerDeps }

func (h *SiteConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.IsAdmin(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	cfg, err := h.store.ReadSiteConfig(siteName)
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "reading site config")
		return
	}

	writeJSON(w, cfg)
}

// --- PATCH /sites/{site}/config ---

type UpdateSiteConfigHandler struct{ handlerDeps }

func (h *UpdateSiteConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.IsAdmin(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	var cfg storage.SiteConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.store.WriteSiteConfig(siteName, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, cfg)
}
