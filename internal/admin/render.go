package admin

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"sync/atomic"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

//go:embed all:assets/dist
var assetFS embed.FS

//go:embed openapi.yaml
var openapiSpec []byte

// OpenAPIHandler returns an http.Handler that serves the OpenAPI spec.
func OpenAPIHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/openapi+yaml")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = w.Write(openapiSpec)
	})
}

// SwaggerUIHandler returns an http.Handler that serves the Swagger UI,
// pointed at the local OpenAPI spec, themed to match the admin panel.
// It is served standalone at /openapi and embedded via iframe at /api.
func SwaggerUIHandler() http.Handler {
	const page = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>tspages API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  <style>
    :root {
      --paper: #fffcf0;
      --black: #100f0f;
      --base-50: #f2f0e5;
      --base-100: #e6e4d9;
      --base-200: #cecdc3;
      --base-300: #b7b5ac;
      --base-500: #878580;
      --base-600: #6f6e69;
      --base-800: #403e3c;
      --base-900: #282726;
      --base-950: #1c1b1a;
      --blue-400: #4385be;
      --blue-500: #3171b2;
      --blue-600: #205ea6;
      --green-600: #66800b;
      --orange-600: #bc5215;
      --red-600: #af3029;
    }
    html, body {
      margin: 0;
      background: var(--paper);
      color: var(--black);
      font-family: system-ui, -apple-system, sans-serif;
      -webkit-font-smoothing: antialiased;
    }
    /* top bar */
    .swagger-ui .topbar { display: none; }
    .swagger-ui .wrapper { max-width: 56rem; padding: 2rem; }
    /* info section */
    .swagger-ui .info { margin: 0 0 2rem; }
    .swagger-ui .info .title { color: var(--black); font-family: inherit; }
    .swagger-ui .info p, .swagger-ui .info li { color: var(--base-600); font-family: inherit; }
    .swagger-ui .info a { color: var(--blue-600); }
    /* operations */
    .swagger-ui .opblock-tag { color: var(--black); font-family: inherit; border-bottom: 1px solid var(--base-100); }
    .swagger-ui .opblock { border-radius: 0.375rem; border: 1px solid var(--base-100); box-shadow: none; }
    .swagger-ui .opblock .opblock-summary { border: none; }
    .swagger-ui .opblock .opblock-summary-method { border-radius: 0.25rem; font-family: inherit; font-size: 0.75rem; }
    .swagger-ui .opblock .opblock-summary-path,
    .swagger-ui .opblock .opblock-summary-description { font-family: inherit; }
    .swagger-ui .opblock .opblock-summary-path { color: var(--black); }
    /* method colors */
    .swagger-ui .opblock.opblock-get { background: color-mix(in srgb, var(--blue-600) 5%, transparent); border-color: color-mix(in srgb, var(--blue-600) 20%, transparent); }
    .swagger-ui .opblock.opblock-get .opblock-summary-method { background: var(--blue-600); }
    .swagger-ui .opblock.opblock-get .opblock-summary { border-color: transparent; }
    .swagger-ui .opblock.opblock-post { background: color-mix(in srgb, var(--green-600) 5%, transparent); border-color: color-mix(in srgb, var(--green-600) 20%, transparent); }
    .swagger-ui .opblock.opblock-post .opblock-summary-method { background: var(--green-600); }
    .swagger-ui .opblock.opblock-post .opblock-summary { border-color: transparent; }
    .swagger-ui .opblock.opblock-put { background: color-mix(in srgb, var(--orange-600) 5%, transparent); border-color: color-mix(in srgb, var(--orange-600) 20%, transparent); }
    .swagger-ui .opblock.opblock-put .opblock-summary-method { background: var(--orange-600); }
    .swagger-ui .opblock.opblock-put .opblock-summary { border-color: transparent; }
    .swagger-ui .opblock.opblock-delete { background: color-mix(in srgb, var(--red-600) 5%, transparent); border-color: color-mix(in srgb, var(--red-600) 20%, transparent); }
    .swagger-ui .opblock.opblock-delete .opblock-summary-method { background: var(--red-600); }
    .swagger-ui .opblock.opblock-delete .opblock-summary { border-color: transparent; }
    /* parameters & responses */
    .swagger-ui table thead tr th, .swagger-ui table thead tr td { color: var(--base-600); font-family: inherit; border-bottom: 1px solid var(--base-100); }
    .swagger-ui .parameter__name, .swagger-ui .parameter__type { color: var(--black); font-family: inherit; }
    .swagger-ui .parameter__name.required::after { color: var(--red-600); }
    .swagger-ui .response-col_status { color: var(--black); font-family: inherit; }
    .swagger-ui .response-col_description { color: var(--base-600); font-family: inherit; }
    /* models */
    .swagger-ui section.models { border: 1px solid var(--base-100); border-radius: 0.375rem; }
    .swagger-ui section.models h4 { color: var(--black); font-family: inherit; }
    .swagger-ui .model-box { background: var(--base-50); }
    .swagger-ui .model { color: var(--base-600); font-family: inherit; }
    .swagger-ui .prop-type { color: var(--blue-600); }
    /* inputs */
    .swagger-ui input[type=text], .swagger-ui textarea, .swagger-ui select {
      border: 1px solid var(--base-200);
      border-radius: 0.375rem;
      font-family: inherit;
      background: var(--paper);
      color: var(--black);
    }
    .swagger-ui input[type=text]:focus, .swagger-ui textarea:focus {
      border-color: var(--blue-400);
      outline: none;
    }
    /* buttons */
    .swagger-ui .btn { border-radius: 0.375rem; font-family: inherit; box-shadow: none; }
    .swagger-ui .btn.execute { background: var(--blue-600); border-color: var(--blue-600); }
    .swagger-ui .btn.execute:hover { background: var(--blue-500); border-color: var(--blue-500); }
    /* code blocks */
    .swagger-ui .highlight-code, .swagger-ui .microlight {
      background: var(--base-50) !important;
      border-radius: 0.375rem;
      font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
      font-size: 0.8125rem;
    }
    /* response body */
    .swagger-ui .responses-inner { background: transparent; }
    /* scheme selector */
    .swagger-ui .scheme-container { background: transparent; box-shadow: none; border-bottom: 1px solid var(--base-100); padding: 1rem 0; }
    /* authorize */
    .swagger-ui .btn.authorize { color: var(--green-600); border-color: var(--green-600); }
    .swagger-ui .btn.authorize svg { fill: var(--green-600); }
    /* misc cleanup */
    .swagger-ui .opblock-body pre.microlight { border: 1px solid var(--base-100); }
    .swagger-ui .loading-container .loading::after { color: var(--base-500); font-family: inherit; }
    .swagger-ui select { appearance: auto; }
    /* dark mode */
    @media (prefers-color-scheme: dark) {
      html, body { background: var(--base-950); color: var(--base-200); }
      .swagger-ui .info .title { color: var(--base-200); }
      .swagger-ui .info p, .swagger-ui .info li { color: var(--base-500); }
      .swagger-ui .opblock-tag { color: var(--base-200); border-bottom-color: var(--base-800); }
      .swagger-ui .opblock { border-color: var(--base-800); }
      .swagger-ui .opblock .opblock-summary-path { color: var(--base-200); }
      .swagger-ui table thead tr th, .swagger-ui table thead tr td { color: var(--base-500); border-bottom-color: var(--base-800); }
      .swagger-ui .parameter__name, .swagger-ui .response-col_status { color: var(--base-200); }
      .swagger-ui .parameter__type, .swagger-ui .response-col_description { color: var(--base-500); }
      .swagger-ui section.models { border-color: var(--base-800); }
      .swagger-ui section.models h4 { color: var(--base-200); }
      .swagger-ui .model-box { background: var(--base-900); }
      .swagger-ui .model { color: var(--base-500); }
      .swagger-ui input[type=text], .swagger-ui textarea, .swagger-ui select {
        background: var(--base-900); border-color: var(--base-800); color: var(--base-200);
      }
      .swagger-ui .highlight-code, .swagger-ui .microlight { background: var(--base-900) !important; color: var(--base-200); }
      .swagger-ui .opblock-body pre.microlight { border-color: var(--base-800); }
      .swagger-ui .scheme-container { border-bottom-color: var(--base-800); }
      .swagger-ui .opblock.opblock-get { background: color-mix(in srgb, var(--blue-600) 8%, transparent); border-color: color-mix(in srgb, var(--blue-600) 25%, transparent); }
      .swagger-ui .opblock.opblock-post { background: color-mix(in srgb, var(--green-600) 8%, transparent); border-color: color-mix(in srgb, var(--green-600) 25%, transparent); }
      .swagger-ui .opblock.opblock-put { background: color-mix(in srgb, var(--orange-600) 8%, transparent); border-color: color-mix(in srgb, var(--orange-600) 25%, transparent); }
      .swagger-ui .opblock.opblock-delete { background: color-mix(in srgb, var(--red-600) 8%, transparent); border-color: color-mix(in srgb, var(--red-600) 25%, transparent); }
      .swagger-ui .opblock-description-wrapper p, .swagger-ui .opblock-external-docs-wrapper p { color: var(--base-500); }
      .swagger-ui .response-col_links { color: var(--base-500); }
    }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({url: "/openapi.yaml", dom_id: "#swagger-ui", deepLinking: true});
  </script>
</body>
</html>`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, page)
	})
}

// --- dev mode ---

var (
	devModeFlag atomic.Bool
	devTmplDir  string // set once before server starts, read-only after
)

// EnableDevMode activates development mode: templates are re-parsed from
// disk on every request and asset URLs point to the Vite dev server.
// Must be called before the HTTP server starts.
func EnableDevMode(tmplDir string) {
	devTmplDir = tmplDir
	devModeFlag.Store(true)
}

// DevAssetProxy returns a reverse proxy that forwards requests to the
// Vite dev server at localhost:5173.
func DevAssetProxy() http.Handler {
	target, _ := url.Parse("http://localhost:5173")
	rp := httputil.NewSingleHostReverseProxy(target)
	orig := rp.Director
	rp.Director = func(r *http.Request) {
		orig(r)
		r.Host = target.Host
	}
	return rp
}

// DevWebSocketProxy returns an HTTP handler that tunnels WebSocket
// upgrade requests to the Vite dev server via a raw TCP connection,
// bypassing httputil.ReverseProxy which can fail to hijack on some
// listeners (e.g. tsnet TLS).
func DevWebSocketProxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backConn, err := net.DialTimeout("tcp", "localhost:5173", 5*time.Second)
		if err != nil {
			http.Error(w, "vite not reachable", http.StatusBadGateway)
			return
		}

		// Send the upgrade request to Vite with rewritten Host.
		outreq := r.Clone(r.Context())
		outreq.Host = "localhost:5173"
		outreq.RequestURI = ""
		outreq.URL.Scheme = ""
		outreq.URL.Host = ""
		if err := outreq.Write(backConn); err != nil {
			backConn.Close()
			http.Error(w, "write to vite failed", http.StatusBadGateway)
			return
		}

		// Hijack the client connection.
		rc := http.NewResponseController(w)
		clientConn, clientBuf, err := rc.Hijack()
		if err != nil {
			backConn.Close()
			return
		}

		// Flush any buffered client data to Vite.
		if n := clientBuf.Reader.Buffered(); n > 0 {
			buf := make([]byte, n)
			clientBuf.Read(buf)
			backConn.Write(buf)
		}

		// Bidirectional tunnel: Vite's 101 response and subsequent
		// WebSocket frames flow through untouched.
		go func() {
			io.Copy(clientConn, backConn)
			clientConn.Close()
		}()
		io.Copy(backConn, clientConn)
		backConn.Close()
	})
}

// --- manifest ---

// manifest maps Vite source paths to hashed output filenames.
type manifest struct {
	entries map[string]string // full src path → output file path
}

// resolve takes a short key like "main.css" or "pages/sites.ts",
// prepends the Vite source prefix, and returns the served path.
func (m *manifest) resolve(key string) string {
	entry, ok := m.entries["web/admin/src/"+key]
	if !ok {
		return ""
	}
	return "/assets/dist/" + entry
}

var viteManifest = loadManifest()

func loadManifest() *manifest {
	data, err := assetFS.ReadFile("assets/dist/.vite/manifest.json")
	if err != nil {
		// During tests or when dist hasn't been built, return empty manifest.
		return &manifest{entries: map[string]string{}}
	}
	var raw map[string]struct {
		File string `json:"file"`
		Src  string `json:"src"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return &manifest{entries: map[string]string{}}
	}
	entries := make(map[string]string, len(raw))
	for _, v := range raw {
		entries[v.Src] = v.File
	}
	return &manifest{entries: entries}
}

// AssetHandler returns an http.Handler that serves embedded static assets.
func AssetHandler() http.Handler {
	sub, _ := fs.Sub(assetFS, "assets/dist")
	return http.StripPrefix("/assets/dist/", http.FileServerFS(sub))
}

// --- templates ---

// tmpl wraps a pre-parsed template with its source file names,
// enabling live reloading from disk in dev mode.
type tmpl struct {
	cached *template.Template
	files  []string // paths relative to templateFS root
}

func newTmpl(files ...string) *tmpl {
	return &tmpl{
		cached: template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, files...)),
		files:  files,
	}
}

var funcs = template.FuncMap{
	"nav": func() string { return "" }, // placeholder; overridden per-render
	"asset": func(key string) string {
		if devModeFlag.Load() {
			return "/web/admin/src/" + key
		}
		return viteManifest.resolve(key)
	},
	"viteclient": func() template.HTML {
		if devModeFlag.Load() {
			return `<script type="module" src="/@vite/client"></script>`
		}
		return ""
	},
	"add": func(a, b int) int { return a + b },
	"sub": func(a, b int) int { return a - b },
	"reltime": func(v any) string {
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
	},
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
		return formatBytes(n)
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
		if picURL != "" {
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
	"deref": func(b *bool) bool {
		return b != nil && *b
	},
	"helpicon": func(slug, title string) template.HTML {
		return template.HTML(fmt.Sprintf(
			`<a href="/help/%s" class="inline-block align-middle ml-1 text-base-300 dark:text-base-700 hover:text-blue-500 transition" title="%s" aria-label="%s">`+
				`<svg aria-hidden="true" xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">`+
				`<circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><path d="M12 17h.01"/>`+
				`</svg></a>`,
			template.HTMLEscapeString(slug), template.HTMLEscapeString(title), template.HTMLEscapeString(title),
		))
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
}

var (
	sitesTmpl       = newTmpl("templates/layout.gohtml", "templates/sites.gohtml")
	siteTmpl        = newTmpl("templates/layout.gohtml", "templates/site.gohtml")
	deploymentTmpl  = newTmpl("templates/layout.gohtml", "templates/deployment.gohtml")
	deploymentsTmpl = newTmpl("templates/layout.gohtml", "templates/deployments.gohtml")
	analyticsTmpl   = newTmpl("templates/layout.gohtml", "templates/analytics.gohtml")
	helpTmpl        = newTmpl("templates/layout.gohtml", "templates/help.gohtml")
	apiTmpl         = newTmpl("templates/layout.gohtml", "templates/api.gohtml")
	webhooksTmpl        = newTmpl("templates/layout.gohtml", "templates/webhooks.gohtml")
	siteDeploymentsTmpl = newTmpl("templates/layout.gohtml", "templates/site-deployments.gohtml")
	errorTmpl           = newTmpl("templates/layout.gohtml", "templates/error.gohtml")
)

// wantsJSON returns true if the request prefers JSON output,
// based on a .json suffix on the last path value or the Accept header.
func wantsJSON(r *http.Request) bool {
	if strings.HasSuffix(r.URL.Path, ".json") {
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// trimSuffix strips a .json or .html extension from a path parameter.
func trimSuffix(s string) string {
	s = strings.TrimSuffix(s, ".json")
	s = strings.TrimSuffix(s, ".html")
	return s
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("warning: encoding JSON response: %v", err)
	}
}

// setAlternateLinks sets RFC 8288 Link headers for alternate representations.
// Each pair is [href, media-type].
func setAlternateLinks(w http.ResponseWriter, alternates [][2]string) {
	var parts []string
	for _, a := range alternates {
		parts = append(parts, fmt.Sprintf(`<%s>; rel="alternate"; type="%s"`, a[0], a[1]))
	}
	w.Header().Set("Link", strings.Join(parts, ", "))
}

func renderPage(w http.ResponseWriter, r *http.Request, t *tmpl, nav string, data any) {
	tpl := t.cached
	if devModeFlag.Load() {
		paths := make([]string, len(t.files))
		for i, f := range t.files {
			paths[i] = filepath.Join(devTmplDir, filepath.Base(f))
		}
		parsed, err := template.New("").Funcs(funcs).ParseFiles(paths...)
		if err != nil {
			log.Printf("template parse error (%s): %v", nav, err)
			RenderError(w, r, http.StatusInternalServerError, "template error")
			return
		}
		tpl = parsed
	}
	tpl, err := tpl.Clone()
	if err != nil {
		log.Printf("template clone error (%s): %v", nav, err)
		RenderError(w, r, http.StatusInternalServerError, "rendering page")
		return
	}
	tpl.Funcs(template.FuncMap{"nav": func() string { return nav }})
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		log.Printf("template error (%s): %v", nav, err)
		RenderError(w, r, http.StatusInternalServerError, "rendering page")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

// RenderError sends an error response. For JSON requests it returns
// {"error": msg}; for HTML requests it renders a styled error page
// within the admin layout. The status code is set on the response.
func RenderError(w http.ResponseWriter, r *http.Request, code int, msg string) {
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
			log.Printf("warning: encoding JSON error response: %v", err)
		}
		return
	}

	identity := auth.IdentityFromContext(r.Context())
	data := struct {
		User       UserInfo
		Code       int
		StatusText string
		Message    string
	}{userInfo(identity), code, http.StatusText(code), msg}

	tpl := errorTmpl.cached
	if devModeFlag.Load() {
		paths := make([]string, len(errorTmpl.files))
		for i, f := range errorTmpl.files {
			paths[i] = filepath.Join(devTmplDir, filepath.Base(f))
		}
		if parsed, err := template.New("").Funcs(funcs).ParseFiles(paths...); err == nil {
			tpl = parsed
		}
	}
	tpl, err := tpl.Clone()
	if err != nil {
		http.Error(w, msg, code)
		return
	}
	tpl.Funcs(template.FuncMap{"nav": func() string { return "" }})
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		http.Error(w, msg, code)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = buf.WriteTo(w)
}

func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}
