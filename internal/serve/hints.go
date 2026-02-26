package serve

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxHintScanBytes = 16 << 10 // scan first 16KB for <head> resources
	maxHints         = 10
)

var (
	// Match <link> tags with rel="stylesheet" (either attribute order).
	linkRelFirstRe  = regexp.MustCompile(`(?i)<link[^>]*\brel=["']stylesheet["'][^>]*\bhref=["']([^"']+)["']`)
	linkHrefFirstRe = regexp.MustCompile(`(?i)<link[^>]*\bhref=["']([^"']+)["'][^>]*\brel=["']stylesheet["']`)
	// Match <script> tags with a src attribute.
	scriptSrcRe = regexp.MustCompile(`(?i)<script[^>]*\bsrc=["']([^"']+)["']`)
)

// extractHints scans the <head> of an HTML file for stylesheets and scripts
// that can be sent as 103 Early Hints.
func extractHints(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	buf := make([]byte, maxHintScanBytes)
	n, _ := f.Read(buf)
	head := buf[:n]

	// Only scan up to </head> to avoid picking up body resources.
	if idx := bytes.Index(bytes.ToLower(head), []byte("</head>")); idx >= 0 {
		head = head[:idx]
	}

	seen := make(map[string]bool)
	var hints []string
	add := func(url, as string) {
		if len(hints) >= maxHints || seen[url] || !isSameOrigin(url) {
			return
		}
		seen[url] = true
		hints = append(hints, "<"+url+">; rel=preload; as="+as)
	}

	for _, m := range linkRelFirstRe.FindAllSubmatch(head, -1) {
		add(string(m[1]), "style")
	}
	for _, m := range linkHrefFirstRe.FindAllSubmatch(head, -1) {
		add(string(m[1]), "style")
	}
	for _, m := range scriptSrcRe.FindAllSubmatch(head, -1) {
		add(string(m[1]), "script")
	}

	return hints
}

// isSameOrigin reports whether a URL refers to the same origin
// (absolute path, or relative — not external or data: URIs).
func isSameOrigin(url string) bool {
	if url == "" || strings.HasPrefix(url, "//") || strings.Contains(url, "://") || strings.HasPrefix(url, "data:") {
		return false
	}
	return true
}

// sendEarlyHints sends a 103 Early Hints response with Link preload headers
// for stylesheets and scripts found in an HTML file's <head>.
func (h *Handler) sendEarlyHints(w http.ResponseWriter, deploymentID, filePath, fullPath string) {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != ".html" && ext != ".htm" {
		return
	}

	hints := h.loadHints(deploymentID, filePath, fullPath)
	if len(hints) == 0 {
		return
	}

	for _, hint := range hints {
		w.Header().Add("Link", hint)
	}
	w.WriteHeader(http.StatusEarlyHints)
}

func (h *Handler) loadHints(deploymentID, filePath, fullPath string) []string {
	h.mu.RLock()
	if h.cachedID == deploymentID && h.hintCache != nil {
		if hints, ok := h.hintCache[filePath]; ok {
			h.mu.RUnlock()
			return hints
		}
	}
	h.mu.RUnlock()

	hints := extractHints(fullPath)

	h.mu.Lock()
	if h.cachedID == deploymentID {
		if h.hintCache == nil {
			h.hintCache = make(map[string][]string)
		}
		h.hintCache[filePath] = hints
	}
	h.mu.Unlock()

	return hints
}
