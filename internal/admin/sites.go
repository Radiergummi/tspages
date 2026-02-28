package admin

import (
	"errors"
	"log"
	"net/http"
	"sort"
	"time"

	"tspages/internal/auth"
	"tspages/internal/storage"
	"tspages/internal/webhook"
)

// --- GET /sites ---

type SitesHandler struct{ handlerDeps }

func (h *SitesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	admin := auth.HasAdminCap(caps)

	sites, err := h.store.ListSites()
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "listing sites")
		return
	}

	now := time.Now()
	out := make([]SiteStatus, 0)
	for _, s := range sites {
		if !auth.CanView(caps, s.Name) {
			continue
		}
		ss := SiteStatus{
			Name:               s.Name,
			ActiveDeploymentID: s.ActiveDeploymentID,
			CanDeploy:          auth.CanDeploy(caps, s.Name),
		}
		if auth.IsAdmin(caps, s.Name) && h.recorder != nil && h.analyticsEnabled(s.Name) {
			var err error
			ss.Requests, err = h.recorder.TotalRequests(s.Name, time.Time{}, now)
			if err != nil {
				log.Printf("analytics: total requests for %s: %v", s.Name, err)
			}
			ts, err := h.recorder.RequestsOverTime(s.Name, now.Add(-7*24*time.Hour), now)
			if err != nil {
				log.Printf("analytics: requests over time for %s: %v", s.Name, err)
			}
			ss.Sparkline = countsJSON(ts)
		}
		if s.ActiveDeploymentID != "" {
			if m, err := h.store.ReadManifest(s.Name, s.ActiveDeploymentID); err == nil {
				ss.LastDeployedBy = m.CreatedBy
				ss.LastDeployedByAvatar = m.CreatedByAvatar
				if !m.CreatedAt.IsZero() {
					ss.LastDeployedAt = m.CreatedAt.Format(time.RFC3339)
				}
			}
		}
		out = append(out, ss)
	}

	resp := SitesResponse{Admin: admin, User: userInfo(identity, caps), DNSSuffix: h.dnsSuffix, Sites: out}

	if wantsJSON(r) {
		setAlternateLinks(w, [][2]string{
			{"/sites", "text/html"},
			{"/feed.atom", "application/atom+xml"},
		})
		writeJSON(w, resp)
		return
	}

	// Show "New site" button if user has any admin access (scoped or unscoped).
	// Server validates the specific name on POST.
	canCreate := admin

	renderPage(w, r, sitesTmpl, "sites", struct {
		SitesResponse
		CanCreate  bool
		Host       string
		MaxNameLen int
	}{resp, canCreate, r.Host, storage.MaxSiteNameLen(h.dnsSuffix)})
}

// --- POST /sites ---

type CreateSiteHandler struct {
	handlerDeps
	ensurer  SiteEnsurer
	notifier *webhook.Notifier
}

func (h *CreateSiteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if !storage.ValidSiteNameForSuffix(name, h.dnsSuffix) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanCreateSite(caps, name) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	if err := h.store.CreateSite(name); err != nil {
		if errors.Is(err, storage.ErrSiteExists) {
			RenderError(w, r, http.StatusConflict, "site already exists")
			return
		}
		RenderError(w, r, http.StatusInternalServerError, "creating site")
		return
	}

	if err := h.ensurer.EnsureServer(name); err != nil {
		log.Printf("warning: site %q created but server failed to start: %v", name, err)
	}

	if h.notifier != nil {
		identity := auth.IdentityFromContext(r.Context())
		resolvedCfg := storage.SiteConfig{}.Merge(h.defaults)
		h.notifier.Fire("site.created", name, resolvedCfg, map[string]any{
			"site":       name,
			"created_by": identity.DisplayName,
		})
	}

	if wantsJSON(r) {
		writeJSON(w, map[string]string{"name": name})
		return
	}

	http.Redirect(w, r, "/sites/"+name, http.StatusSeeOther)
}

// --- GET /sites/{site} ---

type SiteHandler struct {
	handlerDeps
	notifier *webhook.Notifier
}

func (h *SiteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	found, err := h.store.GetSite(siteName)
	if err != nil {
		RenderError(w, r, http.StatusNotFound, "site not found")
		return
	}

	ss := SiteStatus{
		Name:               found.Name,
		ActiveDeploymentID: found.ActiveDeploymentID,
	}
	// Read the merged config for the active deployment.
	var siteConfig storage.SiteConfig
	if found.ActiveDeploymentID != "" {
		cfg, _ := h.store.ReadSiteConfig(siteName, found.ActiveDeploymentID)
		siteConfig = cfg.Merge(h.defaults)
	}

	analyticsOn := siteConfig.Analytics == nil || *siteConfig.Analytics
	var sparkline string
	if admin && h.recorder != nil && analyticsOn {
		now := time.Now()
		var err error
		ss.Requests, err = h.recorder.TotalRequests(siteName, time.Time{}, now)
		if err != nil {
			log.Printf("analytics: total requests for %s: %v", siteName, err)
		}
		ts, err := h.recorder.RequestsOverTime(siteName, now.Add(-7*24*time.Hour), now)
		if err != nil {
			log.Printf("analytics: requests over time for %s: %v", siteName, err)
		}
		sparkline = countsJSON(ts)
	}
	if found.ActiveDeploymentID != "" {
		if m, err := h.store.ReadManifest(siteName, found.ActiveDeploymentID); err == nil {
			ss.LastDeployedBy = m.CreatedBy
			ss.LastDeployedByAvatar = m.CreatedByAvatar
			if !m.CreatedAt.IsZero() {
				ss.LastDeployedAt = m.CreatedAt.Format(time.RFC3339)
			}
		}
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

	var recentDeliveries []webhook.DeliverySummary
	if h.notifier != nil && auth.CanDeploy(caps, siteName) {
		var err error
		recentDeliveries, _, err = h.notifier.ListDeliveries(siteName, "", "", 5, 0)
		if err != nil {
			log.Printf("webhooks: list deliveries for %s: %v", siteName, err)
		}
	}

	resp := SiteDetailResponse{Site: ss, Deployments: deployments}

	if wantsJSON(r) {
		setAlternateLinks(w, [][2]string{
			{"/sites/" + siteName, "text/html"},
			{"/sites/" + siteName + "/feed.atom", "application/atom+xml"},
		})
		writeJSON(w, resp)
		return
	}

	hasInactive := false
	for _, d := range deployments {
		if !d.Active {
			hasInactive = true
			break
		}
	}

	totalDeployments := len(deployments)

	renderPage(w, r, siteTmpl, "sites", struct {
		SiteDetailResponse
		User             UserInfo
		Admin            bool
		CanDelete        bool
		CanDeploy        bool
		HasInactive      bool
		AnalyticsEnabled bool
		Config           storage.SiteConfig
		DNSSuffix        string
		Host             string
		Sparkline        string
		RecentDeliveries []webhook.DeliverySummary
		TotalDeployments int
	}{resp, userInfo(identity, caps), admin, auth.CanDeleteSite(caps, siteName), auth.CanDeploy(caps, siteName), hasInactive, analyticsOn, siteConfig, h.dnsSuffix, r.Host, sparkline, recentDeliveries, totalDeployments})
}
