// Package web provides shared rendering infrastructure for all feature modules.
// Each module embeds its own templates and uses NewTmpl to compose them with the
// shared layout. Dev mode hot-reloads templates from disk per-module.
package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"

	"tspages/internal/auth"
)

//go:embed templates/*.gohtml
var sharedFS embed.FS

var (
	devModeFlag    atomic.Bool
	devPaths       sync.Map // module name → disk path
	hideFooterFlag bool     // set once before server starts, read-only after
)

// DevMode reports whether development mode is active.
func DevMode() bool { return devModeFlag.Load() }

// EnableDevMode activates development mode: templates are re-parsed from
// disk on every request. paths maps module names to their template directories
// on disk (e.g. {"admin": "/abs/path/to/internal/admin/templates"}).
func EnableDevMode(paths map[string]string) {
	for k, v := range paths {
		devPaths.Store(k, v)
	}
	devModeFlag.Store(true)
}

// SetHideFooter controls whether the admin footer is hidden.
// Must be called before the HTTP server starts.
func SetHideFooter(v bool) { hideFooterFlag = v }

// Tmpl wraps a pre-parsed template with its source metadata,
// enabling live reloading from disk in dev mode.
type Tmpl struct {
	cached   *template.Template
	files    []string // paths relative to moduleFS root
	moduleFS fs.FS
	module   string // dev path lookup key
}

// NewTmpl creates a template by composing the shared layout with module-specific
// content templates. The module name is used for dev mode path lookup.
func NewTmpl(module string, moduleFS fs.FS, files ...string) *Tmpl {
	t := template.Must(template.New("").Funcs(funcMap).ParseFS(sharedFS, "templates/layout.gohtml"))
	t = template.Must(t.ParseFS(moduleFS, files...))
	return &Tmpl{cached: t, files: files, moduleFS: moduleFS, module: module}
}

var errorTmpl = &Tmpl{
	cached: template.Must(template.New("").Funcs(funcMap).ParseFS(
		sharedFS, "templates/layout.gohtml", "templates/error.gohtml",
	)),
	files:    []string{"templates/error.gohtml"},
	moduleFS: sharedFS,
	module:   "web",
}

// devReparse re-parses a template from disk (layout + content), falling back
// to the embedded FS for any module without a registered dev path.
func devReparse(t *Tmpl) (*template.Template, error) {
	parsed := template.New("").Funcs(funcMap)

	// Layout: prefer disk if "web" dev path is registered.
	if webDir, ok := devPaths.Load("web"); ok {
		var err error
		parsed, err = parsed.ParseFiles(filepath.Join(webDir.(string), "layout.gohtml"))
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		parsed, err = parsed.ParseFS(sharedFS, "templates/layout.gohtml")
		if err != nil {
			return nil, err
		}
	}

	// Content: prefer disk if the module's dev path is registered.
	if modDir, ok := devPaths.Load(t.module); ok {
		paths := make([]string, len(t.files))
		for i, f := range t.files {
			paths[i] = filepath.Join(modDir.(string), filepath.Base(f))
		}
		var err error
		parsed, err = parsed.ParseFiles(paths...)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		parsed, err = parsed.ParseFS(t.moduleFS, t.files...)
		if err != nil {
			return nil, err
		}
	}

	return parsed, nil
}

// RenderPage renders a template within the shared layout, setting the active
// nav tab. In dev mode, templates are re-parsed from disk on every request.
func RenderPage(w http.ResponseWriter, r *http.Request, t *Tmpl, nav string, data any) {
	tpl := t.cached
	if devModeFlag.Load() {
		parsed, err := devReparse(t)
		if err != nil {
			slog.Error("template parse failed", "module", t.module, "err", err)
			RenderError(w, r, http.StatusInternalServerError, "template error")
			return
		}
		tpl = parsed
	}
	tpl, err := tpl.Clone()
	if err != nil {
		slog.Error("template clone failed", "module", t.module, "err", err)
		RenderError(w, r, http.StatusInternalServerError, "rendering page")
		return
	}
	tpl.Funcs(template.FuncMap{"nav": func() string { return nav }})
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		slog.Error("template execution failed", "module", t.module, "err", err)
		RenderError(w, r, http.StatusInternalServerError, "rendering page")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

// RenderError sends an error response. For JSON requests it returns
// {"error": msg}; for HTML requests it renders a styled error page
// within the shared layout. The status code is set on the response.
func RenderError(w http.ResponseWriter, r *http.Request, code int, msg string) {
	if WantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
			slog.Warn("encoding JSON error response failed", "err", err)
		}
		return
	}

	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	data := struct {
		User       UserInfo
		Code       int
		StatusText string
		Message    string
	}{NewUserInfo(identity, caps), code, http.StatusText(code), msg}

	tpl := errorTmpl.cached
	if devModeFlag.Load() {
		if parsed, err := devReparse(errorTmpl); err == nil {
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
