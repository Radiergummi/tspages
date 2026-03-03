package analytics

import (
	"embed"
	"log/slog"
	"net/http"

	"tspages/internal/auth"
	"tspages/internal/storage"
	"tspages/internal/web"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

var analyticsTmpl = web.NewTmpl("analytics", templateFS, "templates/analytics.gohtml")

// RegisterRoutes registers all analytics admin UI routes on the given mux.
func RegisterRoutes(mux *http.ServeMux, withAuth, withMutate, withQuery func(http.Handler) http.Handler,
	store *storage.Store, recorder *Recorder, defaults storage.SiteConfig) {

	ah := &AnalyticsHandler{store: store, recorder: recorder, defaults: defaults}
	allAh := &AllAnalyticsHandler{store: store, recorder: recorder, defaults: defaults}
	purge := &PurgeAnalyticsHandler{recorder: recorder}

	mux.Handle("GET /sites/{site}/analytics", withQuery(ah))
	mux.Handle("GET /sites/{site}/analytics.json", withQuery(ah))
	mux.Handle("GET /analytics", withQuery(allAh))
	mux.Handle("GET /analytics.json", withQuery(allAh))
	mux.Handle("POST /sites/{site}/analytics/purge", withMutate(purge))
}

// region GET /sites/{site}/analytics

type AnalyticsHandler struct {
	store    *storage.Store
	recorder *Recorder
	defaults storage.SiteConfig
}

func (h *AnalyticsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := web.TrimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		web.RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}
	if h.recorder == nil {
		web.RenderError(w, r, http.StatusServiceUnavailable, "analytics not configured")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	admin := auth.IsAdmin(caps, siteName)

	if !auth.CanDeploy(caps, siteName) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	if !web.AnalyticsEnabled(h.store, h.defaults, siteName) {
		web.RenderError(w, r, http.StatusNotFound, "analytics disabled for this site")
		return
	}

	rangeParam, from, now := web.ParseRange(r)

	total, err := h.recorder.TotalRequests(siteName, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "total_requests", "site", siteName, "err", err)
	}
	visitors, err := h.recorder.UniqueVisitors(siteName, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "unique_visitors", "site", siteName, "err", err)
	}
	pages, err := h.recorder.UniquePages(siteName, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "unique_pages", "site", siteName, "err", err)
	}
	timeSeries, err := h.recorder.RequestsOverTime(siteName, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "requests_over_time", "site", siteName, "err", err)
	}
	statusTS, err := h.recorder.RequestsOverTimeByStatus(siteName, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "requests_by_status", "site", siteName, "err", err)
	}
	topPages, err := h.recorder.TopPages(siteName, from, now, 20)
	if err != nil {
		slog.Error("analytics query failed", "query", "top_pages", "site", siteName, "err", err)
	}
	topVisitors, err := h.recorder.TopVisitors(siteName, from, now, 20)
	if err != nil {
		slog.Error("analytics query failed", "query", "top_visitors", "site", siteName, "err", err)
	}
	statusCodes, err := h.recorder.StatusBreakdown(siteName, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "status_breakdown", "site", siteName, "err", err)
	}
	osBreakdown, err := h.recorder.OSBreakdown(siteName, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "os_breakdown", "site", siteName, "err", err)
	}
	nodes, err := h.recorder.NodeBreakdown(siteName, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "node_breakdown", "site", siteName, "err", err)
	}
	countOK, count4xx, count5xx := statusTotals(statusCodes)

	if web.WantsJSON(r) {
		web.SetAlternateLinks(w, [][2]string{
			{"/sites/" + siteName + "/analytics", "text/html"},
		})
		web.WriteJSON(w, map[string]any{
			"site": siteName, "range": rangeParam,
			"total": total, "unique_visitors": visitors, "unique_pages": pages,
			"time_series": timeSeries, "status_time_series": statusTS,
			"top_pages": topPages, "top_visitors": topVisitors,
			"status_codes": statusCodes, "os": osBreakdown, "nodes": nodes,
		})
		return
	}

	data := AnalyticsData{
		User: web.NewUserInfo(identity, caps), Admin: admin, SiteName: siteName,
		Range: rangeParam, Total: total, Visitors: visitors, Pages: pages,
		TimeSeries: timeSeries, StatusTimeSeries: statusTS, TopPages: topPages,
		TopVisitors: topVisitors, StatusCodes: statusCodes,
		CountOK: countOK, Count4xx: count4xx, Count5xx: count5xx,
		OS: osBreakdown, Nodes: nodes,
	}
	web.RenderPage(w, r, analyticsTmpl, "sites", data)
}

// region GET /analytics

type AllAnalyticsHandler struct {
	store    *storage.Store
	recorder *Recorder
	defaults storage.SiteConfig
}

func (h *AllAnalyticsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.recorder == nil {
		web.RenderError(w, r, http.StatusServiceUnavailable, "analytics not configured")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	admin := auth.HasAdminCap(caps)

	if !auth.HasDeployCap(caps) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	sites, err := h.store.ListSites()
	if err != nil {
		web.RenderError(w, r, http.StatusInternalServerError, "listing sites")
		return
	}
	var viewable []string
	for _, s := range sites {
		if auth.CanDeploy(caps, s.Name) && web.AnalyticsEnabled(h.store, h.defaults, s.Name) {
			viewable = append(viewable, s.Name)
		}
	}

	rangeParam, from, now := web.ParseRange(r)

	total, err := h.recorder.TotalRequestsMulti(viewable, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "total_requests_multi", "err", err)
	}
	visitors, err := h.recorder.UniqueVisitorsMulti(viewable, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "unique_visitors_multi", "err", err)
	}
	timeSeries, err := h.recorder.RequestsOverTimeMulti(viewable, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "requests_over_time_multi", "err", err)
	}
	statusTS, err := h.recorder.RequestsOverTimeByStatusMulti(viewable, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "requests_by_status_multi", "err", err)
	}
	siteBreakdown, err := h.recorder.SiteBreakdown(viewable, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "site_breakdown", "err", err)
	}
	topVisitors, err := h.recorder.TopVisitorsMulti(viewable, from, now, 20)
	if err != nil {
		slog.Error("analytics query failed", "query", "top_visitors_multi", "err", err)
	}
	statusCodes, err := h.recorder.StatusBreakdownMulti(viewable, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "status_breakdown_multi", "err", err)
	}
	osBreakdown, err := h.recorder.OSBreakdownMulti(viewable, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "os_breakdown_multi", "err", err)
	}
	nodes, err := h.recorder.NodeBreakdownMulti(viewable, from, now)
	if err != nil {
		slog.Error("analytics query failed", "query", "node_breakdown_multi", "err", err)
	}
	countOK, count4xx, count5xx := statusTotals(statusCodes)

	if web.WantsJSON(r) {
		web.SetAlternateLinks(w, [][2]string{
			{"/analytics", "text/html"},
		})
		web.WriteJSON(w, map[string]any{
			"range": rangeParam,
			"total": total, "unique_visitors": visitors,
			"time_series": timeSeries, "status_time_series": statusTS,
			"sites": siteBreakdown, "top_visitors": topVisitors,
			"status_codes": statusCodes, "os": osBreakdown, "nodes": nodes,
		})
		return
	}

	data := AnalyticsData{
		User: web.NewUserInfo(identity, caps), Admin: admin,
		Range: rangeParam, Total: total, Visitors: visitors, SiteCount: len(viewable),
		TimeSeries: timeSeries, StatusTimeSeries: statusTS, Sites: siteBreakdown,
		TopVisitors: topVisitors, StatusCodes: statusCodes,
		CountOK: countOK, Count4xx: count4xx, Count5xx: count5xx,
		OS: osBreakdown, Nodes: nodes,
	}
	web.RenderPage(w, r, analyticsTmpl, "analytics", data)
}

// region POST /sites/{site}/analytics/purge

type PurgeAnalyticsHandler struct {
	recorder *Recorder
}

func (h *PurgeAnalyticsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := web.TrimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		web.RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}
	if h.recorder == nil {
		web.RenderError(w, r, http.StatusServiceUnavailable, "analytics not configured")
		return
	}
	caps := auth.CapsFromContext(r.Context())
	if !auth.IsAdmin(caps, siteName) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}
	deleted, err := h.recorder.PurgeSite(siteName)
	if err != nil {
		web.RenderError(w, r, http.StatusInternalServerError, "purging analytics")
		return
	}
	if web.WantsJSON(r) {
		web.WriteJSON(w, map[string]int64{"deleted": deleted})
		return
	}
	http.Redirect(w, r, "/sites/"+siteName+"/analytics", http.StatusSeeOther)
}
