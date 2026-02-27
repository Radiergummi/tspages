package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"tspages/internal/auth"
	"tspages/internal/metrics"
	"tspages/internal/storage"
	"tspages/internal/webhook"
)

// SiteManager manages per-site tsnet server lifecycle.
type SiteManager interface {
	EnsureServer(site string) error
	StopServer(site string) error
}

type DeployResponse struct {
	DeploymentID string `json:"deployment_id"`
	Site         string `json:"site"`
	URL          string `json:"url"`
}

type Handler struct {
	store          *storage.Store
	manager        SiteManager
	maxUploadMB    int
	maxDeployments int
	dnsSuffix      string
	notifier       *webhook.Notifier
	defaults       storage.SiteConfig
}

// HandlerConfig holds configuration for creating a new deploy Handler.
type HandlerConfig struct {
	Store          *storage.Store
	Manager        SiteManager
	MaxUploadMB    int
	MaxDeployments int
	DNSSuffix      string
	Notifier       *webhook.Notifier
	Defaults       storage.SiteConfig
}

func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{
		store:          cfg.Store,
		manager:        cfg.Manager,
		maxUploadMB:    cfg.MaxUploadMB,
		maxDeployments: cfg.MaxDeployments,
		dnsSuffix:      cfg.DNSSuffix,
		notifier:       cfg.Notifier,
		defaults:       cfg.Defaults,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site := r.PathValue("site")
	if !storage.ValidSiteNameForSuffix(site, h.dnsSuffix) {
		http.Error(w, "invalid site name", http.StatusBadRequest)
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, site) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	maxBytes := int64(h.maxUploadMB) << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "reading upload", http.StatusBadRequest)
		}
		return
	}

	if len(body) == 0 {
		http.Error(w, "empty upload", http.StatusBadRequest)
		return
	}

	var id, deployDir string
	for range 10 {
		id = storage.NewDeploymentID()
		var err error
		deployDir, err = h.store.CreateDeployment(site, id)
		if err == nil {
			break
		}
		if !errors.Is(err, storage.ErrDeploymentExists) {
			http.Error(w, "creating deployment", http.StatusInternalServerError)
			return
		}
	}
	if deployDir == "" {
		http.Error(w, "creating deployment: too many ID collisions", http.StatusInternalServerError)
		return
	}

	contentDir := filepath.Join(deployDir, "content")
	if err := os.MkdirAll(contentDir, 0755); err != nil {
		os.RemoveAll(deployDir)
		http.Error(w, "creating content dir", http.StatusInternalServerError)
		return
	}

	// Build identity and manifest early so failed deployments have metadata.
	identity := auth.IdentityFromContext(r.Context())
	deployedBy := identity.DisplayName
	if deployedBy == "" {
		deployedBy = identity.LoginName
	}
	writeManifest := func(size int64) error {
		return h.store.WriteManifest(site, id, storage.Manifest{
			Site:            site,
			ID:              id,
			CreatedAt:       time.Now(),
			CreatedBy:       deployedBy,
			CreatedByAvatar: identity.ProfilePicURL,
			SizeBytes:       size,
		})
	}
	// markFailed writes a manifest (if possible) and marks the deployment as failed.
	markFailed := func(size int64, reason string) {
		if err := writeManifest(size); err != nil {
			log.Printf("warning: writing manifest for failed deployment %s/%s: %v", site, id, err)
		}
		if files, err := h.store.ListDeploymentFiles(site, id); err == nil {
			if err := h.store.WriteFileIndex(site, id, files); err != nil {
				log.Printf("warning: writing file index for failed deployment %s/%s: %v", site, id, err)
			}
		}
		if err := h.store.MarkFailed(site, id, reason); err != nil {
			log.Printf("warning: marking deployment %s/%s as failed: %v", site, id, err)
		}
	}

	extractReq := ExtractRequest{
		Body:     body,
		Query:    r.URL.Query().Get("format"),
		ContentType:        r.Header.Get("Content-Type"),
		ContentDisposition: r.Header.Get("Content-Disposition"),
		Filename: r.PathValue("filename"),
	}
	extractedBytes, err := Extract(extractReq, contentDir, maxBytes)
	if err != nil {
		markFailed(0, fmt.Sprintf("extracting upload: %v", err))
		h.fireDeployFailed(site, err)
		http.Error(w, fmt.Sprintf("extracting upload: %v", err), http.StatusBadRequest)
		return
	}

	// Write manifest now that we know the extracted size.
	if err := writeManifest(extractedBytes); err != nil {
		os.RemoveAll(deployDir)
		http.Error(w, "writing manifest", http.StatusInternalServerError)
		return
	}

	// Build site config from _redirects, _headers, and tspages.toml.
	// tspages.toml values take priority over _redirects/_headers.
	var siteCfg storage.SiteConfig
	hasConfig := false

	// Parse _redirects file (lower priority).
	redirectsPath := filepath.Join(contentDir, "_redirects")
	if data, err := os.ReadFile(redirectsPath); err == nil {
		rules, err := storage.ParseRedirectsFile(data)
		if err != nil {
			markFailed(extractedBytes, fmt.Sprintf("invalid _redirects: %v", err))
			h.fireDeployFailed(site, err)
			http.Error(w, fmt.Sprintf("invalid _redirects: %v", err), http.StatusBadRequest)
			return
		}
		siteCfg.Redirects = rules
		if err := os.Remove(redirectsPath); err != nil && !os.IsNotExist(err) {
			log.Printf("warning: removing _redirects: %v", err)
		}
		hasConfig = hasConfig || len(rules) > 0
	}

	// Parse _headers file (lower priority).
	headersPath := filepath.Join(contentDir, "_headers")
	if data, err := os.ReadFile(headersPath); err == nil {
		hdrs, err := storage.ParseHeadersFile(data)
		if err != nil {
			markFailed(extractedBytes, fmt.Sprintf("invalid _headers: %v", err))
			h.fireDeployFailed(site, err)
			http.Error(w, fmt.Sprintf("invalid _headers: %v", err), http.StatusBadRequest)
			return
		}
		siteCfg.Headers = hdrs
		if err := os.Remove(headersPath); err != nil && !os.IsNotExist(err) {
			log.Printf("warning: removing _headers: %v", err)
		}
		hasConfig = hasConfig || len(hdrs) > 0
	}

	// Parse tspages.toml (higher priority — merges over _redirects/_headers).
	configPath := filepath.Join(contentDir, "tspages.toml")
	if configData, err := os.ReadFile(configPath); err == nil {
		tomlCfg, err := storage.ParseSiteConfig(configData)
		if err != nil {
			markFailed(extractedBytes, fmt.Sprintf("invalid tspages.toml: %v", err))
			h.fireDeployFailed(site, err)
			http.Error(w, fmt.Sprintf("invalid tspages.toml: %v", err), http.StatusBadRequest)
			return
		}
		siteCfg = tomlCfg.Merge(siteCfg)
		if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
			log.Printf("warning: removing tspages.toml: %v", err)
		}
		hasConfig = true
	}

	if hasConfig {
		if err := siteCfg.Validate(); err != nil {
			markFailed(extractedBytes, fmt.Sprintf("invalid config: %v", err))
			h.fireDeployFailed(site, err)
			http.Error(w, fmt.Sprintf("invalid config: %v", err), http.StatusBadRequest)
			return
		}
		if err := h.store.WriteSiteConfig(site, id, siteCfg); err != nil {
			markFailed(extractedBytes, fmt.Sprintf("writing site config: %v", err))
			http.Error(w, "writing site config", http.StatusInternalServerError)
			return
		}
	}

	// Cache the file index so ListDeploymentFiles can skip hashing later.
	if files, err := h.store.ListDeploymentFiles(site, id); err != nil {
		log.Printf("warning: listing files for %s/%s: %v", site, id, err)
	} else if err := h.store.WriteFileIndex(site, id, files); err != nil {
		log.Printf("warning: writing file index for %s/%s: %v", site, id, err)
	}

	if err := h.store.MarkComplete(site, id); err != nil {
		os.RemoveAll(deployDir)
		http.Error(w, "finalizing deployment", http.StatusInternalServerError)
		return
	}

	if r.URL.Query().Get("activate") != "false" {
		if err := h.store.ActivateDeployment(site, id); err != nil {
			http.Error(w, "activating deployment", http.StatusInternalServerError)
			return
		}
		if err := h.manager.EnsureServer(site); err != nil {
			log.Printf("warning: site %q deployed but server failed to start: %v", site, err)
		}
	}

	// Clean up old deployments, keeping the configured maximum.
	if h.maxDeployments > 0 {
		if n, err := h.store.CleanupOldDeployments(site, h.maxDeployments); err != nil {
			log.Printf("warning: cleaning old deployments for %q: %v", site, err)
		} else if n > 0 {
			log.Printf("cleaned %d old deployment(s) for %q", n, site)
		}
	}

	metrics.CountDeploy(site, extractedBytes)

	resp := DeployResponse{
		DeploymentID: id,
		Site:         site,
		URL:          fmt.Sprintf("https://%s.%s/", site, h.dnsSuffix),
	}
	writeJSON(w, resp)

	if h.notifier != nil {
		resolvedCfg := siteCfg.Merge(h.defaults)
		h.notifier.Fire("deploy.success", site, resolvedCfg, map[string]any{
			"site":          site,
			"deployment_id": id,
			"created_by":    deployedBy,
			"url":           resp.URL,
			"size_bytes":    extractedBytes,
		})
	}
}

func (h *Handler) fireDeployFailed(site string, err error) {
	if h.notifier == nil {
		return
	}
	cfg, _ := h.store.ReadCurrentSiteConfig(site)
	resolvedCfg := cfg.Merge(h.defaults)
	h.notifier.Fire("deploy.failed", site, resolvedCfg, map[string]any{
		"site":  site,
		"error": err.Error(),
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("warning: encoding JSON response: %v", err)
	}
}
