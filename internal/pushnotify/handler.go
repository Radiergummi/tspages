package pushnotify

import (
	"embed"
	"encoding/json"
	"log/slog"
	"net/http"

	"tspages/internal/auth"
	"tspages/internal/storage"
	"tspages/internal/web"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

var pushSubscriptionsTmpl = web.NewTmpl("pushnotify", templateFS, "templates/push-subscriptions.gohtml")

// RegisterRoutes registers push notification routes on the given mux.
func RegisterRoutes(mux *http.ServeMux, withAuth, withMutate, withQuery func(http.Handler) http.Handler,
	push *Notifier, vapidPub string) {

	vapidKey := &VAPIDKeyHandler{vapidPub: vapidPub}
	pushSubscribe := &PushSubscribeHandler{push: push}

	mux.Handle("GET /push/vapidkey", withAuth(vapidKey))
	mux.Handle("GET /sites/{site}/push/subscriptions", withQuery(pushSubscribe))
	mux.Handle("POST /sites/{site}/push/subscriptions", withMutate(pushSubscribe))
	mux.Handle("DELETE /sites/{site}/push/subscriptions", withMutate(pushSubscribe))
}

// region GET /push/vapidkey

// VAPIDKeyHandler returns the VAPID public key for push subscriptions.
type VAPIDKeyHandler struct {
	vapidPub string
}

func (h *VAPIDKeyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	web.WriteJSON(w, map[string]string{"public_key": h.vapidPub})
}

// region /sites/{site}/push/subscriptions

// PushSubscriptionsData holds data for the push subscriptions list template.
type PushSubscriptionsData struct {
	User          web.UserInfo
	SiteName      string
	Admin         bool
	Subscriptions []SubscriptionInfo
}

// PushSubscribeHandler manages push notification subscriptions.
type PushSubscribeHandler struct {
	push *Notifier
}

func (h *PushSubscribeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Branch: status check (endpoint param present) vs list page.
		if r.URL.Query().Get("endpoint") != "" {
			h.status(w, r)
		} else {
			h.list(w, r)
		}
	case http.MethodPost:
		h.subscribe(w, r)
	case http.MethodDelete:
		h.unsubscribe(w, r)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (h *PushSubscribeHandler) status(w http.ResponseWriter, r *http.Request) {
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

	path := r.URL.Query().Get("path")
	endpoint := r.URL.Query().Get("endpoint")
	if path == "" || endpoint == "" {
		http.Error(w, "missing path or endpoint", http.StatusBadRequest)
		return
	}

	subscribed, err := h.push.IsSubscribed(siteName, path, endpoint)
	if err != nil {
		slog.Error("checking push subscription", "component", "pushnotify", "site", siteName, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	web.WriteJSON(w, map[string]bool{"subscribed": subscribed})
}

func (h *PushSubscribeHandler) list(w http.ResponseWriter, r *http.Request) {
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

	subs, err := h.push.ListSubscriptions(siteName)
	if err != nil {
		slog.Error("listing push subscriptions", "component", "pushnotify", "site", siteName, "err", err)
		web.RenderError(w, r, http.StatusInternalServerError, "listing subscriptions")
		return
	}
	if subs == nil {
		subs = []SubscriptionInfo{}
	}

	if web.WantsJSON(r) {
		web.WriteJSON(w, subs)
		return
	}

	identity := auth.IdentityFromContext(r.Context())
	admin := auth.IsAdmin(caps, siteName)

	data := PushSubscriptionsData{
		User:          web.NewUserInfo(identity, caps),
		SiteName:      siteName,
		Admin:         admin,
		Subscriptions: subs,
	}
	web.RenderPage(w, r, pushSubscriptionsTmpl, "sites", data)
}

func (h *PushSubscribeHandler) subscribe(w http.ResponseWriter, r *http.Request) {
	siteName := web.TrimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		web.RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct {
		Path      string `json:"path"`
		Endpoint  string `json:"endpoint"`
		KeyP256dh string `json:"key_p256dh"`
		KeyAuth   string `json:"key_auth"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Path == "" || req.Endpoint == "" || req.KeyP256dh == "" || req.KeyAuth == "" {
		http.Error(w, "missing required fields", http.StatusBadRequest)
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, siteName) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	identity := auth.IdentityFromContext(r.Context())
	if err := h.push.Subscribe(siteName, req.Path, identity.LoginName, Subscription{
		Endpoint:  req.Endpoint,
		KeyP256dh: req.KeyP256dh,
		KeyAuth:   req.KeyAuth,
	}); err != nil {
		slog.Error("subscribing to push notifications", "component", "pushnotify", "site", siteName, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	web.WriteJSON(w, map[string]bool{"ok": true})
}

func (h *PushSubscribeHandler) unsubscribe(w http.ResponseWriter, r *http.Request) {
	siteName := web.TrimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		web.RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct {
		Endpoint string `json:"endpoint"`
		Path     string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Endpoint == "" || req.Path == "" {
		http.Error(w, "missing required fields", http.StatusBadRequest)
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, siteName) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	identity := auth.IdentityFromContext(r.Context())
	if err := h.push.Unsubscribe(req.Endpoint, siteName, req.Path, identity.LoginName); err != nil {
		slog.Error("unsubscribing from push notifications", "component", "pushnotify", "site", siteName, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	web.WriteJSON(w, map[string]bool{"ok": true})
}
