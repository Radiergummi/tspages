package admin

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

//go:embed all:docs
var docsFS embed.FS

// DocPage describes a single documentation page for the sidebar.
type DocPage struct {
	Slug  string
	Title string
}

// docPageOrder defines the sidebar order and metadata.
var docPageOrder = []DocPage{
	{"getting-started", "Getting Started"},
	{"cli-deploy", "CLI Deploy"},
	{"upload-formats", "Upload Formats"},
	{"per-site-config", "Per-Site Configuration"},
	{"authorization", "Authorization"},
	{"api", "API Reference"},
	{"analytics", "Analytics"},
	{"telemetry", "Telemetry"},
	{"github-actions", "GitHub Actions"},
	{"configuration", "Configuration"},
}

// DocPages returns the ordered list of documentation pages.
func DocPages() []DocPage {
	return docPageOrder
}

var docMD = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.DefinitionList,
		extension.Typographer,
	),
)

var (
	docCache   = make(map[string]template.HTML)
	docCacheMu sync.RWMutex
)

// RenderDoc returns the HTML for a documentation page, caching the result.
func RenderDoc(slug string) (template.HTML, error) {
	docCacheMu.RLock()
	if html, ok := docCache[slug]; ok {
		docCacheMu.RUnlock()
		return html, nil
	}
	docCacheMu.RUnlock()

	data, err := docsFS.ReadFile("docs/" + slug + ".md")
	if err != nil {
		return "", fmt.Errorf("doc %q not found", slug)
	}

	// Strip the leading # Title line — it's shown in the page header.
	if i := bytes.IndexByte(data, '\n'); i > 0 && bytes.HasPrefix(data, []byte("# ")) {
		data = data[i+1:]
	}

	var buf bytes.Buffer
	if err := docMD.Convert(data, &buf); err != nil {
		return "", fmt.Errorf("rendering %q: %w", slug, err)
	}

	// Rewrite cross-doc links: (slug) or (slug.md) → (/help/slug)
	html := buf.String()
	for _, p := range docPageOrder {
		html = strings.ReplaceAll(html, `href="`+p.Slug+`.md"`, `href="/help/`+p.Slug+`"`)
		html = strings.ReplaceAll(html, `href="`+p.Slug+`"`, `href="/help/`+p.Slug+`"`)
	}

	result := template.HTML(html)

	docCacheMu.Lock()
	docCache[slug] = result
	docCacheMu.Unlock()

	return result, nil
}
