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
	"time"

	"tspages/internal/web"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

//go:embed all:assets/dist
var assetFS embed.FS

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
	siteDeploymentsTmpl = web.NewTmpl("admin", templateFS, "templates/site-deployments.gohtml")
)

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
