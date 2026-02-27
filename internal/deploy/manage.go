package deploy

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"tspages/internal/auth"
	"tspages/internal/storage"
	"tspages/internal/webhook"
)

// DeleteHandler handles DELETE /deploy/{site}.
type DeleteHandler struct {
	store    *storage.Store
	manager  SiteManager
	notifier *webhook.Notifier
	defaults storage.SiteConfig
}

func NewDeleteHandler(store *storage.Store, manager SiteManager, notifier *webhook.Notifier, defaults storage.SiteConfig) *DeleteHandler {
	return &DeleteHandler{store: store, manager: manager, notifier: notifier, defaults: defaults}
}

func (h *DeleteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site := r.PathValue("site")
	if !storage.ValidSiteName(site) {
		http.Error(w, "invalid site name", http.StatusBadRequest)
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeleteSite(caps, site) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Read config before deletion so the webhook fires to the right destination.
	var resolvedCfg storage.SiteConfig
	if h.notifier != nil {
		if cfg, err := h.store.ReadCurrentSiteConfig(site); err == nil {
			resolvedCfg = cfg.Merge(h.defaults)
		} else {
			resolvedCfg = h.defaults
		}
	}

	if err := h.manager.StopServer(site); err != nil {
		http.Error(w, fmt.Sprintf("stopping server: %v", err), http.StatusInternalServerError)
		return
	}

	if err := h.store.DeleteSite(site); err != nil {
		http.Error(w, fmt.Sprintf("deleting site: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)

	if h.notifier != nil {
		identity := auth.IdentityFromContext(r.Context())
		deletedBy := identity.DisplayName
		if deletedBy == "" {
			deletedBy = identity.LoginName
		}
		h.notifier.Fire("site.deleted", site, resolvedCfg, map[string]any{
			"site":       site,
			"deleted_by": deletedBy,
		})
	}
}

// ListDeploymentsHandler handles GET /deploy/{site}.
type ListDeploymentsHandler struct {
	store *storage.Store
}

func NewListDeploymentsHandler(store *storage.Store) *ListDeploymentsHandler {
	return &ListDeploymentsHandler{store: store}
}

func (h *ListDeploymentsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site := r.PathValue("site")
	if !storage.ValidSiteName(site) {
		http.Error(w, "invalid site name", http.StatusBadRequest)
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, site) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	deployments, err := h.store.ListDeployments(site)
	if err != nil {
		http.Error(w, fmt.Sprintf("listing deployments: %v", err), http.StatusInternalServerError)
		return
	}
	if deployments == nil {
		deployments = []storage.DeploymentInfo{}
	}

	writeJSON(w, deployments)
}

// DeleteDeploymentHandler handles DELETE /deploy/{site}/{id}.
type DeleteDeploymentHandler struct {
	store *storage.Store
}

func NewDeleteDeploymentHandler(store *storage.Store) *DeleteDeploymentHandler {
	return &DeleteDeploymentHandler{store: store}
}

func (h *DeleteDeploymentHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site := r.PathValue("site")
	id := r.PathValue("id")
	if !storage.ValidSiteName(site) {
		http.Error(w, "invalid site name", http.StatusBadRequest)
		return
	}
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
		http.Error(w, "invalid deployment id", http.StatusBadRequest)
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, site) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := h.store.DeleteDeployment(site, id); err != nil {
		switch {
		case errors.Is(err, storage.ErrActiveDeployment):
			http.Error(w, "cannot delete the active deployment", http.StatusConflict)
		case errors.Is(err, storage.ErrDeploymentNotFound):
			http.Error(w, "deployment not found", http.StatusNotFound)
		default:
			http.Error(w, fmt.Sprintf("deleting deployment: %v", err), http.StatusInternalServerError)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// CleanupDeploymentsHandler handles DELETE /deploy/{site}/deployments.
type CleanupDeploymentsHandler struct {
	store *storage.Store
}

func NewCleanupDeploymentsHandler(store *storage.Store) *CleanupDeploymentsHandler {
	return &CleanupDeploymentsHandler{store: store}
}

func (h *CleanupDeploymentsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site := r.PathValue("site")
	if !storage.ValidSiteName(site) {
		http.Error(w, "invalid site name", http.StatusBadRequest)
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, site) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	deleted, err := h.store.DeleteInactiveDeployments(site)
	if err != nil {
		http.Error(w, fmt.Sprintf("cleaning up: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]int{"deleted": deleted})
}

// ActivateHandler handles POST /deploy/{site}/{id}/activate.
type ActivateHandler struct {
	store   *storage.Store
	manager SiteManager
}

func NewActivateHandler(store *storage.Store, manager SiteManager) *ActivateHandler {
	return &ActivateHandler{store: store, manager: manager}
}

func (h *ActivateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site := r.PathValue("site")
	id := r.PathValue("id")
	if !storage.ValidSiteName(site) {
		http.Error(w, "invalid site name", http.StatusBadRequest)
		return
	}
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
		http.Error(w, "invalid deployment id", http.StatusBadRequest)
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, site) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Verify deployment exists and is complete
	deployments, err := h.store.ListDeployments(site)
	if err != nil {
		http.Error(w, fmt.Sprintf("listing deployments: %v", err), http.StatusInternalServerError)
		return
	}
	found := false
	for _, d := range deployments {
		if d.ID == id {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "deployment not found or incomplete", http.StatusNotFound)
		return
	}

	if err := h.store.ActivateDeployment(site, id); err != nil {
		http.Error(w, fmt.Sprintf("activating deployment: %v", err), http.StatusInternalServerError)
		return
	}

	if err := h.manager.EnsureServer(site); err != nil {
		http.Error(w, fmt.Sprintf("starting server: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, storage.DeploymentInfo{ID: id, Active: true})
}
