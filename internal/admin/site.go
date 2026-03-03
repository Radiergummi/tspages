package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/storage"
	"tspages/internal/web"
	"tspages/internal/webhooks"
)

// region GET /sites

type SitesHandler struct {
	store     *storage.Store
	recorder  *analytics.Recorder
	dnsSuffix string
	defaults  storage.SiteConfig
}

func (h *SitesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	admin := auth.HasAdminCap(caps)

	sites, err := h.store.ListSites()
	if err != nil {
		web.RenderError(w, r, http.StatusInternalServerError, "listing sites")
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
		if auth.IsAdmin(caps, s.Name) && h.recorder != nil && analytics.Enabled(h.store, h.defaults, s.Name) {
			var err error
			ss.Requests, err = h.recorder.TotalRequests(s.Name, time.Time{}, now)
			if err != nil {
				slog.Error("analytics query failed", "query", "total_requests", "site", s.Name, "err", err)
			}
			ts, err := h.recorder.RequestsOverTime(s.Name, now.Add(-7*24*time.Hour), now)
			if err != nil {
				slog.Error("analytics query failed", "query", "requests_over_time", "site", s.Name, "err", err)
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
		if p, err := h.store.ReadCurrentPreview(s.Name); err == nil {
			ss.OGTitle = p.Title
			ss.OGDescription = p.Description
			if p.OGImage != "" {
				ss.OGImage = fmt.Sprintf("https://%s.%s%s", s.Name, h.dnsSuffix, p.OGImage)
			}
			if p.Favicon != "" {
				ss.Favicon = fmt.Sprintf("https://%s.%s%s", s.Name, h.dnsSuffix, p.Favicon)
			}
		}
		out = append(out, ss)
	}

	resp := SitesResponse{Admin: admin, User: web.NewUserInfo(identity, caps), DNSSuffix: h.dnsSuffix, Sites: out}

	if web.WantsJSON(r) {
		web.SetAlternateLinks(w, [][2]string{
			{"/sites", "text/html"},
			{"/feed.atom", "application/atom+xml"},
		})
		web.WriteJSON(w, resp)
		return
	}

	// Show "New site" button if user has any admin access (scoped or unscoped).
	// Server validates the specific name on POST.
	canCreate := admin

	web.RenderPage(w, r, sitesTmpl, "sites", struct {
		SitesResponse
		CanCreate  bool
		Host       string
		MaxNameLen int
	}{resp, canCreate, r.Host, storage.MaxSiteNameLen(h.dnsSuffix)})
}

// region POST /sites

type CreateSiteHandler struct {
	store     *storage.Store
	dnsSuffix string
	defaults  storage.SiteConfig
	ensurer   SiteEnsurer
	notifier  DeliveryLister
}

func (h *CreateSiteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if !storage.ValidSiteNameForSuffix(name, h.dnsSuffix) {
		web.RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanCreateSite(caps, name) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	if err := h.store.CreateSite(name); err != nil {
		if errors.Is(err, storage.ErrSiteExists) {
			web.RenderError(w, r, http.StatusConflict, "site already exists")
			return
		}
		web.RenderError(w, r, http.StatusInternalServerError, "creating site")
		return
	}

	if err := h.ensurer.EnsureServer(name); err != nil {
		slog.Warn("site created but server failed to start", "site", name, "err", err)
	}

	if h.notifier != nil {
		identity := auth.IdentityFromContext(r.Context())
		resolvedCfg := storage.SiteConfig{}.Merge(h.defaults)
		h.notifier.Fire("site.created", name, resolvedCfg, map[string]any{
			"site":       name,
			"created_by": identity.DisplayName,
		})
	}

	if web.WantsJSON(r) {
		web.WriteJSON(w, map[string]string{"name": name})
		return
	}

	http.Redirect(w, r, "/sites/"+name, http.StatusSeeOther)
}

// region GET /sites/{site}

type SiteHandler struct {
	store     *storage.Store
	recorder  *analytics.Recorder
	dnsSuffix string
	defaults  storage.SiteConfig
	notifier  DeliveryLister
}

func (h *SiteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := web.TrimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		web.RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	admin := auth.IsAdmin(caps, siteName)

	if !auth.CanDeploy(caps, siteName) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	found, err := h.store.GetSite(siteName)
	if err != nil {
		web.RenderError(w, r, http.StatusNotFound, "site not found")
		return
	}

	ss := SiteStatus{
		Name:               found.Name,
		ActiveDeploymentID: found.ActiveDeploymentID,
	}
	// Read the merged config for the active deployment.
	siteOverrides, _ := h.store.ReadSiteConfig(siteName)

	var siteConfig storage.SiteConfig
	if found.ActiveDeploymentID != "" {
		deployCfg, _ := h.store.ReadDeploymentConfig(siteName, found.ActiveDeploymentID)
		siteConfig = deployCfg.Merge(siteOverrides).Merge(h.defaults)
	}

	analyticsOn := siteConfig.Analytics == nil || *siteConfig.Analytics
	var sparkline string
	if admin && h.recorder != nil && analyticsOn {
		now := time.Now()
		var err error
		ss.Requests, err = h.recorder.TotalRequests(siteName, time.Time{}, now)
		if err != nil {
			slog.Error("analytics query failed", "query", "total_requests", "site", siteName, "err", err)
		}
		ts, err := h.recorder.RequestsOverTime(siteName, now.Add(-7*24*time.Hour), now)
		if err != nil {
			slog.Error("analytics query failed", "query", "requests_over_time", "site", siteName, "err", err)
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
	if p, err := h.store.ReadCurrentPreview(siteName); err == nil {
		ss.OGTitle = p.Title
		ss.OGDescription = p.Description
		if p.OGImage != "" {
			ss.OGImage = fmt.Sprintf("https://%s.%s%s", siteName, h.dnsSuffix, p.OGImage)
		}
		if p.Favicon != "" {
			ss.Favicon = fmt.Sprintf("https://%s.%s%s", siteName, h.dnsSuffix, p.Favicon)
		}
	}

	deployments, err := h.store.ListDeployments(siteName)
	if err != nil {
		web.RenderError(w, r, http.StatusInternalServerError, "listing deployments")
		return
	}
	if deployments == nil {
		deployments = []storage.DeploymentInfo{}
	}
	sort.Slice(deployments, func(i, j int) bool {
		return deployments[i].CreatedAt.After(deployments[j].CreatedAt)
	})

	var recentDeliveries []webhooks.DeliverySummary
	if h.notifier != nil && auth.CanDeploy(caps, siteName) {
		var err error
		recentDeliveries, _, err = h.notifier.ListDeliveries(siteName, "", "", 5, 0)
		if err != nil {
			slog.Error("listing webhook deliveries failed", "site", siteName, "err", err)
		}
	}

	resp := SiteDetailResponse{Site: ss, Deployments: deployments}

	if web.WantsJSON(r) {
		web.SetAlternateLinks(w, [][2]string{
			{"/sites/" + siteName, "text/html"},
			{"/sites/" + siteName + "/feed.atom", "application/atom+xml"},
		})
		web.WriteJSON(w, resp)
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

	web.RenderPage(w, r, siteTmpl, "sites", struct {
		SiteDetailResponse
		User             web.UserInfo
		Admin            bool
		CanDelete        bool
		CanDeploy        bool
		HasInactive      bool
		AnalyticsEnabled bool
		Config           storage.SiteConfig
		DNSSuffix        string
		Host             string
		Sparkline        string
		RecentDeliveries []webhooks.DeliverySummary
		TotalDeployments int
	}{resp, web.NewUserInfo(identity, caps), admin, auth.CanDeleteSite(caps, siteName), auth.CanDeploy(caps, siteName), hasInactive, analyticsOn, siteConfig, h.dnsSuffix, r.Host, sparkline, recentDeliveries, totalDeployments})
}

// countsJSON returns a JSON array of counts from the given time buckets,
// e.g. "[4,7,2,9]". Returns an empty string if there are fewer than 2 buckets
// or all counts are zero.
func countsJSON(buckets []analytics.TimeBucket) string {
	if len(buckets) < 2 {
		return ""
	}
	var hasData bool
	var sb strings.Builder
	sb.WriteByte('[')
	for i, b := range buckets {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatInt(b.Count, 10))
		if b.Count > 0 {
			hasData = true
		}
	}
	sb.WriteByte(']')
	if !hasData {
		return ""
	}
	return sb.String()
}
