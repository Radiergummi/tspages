package admin

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/storage"
)

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
	TopPages         []analytics.PathCount // per-site only
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
	admin := auth.IsAdmin(caps, siteName)

	if !auth.CanDeploy(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	if !h.analyticsEnabled(siteName) {
		RenderError(w, r, http.StatusNotFound, "analytics disabled for this site")
		return
	}

	rangeParam, from, now := parseRange(r)

	total, err := h.recorder.TotalRequests(siteName, from, now)
	if err != nil {
		log.Printf("analytics: total requests for %s: %v", siteName, err)
	}
	visitors, err := h.recorder.UniqueVisitors(siteName, from, now)
	if err != nil {
		log.Printf("analytics: unique visitors for %s: %v", siteName, err)
	}
	pages, err := h.recorder.UniquePages(siteName, from, now)
	if err != nil {
		log.Printf("analytics: unique pages for %s: %v", siteName, err)
	}
	timeSeries, err := h.recorder.RequestsOverTime(siteName, from, now)
	if err != nil {
		log.Printf("analytics: requests over time for %s: %v", siteName, err)
	}
	statusTS, err := h.recorder.RequestsOverTimeByStatus(siteName, from, now)
	if err != nil {
		log.Printf("analytics: requests by status for %s: %v", siteName, err)
	}
	topPages, err := h.recorder.TopPages(siteName, from, now, 20)
	if err != nil {
		log.Printf("analytics: top pages for %s: %v", siteName, err)
	}
	topVisitors, err := h.recorder.TopVisitors(siteName, from, now, 20)
	if err != nil {
		log.Printf("analytics: top visitors for %s: %v", siteName, err)
	}
	statusCodes, err := h.recorder.StatusBreakdown(siteName, from, now)
	if err != nil {
		log.Printf("analytics: status breakdown for %s: %v", siteName, err)
	}
	osBreakdown, err := h.recorder.OSBreakdown(siteName, from, now)
	if err != nil {
		log.Printf("analytics: os breakdown for %s: %v", siteName, err)
	}
	nodes, err := h.recorder.NodeBreakdown(siteName, from, now)
	if err != nil {
		log.Printf("analytics: node breakdown for %s: %v", siteName, err)
	}
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
		User: userInfo(identity, caps), Admin: admin, SiteName: siteName,
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
	admin := auth.HasAdminCap(caps)

	if !auth.HasDeployCap(caps) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	sites, err := h.store.ListSites()
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "listing sites")
		return
	}
	var viewable []string
	for _, s := range sites {
		if auth.CanDeploy(caps, s.Name) && h.analyticsEnabled(s.Name) {
			viewable = append(viewable, s.Name)
		}
	}

	rangeParam, from, now := parseRange(r)

	total, err := h.recorder.TotalRequestsMulti(viewable, from, now)
	if err != nil {
		log.Printf("analytics: total requests multi: %v", err)
	}
	visitors, err := h.recorder.UniqueVisitorsMulti(viewable, from, now)
	if err != nil {
		log.Printf("analytics: unique visitors multi: %v", err)
	}
	timeSeries, err := h.recorder.RequestsOverTimeMulti(viewable, from, now)
	if err != nil {
		log.Printf("analytics: requests over time multi: %v", err)
	}
	statusTS, err := h.recorder.RequestsOverTimeByStatusMulti(viewable, from, now)
	if err != nil {
		log.Printf("analytics: requests by status multi: %v", err)
	}
	siteBreakdown, err := h.recorder.SiteBreakdown(viewable, from, now)
	if err != nil {
		log.Printf("analytics: site breakdown: %v", err)
	}
	topVisitors, err := h.recorder.TopVisitorsMulti(viewable, from, now, 20)
	if err != nil {
		log.Printf("analytics: top visitors multi: %v", err)
	}
	statusCodes, err := h.recorder.StatusBreakdownMulti(viewable, from, now)
	if err != nil {
		log.Printf("analytics: status breakdown multi: %v", err)
	}
	osBreakdown, err := h.recorder.OSBreakdownMulti(viewable, from, now)
	if err != nil {
		log.Printf("analytics: os breakdown multi: %v", err)
	}
	nodes, err := h.recorder.NodeBreakdownMulti(viewable, from, now)
	if err != nil {
		log.Printf("analytics: node breakdown multi: %v", err)
	}
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
		User: userInfo(identity, caps), Admin: admin,
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
	if !auth.IsAdmin(caps, siteName) {
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
