package deploy

import (
	"encoding/json"
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
	dnsSuffix      *string
}

func NewHandler(store *storage.Store, manager SiteManager, maxUploadMB, maxDeployments int, dnsSuffix *string) *Handler {
	return &Handler{store: store, manager: manager, maxUploadMB: maxUploadMB, maxDeployments: maxDeployments, dnsSuffix: dnsSuffix}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site := r.PathValue("site")
	if !storage.ValidSiteNameForSuffix(site, *h.dnsSuffix) {
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
		http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
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
		if err != storage.ErrDeploymentExists {
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
		http.Error(w, "creating content dir", http.StatusInternalServerError)
		return
	}

	req := ExtractRequest{
		Body:     body,
		Query:    r.URL.Query().Get("format"),
		ContentType:        r.Header.Get("Content-Type"),
		ContentDisposition: r.Header.Get("Content-Disposition"),
		Filename: r.PathValue("filename"),
	}
	_, err = Extract(req, contentDir, maxBytes)
	if err != nil {
		os.RemoveAll(deployDir)
		http.Error(w, fmt.Sprintf("extracting upload: %v", err), http.StatusBadRequest)
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
			os.RemoveAll(deployDir)
			http.Error(w, fmt.Sprintf("invalid _redirects: %v", err), http.StatusBadRequest)
			return
		}
		siteCfg.Redirects = rules
		os.Remove(redirectsPath)
		hasConfig = hasConfig || len(rules) > 0
	}

	// Parse _headers file (lower priority).
	headersPath := filepath.Join(contentDir, "_headers")
	if data, err := os.ReadFile(headersPath); err == nil {
		hdrs, err := storage.ParseHeadersFile(data)
		if err != nil {
			os.RemoveAll(deployDir)
			http.Error(w, fmt.Sprintf("invalid _headers: %v", err), http.StatusBadRequest)
			return
		}
		siteCfg.Headers = hdrs
		os.Remove(headersPath)
		hasConfig = hasConfig || len(hdrs) > 0
	}

	// Parse tspages.toml (higher priority — merges over _redirects/_headers).
	configPath := filepath.Join(contentDir, "tspages.toml")
	if configData, err := os.ReadFile(configPath); err == nil {
		tomlCfg, err := storage.ParseSiteConfig(configData)
		if err != nil {
			os.RemoveAll(deployDir)
			http.Error(w, fmt.Sprintf("invalid tspages.toml: %v", err), http.StatusBadRequest)
			return
		}
		siteCfg = tomlCfg.Merge(siteCfg)
		os.Remove(configPath)
		hasConfig = true
	}

	if hasConfig {
		if err := siteCfg.Validate(); err != nil {
			os.RemoveAll(deployDir)
			http.Error(w, fmt.Sprintf("invalid config: %v", err), http.StatusBadRequest)
			return
		}
		if err := h.store.WriteSiteConfig(site, id, siteCfg); err != nil {
			os.RemoveAll(deployDir)
			http.Error(w, "writing site config", http.StatusInternalServerError)
			return
		}
	}

	identity := auth.IdentityFromContext(r.Context())
	deployedBy := identity.DisplayName
	if deployedBy == "" {
		deployedBy = identity.LoginName
	}
	manifest := storage.Manifest{
		Site:            site,
		ID:              id,
		CreatedAt:       time.Now(),
		CreatedBy:       deployedBy,
		CreatedByAvatar: identity.ProfilePicURL,
		SizeBytes:       int64(len(body)),
	}
	if err := h.store.WriteManifest(site, id, manifest); err != nil {
		http.Error(w, "writing manifest", http.StatusInternalServerError)
		return
	}

	if err := h.store.MarkComplete(site, id); err != nil {
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

	metrics.CountDeploy(site, int64(len(body)))

	resp := DeployResponse{
		DeploymentID: id,
		Site:         site,
		URL:          fmt.Sprintf("https://%s.%s/", site, *h.dnsSuffix),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
