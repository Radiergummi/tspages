package admin

import (
	"log/slog"
	"net/http"
	"sort"
	"strconv"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

// --- GET /sites/{site}/deployments/{id} ---

type DeploymentHandler struct{ handlerDeps }

func (h *DeploymentHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	depID := trimSuffix(r.PathValue("id"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	admin := auth.IsAdmin(caps, siteName)

	if !auth.CanDeploy(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	deployments, err := h.store.ListDeployments(siteName)
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "listing deployments")
		return
	}

	// Sort by CreatedAt descending before scanning so that pointer into
	// the slice remains valid and we can find the previous deployment.
	sort.Slice(deployments, func(i, j int) bool {
		return deployments[i].CreatedAt.After(deployments[j].CreatedAt)
	})

	var dep *storage.DeploymentInfo
	var prevID string
	for i := range deployments {
		if deployments[i].ID == depID {
			dep = &deployments[i]
			if i+1 < len(deployments) {
				prevID = deployments[i+1].ID
			}
			break
		}
	}
	if dep == nil {
		RenderError(w, r, http.StatusNotFound, "deployment not found")
		return
	}

	if wantsJSON(r) {
		setAlternateLinks(w, [][2]string{
			{"/sites/" + siteName + "/deployments/" + depID, "text/html"},
		})
		writeJSON(w, dep)
		return
	}

	// List files in this deployment (capped at 250).
	const maxFiles = 250
	allFiles, err := h.store.ListDeploymentFiles(siteName, depID)
	if err != nil {
		slog.Warn("listing deployment files failed", "site", siteName, "deployment", depID, "err", err)
	}
	fileCount := len(allFiles)
	files := allFiles
	if len(files) > maxFiles {
		files = files[:maxFiles]
	}

	// Compute diff against previous deployment.
	var added, removed, changed []string
	if prevID != "" {
		prevFiles, err := h.store.ListDeploymentFiles(siteName, prevID)
		if err != nil {
			slog.Warn("listing deployment files failed", "site", siteName, "deployment", prevID, "err", err)
		}
		added, removed, changed = diffFiles(allFiles, prevFiles)
		// Cap diff output to avoid huge tables.
		if len(added) > maxFiles {
			added = added[:maxFiles]
		}
		if len(removed) > maxFiles {
			removed = removed[:maxFiles]
		}
		if len(changed) > maxFiles {
			changed = changed[:maxFiles]
		}
	}

	renderPage(w, r, deploymentTmpl, "sites", struct {
		User       UserInfo
		Admin      bool
		CanDeploy  bool
		DNSSuffix  string
		SiteName   string
		Deployment storage.DeploymentInfo
		Files      []storage.FileInfo
		FileCount  int
		PrevID     string
		Added      []string
		Removed    []string
		Changed    []string
	}{
		userInfo(identity, caps), admin, auth.CanDeploy(caps, siteName),
		h.dnsSuffix, siteName, *dep,
		files, fileCount, prevID,
		added, removed, changed,
	})
}

// --- GET /deployments ---

// DeploymentEntry is a deployment with its site name, for the global feed.
type DeploymentEntry struct {
	storage.DeploymentInfo
	Site string `json:"site"`
}

// DeploymentsResponse is the JSON response for GET /deployments.
type DeploymentsResponse struct {
	Deployments []DeploymentEntry `json:"deployments"`
	Page        int               `json:"page"`
	TotalPages  int               `json:"total_pages"`
}

const deploymentsPageSize = 50

type DeploymentsHandler struct{ handlerDeps }

func (h *DeploymentsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())

	if !auth.HasDeployCap(caps) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	sites, err := h.store.ListSites()
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "listing sites")
		return
	}

	// Collect deployments from all sites the user can deploy to.
	all := make([]DeploymentEntry, 0)
	for _, s := range sites {
		if !auth.CanDeploy(caps, s.Name) {
			continue
		}
		deps, err := h.store.ListDeployments(s.Name)
		if err != nil {
			continue
		}
		for _, d := range deps {
			all = append(all, DeploymentEntry{DeploymentInfo: d, Site: s.Name})
		}
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})

	// Pagination.
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	totalPages := (len(all) + deploymentsPageSize - 1) / deploymentsPageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * deploymentsPageSize
	end := start + deploymentsPageSize
	if end > len(all) {
		end = len(all)
	}
	pageItems := all[start:end]

	resp := DeploymentsResponse{
		Deployments: pageItems,
		Page:        page,
		TotalPages:  totalPages,
	}

	if wantsJSON(r) {
		setAlternateLinks(w, [][2]string{
			{"/deployments", "text/html"},
			{"/feed.atom", "application/atom+xml"},
		})
		writeJSON(w, resp)
		return
	}

	renderPage(w, r, deploymentsTmpl, "deployments", struct {
		DeploymentsResponse
		User UserInfo
	}{resp, userInfo(identity, caps)})
}

// --- GET /sites/{site}/deployments ---

type SiteDeploymentsHandler struct{ handlerDeps }

func (h *SiteDeploymentsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	admin := auth.IsAdmin(caps, siteName)

	if !auth.CanDeploy(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	deployments, err := h.store.ListDeployments(siteName)
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "listing deployments")
		return
	}
	if deployments == nil {
		deployments = []storage.DeploymentInfo{}
	}
	sort.Slice(deployments, func(i, j int) bool {
		return deployments[i].CreatedAt.After(deployments[j].CreatedAt)
	})

	// Pagination.
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	totalPages := (len(deployments) + deploymentsPageSize - 1) / deploymentsPageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * deploymentsPageSize
	end := start + deploymentsPageSize
	if end > len(deployments) {
		end = len(deployments)
	}
	pageItems := deployments[start:end]

	if wantsJSON(r) {
		writeJSON(w, map[string]any{
			"deployments": pageItems,
			"page":        page,
			"total_pages": totalPages,
		})
		return
	}

	hasInactive := false
	for _, d := range deployments {
		if !d.Active {
			hasInactive = true
			break
		}
	}

	renderPage(w, r, siteDeploymentsTmpl, "sites", struct {
		Deployments []storage.DeploymentInfo
		Page        int
		TotalPages  int
		Site        string
		Admin       bool
		CanDeploy   bool
		HasInactive bool
		User        UserInfo
	}{pageItems, page, totalPages, siteName, admin, auth.CanDeploy(caps, siteName), hasInactive, userInfo(identity, caps)})
}

// diffFiles compares two file lists and returns added, removed, and changed paths.
// A file is considered "changed" if its content hash differs.
func diffFiles(current, previous []storage.FileInfo) (added, removed, changed []string) {
	prevMap := make(map[string]string, len(previous))
	for _, f := range previous {
		prevMap[f.Path] = f.Hash
	}
	currMap := make(map[string]struct{}, len(current))
	for _, f := range current {
		currMap[f.Path] = struct{}{}
		if prevHash, ok := prevMap[f.Path]; ok {
			if f.Hash != prevHash {
				changed = append(changed, f.Path)
			}
		} else {
			added = append(added, f.Path)
		}
	}
	for _, f := range previous {
		if _, ok := currMap[f.Path]; !ok {
			removed = append(removed, f.Path)
		}
	}
	return
}
