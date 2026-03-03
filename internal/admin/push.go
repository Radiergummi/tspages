package admin

import (
	"encoding/json"
	"net/http"

	"tspages/internal/auth"
	"tspages/internal/pushnotify"
	"tspages/internal/web"
)

// region GET /push/vapidkey

// VAPIDKeyHandler returns the VAPID public key for push subscriptions.
type VAPIDKeyHandler struct {
	vapidPub string
}

func (h *VAPIDKeyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	web.WriteJSON(w, map[string]string{"public_key": h.vapidPub})
}

// region POST /push/subscribe

// PushSubscribeHandler manages push notification subscriptions.
type PushSubscribeHandler struct {
	push *pushnotify.Notifier
}

func (h *PushSubscribeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.status(w, r)
	case http.MethodPost:
		h.subscribe(w, r)
	case http.MethodDelete:
		h.unsubscribe(w, r)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (h *PushSubscribeHandler) status(w http.ResponseWriter, r *http.Request) {
	site := r.URL.Query().Get("site")
	path := r.URL.Query().Get("path")
	endpoint := r.URL.Query().Get("endpoint")
	if site == "" || path == "" || endpoint == "" {
		http.Error(w, "missing site, path, or endpoint", http.StatusBadRequest)
		return
	}
	subscribed, err := h.push.IsSubscribed(site, path, endpoint)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	web.WriteJSON(w, map[string]bool{"subscribed": subscribed})
}

func (h *PushSubscribeHandler) subscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Site      string `json:"site"`
		Path      string `json:"path"`
		Endpoint  string `json:"endpoint"`
		KeyP256dh string `json:"key_p256dh"`
		KeyAuth   string `json:"key_auth"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Site == "" || req.Path == "" || req.Endpoint == "" || req.KeyP256dh == "" || req.KeyAuth == "" {
		http.Error(w, "missing required fields", http.StatusBadRequest)
		return
	}

	// Require deploy capability on the site.
	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, req.Site) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	identity := auth.IdentityFromContext(r.Context())
	if err := h.push.Subscribe(req.Site, req.Path, identity.LoginName, pushnotify.Subscription{
		Endpoint:  req.Endpoint,
		KeyP256dh: req.KeyP256dh,
		KeyAuth:   req.KeyAuth,
	}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	web.WriteJSON(w, map[string]bool{"ok": true})
}

func (h *PushSubscribeHandler) unsubscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Endpoint string `json:"endpoint"`
		Site     string `json:"site"`
		Path     string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Endpoint == "" || req.Site == "" || req.Path == "" {
		http.Error(w, "missing required fields", http.StatusBadRequest)
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, req.Site) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	identity := auth.IdentityFromContext(r.Context())
	if err := h.push.Unsubscribe(req.Endpoint, req.Site, req.Path, identity.LoginName); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	web.WriteJSON(w, map[string]bool{"ok": true})
}
