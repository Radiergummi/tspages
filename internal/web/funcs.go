package web

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"tspages/internal/storage"
)

// resolveAsset maps a short asset key (e.g. "main.css") to its served URL.
// Overridden by the admin package via SetAssetResolver to integrate Vite manifest
// resolution and dev mode asset paths.
var resolveAsset = func(key string) string { return key }

// viteClientFunc returns the Vite client script tag in dev mode.
// Overridden by the admin package via SetViteClientFunc.
var viteClientFunc = func() template.HTML { return "" }

// SetAssetResolver overrides the function used by the "asset" template func.
func SetAssetResolver(fn func(string) string) { resolveAsset = fn }

// SetViteClientFunc overrides the function used by the "viteclient" template func.
func SetViteClientFunc(fn func() template.HTML) { viteClientFunc = fn }

var funcMap = template.FuncMap{
	"nav":        func() string { return "" }, // placeholder; overridden per-render
	"hideFooter": func() bool { return hideFooterFlag },
	"asset": func(key string) string {
		return resolveAsset(key)
	},
	"viteclient": func() template.HTML {
		return viteClientFunc()
	},
	"add": func(a, b int) int { return a + b },
	"sub": func(a, b int) int { return a - b },
	"reltime": reltime,
	"abstime": func(v any) string {
		var t time.Time
		switch x := v.(type) {
		case time.Time:
			t = x
		case string:
			if x == "" {
				return ""
			}
			parsed, err := time.Parse(time.RFC3339, x)
			if err != nil {
				return x
			}
			t = parsed
		default:
			return ""
		}
		if t.IsZero() {
			return ""
		}
		return t.Format("2006-01-02 15:04 MST")
	},
	"bytes": func(n int64) string {
		if n == 0 {
			return "\u2014"
		}
		return FormatBytes(n)
	},
	"pct": func(count, max int64) int {
		if max == 0 {
			return 0
		}
		return int(count * 100 / max)
	},
	"fmtnum": func(n int64) string {
		if n >= 1_000_000 {
			v := float64(n) / 1_000_000
			if v == float64(int64(v)) {
				return fmt.Sprintf("%dM", int64(v))
			}
			return fmt.Sprintf("%.1fM", v)
		}
		if n >= 1_000 {
			v := float64(n) / 1_000
			if v == float64(int64(v)) {
				return fmt.Sprintf("%dk", int64(v))
			}
			return fmt.Sprintf("%.1fk", v)
		}
		return fmt.Sprintf("%d", n)
	},
	"avatarHTML": func(name, picURL string) template.HTML {
		initial := "?"
		for _, r := range name {
			initial = string(r)
			break
		}
		if picURL != "" && (strings.HasPrefix(picURL, "https://") || strings.HasPrefix(picURL, "/")) {
			return template.HTML(fmt.Sprintf(
				`<img class="w-6 h-6 rounded-full shrink-0 object-cover" src="%s" alt="">`,
				template.HTMLEscapeString(picURL),
			))
		}
		return template.HTML(fmt.Sprintf(
			`<span class="w-6 h-6 rounded-full shrink-0 flex items-center justify-center bg-blue-500/10 text-blue-500 text-xs font-semibold uppercase">%s</span>`,
			template.HTMLEscapeString(initial),
		))
	},
	"initial": func(name, login string) string {
		s := name
		if s == "" {
			s = login
		}
		if s == "" {
			return "?"
		}
		for _, r := range s {
			return string(r)
		}
		return "?"
	},
	"fmtms": func(v float64) string {
		if v >= 1000 {
			return fmt.Sprintf("%.2fs", v/1000)
		}
		return fmt.Sprintf("%dms", int(v))
	},
	"deref": func(b *bool) bool {
		return b != nil && *b
	},
	"helpicon": func(slug, title string) template.HTML {
		return template.HTML(fmt.Sprintf(
			`<a href="/help/%s" class="inline-block align-middle text-base-300 dark:text-base-700 hover:text-blue-500 transition" title="%s" aria-label="%s">`+
				`<svg aria-hidden="true" xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">`+
				`<circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><path d="M12 17h.01"/>`+
				`</svg></a>`,
			template.HTMLEscapeString(slug), template.HTMLEscapeString(title), template.HTMLEscapeString(title),
		))
	},
	"containsStr": func(slice []string, s string) bool {
		for _, v := range slice {
			if v == s {
				return true
			}
		}
		return false
	},
	"first": func(n int, items []storage.DeploymentInfo) []storage.DeploymentInfo {
		if n >= len(items) {
			return items
		}
		return items[:n]
	},
	"siteurl": func(name, dnsSuffix string) string {
		if dnsSuffix == "" {
			return ""
		}
		return "https://" + name + "." + dnsSuffix
	},
	"badge": func(status string, titleArgs ...string) template.HTML {
		title := ""
		if len(titleArgs) > 0 {
			title = titleArgs[0]
		}
		colors := map[string]string{
			"active":  "bg-blue-500/10 text-blue-500",
			"failed":  "bg-red-500/10 text-red-600 dark:text-red-400",
			"ok":      "bg-green-500/10 text-green-600 dark:text-green-400",
			"pending": "bg-base-500/10 text-muted",
		}
		c := colors[status]
		if c == "" {
			c = colors["pending"]
		}
		titleAttr := ""
		if title != "" {
			titleAttr = fmt.Sprintf(` title="%s"`, template.HTMLEscapeString(title))
		}
		return template.HTML(fmt.Sprintf(
			`<span class="inline-block text-xs font-semibold uppercase tracking-wide px-2 py-0.5 rounded-full %s"%s>%s</span>`,
			c, titleAttr, template.HTMLEscapeString(status),
		))
	},
	"emptystate": func(msg string) template.HTML {
		return template.HTML(fmt.Sprintf(
			`<p class="text-center py-12 px-8 text-muted text-sm border border-default rounded-md bg-surface">%s</p>`,
			template.HTMLEscapeString(msg),
		))
	},
	"timeago": func(v any) template.HTML {
		var t time.Time
		switch x := v.(type) {
		case time.Time:
			t = x
		case string:
			if x == "" {
				return "\u2014"
			}
			parsed, err := time.Parse(time.RFC3339, x)
			if err != nil {
				return template.HTML(template.HTMLEscapeString(x))
			}
			t = parsed
		default:
			return "\u2014"
		}
		if t.IsZero() {
			return "\u2014"
		}
		abs := t.Format("2006-01-02 15:04 MST")
		rel := reltime(t)
		return template.HTML(fmt.Sprintf(
			`<time datetime="%s" title="%s">%s</time>`,
			abs, abs, template.HTMLEscapeString(rel),
		))
	},
	"breadcrumbs": func(crumbs ...any) (template.HTML, error) {
		// Pairs: label, href, label, href, ..., label (last = current page, no href).
		if len(crumbs)%2 == 0 {
			return "", fmt.Errorf("breadcrumbs: expected odd number of arguments (label/href pairs + trailing label), got %d", len(crumbs))
		}
		var b strings.Builder
		b.WriteString(`<nav aria-label="Breadcrumb" class="text-sm flex items-center gap-1.5">`)
		n := len(crumbs)
		for i := 0; i < n; i++ {
			label := fmt.Sprint(crumbs[i])
			if i > 0 {
				b.WriteString(`<span class="text-muted opacity-50" aria-hidden="true">/</span>`)
			}
			if i+1 < n {
				// Has a following href — this is a link crumb.
				i++
				href := fmt.Sprint(crumbs[i])
				b.WriteString(`<a href="`)
				b.WriteString(template.HTMLEscapeString(href))
				b.WriteString(`" class="text-muted no-underline hover:text-black dark:hover:text-base-200">`)
				b.WriteString(template.HTMLEscapeString(label))
				b.WriteString(`</a>`)
			} else {
				// Last item — current page.
				b.WriteString(`<span aria-current="page">`)
				b.WriteString(template.HTMLEscapeString(label))
				b.WriteString(`</span>`)
			}
		}
		b.WriteString(`</nav>`)
		return template.HTML(b.String()), nil
	},
}

func reltime(v any) string {
	var t time.Time
	switch x := v.(type) {
	case time.Time:
		t = x
	case string:
		if x == "" {
			return "\u2014"
		}
		parsed, err := time.Parse(time.RFC3339, x)
		if err != nil {
			return x
		}
		t = parsed
	default:
		return "\u2014"
	}
	if t.IsZero() {
		return "\u2014"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/(24*365)))
	}
}

// Funcs returns the shared template function map.
func Funcs() template.FuncMap { return funcMap }
