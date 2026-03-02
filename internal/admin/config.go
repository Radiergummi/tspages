package admin

import (
	"encoding/json"
	"net/http"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

// formBool returns true if the last value for name is "true".
// Works with the hidden+checkbox pattern: unchecked = ["false"], checked = ["false", "true"].
func formBool(form map[string][]string, name string) bool {
	vals := form[name]
	if len(vals) == 0 {
		return false
	}
	return vals[len(vals)-1] == "true"
}

func boolPtr(b bool) *bool { return &b }

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

type UpdateSiteConfigHandler struct {
	handlerDeps
	ensurer SiteEnsurer
}

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

	// Invalidate the serve handler's cached config so the new settings take
	// effect immediately. EnsureServer on a running site just calls
	// InvalidateConfig (and restarts if the public flag changed).
	if h.ensurer != nil {
		h.ensurer.EnsureServer(siteName)
	}

	writeJSON(w, cfg)
}

// --- POST /sites/{site}/config ---

type PostSiteConfigHandler struct {
	handlerDeps
	ensurer SiteEnsurer
}

func parseSiteConfigForm(r *http.Request) storage.SiteConfig {
	form := r.Form
	var events []string
	for _, v := range form["webhook_events"] {
		if v != "" {
			events = append(events, v)
		}
	}
	return storage.SiteConfig{
		Public:           boolPtr(formBool(form, "public")),
		SPARouting:       boolPtr(formBool(form, "spa_routing")),
		HTMLExtensions:   boolPtr(!formBool(form, "clean_urls")),
		Analytics:        boolPtr(formBool(form, "analytics")),
		DirectoryListing: boolPtr(formBool(form, "directory_listing")),
		IndexPage:        r.FormValue("index_page"),
		NotFoundPage:     r.FormValue("not_found_page"),
		TrailingSlash:    r.FormValue("trailing_slash"),
		WebhookURL:       r.FormValue("webhook_url"),
		WebhookEvents:    events,
		WebhookSecret:    r.FormValue("webhook_secret"),
	}
}

func (h *PostSiteConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		http.Error(w, "invalid site name", http.StatusBadRequest)
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.IsAdmin(caps, siteName) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form data", http.StatusBadRequest)
		return
	}

	cfg := parseSiteConfigForm(r)
	if err := h.store.WriteSiteConfig(siteName, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if h.ensurer != nil {
		h.ensurer.EnsureServer(siteName)
	}

	http.Redirect(w, r, "/sites/"+siteName, http.StatusSeeOther)
}
