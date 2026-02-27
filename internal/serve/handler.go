package serve

import (
	_ "embed"
	"fmt"
	"html/template"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

//go:embed templates/404.gohtml
var default404HTML []byte

//go:embed templates/placeholder.gohtml
var placeholderTmplStr string

//go:embed templates/dirlist.gohtml
var dirlistTmplStr string

var placeholderTmpl = template.Must(template.New("placeholder").Parse(placeholderTmplStr))
var dirlistTmpl = template.Must(template.New("dirlist").Parse(dirlistTmplStr))

type Handler struct {
	store     *storage.Store
	site      string
	dnsSuffix string
	defaults  storage.SiteConfig

	mu        sync.RWMutex
	cachedID  string
	cachedCfg storage.SiteConfig
	hintCache map[string][]string
}

// isUnderRoot reports whether resolved is equal to resolvedRoot or a child of it.
func isUnderRoot(resolved, resolvedRoot string) bool {
	return resolved == resolvedRoot || strings.HasPrefix(resolved, resolvedRoot+string(os.PathSeparator))
}

func NewHandler(store *storage.Store, site, dnsSuffix string, defaults storage.SiteConfig) *Handler {
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
	h.hintCache = nil
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

	// Trailing slash normalization (before file resolution).
	if target, ok := checkTrailingSlash(r.URL.Path, cfg.TrailingSlash); ok {
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	cleanURLs := cfg.HTMLExtensions == nil || !*cfg.HTMLExtensions

	// Canonical redirect: strip .html/.htm extension when clean URLs are on.
	if cleanURLs {
		if target, ok := cleanURLRedirect(r.URL.Path); ok {
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
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
		// Clean URL fallback: try path + ".html" before SPA/404.
		if cleanURLs {
			htmlPath := fullPath + ".html"
			if resolvedHTML, err := filepath.EvalSymlinks(htmlPath); err == nil {
				if isUnderRoot(resolvedHTML, resolvedRoot) {
					htmlFilePath := filePath + ".html"
					h.sendEarlyHints(w, deploymentID, htmlFilePath, htmlPath)
					w.Header().Set("Cache-Control", defaultCacheControl(htmlFilePath))
					h.applyHeaders(w, htmlFilePath, cfg)
					w.Header().Set("ETag", fmt.Sprintf(`"%s:%s"`, deploymentID, htmlFilePath))
					h.serveFileCompressed(w, r, htmlPath)
					return
				}
			}
		}
		// SPA fallback or 404
		if cfg.SPARouting != nil && *cfg.SPARouting {
			h.serveSPAFallback(w, r, root, resolvedRoot, deploymentID, indexPage, cfg)
			return
		}
		h.serve404(w, root, resolvedRoot, cfg)
		return
	}
	if !isUnderRoot(resolved, resolvedRoot) {
		http.NotFound(w, r)
		return
	}

	// If the path is a directory, try to serve its index file or a listing.
	if info, err := os.Stat(resolved); err == nil && info.IsDir() {
		dirIndexPath := filepath.Join(fullPath, indexPage)
		resolvedIndex, err := filepath.EvalSymlinks(dirIndexPath)
		if err == nil && isUnderRoot(resolvedIndex, resolvedRoot) {
			indexFilePath := filepath.Join(filePath, indexPage)
			h.sendEarlyHints(w, deploymentID, indexFilePath, dirIndexPath)
			w.Header().Set("Cache-Control", defaultCacheControl(indexFilePath))
			h.applyHeaders(w, indexFilePath, cfg)
			w.Header().Set("ETag", fmt.Sprintf(`"%s:%s"`, deploymentID, indexFilePath))
			h.serveFileCompressed(w, r, dirIndexPath)
			return
		}
		// No index file — try directory listing
		if cfg.DirectoryListing != nil && *cfg.DirectoryListing {
			h.serveDirectoryListing(w, resolved, r.URL.Path)
			return
		}
		// No index, no listing — SPA fallback or 404
		if cfg.SPARouting != nil && *cfg.SPARouting {
			h.serveSPAFallback(w, r, root, resolvedRoot, deploymentID, indexPage, cfg)
			return
		}
		h.serve404(w, root, resolvedRoot, cfg)
		return
	}

	// Send early hints for HTML files before setting final response headers.
	h.sendEarlyHints(w, deploymentID, filePath, fullPath)
	// Set default Cache-Control before user headers so [headers] config can override.
	w.Header().Set("Cache-Control", defaultCacheControl(filePath))
	h.applyHeaders(w, filePath, cfg)
	// Deployments are immutable, so deploymentID:filePath is a stable ETag.
	// http.ServeFile checks If-None-Match and returns 304 when it matches.
	w.Header().Set("ETag", fmt.Sprintf(`"%s:%s"`, deploymentID, filePath))
	h.serveFileCompressed(w, r, fullPath)
}

func (h *Handler) serveSPAFallback(w http.ResponseWriter, r *http.Request, root, resolvedRoot, deploymentID, indexPage string, cfg storage.SiteConfig) {
	indexPath := filepath.Join(root, indexPage)
	resolved, err := filepath.EvalSymlinks(indexPath)
	if err != nil {
		h.serveDefault404(w)
		return
	}
	if !isUnderRoot(resolved, resolvedRoot) {
		h.serveDefault404(w)
		return
	}
	h.sendEarlyHints(w, deploymentID, indexPage, indexPath)
	w.Header().Set("Cache-Control", defaultCacheControl(indexPage))
	h.applyHeaders(w, indexPage, cfg)
	w.Header().Set("ETag", fmt.Sprintf(`"%s:%s"`, deploymentID, indexPage))
	h.serveFileCompressed(w, r, indexPath)
}

func (h *Handler) applyHeaders(w http.ResponseWriter, reqPath string, cfg storage.SiteConfig) {
	// Sort patterns so that more specific patterns (longer, no wildcard)
	// are applied after less specific ones, producing deterministic results
	// when multiple patterns match.
	patterns := make([]string, 0, len(cfg.Headers))
	for pattern := range cfg.Headers {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	for _, pattern := range patterns {
		if matchHeaderPath(pattern, "/"+reqPath) {
			for name, value := range cfg.Headers[pattern] {
				w.Header().Set(name, value)
			}
		}
	}
}

// defaultCacheControl returns a Cache-Control header value based on the
// file path. HTML is always revalidated (ETags provide fast 304s). Assets
// with content hashes in their filenames are cached immutably. Everything
// else gets a moderate 1-hour cache.
func defaultCacheControl(filePath string) string {
	ext := strings.ToLower(path.Ext(filePath))
	switch ext {
	case ".html", ".htm":
		return "public, no-cache, stale-while-revalidate=60"
	default:
		if hasContentHash(filePath) {
			return "public, max-age=31536000, immutable"
		}
		return "public, max-age=3600, stale-while-revalidate=120"
	}
}

// hasContentHash reports whether the filename contains a content hash,
// indicating it can be cached immutably. It looks for segments of 8+
// alphanumeric characters (containing both letters and digits) after
// the first segment of the basename. Matches patterns like
// "main.a1b2c3d4.js" or "index-BdH3bPq2.css".
func hasContentHash(name string) bool {
	base := path.Base(name)
	ext := path.Ext(base)
	if ext == "" {
		return false
	}
	stem := base[:len(base)-len(ext)]
	start := 0
	for i := 0; i <= len(stem); i++ {
		if i == len(stem) || stem[i] == '.' || stem[i] == '-' {
			if start > 0 { // skip the first segment (the actual name)
				seg := stem[start:i]
				if len(seg) >= 8 && isMixedAlphanumeric(seg) {
					return true
				}
			}
			start = i + 1
		}
	}
	return false
}

func isMixedAlphanumeric(s string) bool {
	var hasLetter, hasDigit bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			hasLetter = true
		case c >= '0' && c <= '9':
			hasDigit = true
		default:
			return false
		}
	}
	return hasLetter && hasDigit
}

// serveFileCompressed serves a file, preferring a precompressed variant on
// disk (.br, .gz) before falling back to on-the-fly gzip compression.
func (h *Handler) serveFileCompressed(w http.ResponseWriter, r *http.Request, path string) {
	// Set Vary unconditionally for compressible types so caches know the
	// response can differ by encoding, even when served uncompressed.
	if ct := mime.TypeByExtension(filepath.Ext(path)); isCompressible(ct) {
		w.Header().Set("Vary", "Accept-Encoding")
	}
	if acceptsBrotli(r) {
		if servePrecompressed(w, r, path, ".br", "br") {
			return
		}
	}
	if acceptsGzip(r) {
		if servePrecompressed(w, r, path, ".gz", "gzip") {
			return
		}
		cw := &compressWriter{ResponseWriter: w}
		defer cw.Close()
		serveFileContent(cw, r, path)
		return
	}
	serveFileContent(w, r, path)
}

// serveFileContent opens a file and serves it with http.ServeContent.
// Unlike http.ServeFile, it does not perform internal redirects, so
// caller-set headers (ETag, Cache-Control) are never leaked into a
// redirect response.
func serveFileContent(w http.ResponseWriter, r *http.Request, name string) {
	f, err := os.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, filepath.Base(name), stat.ModTime(), f)
}

// servePrecompressed tries to serve a precompressed variant of origPath
// (e.g. style.css.br for style.css). Returns true if the file existed
// and was served.
func servePrecompressed(w http.ResponseWriter, r *http.Request, origPath, ext, encoding string) bool {
	f, err := os.Open(origPath + ext)
	if err != nil {
		return false
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return false
	}

	if ct := mime.TypeByExtension(filepath.Ext(origPath)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Content-Encoding", encoding)
	w.Header().Set("Vary", "Accept-Encoding")

	// ETag is already set by the caller; http.ServeContent handles
	// If-None-Match and range requests.
	http.ServeContent(w, r, "", stat.ModTime(), f)
	return true
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

// matchRedirect checks if pathSegs matches a redirect rule's From pattern.
// Returns the substituted target URL and true if matched, or ("", false).
// Patterns: "/exact", "/blog/:slug" (named param), "/docs/*" (splat).
func matchRedirect(rule storage.RedirectRule, pathSegs []string) (string, bool) {
	fromSegs := strings.Split(rule.From, "/")

	var params map[string]string

	for i, seg := range fromSegs {
		if seg == "*" {
			// Splat: capture everything remaining
			if params == nil {
				params = make(map[string]string)
			}
			params["*"] = strings.Join(pathSegs[i:], "/")
			break
		}
		if i >= len(pathSegs) {
			return "", false
		}
		if strings.HasPrefix(seg, ":") {
			if params == nil {
				params = make(map[string]string)
			}
			params[seg[1:]] = pathSegs[i]
		} else if seg != pathSegs[i] {
			return "", false
		}
	}

	// If no splat, segment counts must match exactly
	if !strings.Contains(rule.From, "*") && len(fromSegs) != len(pathSegs) {
		return "", false
	}

	if params == nil {
		return rule.To, true
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
	pathSegs := strings.Split(reqPath, "/")
	for _, rule := range cfg.Redirects {
		if target, ok := matchRedirect(rule, pathSegs); ok {
			status := rule.Status
			if status == 0 {
				status = 301
			}
			return target, status, true
		}
	}
	return "", 0, false
}

type dirlistEntry struct {
	Name  string
	Href  string
	IsDir bool
	Size  string
}

func (h *Handler) serveDirectoryListing(w http.ResponseWriter, dirPath, reqPath string) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Sort: directories first, then files, alphabetical within each group.
	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}
		return entries[i].Name() < entries[j].Name()
	})

	if !strings.HasSuffix(reqPath, "/") {
		reqPath += "/"
	}

	var items []dirlistEntry
	for _, e := range entries {
		name := e.Name()
		href := reqPath + name
		size := ""
		if !e.IsDir() {
			if info, err := e.Info(); err == nil {
				size = formatBytes(info.Size())
			}
		}
		items = append(items, dirlistEntry{
			Name:  name,
			Href:  href,
			IsDir: e.IsDir(),
			Size:  size,
		})
	}

	parent := ""
	if reqPath != "/" {
		parent = path.Dir(strings.TrimRight(reqPath, "/"))
		if parent != "/" {
			parent += "/"
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = dirlistTmpl.Execute(w, struct {
		Path    string
		Parent  string
		Entries []dirlistEntry
	}{reqPath, parent, items})
}

func formatBytes(b int64) string {
	const (
		kB = 1024
		mB = 1024 * kB
		gB = 1024 * mB
	)
	switch {
	case b >= gB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gB))
	case b >= mB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mB))
	case b >= kB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// checkTrailingSlash returns a redirect target if the request path needs
// trailing-slash normalization. mode is "add", "remove", or "" (disabled).
// Paths with file extensions and the root "/" are never redirected.
func checkTrailingSlash(reqPath, mode string) (string, bool) {
	if mode == "" || reqPath == "/" {
		return "", false
	}
	if mode == "add" {
		if !strings.HasSuffix(reqPath, "/") && path.Ext(reqPath) == "" {
			return reqPath + "/", true
		}
	}
	if mode == "remove" {
		if strings.HasSuffix(reqPath, "/") {
			return strings.TrimSuffix(reqPath, "/"), true
		}
	}
	return "", false
}

// cleanURLRedirect returns a redirect target if the request path has a .html or
// .htm extension that should be stripped for clean URLs. Index files are not
// redirected (they're served at their directory path already).
func cleanURLRedirect(reqPath string) (string, bool) {
	ext := strings.ToLower(path.Ext(reqPath))
	if ext != ".html" && ext != ".htm" {
		return "", false
	}
	base := path.Base(reqPath)
	baseLower := strings.ToLower(base)
	if baseLower == "index.html" || baseLower == "index.htm" {
		return "", false
	}
	return strings.TrimSuffix(reqPath, path.Ext(reqPath)), true
}

func (h *Handler) serve404(w http.ResponseWriter, root, resolvedRoot string, cfg storage.SiteConfig) {
	notFoundPage := cfg.NotFoundPage
	if notFoundPage == "" {
		notFoundPage = "404.html"
	}
	custom404 := filepath.Join(root, notFoundPage)
	if resolved, err := filepath.EvalSymlinks(custom404); err == nil {
		if isUnderRoot(resolved, resolvedRoot) {
			if content, err := os.ReadFile(resolved); err == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "public, no-cache, stale-while-revalidate=60")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write(content)
				return
			}
		}
	}
	h.serveDefault404(w)
}

func (h *Handler) serveDefault404(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(default404HTML)
}

func (h *Handler) servePlaceholder(w http.ResponseWriter) {
	controlPlane := "the control plane"
	if h.dnsSuffix != "" {
		controlPlane = fmt.Sprintf("https://pages.%s", h.dnsSuffix)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = placeholderTmpl.Execute(w, struct {
		Site         string
		ControlPlane string
	}{h.site, controlPlane})
}
