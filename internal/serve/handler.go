package serve

import (
	_ "embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

//go:embed templates/404.gohtml
var default404HTML []byte

//go:embed templates/placeholder.gohtml
var placeholderTmplStr string

var placeholderTmpl = template.Must(template.New("placeholder").Parse(placeholderTmplStr))

type Handler struct {
	store     *storage.Store
	site      string
	dnsSuffix *string
	defaults  storage.SiteConfig

	mu        sync.RWMutex
	cachedID  string
	cachedCfg storage.SiteConfig
}

func NewHandler(store *storage.Store, site string, dnsSuffix *string, defaults storage.SiteConfig) *Handler {
	return &Handler{store: store, site: site, dnsSuffix: dnsSuffix, defaults: defaults,
		cachedCfg: storage.SiteConfig{}.Merge(defaults)}
}

func (h *Handler) loadConfig(deploymentID string) storage.SiteConfig {
	h.mu.RLock()
	if h.cachedID == deploymentID {
		cfg := h.cachedCfg
		h.mu.RUnlock()
		return cfg
	}
	h.mu.RUnlock()

	cfg, err := h.store.ReadSiteConfig(h.site, deploymentID)
	if err != nil {
		slog.Error("reading site config", "site", h.site, "deployment", deploymentID, "err", err)
	}
	merged := cfg.Merge(h.defaults)

	h.mu.Lock()
	h.cachedID = deploymentID
	h.cachedCfg = merged
	h.mu.Unlock()

	return merged
}

// AnalyticsEnabled reports whether analytics recording is enabled for the
// current deployment's merged config. Safe to call from other goroutines.
func (h *Handler) AnalyticsEnabled() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.cachedCfg.Analytics == nil {
		return true
	}
	return *h.cachedCfg.Analytics
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())
	if !auth.CanView(caps, h.site) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	deploymentID, err := h.store.CurrentDeployment(h.site)
	if err != nil {
		h.servePlaceholder(w)
		return
	}

	cfg := h.loadConfig(deploymentID)

	// Check redirects before file resolution (first match wins).
	if target, status, ok := h.checkRedirects(r.URL.Path, cfg); ok {
		http.Redirect(w, r, target, status)
		return
	}

	indexPage := cfg.IndexPage
	if indexPage == "" {
		indexPage = "index.html"
	}

	root := h.store.SiteRoot(h.site)
	filePath := filepath.Clean(r.PathValue("path"))
	if filePath == "" || filePath == "." {
		filePath = indexPage
	}
	if strings.Contains(filePath, "..") {
		http.NotFound(w, r)
		return
	}

	fullPath := filepath.Join(root, filePath)

	// Resolve symlinks before the containment check so http.ServeFile
	// cannot follow a symlink that escapes the site root.
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	resolved, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		// File not found — SPA fallback or 404
		if cfg.SPA != nil && *cfg.SPA {
			h.serveSPAFallback(w, r, root, resolvedRoot, deploymentID, indexPage, cfg)
			return
		}
		h.serve404(w, root, resolvedRoot, cfg)
		return
	}
	if !strings.HasPrefix(resolved, resolvedRoot+string(os.PathSeparator)) && resolved != resolvedRoot {
		http.NotFound(w, r)
		return
	}

	// Deployments are immutable, so deploymentID:filePath is a stable ETag.
	// http.ServeFile checks If-None-Match and returns 304 when it matches.
	h.applyHeaders(w, filePath, cfg)
	w.Header().Set("ETag", fmt.Sprintf(`"%s:%s"`, deploymentID, filePath))
	http.ServeFile(w, r, fullPath)
}

func (h *Handler) serveSPAFallback(w http.ResponseWriter, r *http.Request, root, resolvedRoot, deploymentID, indexPage string, cfg storage.SiteConfig) {
	indexPath := filepath.Join(root, indexPage)
	resolved, err := filepath.EvalSymlinks(indexPath)
	if err != nil {
		h.serveDefault404(w)
		return
	}
	if !strings.HasPrefix(resolved, resolvedRoot+string(os.PathSeparator)) && resolved != resolvedRoot {
		h.serveDefault404(w)
		return
	}
	h.applyHeaders(w, indexPage, cfg)
	w.Header().Set("ETag", fmt.Sprintf(`"%s:%s"`, deploymentID, indexPage))
	http.ServeFile(w, r, indexPath)
}

func (h *Handler) applyHeaders(w http.ResponseWriter, reqPath string, cfg storage.SiteConfig) {
	for pattern, hdrs := range cfg.Headers {
		if matchHeaderPath(pattern, "/"+reqPath) {
			for name, value := range hdrs {
				w.Header().Set(name, value)
			}
		}
	}
}

// matchHeaderPath matches a request path against a header pattern.
// Patterns: "/*" matches all, "/dir/*" matches paths under /dir/,
// "/*.ext" matches files with that extension anywhere.
func matchHeaderPath(pattern, reqPath string) bool {
	if pattern == "/*" {
		return true
	}
	// "/*.ext" — match file extension anywhere
	if strings.HasPrefix(pattern, "/*.") {
		ext := pattern[2:] // e.g., ".css"
		return strings.HasSuffix(reqPath, ext)
	}
	// "/dir/*" — prefix match
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(reqPath, prefix)
	}
	// Exact match
	return pattern == reqPath
}

// matchRedirect checks if reqPath matches a redirect rule's From pattern.
// Returns the substituted target URL and true if matched, or ("", false).
// Patterns: "/exact", "/blog/:slug" (named param), "/docs/*" (splat).
func matchRedirect(rule storage.RedirectRule, reqPath string) (string, bool) {
	fromSegs := strings.Split(rule.From, "/")
	pathSegs := strings.Split(reqPath, "/")

	params := make(map[string]string)

	for i, seg := range fromSegs {
		if seg == "*" {
			// Splat: capture everything remaining
			params["*"] = strings.Join(pathSegs[i:], "/")
			break
		}
		if i >= len(pathSegs) {
			return "", false
		}
		if strings.HasPrefix(seg, ":") {
			params[seg[1:]] = pathSegs[i]
		} else if seg != pathSegs[i] {
			return "", false
		}
	}

	// If no splat, segment counts must match exactly
	if !strings.Contains(rule.From, "*") && len(fromSegs) != len(pathSegs) {
		return "", false
	}

	// Substitute params into target by rebuilding segment-by-segment
	// to avoid double-substitution when captured values contain ":name".
	toSegs := strings.Split(rule.To, "/")
	for i, seg := range toSegs {
		if seg == "*" {
			toSegs[i] = params["*"]
		} else if strings.HasPrefix(seg, ":") {
			if v, ok := params[seg[1:]]; ok {
				toSegs[i] = v
			}
		}
	}
	return strings.Join(toSegs, "/"), true
}

func (h *Handler) checkRedirects(reqPath string, cfg storage.SiteConfig) (string, int, bool) {
	for _, rule := range cfg.Redirects {
		if target, ok := matchRedirect(rule, reqPath); ok {
			status := rule.Status
			if status == 0 {
				status = 301
			}
			return target, status, true
		}
	}
	return "", 0, false
}

func (h *Handler) serve404(w http.ResponseWriter, root, resolvedRoot string, cfg storage.SiteConfig) {
	notFoundPage := cfg.NotFoundPage
	if notFoundPage == "" {
		notFoundPage = "404.html"
	}
	custom404 := filepath.Join(root, notFoundPage)
	if resolved, err := filepath.EvalSymlinks(custom404); err == nil {
		if strings.HasPrefix(resolved, resolvedRoot+string(os.PathSeparator)) {
			if content, err := os.ReadFile(resolved); err == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusNotFound)
				w.Write(content)
				return
			}
		}
	}
	h.serveDefault404(w)
}

func (h *Handler) serveDefault404(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	w.Write(default404HTML)
}

func (h *Handler) servePlaceholder(w http.ResponseWriter) {
	controlPlane := "the control plane"
	if h.dnsSuffix != nil && *h.dnsSuffix != "" {
		controlPlane = fmt.Sprintf("https://pages.%s", *h.dnsSuffix)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	placeholderTmpl.Execute(w, struct {
		Site         string
		ControlPlane string
	}{h.site, controlPlane})
}
