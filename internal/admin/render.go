package admin

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

//go:embed all:assets/dist
var assetFS embed.FS

// --- dev mode ---

var (
	devMode    bool
	devTmplDir string
)

// EnableDevMode activates development mode: templates are re-parsed from
// disk on every request and asset URLs point to the Vite dev server.
func EnableDevMode(tmplDir string) {
	devMode = true
	devTmplDir = tmplDir
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
		if devMode {
			return "/web/admin/src/" + key
		}
		return viteManifest.resolve(key)
	},
	"viteclient": func() template.HTML {
		if devMode {
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
		if n < 1024 {
			return fmt.Sprintf("%d B", n)
		}
		if n < 1024*1024 {
			return fmt.Sprintf("%.1f KB", float64(n)/1024)
		}
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
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
	json.NewEncoder(w).Encode(v)
}

func renderPage(w http.ResponseWriter, t *tmpl, nav string, data any) {
	tpl := t.cached
	if devMode {
		paths := make([]string, len(t.files))
		for i, f := range t.files {
			paths[i] = filepath.Join(devTmplDir, filepath.Base(f))
		}
		parsed, err := template.New("").Funcs(funcs).ParseFiles(paths...)
		if err != nil {
			http.Error(w, fmt.Sprintf("template error: %v", err), http.StatusInternalServerError)
			return
		}
		tpl = parsed
	}
	tpl, err := tpl.Clone()
	if err != nil {
		http.Error(w, "rendering page", http.StatusInternalServerError)
		return
	}
	tpl.Funcs(template.FuncMap{"nav": func() string { return nav }})
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		http.Error(w, "rendering page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}
