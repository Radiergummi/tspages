package forms

import (
	crypto_rand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net/http"
	"strings"
	"time"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/storage"
)

// Notifier is the subset of webhook.Notifier needed by the form handler.
type Notifier interface {
	Fire(event string, site string, cfg storage.SiteConfig, data map[string]any)
}

// Handler processes POST requests to site paths when forms are enabled.
type Handler struct {
	recorder   *analytics.Recorder
	notifier   Notifier
	site       string
	configFunc func() storage.SiteConfig
}

// NewHandler creates a form submission handler for the given site.
func NewHandler(recorder *analytics.Recorder, notifier Notifier, site string, configFunc func() storage.SiteConfig) *Handler {
	return &Handler{
		recorder:   recorder,
		notifier:   notifier,
		site:       site,
		configFunc: configFunc,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cfg := h.configFunc()

	// Check forms enabled.
	if cfg.Forms == nil || cfg.Forms.Enabled == nil || !*cfg.Forms.Enabled {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit body size.
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)

	// Parse based on Content-Type.
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch ct {
	case "application/x-www-form-urlencoded":
		if err := r.ParseForm(); err != nil {
			if err.Error() == "http: request body too large" {
				http.Error(w, "Payload Too Large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
	case "multipart/form-data":
		if err := r.ParseMultipartForm(64 << 10); err != nil {
			if err.Error() == "http: request body too large" {
				http.Error(w, "Payload Too Large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "Unsupported Media Type", http.StatusUnsupportedMediaType)
		return
	}

	// Collect text fields (first value only).
	fields := make(map[string]string, len(r.Form))
	for key, values := range r.Form {
		if len(values) > 0 {
			fields[key] = values[0]
		}
	}

	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	id := "sub_" + randomHex(16)

	info := auth.RequestInfoFromContext(r.Context())

	sub := analytics.Submission{
		ID:        id,
		Site:      h.site,
		Path:      r.URL.Path,
		Fields:    fieldsJSON,
		Submitter: info.UserLogin,
		Node:      info.NodeName,
		IP:        info.NodeIP,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if err := h.recorder.RecordSubmission(sub); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Fire webhook.
	h.notifier.Fire("form.submission", h.site, cfg, map[string]any{
		"id":        id,
		"path":      r.URL.Path,
		"fields":    fields,
		"submitter": info.UserLogin,
	})

	// Respond based on Accept header.
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"id": id,
		})
		return
	}

	http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
}

func randomHex(n int) string {
	b := make([]byte, n)
	crypto_rand.Read(b)
	return hex.EncodeToString(b)
}
