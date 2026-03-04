package help

import (
	"embed"
	"html/template"
	"io"
	"net/http"
	"os"

	"tspages/internal/auth"
	"tspages/internal/web"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

//go:embed openapi.yaml
var openapiSpec []byte

// devOpenAPIPath is the on-disk path to openapi.yaml, set in dev mode.
var devOpenAPIPath string

// SetDevOpenAPIPath enables reading openapi.yaml from disk in dev mode.
func SetDevOpenAPIPath(path string) { devOpenAPIPath = path }

var (
	helpTmpl = web.NewTmpl("help", templateFS, "templates/help.gohtml")
	apiTmpl  = web.NewTmpl("help", templateFS, "templates/api.gohtml")
)

// RegisterRoutes registers help, API docs, and OpenAPI routes on the given mux.
func RegisterRoutes(mux *http.ServeMux, withAuth func(http.Handler) http.Handler) {
	help := &HelpHandler{}
	api := &APIHandler{}

	mux.Handle("GET /help", withAuth(help))
	mux.Handle("GET /help/{page...}", withAuth(help))
	mux.Handle("GET /api", withAuth(api))
	mux.Handle("GET /openapi.yaml", OpenAPIHandler())
	mux.Handle("GET /openapi", SwaggerUIHandler())
}

// HelpHandler serves the documentation pages.
type HelpHandler struct{}

func (h *HelpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())

	pages := DocPages()

	slug := r.PathValue("page")
	if slug == "" {
		slug = pages[0].Slug
	}

	var current *DocPage
	for i := range pages {
		if pages[i].Slug == slug {
			current = &pages[i]
			break
		}
	}
	if current == nil {
		web.RenderError(w, r, http.StatusNotFound, "")
		return
	}

	result, err := RenderDoc(slug)
	if err != nil {
		web.RenderError(w, r, http.StatusNotFound, "")
		return
	}

	web.RenderPage(w, r, helpTmpl, "help", struct {
		User    web.UserInfo
		Nav     []NavGroup
		Current DocPage
		Content template.HTML
		TOC     []TOCEntry
		Meta    DocMeta
	}{web.NewUserInfo(identity, caps), DocNav(), *current, result.Content, result.TOC, result.Meta})
}

// APIHandler serves the API documentation iframe page.
type APIHandler struct{}

func (h *APIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	web.RenderPage(w, r, apiTmpl, "api", struct {
		User web.UserInfo
	}{web.NewUserInfo(identity, caps)})
}

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
