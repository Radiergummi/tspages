package admin

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"
	"sync"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"go.abhg.dev/goldmark/anchor"
	"go.abhg.dev/goldmark/frontmatter"
	"go.abhg.dev/goldmark/toc"
)

//go:embed all:docs
var docsFS embed.FS

// DocPage describes a single documentation page for the sidebar.
type DocPage struct {
	Slug  string
	Title string
}

// TOCEntry is a heading extracted from rendered documentation for the
// "On this page" sidebar.
type TOCEntry struct {
	ID    string
	Text  string
	Level int // 2 or 3
}

// DocMeta holds optional frontmatter fields from documentation pages.
type DocMeta struct {
	Description string `yaml:"description"`
	Category    string `yaml:"category"` // "guide" or "reference"
	Updated     string `yaml:"updated"`
}

// DocResult is the rendered output of a documentation page.
type DocResult struct {
	Content template.HTML
	TOC     []TOCEntry
	Meta    DocMeta
}

// docPageOrder defines the sidebar order and metadata.
var docPageOrder = []DocPage{
	{"getting-started", "Getting Started"},
	{"concepts", "Concepts"},
	{"cli-deploy", "CLI Deploy"},
	{"upload-formats", "Upload Formats"},
	{"per-site-config", "Per-Site Configuration"},
	{"authorization", "Authorization"},
	{"api", "API Reference"},
	{"analytics", "Analytics"},
	{"forms", "Forms"},
	{"webhooks", "Webhooks"},
	{"telemetry", "Telemetry"},
	{"github-actions", "GitHub Actions"},
	{"configuration", "Configuration"},
}

// DocPages returns the ordered list of documentation pages.
func DocPages() []DocPage {
	return docPageOrder
}

// NavGroup is a category section in the help sidebar.
type NavGroup struct {
	Label string
	Pages []DocPage
}

// DocNav returns documentation pages grouped by frontmatter category.
// Pages without a category are placed under "Reference".
func DocNav() []NavGroup {
	guides := NavGroup{Label: "Guides"}
	reference := NavGroup{Label: "Reference"}
	for _, p := range docPageOrder {
		result, _ := RenderDoc(p.Slug)
		if result != nil && result.Meta.Category == "guide" {
			guides.Pages = append(guides.Pages, p)
		} else {
			reference.Pages = append(reference.Pages, p)
		}
	}
	var groups []NavGroup
	if len(guides.Pages) > 0 {
		groups = append(groups, guides)
	}
	if len(reference.Pages) > 0 {
		groups = append(groups, reference)
	}
	return groups
}

// tableWrapRenderer wraps <table> elements in a <div class="table-wrap">
// so horizontal overflow can scroll independently of the page.
type tableWrapRenderer struct{}

func (r *tableWrapRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(east.KindTable, r.renderTable)
}

func (r *tableWrapRenderer) renderTable(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<div class=\"table-wrap\">\n<table")
		if n.Attributes() != nil {
			goldmarkhtml.RenderAttributes(w, n, goldmarkhtml.GlobalAttributeFilter)
		}
		_, _ = w.WriteString(">\n")
	} else {
		_, _ = w.WriteString("</table>\n</div>\n")
	}
	return ast.WalkContinue, nil
}

var docMD = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.DefinitionList,
		extension.Typographer,
		highlighting.NewHighlighting(
			highlighting.WithFormatOptions(
				chromahtml.WithClasses(true),
			),
		),
		&anchor.Extender{
			Texter:   anchor.Text("#"),
			Position: anchor.Before,
		},
		&frontmatter.Extender{},
	),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
	),
	goldmark.WithRendererOptions(
		renderer.WithNodeRenderers(
			util.Prioritized(&tableWrapRenderer{}, 100),
		),
	),
)

var (
	docCache   = make(map[string]*DocResult)
	docCacheMu sync.RWMutex
)

// RenderDoc returns the rendered HTML, table of contents, and frontmatter
// for a documentation page, caching the result.
func RenderDoc(slug string) (*DocResult, error) {
	if !devModeFlag.Load() {
		docCacheMu.RLock()
		if res, ok := docCache[slug]; ok {
			docCacheMu.RUnlock()
			return res, nil
		}
		docCacheMu.RUnlock()
	}

	data, err := docsFS.ReadFile("docs/" + slug + ".md")
	if err != nil {
		return nil, fmt.Errorf("doc %q not found", slug)
	}

	// Parse with a context so the frontmatter extension can store metadata.
	ctx := parser.NewContext()
	doc := docMD.Parser().Parse(text.NewReader(data), parser.WithContext(ctx))

	// Extract frontmatter.
	var meta DocMeta
	if d := frontmatter.Get(ctx); d != nil {
		_ = d.Decode(&meta)
	}

	// Remove the first h1 — it's shown in the page header, not the body.
	for child := doc.FirstChild(); child != nil; child = child.NextSibling() {
		if heading, ok := child.(*ast.Heading); ok && heading.Level == 1 {
			doc.RemoveChild(doc, heading)
			break
		}
	}

	// Extract table of contents from the AST (h2 and h3 only).
	tocTree, err := toc.Inspect(doc, data, toc.MinDepth(2), toc.MaxDepth(3), toc.Compact(true))
	if err != nil {
		return nil, fmt.Errorf("toc for %q: %w", slug, err)
	}

	// Render to HTML.
	var buf bytes.Buffer
	if err := docMD.Renderer().Render(&buf, data, doc); err != nil {
		return nil, fmt.Errorf("rendering %q: %w", slug, err)
	}

	// Rewrite cross-doc links: (slug) or (slug.md) → (/help/slug)
	html := buf.String()
	for _, p := range docPageOrder {
		html = strings.ReplaceAll(html, `href="`+p.Slug+`.md"`, `href="/help/`+p.Slug+`"`)
		html = strings.ReplaceAll(html, `href="`+p.Slug+`"`, `href="/help/`+p.Slug+`"`)
	}

	result := &DocResult{
		Content: template.HTML(html),
		TOC:     flattenTOC(tocTree),
		Meta:    meta,
	}

	if !devModeFlag.Load() {
		docCacheMu.Lock()
		docCache[slug] = result
		docCacheMu.Unlock()
	}

	return result, nil
}

// flattenTOC converts the nested TOC tree into a flat list of entries
// with level indicators for indentation.
func flattenTOC(tree *toc.TOC) []TOCEntry {
	if tree == nil {
		return nil
	}
	var entries []TOCEntry
	for _, item := range tree.Items {
		entries = append(entries, TOCEntry{
			ID:    string(item.ID),
			Text:  string(item.Title),
			Level: 2,
		})
		for _, child := range item.Items {
			entries = append(entries, TOCEntry{
				ID:    string(child.ID),
				Text:  string(child.Title),
				Level: 3,
			})
		}
	}
	return entries
}
