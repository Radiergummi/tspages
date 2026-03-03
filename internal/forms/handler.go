package forms

import (
	crypto_rand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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

	// Limit total body size to 64 KB.
	const maxBodySize = 64 << 10
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)

	// Parse based on Content-Type.
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch ct {
	case "application/x-www-form-urlencoded":
		if err := r.ParseForm(); err != nil {
			if errors.As(err, new(*http.MaxBytesError)) {
				http.Error(w, "Payload Too Large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
	case "multipart/form-data":
		// maxMemory controls how much form data Go buffers in RAM vs disk;
		// the total body size is already enforced by MaxBytesReader above.
		if err := r.ParseMultipartForm(maxBodySize); err != nil {
			if errors.As(err, new(*http.MaxBytesError)) {
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

	// Collect text fields. Multi-value keys (checkboxes, multi-selects) are
	// joined with the Record Separator so they round-trip as arrays in JSON.
	// Keys ending in "[]" are treated as multi-value: the suffix is stripped
	// and even a single value is RS-encoded so it always expands to an array.
	// Any RS characters in the raw input are stripped first to prevent forgery.
	fields := make(map[string]string, len(r.Form))
	for key, values := range r.Form {
		if len(values) == 0 {
			continue
		}
		multi := strings.HasSuffix(key, "[]")
		if multi {
			key = strings.TrimSuffix(key, "[]")
		}
		// If both "foo" and "foo[]" exist in the form, the [] variant wins
		// (it was explicitly declared multi-value). Skip if already set by
		// the bracket form.
		if _, exists := fields[key]; exists {
			if !multi {
				continue
			}
		}
		clean := make([]string, len(values))
		for i, v := range values {
			clean[i] = strings.ReplaceAll(v, analytics.FieldSeparator, "")
		}
		if multi || len(clean) > 1 {
			// Trailing RS ensures even a single [] value is detected as
			// multi-valued by ExpandFields (which filters empty parts).
			fields[key] = strings.Join(clean, analytics.FieldSeparator) + analytics.FieldSeparator
		} else {
			fields[key] = clean[0]
		}
	}

	fieldsJSON, _ := json.Marshal(fields) // map[string]string cannot fail

	id := "sub_" + randomHex(16)

	info := auth.RequestInfoFromContext(r.Context())

	sub := analytics.Submission{
		ID:              id,
		Site:            h.site,
		Path:            r.URL.Path,
		Fields:          fieldsJSON,
		Submitter:       info.UserLogin,
		SubmitterName:   info.UserName,
		SubmitterPicURL: info.ProfilePicURL,
		Node:            info.NodeName,
		IP:              info.NodeIP,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
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
	if _, err := crypto_rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
