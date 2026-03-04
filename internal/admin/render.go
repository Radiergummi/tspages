package admin

import (
	"embed"
	"encoding/json"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"tspages/internal/web"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

//go:embed all:assets/dist
var assetFS embed.FS

//go:embed openapi.yaml
var openapiSpec []byte

// devOpenAPIPath is the on-disk path to openapi.yaml, set in dev mode.
var devOpenAPIPath string

// SetDevOpenAPIPath enables reading openapi.yaml from disk in dev mode.
func SetDevOpenAPIPath(path string) { devOpenAPIPath = path }

// SetupWeb configures the web package's asset resolver and Vite client
// function using the admin package's embedded assets and manifest.
// Must be called before any HTTP request is served.
func SetupWeb() {
	web.SetAssetResolver(func(key string) string {
		if web.DevMode() {
			return "/web/admin/src/" + key
		}
		return viteManifest.resolve(key)
	})
	web.SetViteClientFunc(func() template.HTML {
		if web.DevMode() {
			return `<script type="module" src="/@vite/client"></script>`
		}
		return ""
	})
}

// region templates

var (
	sitesTmpl           = web.NewTmpl("admin", templateFS, "templates/sites.gohtml")
	siteTmpl            = web.NewTmpl("admin", templateFS, "templates/site.gohtml")
	deploymentTmpl      = web.NewTmpl("admin", templateFS, "templates/deployment.gohtml")
	deploymentsTmpl     = web.NewTmpl("admin", templateFS, "templates/deployments.gohtml")
	helpTmpl            = web.NewTmpl("admin", templateFS, "templates/help.gohtml")
	apiTmpl             = web.NewTmpl("admin", templateFS, "templates/api.gohtml")
	siteDeploymentsTmpl    = web.NewTmpl("admin", templateFS, "templates/site-deployments.gohtml")
	pushSubscriptionsTmpl = web.NewTmpl("admin", templateFS, "templates/push-subscriptions.gohtml")
)

// region OpenAPI / Swagger

// OpenAPIHandler returns an http.Handler that serves the OpenAPI spec.
// In dev mode it re-reads the file from disk on each request.
func OpenAPIHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/openapi+yaml")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if web.DevMode() && devOpenAPIPath != "" {
			data, err := os.ReadFile(devOpenAPIPath)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(data)
			return
		}
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

// region dev proxies

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
			clientBuf.Read(buf) //nolint:errcheck // WebSocket proxy; errors handled by close
			backConn.Write(buf) //nolint:errcheck // WebSocket proxy; errors handled by close
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

// region manifest

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

// ServiceWorkerHandler returns an http.Handler that serves the compiled
// service worker at /sw.js with the correct Content-Type and scope header.
// In dev mode it proxies to the Vite dev server.
func ServiceWorkerHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if web.DevMode() {
			// Rewrite to the Vite source path so the dev server can resolve it.
			r.URL.Path = "/web/admin/src/sw.ts"
			DevAssetProxy().ServeHTTP(w, r)
			return
		}
		resolved := viteManifest.resolve("sw.ts")
		if resolved == "" {
			http.NotFound(w, r)
			return
		}
		// resolved is "/assets/dist/<hashed>.js"; strip the leading / to
		// read from the embedded FS.
		data, err := assetFS.ReadFile(resolved[1:]) // drop leading /
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Write(data)
	})
}
