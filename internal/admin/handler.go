package admin

import (
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/storage"
	"tspages/internal/webhook"
)

// SiteStatus is the per-site data returned by the sites list endpoint.
type SiteStatus struct {
	Name                 string `json:"name"`
	ActiveDeploymentID   string `json:"active_deployment_id,omitempty"`
	Requests             int64  `json:"requests"`
	Sparkline            string `json:"sparkline,omitempty"`
	LastDeployedBy       string `json:"last_deployed_by,omitempty"`
	LastDeployedByAvatar string `json:"last_deployed_by_avatar,omitempty"`
	LastDeployedAt       string `json:"last_deployed_at,omitempty"`
}

// SitesResponse is the JSON response for GET /sites.
type SitesResponse struct {
	Admin     bool         `json:"admin"`
	User      UserInfo     `json:"user"`
	DNSSuffix string       `json:"dns_suffix"`
	Sites     []SiteStatus `json:"sites"`
}

// SiteDetailResponse is the JSON response for GET /sites/{site}.
type SiteDetailResponse struct {
	Site        SiteStatus              `json:"site"`
	Deployments []storage.DeploymentInfo `json:"deployments"`
}

// --- shared deps ---

type handlerDeps struct {
	store     *storage.Store
	recorder  *analytics.Recorder
	dnsSuffix *string
	defaults  storage.SiteConfig
}

// analyticsEnabled reports whether analytics are enabled for the given site
// by reading the current deployment's config and merging with server defaults.
func (d *handlerDeps) analyticsEnabled(site string) bool {
	cfg, _ := d.store.ReadCurrentSiteConfig(site)
	merged := cfg.Merge(d.defaults)
	if merged.Analytics == nil {
		return true
	}
	return *merged.Analytics
}

// UserInfo holds user display data for templates.
type UserInfo struct {
	Name          string `json:"name"`
	ProfilePicURL string `json:"profile_pic_url,omitempty"`
}

func userInfo(identity auth.Identity) UserInfo {
	name := identity.DisplayName
	if name == "" {
		name = identity.LoginName
	}
	return UserInfo{Name: name, ProfilePicURL: identity.ProfilePicURL}
}

// SiteEnsurer is the subset of multihost.Manager needed to start a site server.
type SiteEnsurer interface {
	EnsureServer(site string) error
}

// SiteHealthChecker is the subset of multihost.Manager needed for health checks.
type SiteHealthChecker interface {
	IsRunning(site string) bool
}

// Handlers groups all admin HTTP handlers.
type Handlers struct {
	Sites          *SitesHandler
	Site           *SiteHandler
	Deployment     *DeploymentHandler
	CreateSite     *CreateSiteHandler
	Deployments    *DeploymentsHandler
	Analytics      *AnalyticsHandler
	PurgeAnalytics *PurgeAnalyticsHandler
	AllAnalytics   *AllAnalyticsHandler
	Webhooks       *WebhooksHandler
	SiteWebhooks   *SiteWebhooksHandler
	Help           *HelpHandler
	API            *APIHandler
	Feed           *FeedHandler
	SiteFeed       *SiteFeedHandler
	SiteHealth     *SiteHealthHandler
}

func NewHandlers(store *storage.Store, recorder *analytics.Recorder, dnsSuffix *string, ensurer SiteEnsurer, checker SiteHealthChecker, defaults storage.SiteConfig, notifier *webhook.Notifier) *Handlers {
	d := handlerDeps{store: store, recorder: recorder, dnsSuffix: dnsSuffix, defaults: defaults}
	return &Handlers{
		Sites:          &SitesHandler{d},
		Site:           &SiteHandler{handlerDeps: d, notifier: notifier},
		Deployment:     &DeploymentHandler{d},
		CreateSite:     &CreateSiteHandler{handlerDeps: d, ensurer: ensurer, notifier: notifier},
		Deployments:    &DeploymentsHandler{d},
		Analytics:      &AnalyticsHandler{d},
		PurgeAnalytics: &PurgeAnalyticsHandler{d},
		AllAnalytics:   &AllAnalyticsHandler{d},
		Webhooks:       &WebhooksHandler{handlerDeps: d, notifier: notifier},
		SiteWebhooks:   &SiteWebhooksHandler{handlerDeps: d, notifier: notifier},
		Help:           &HelpHandler{},
		API:            &APIHandler{},
		Feed:           &FeedHandler{d},
		SiteFeed:       &SiteFeedHandler{d},
		SiteHealth:     &SiteHealthHandler{handlerDeps: d, checker: checker},
	}
}

// --- GET /sites ---

type SitesHandler struct{ handlerDeps }

func (h *SitesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	admin := auth.IsAdmin(caps)

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
		}
		if admin && h.recorder != nil && h.analyticsEnabled(s.Name) {
			ss.Requests, _ = h.recorder.TotalRequests(s.Name, time.Time{}, now)
			ts, _ := h.recorder.RequestsOverTime(s.Name, now.Add(-7*24*time.Hour), now)
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

	resp := SitesResponse{Admin: admin, User: userInfo(identity), DNSSuffix: *h.dnsSuffix, Sites: out}

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
	canCreate := auth.IsAdmin(caps)

	renderPage(w, r, sitesTmpl, "sites", struct {
		SitesResponse
		CanCreate  bool
		Host       string
		MaxNameLen int
	}{resp, canCreate, r.Host, storage.MaxSiteNameLen(*h.dnsSuffix)})
}

// --- POST /sites ---

type CreateSiteHandler struct {
	handlerDeps
	ensurer  SiteEnsurer
	notifier *webhook.Notifier
}

func (h *CreateSiteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if !storage.ValidSiteNameForSuffix(name, *h.dnsSuffix) {
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
	admin := auth.IsAdmin(caps)

	if !admin && !auth.CanView(caps, siteName) {
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
		ss.Requests, _ = h.recorder.TotalRequests(siteName, time.Time{}, now)
		ts, _ := h.recorder.RequestsOverTime(siteName, now.Add(-7*24*time.Hour), now)
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
	if h.notifier != nil {
		recentDeliveries, _, _ = h.notifier.ListDeliveries(siteName, "", "", 5, 0)
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

	renderPage(w, r, siteTmpl, "sites", struct {
		SiteDetailResponse
		User              UserInfo
		Admin             bool
		CanDelete         bool
		CanDeploy         bool
		HasInactive       bool
		AnalyticsEnabled  bool
		Config            storage.SiteConfig
		DNSSuffix         string
		Host              string
		Sparkline         string
		RecentDeliveries  []webhook.DeliverySummary
	}{resp, userInfo(identity), admin, auth.CanDeleteSite(caps, siteName), auth.CanDeploy(caps, siteName), hasInactive, analyticsOn, siteConfig, *h.dnsSuffix, r.Host, sparkline, recentDeliveries})
}

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
	admin := auth.IsAdmin(caps)

	if !admin && !auth.CanView(caps, siteName) {
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
		log.Printf("warning: listing files for %s/%s: %v", siteName, depID, err)
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
			log.Printf("warning: listing files for %s/%s: %v", siteName, prevID, err)
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
		userInfo(identity), admin, auth.CanDeploy(caps, siteName),
		*h.dnsSuffix, siteName, *dep,
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

type DeploymentsHandler struct{ handlerDeps }

const deploymentsPageSize = 50

func (h *DeploymentsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())

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
	}{resp, userInfo(identity)})
}

// --- analytics shared data ---

// AnalyticsData is the template data for both per-site and all-sites analytics.
// SiteName is empty for the all-sites view.
type AnalyticsData struct {
	User      UserInfo
	Admin     bool
	SiteName  string // empty = all-sites view
	Range     string
	Total     int64
	Visitors  int64
	Pages     int64 // per-site only
	SiteCount int   // all-sites only

	TimeSeries       []analytics.TimeBucket
	StatusTimeSeries []analytics.StatusTimeBucket
	CountOK          int64
	Count4xx         int64
	Count5xx         int64
	TopPages         []analytics.PathCount   // per-site only
	TopVisitors      []analytics.VisitorCount
	StatusCodes      []analytics.StatusCount
	OS               []analytics.OSCount
	Nodes            []analytics.NodeCount
	Sites            []analytics.SiteCount // all-sites only
}

func statusTotals(codes []analytics.StatusCount) (ok, clientErr, serverErr int64) {
	for _, c := range codes {
		switch c.Status {
		case "4xx":
			clientErr = c.Count
		case "5xx":
			serverErr = c.Count
		default:
			ok += c.Count
		}
	}
	return
}

func parseRange(r *http.Request) (rangeParam string, from, to time.Time) {
	rangeParam = r.URL.Query().Get("range")
	to = time.Now()
	if rangeParam == "all" {
		from = time.Time{}
		return
	}
	var ok bool
	from, ok = subtractISO8601(to, rangeParam)
	if !ok {
		rangeParam = "PT24H"
		from = to.Add(-24 * time.Hour)
	}
	return
}

// subtractISO8601 parses an ISO 8601 duration string (e.g. "P7D", "PT24H",
// "P1M") and returns now minus that duration. Returns false for invalid or
// zero-length durations.
func subtractISO8601(now time.Time, s string) (time.Time, bool) {
	if len(s) < 3 || s[0] != 'P' {
		return time.Time{}, false
	}
	rest := s[1:]
	var years, months, days, hours, minutes, seconds int
	inTime := false
	for len(rest) > 0 {
		if rest[0] == 'T' {
			inTime = true
			rest = rest[1:]
			continue
		}
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i == 0 || i >= len(rest) {
			return time.Time{}, false
		}
		n, _ := strconv.Atoi(rest[:i])
		unit := rest[i]
		rest = rest[i+1:]
		switch {
		case !inTime && unit == 'Y':
			years = n
		case !inTime && unit == 'M':
			months = n
		case !inTime && unit == 'W':
			days += n * 7
		case !inTime && unit == 'D':
			days += n
		case inTime && unit == 'H':
			hours = n
		case inTime && unit == 'M':
			minutes = n
		case inTime && unit == 'S':
			seconds = n
		default:
			return time.Time{}, false
		}
	}
	if years == 0 && months == 0 && days == 0 && hours == 0 && minutes == 0 && seconds == 0 {
		return time.Time{}, false
	}
	from := now.AddDate(-years, -months, -days).Add(
		-time.Duration(hours)*time.Hour - time.Duration(minutes)*time.Minute - time.Duration(seconds)*time.Second,
	)
	return from, true
}

// --- GET /sites/{site}/analytics ---

type AnalyticsHandler struct{ handlerDeps }

func (h *AnalyticsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}
	if h.recorder == nil {
		RenderError(w, r, http.StatusServiceUnavailable, "analytics not configured")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	admin := auth.IsAdmin(caps)

	if !admin && !auth.CanView(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	if !h.analyticsEnabled(siteName) {
		RenderError(w, r, http.StatusNotFound, "analytics disabled for this site")
		return
	}

	rangeParam, from, now := parseRange(r)

	total, _ := h.recorder.TotalRequests(siteName, from, now)
	visitors, _ := h.recorder.UniqueVisitors(siteName, from, now)
	pages, _ := h.recorder.UniquePages(siteName, from, now)
	timeSeries, _ := h.recorder.RequestsOverTime(siteName, from, now)
	statusTS, _ := h.recorder.RequestsOverTimeByStatus(siteName, from, now)
	topPages, _ := h.recorder.TopPages(siteName, from, now, 20)
	topVisitors, _ := h.recorder.TopVisitors(siteName, from, now, 20)
	statusCodes, _ := h.recorder.StatusBreakdown(siteName, from, now)
	osBreakdown, _ := h.recorder.OSBreakdown(siteName, from, now)
	nodes, _ := h.recorder.NodeBreakdown(siteName, from, now)
	countOK, count4xx, count5xx := statusTotals(statusCodes)

	if wantsJSON(r) {
		setAlternateLinks(w, [][2]string{
			{"/sites/" + siteName + "/analytics", "text/html"},
		})
		writeJSON(w, map[string]any{
			"site": siteName, "range": rangeParam,
			"total": total, "unique_visitors": visitors, "unique_pages": pages,
			"time_series": timeSeries, "status_time_series": statusTS,
			"top_pages": topPages, "top_visitors": topVisitors,
			"status_codes": statusCodes, "os": osBreakdown, "nodes": nodes,
		})
		return
	}

	data := AnalyticsData{
		User: userInfo(identity), Admin: admin, SiteName: siteName,
		Range: rangeParam, Total: total, Visitors: visitors, Pages: pages,
		TimeSeries: timeSeries, StatusTimeSeries: statusTS, TopPages: topPages,
		TopVisitors: topVisitors, StatusCodes: statusCodes,
		CountOK: countOK, Count4xx: count4xx, Count5xx: count5xx,
		OS: osBreakdown, Nodes: nodes,
	}
	renderPage(w, r, analyticsTmpl, "sites", data)
}

// --- GET /analytics ---

type AllAnalyticsHandler struct{ handlerDeps }

func (h *AllAnalyticsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.recorder == nil {
		RenderError(w, r, http.StatusServiceUnavailable, "analytics not configured")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	admin := auth.IsAdmin(caps)

	sites, err := h.store.ListSites()
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "listing sites")
		return
	}
	var viewable []string
	for _, s := range sites {
		if (admin || auth.CanView(caps, s.Name)) && h.analyticsEnabled(s.Name) {
			viewable = append(viewable, s.Name)
		}
	}

	rangeParam, from, now := parseRange(r)

	total, _ := h.recorder.TotalRequestsMulti(viewable, from, now)
	visitors, _ := h.recorder.UniqueVisitorsMulti(viewable, from, now)
	timeSeries, _ := h.recorder.RequestsOverTimeMulti(viewable, from, now)
	statusTS, _ := h.recorder.RequestsOverTimeByStatusMulti(viewable, from, now)
	siteBreakdown, _ := h.recorder.SiteBreakdown(viewable, from, now)
	topVisitors, _ := h.recorder.TopVisitorsMulti(viewable, from, now, 20)
	statusCodes, _ := h.recorder.StatusBreakdownMulti(viewable, from, now)
	osBreakdown, _ := h.recorder.OSBreakdownMulti(viewable, from, now)
	nodes, _ := h.recorder.NodeBreakdownMulti(viewable, from, now)
	countOK, count4xx, count5xx := statusTotals(statusCodes)

	if wantsJSON(r) {
		setAlternateLinks(w, [][2]string{
			{"/analytics", "text/html"},
		})
		writeJSON(w, map[string]any{
			"range": rangeParam,
			"total": total, "unique_visitors": visitors,
			"time_series": timeSeries, "status_time_series": statusTS,
			"sites": siteBreakdown, "top_visitors": topVisitors,
			"status_codes": statusCodes, "os": osBreakdown, "nodes": nodes,
		})
		return
	}

	data := AnalyticsData{
		User: userInfo(identity), Admin: admin,
		Range: rangeParam, Total: total, Visitors: visitors, SiteCount: len(viewable),
		TimeSeries: timeSeries, StatusTimeSeries: statusTS, Sites: siteBreakdown,
		TopVisitors: topVisitors, StatusCodes: statusCodes,
		CountOK: countOK, Count4xx: count4xx, Count5xx: count5xx,
		OS: osBreakdown, Nodes: nodes,
	}
	renderPage(w, r, analyticsTmpl, "analytics", data)
}

// --- POST /sites/{site}/analytics/purge ---

type PurgeAnalyticsHandler struct{ handlerDeps }

func (h *PurgeAnalyticsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}
	if h.recorder == nil {
		RenderError(w, r, http.StatusServiceUnavailable, "analytics not configured")
		return
	}
	caps := auth.CapsFromContext(r.Context())
	if !auth.IsAdmin(caps) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}
	deleted, err := h.recorder.PurgeSite(siteName)
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "purging analytics")
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]int64{"deleted": deleted})
		return
	}
	http.Redirect(w, r, "/sites/"+siteName+"/analytics", http.StatusSeeOther)
}

// --- GET /webhooks ---

const webhooksPageSize = 50

type WebhooksHandler struct {
	handlerDeps
	notifier *webhook.Notifier
}

func (h *WebhooksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())

	if !auth.IsAdmin(caps) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	event := r.URL.Query().Get("event")
	status := r.URL.Query().Get("status")
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}

	offset := (page - 1) * webhooksPageSize
	var deliveries []webhook.DeliverySummary
	var total int
	if h.notifier != nil {
		deliveries, total, _ = h.notifier.ListDeliveries("", event, status, webhooksPageSize, offset)
	}

	totalPages := (total + webhooksPageSize - 1) / webhooksPageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	if wantsJSON(r) {
		writeJSON(w, map[string]any{
			"deliveries":  deliveries,
			"page":        page,
			"total_pages": totalPages,
		})
		return
	}

	renderPage(w, r, webhooksTmpl, "webhooks", struct {
		Deliveries []webhook.DeliverySummary
		Page       int
		TotalPages int
		Site       string
		Global     bool
		Event      string
		Status     string
		User       UserInfo
		BasePath   string
	}{deliveries, page, totalPages, "", true, event, status, userInfo(identity), "/webhooks"})
}

// --- GET /sites/{site}/webhooks ---

type SiteWebhooksHandler struct {
	handlerDeps
	notifier *webhook.Notifier
}

func (h *SiteWebhooksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())

	if !auth.IsAdmin(caps) && !auth.CanView(caps, siteName) && !auth.CanDeploy(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	event := r.URL.Query().Get("event")
	status := r.URL.Query().Get("status")
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}

	offset := (page - 1) * webhooksPageSize
	var deliveries []webhook.DeliverySummary
	var total int
	if h.notifier != nil {
		deliveries, total, _ = h.notifier.ListDeliveries(siteName, event, status, webhooksPageSize, offset)
	}

	totalPages := (total + webhooksPageSize - 1) / webhooksPageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	basePath := "/sites/" + siteName + "/webhooks"

	if wantsJSON(r) {
		writeJSON(w, map[string]any{
			"deliveries":  deliveries,
			"page":        page,
			"total_pages": totalPages,
		})
		return
	}

	renderPage(w, r, webhooksTmpl, "sites", struct {
		Deliveries []webhook.DeliverySummary
		Page       int
		TotalPages int
		Site       string
		Global     bool
		Event      string
		Status     string
		User       UserInfo
		BasePath   string
	}{deliveries, page, totalPages, siteName, false, event, status, userInfo(identity), basePath})
}

// countsJSON returns a JSON array of counts from the given time buckets,
// e.g. "[4,7,2,9]". Returns an empty string if there are fewer than 2 buckets
// or all counts are zero.
func countsJSON(buckets []analytics.TimeBucket) string {
	if len(buckets) < 2 {
		return ""
	}
	var any bool
	var sb strings.Builder
	sb.WriteByte('[')
	for i, b := range buckets {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatInt(b.Count, 10))
		if b.Count > 0 {
			any = true
		}
	}
	sb.WriteByte(']')
	if !any {
		return ""
	}
	return sb.String()
}

// --- GET /help/{page...} ---

type HelpHandler struct{}

func (h *HelpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	identity := auth.IdentityFromContext(r.Context())

	slug := r.PathValue("page")
	if slug == "" {
		slug = DocPages()[0].Slug
	}

	var current *DocPage
	for i := range DocPages() {
		if DocPages()[i].Slug == slug {
			current = &DocPages()[i]
			break
		}
	}
	if current == nil {
		RenderError(w, r, http.StatusNotFound, "")
		return
	}

	content, err := RenderDoc(slug)
	if err != nil {
		RenderError(w, r, http.StatusNotFound, "")
		return
	}

	renderPage(w, r, helpTmpl, "help", struct {
		User    UserInfo
		Pages   []DocPage
		Current DocPage
		Content template.HTML
	}{userInfo(identity), DocPages(), *current, content})
}

// --- GET /api ---

type APIHandler struct{}

func (h *APIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	identity := auth.IdentityFromContext(r.Context())
	renderPage(w, r, apiTmpl, "api", struct {
		User UserInfo
	}{userInfo(identity)})
}

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
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": status,
		"checks": checks,
	})
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
	if !auth.IsAdmin(caps) && !auth.CanView(caps, siteName) {
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
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":            status,
		"site":              siteName,
		"server":            map[bool]string{true: "running", false: "stopped"}[running],
		"active_deployment": site.ActiveDeploymentID,
	})
}

// diffFiles compares two file lists and returns added, removed, and changed paths.
// A file is considered "changed" if its size differs; same-size modifications are not detected.
func diffFiles(current, previous []storage.FileInfo) (added, removed, changed []string) {
	prevMap := make(map[string]int64, len(previous))
	for _, f := range previous {
		prevMap[f.Path] = f.Size
	}
	currMap := make(map[string]int64, len(current))
	for _, f := range current {
		currMap[f.Path] = f.Size
		if prevSize, ok := prevMap[f.Path]; ok {
			if f.Size != prevSize {
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
