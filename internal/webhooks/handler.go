package webhooks

import (
	"bytes"
	"embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"tspages/internal/auth"
	"tspages/internal/storage"
	"tspages/internal/web"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

var (
	webhooksTmpl      = web.NewTmpl("webhooks", templateFS, "templates/webhooks.gohtml")
	webhookDetailTmpl = web.NewTmpl("webhooks", templateFS, "templates/webhook.gohtml")
)

// RegisterRoutes registers all webhook admin UI routes on the given mux.
func RegisterRoutes(mux *http.ServeMux, withAuth, withMutate, withQuery func(http.Handler) http.Handler,
	store *storage.Store, defaults storage.SiteConfig, notifier *Notifier) {

	wh := &WebhooksHandler{store: store, defaults: defaults, notifier: notifier}
	swh := &SiteWebhooksHandler{WebhooksHandler: wh}
	det := &WebhookDetailHandler{store: store, defaults: defaults, notifier: notifier}
	retry := &WebhookRetryHandler{store: store, defaults: defaults, notifier: notifier}

	mux.Handle("GET /sites/{site}/webhooks", withQuery(swh))
	mux.Handle("GET /sites/{site}/webhooks.json", withQuery(swh))
	mux.Handle("GET /webhooks", withQuery(wh))
	mux.Handle("GET /webhooks.json", withQuery(wh))
	mux.Handle("GET /webhooks/{id}", withQuery(det))
	mux.Handle("POST /webhooks/{id}/retry", withMutate(retry))
}

const webhooksPageSize = 50

// region GET /webhooks

type WebhooksHandler struct {
	store    *storage.Store
	defaults storage.SiteConfig
	notifier *Notifier
}

func (h *WebhooksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())
	if !auth.HasDeployCap(caps) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}
	h.serveWebhooks(w, r, "", "/webhooks", "webhooks", true)
}

// region GET /sites/{site}/webhooks

type SiteWebhooksHandler struct {
	*WebhooksHandler
}

func (h *SiteWebhooksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := web.TrimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		web.RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}
	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, siteName) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}
	h.serveWebhooks(w, r, siteName, "/sites/"+siteName+"/webhooks", "sites", false)
}

// serveWebhooks is the shared implementation for global and site-scoped webhook pages.
func (h *WebhooksHandler) serveWebhooks(w http.ResponseWriter, r *http.Request, site, basePath, navTab string, global bool) {
	identity := auth.IdentityFromContext(r.Context())
	caps := auth.CapsFromContext(r.Context())

	event := r.URL.Query().Get("event")
	status := r.URL.Query().Get("status")
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}

	offset := (page - 1) * webhooksPageSize
	var deliveries []DeliverySummary
	var total int
	if h.notifier != nil {
		var err error
		deliveries, total, err = h.notifier.ListDeliveries(site, event, status, webhooksPageSize, offset)
		if err != nil {
			slog.Error("listing webhook deliveries failed", "err", err)
		}
	}

	totalPages := (total + webhooksPageSize - 1) / webhooksPageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	rangeParam, from, now := web.ParseRange(r)

	var statsTotal, statsSucceeded, statsFailed int64
	var timeSeries []DeliveryTimeBucket
	var events []EventCount
	var latency []LatencyTimeBucket
	var latencyStats LatencyStats
	if h.notifier != nil {
		var err error
		statsTotal, statsSucceeded, statsFailed, err = h.notifier.DeliveryStats(site, from, now)
		if err != nil {
			slog.Error("webhook query failed", "query", "delivery_stats", "err", err)
		}
		timeSeries, err = h.notifier.DeliveriesOverTime(site, from, now)
		if err != nil {
			slog.Error("webhook query failed", "query", "deliveries_over_time", "err", err)
		}
		events, err = h.notifier.EventBreakdown(site, from, now)
		if err != nil {
			slog.Error("webhook query failed", "query", "event_breakdown", "err", err)
		}
		latency, err = h.notifier.LatencyOverTime(site, from, now)
		if err != nil {
			slog.Error("webhook query failed", "query", "latency_over_time", "err", err)
		}
		latencyStats, err = h.notifier.LatencyStats(site, from, now)
		if err != nil {
			slog.Error("webhook query failed", "query", "latency_stats", "err", err)
		}
	}

	if web.WantsJSON(r) {
		web.WriteJSON(w, map[string]any{
			"deliveries":    deliveries,
			"page":          page,
			"total_pages":   totalPages,
			"range":         rangeParam,
			"total":         statsTotal,
			"succeeded":     statsSucceeded,
			"failed":        statsFailed,
			"time_series":   timeSeries,
			"events":        events,
			"latency":       latency,
			"latency_stats": latencyStats,
		})
		return
	}

	web.RenderPage(w, r, webhooksTmpl, navTab, struct {
		Deliveries   []DeliverySummary
		Page         int
		TotalPages   int
		Site         string
		Global       bool
		Event        string
		Status       string
		User         web.UserInfo
		BasePath     string
		Range        string
		Total        int64
		Succeeded    int64
		Failed       int64
		TimeSeries   []DeliveryTimeBucket
		Events       []EventCount
		Latency      []LatencyTimeBucket
		LatencyStats LatencyStats
	}{deliveries, page, totalPages, site, global, event, status, web.NewUserInfo(identity, caps), basePath,
		rangeParam, statsTotal, statsSucceeded, statsFailed, timeSeries, events, latency, latencyStats})
}

// region GET /webhooks/{id}

type WebhookDetailHandler struct {
	store    *storage.Store
	defaults storage.SiteConfig
	notifier *Notifier
}

func (h *WebhookDetailHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	webhookID := web.TrimSuffix(r.PathValue("id"))
	if webhookID == "" {
		web.RenderError(w, r, http.StatusBadRequest, "missing webhook ID")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())

	if !auth.HasDeployCap(caps) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	if h.notifier == nil {
		web.RenderError(w, r, http.StatusNotFound, "webhooks not configured")
		return
	}

	delivery, err := h.notifier.GetDelivery(webhookID)
	if err != nil {
		web.RenderError(w, r, http.StatusNotFound, "delivery not found")
		return
	}

	if !auth.CanDeploy(caps, delivery.Site) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	attempts, err := h.notifier.GetDeliveryAttempts(webhookID)
	if err != nil {
		slog.Error("getting webhook delivery attempts failed", "webhook_id", webhookID, "err", err)
	}
	for i, a := range attempts {
		var buf bytes.Buffer
		if json.Indent(&buf, []byte(a.Payload), "", "  ") == nil {
			attempts[i].Payload = buf.String()
		}
	}

	if web.WantsJSON(r) {
		web.WriteJSON(w, map[string]any{
			"delivery": delivery,
			"attempts": attempts,
		})
		return
	}

	web.RenderPage(w, r, webhookDetailTmpl, "webhooks", struct {
		Delivery DeliverySummary
		Attempts []DeliveryAttempt
		User     web.UserInfo
	}{delivery, attempts, web.NewUserInfo(identity, caps)})
}

// region POST /webhooks/{id}/retry

type WebhookRetryHandler struct {
	store    *storage.Store
	defaults storage.SiteConfig
	notifier *Notifier
}

func (h *WebhookRetryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	webhookID := web.TrimSuffix(r.PathValue("id"))
	if webhookID == "" {
		web.RenderError(w, r, http.StatusBadRequest, "missing webhook ID")
		return
	}

	caps := auth.CapsFromContext(r.Context())

	if !auth.HasAdminCap(caps) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	if h.notifier == nil {
		web.RenderError(w, r, http.StatusNotFound, "webhooks not configured")
		return
	}

	delivery, err := h.notifier.GetDelivery(webhookID)
	if err != nil {
		web.RenderError(w, r, http.StatusNotFound, "delivery not found")
		return
	}

	if !auth.IsAdmin(caps, delivery.Site) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	deployCfg, _ := h.store.ReadCurrentDeploymentConfig(delivery.Site)
	siteCfg, _ := h.store.ReadSiteConfig(delivery.Site)
	merged := deployCfg.Merge(siteCfg).Merge(h.defaults)

	status, err := h.notifier.Resend(webhookID, merged.WebhookSecret)
	if err != nil {
		slog.Error("webhook retry failed", "webhook_id", webhookID, "err", err)
		web.RenderError(w, r, http.StatusBadGateway, "retry failed")
		return
	}

	if web.WantsJSON(r) {
		web.WriteJSON(w, map[string]int{"status": status})
		return
	}
	http.Redirect(w, r, "/webhooks/"+webhookID, http.StatusSeeOther)
}
