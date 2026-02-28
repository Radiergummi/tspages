package admin

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"tspages/internal/auth"
	"tspages/internal/storage"
	"tspages/internal/webhook"
)

// --- GET /webhooks ---

const webhooksPageSize = 50

type WebhooksHandler struct {
	handlerDeps
	notifier *webhook.Notifier
}

func (h *WebhooksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())
	if !auth.HasDeployCap(caps) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}
	h.serveWebhooks(w, r, "", "/webhooks", "webhooks", true)
}

// --- GET /sites/{site}/webhooks ---

type SiteWebhooksHandler struct {
	*WebhooksHandler
}

func (h *SiteWebhooksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}
	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
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
	var deliveries []webhook.DeliverySummary
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

	rangeParam, from, now := parseRange(r)

	var statsTotal, statsSucceeded, statsFailed int64
	var timeSeries []webhook.DeliveryTimeBucket
	var events []webhook.EventCount
	var latency []webhook.LatencyTimeBucket
	var latencyStats webhook.LatencyStats
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

	if wantsJSON(r) {
		writeJSON(w, map[string]any{
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

	renderPage(w, r, webhooksTmpl, navTab, struct {
		Deliveries   []webhook.DeliverySummary
		Page         int
		TotalPages   int
		Site         string
		Global       bool
		Event        string
		Status       string
		User         UserInfo
		BasePath     string
		Range        string
		Total        int64
		Succeeded    int64
		Failed       int64
		TimeSeries   []webhook.DeliveryTimeBucket
		Events       []webhook.EventCount
		Latency      []webhook.LatencyTimeBucket
		LatencyStats webhook.LatencyStats
	}{deliveries, page, totalPages, site, global, event, status, userInfo(identity, caps), basePath,
		rangeParam, statsTotal, statsSucceeded, statsFailed, timeSeries, events, latency, latencyStats})
}

// --- GET /webhooks/{id} ---

type WebhookDetailHandler struct {
	handlerDeps
	notifier *webhook.Notifier
}

func (h *WebhookDetailHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	webhookID := trimSuffix(r.PathValue("id"))
	if webhookID == "" {
		RenderError(w, r, http.StatusBadRequest, "missing webhook ID")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())

	if !auth.HasDeployCap(caps) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	if h.notifier == nil {
		RenderError(w, r, http.StatusNotFound, "webhooks not configured")
		return
	}

	delivery, err := h.notifier.GetDelivery(webhookID)
	if err != nil {
		RenderError(w, r, http.StatusNotFound, "delivery not found")
		return
	}

	if !auth.CanDeploy(caps, delivery.Site) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
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

	if wantsJSON(r) {
		writeJSON(w, map[string]any{
			"delivery": delivery,
			"attempts": attempts,
		})
		return
	}

	renderPage(w, r, webhookDetailTmpl, "webhooks", struct {
		Delivery webhook.DeliverySummary
		Attempts []webhook.DeliveryAttempt
		User     UserInfo
	}{delivery, attempts, userInfo(identity, caps)})
}

// --- POST /webhooks/{id}/retry ---

type WebhookRetryHandler struct {
	handlerDeps
	notifier *webhook.Notifier
}

func (h *WebhookRetryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	webhookID := trimSuffix(r.PathValue("id"))
	if webhookID == "" {
		RenderError(w, r, http.StatusBadRequest, "missing webhook ID")
		return
	}

	caps := auth.CapsFromContext(r.Context())

	if !auth.HasAdminCap(caps) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	if h.notifier == nil {
		RenderError(w, r, http.StatusNotFound, "webhooks not configured")
		return
	}

	delivery, err := h.notifier.GetDelivery(webhookID)
	if err != nil {
		RenderError(w, r, http.StatusNotFound, "delivery not found")
		return
	}

	if !auth.IsAdmin(caps, delivery.Site) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	cfg, _ := h.store.ReadCurrentSiteConfig(delivery.Site)
	merged := cfg.Merge(h.defaults)

	status, err := h.notifier.Resend(webhookID, merged.WebhookSecret)
	if err != nil {
		slog.Error("webhook retry failed", "webhook_id", webhookID, "err", err)
		RenderError(w, r, http.StatusBadGateway, "retry failed")
		return
	}

	if wantsJSON(r) {
		writeJSON(w, map[string]int{"status": status})
		return
	}
	http.Redirect(w, r, "/webhooks/"+webhookID, http.StatusSeeOther)
}
