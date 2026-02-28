package admin

import (
	"encoding/json"
	"log"
	"net/http"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/storage"
)

// --- GET /healthz ---

// HealthHandler returns platform health. It is unauthenticated.
type HealthHandler struct {
	store    *storage.Store
	recorder *analytics.Recorder
}

func NewHealthHandler(store *storage.Store, recorder *analytics.Recorder) *HealthHandler {
	return &HealthHandler{store: store, recorder: recorder}
}

func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	type checkResult struct {
		Storage   string `json:"storage"`
		Analytics string `json:"analytics"`
	}

	status := "ok"
	checks := checkResult{
		Storage:   "ok",
		Analytics: "disabled",
	}

	if _, err := h.store.ListSites(); err != nil {
		checks.Storage = "error"
		status = "degraded"
	}

	if h.recorder != nil {
		checks.Analytics = "ok"
		if err := h.recorder.Ping(); err != nil {
			checks.Analytics = "error"
			status = "degraded"
		}
	}

	code := http.StatusOK
	if status != "ok" {
		code = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status": status,
		"checks": checks,
	}); err != nil {
		log.Printf("warning: encoding health response: %v", err)
	}
}

// --- GET /sites/{site}/healthz ---

// SiteHealthHandler returns health for a single site. It requires auth.
type SiteHealthHandler struct {
	handlerDeps
	checker SiteHealthChecker
}

func (h *SiteHealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanView(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	site, err := h.store.GetSite(siteName)
	if err != nil {
		RenderError(w, r, http.StatusNotFound, "site not found")
		return
	}

	running := h.checker.IsRunning(siteName)

	status := "ok"
	if !running {
		status = "error"
	}

	code := http.StatusOK
	if status != "ok" {
		code = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status":            status,
		"site":              siteName,
		"server":            map[bool]string{true: "running", false: "stopped"}[running],
		"active_deployment": site.ActiveDeploymentID,
	}); err != nil {
		log.Printf("warning: encoding health response: %v", err)
	}
}
