package admin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/forms"
	"tspages/internal/storage"
)

// --- GET /sites/{site}/submissions ---

type SubmissionsPageHandler struct{ handlerDeps }

type SubmissionsPageData struct {
	User        UserInfo
	SiteName    string
	Paths       []PathInfo
	CurrentPath string
	FormName    string
	FormDesc    string
	Submissions []submissionView
	FieldKeys   []string
	FieldLabels map[string]string
	Summary     map[string]string // per-field summary (unique count)
	TotalCount  int64
	Admin       bool
}

type PathInfo struct {
	Path  string
	Name  string
	Count int64
}

type submissionView struct {
	analytics.Submission
	ParsedFields map[string]string
}

func (h *SubmissionsPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	admin := auth.IsAdmin(caps, siteName)

	if !auth.CanDeploy(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	// Load form paths.
	formPaths, err := h.recorder.FormPaths(siteName)
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "listing form paths")
		return
	}

	// Read merged config for label/name lookups.
	deployCfg, _ := h.store.ReadCurrentDeploymentConfig(siteName)
	siteCfg, _ := h.store.ReadSiteConfig(siteName)
	merged := deployCfg.Merge(siteCfg).Merge(h.defaults)

	// Build path infos with display names.
	paths := make([]PathInfo, len(formPaths))
	for i, fp := range formPaths {
		paths[i] = PathInfo{
			Path:  fp.Path,
			Name:  formNameFromConfig(merged, fp.Path),
			Count: fp.Count,
		}
	}

	data := SubmissionsPageData{
		User:     userInfo(identity, caps),
		SiteName: siteName,
		Paths:    paths,
		Admin:    admin,
	}

	// Select current path.
	currentPath := r.URL.Query().Get("path")
	if currentPath == "" && len(formPaths) > 0 {
		currentPath = formPaths[0].Path
	}
	data.CurrentPath = currentPath

	if currentPath != "" {
		data.FormName = formNameFromConfig(merged, currentPath)
		data.FormDesc = formDescFromConfig(merged, currentPath)

		// Load submissions (limit 20).
		subs, err := h.recorder.ListSubmissions(siteName, analytics.ListSubmissionsOpts{
			Path:  currentPath,
			Limit: 20,
		})
		if err != nil {
			RenderError(w, r, http.StatusInternalServerError, "listing submissions")
			return
		}

		// Get total count for this path.
		for _, fp := range formPaths {
			if fp.Path == currentPath {
				data.TotalCount = fp.Count
				break
			}
		}

		// Find the endpoint config for field labels.
		var endpoint *storage.FormEndpoint
		if merged.Forms != nil {
			for i := range merged.Forms.Endpoints {
				if merged.Forms.Endpoints[i].Path == currentPath {
					endpoint = &merged.Forms.Endpoints[i]
					break
				}
			}
		}

		// Parse fields and compute field keys (first-seen order).
		views := make([]submissionView, len(subs))
		seen := map[string]bool{}
		var fieldKeys []string
		for i, s := range subs {
			parsed := map[string]string{}
			if len(s.Fields) > 0 {
				var m map[string]any
				json.Unmarshal(s.Fields, &m)
				for k, v := range m {
					parsed[k] = fieldValueString(v)
					if !seen[k] {
						seen[k] = true
						fieldKeys = append(fieldKeys, k)
					}
				}
			}
			views[i] = submissionView{Submission: s, ParsedFields: parsed}
		}

		// Compute field labels.
		fieldLabels := make(map[string]string, len(fieldKeys))
		for _, k := range fieldKeys {
			fieldLabels[k] = fieldLabelFromConfig(endpoint, k)
		}

		// Compute summary: count unique values per field.
		summary := make(map[string]string, len(fieldKeys))
		for _, k := range fieldKeys {
			unique := map[string]struct{}{}
			for _, v := range views {
				if val := v.ParsedFields[k]; val != "" {
					unique[val] = struct{}{}
				}
			}
			summary[k] = fmt.Sprintf("%d unique", len(unique))
		}

		data.Submissions = views
		data.FieldKeys = fieldKeys
		data.FieldLabels = fieldLabels
		data.Summary = summary
	}

	renderPage(w, r, submissionsTmpl, "sites", data)
}

// formNameFromConfig returns the display name for a form path, checking config first.
func formNameFromConfig(cfg storage.SiteConfig, path string) string {
	if cfg.Forms != nil {
		for _, ep := range cfg.Forms.Endpoints {
			if ep.Path == path && ep.Name != "" {
				return ep.Name
			}
		}
	}
	return forms.HumanizePath(path)
}

// formDescFromConfig returns the description for a form path from config.
func formDescFromConfig(cfg storage.SiteConfig, path string) string {
	if cfg.Forms != nil {
		for _, ep := range cfg.Forms.Endpoints {
			if ep.Path == path {
				return ep.Description
			}
		}
	}
	return ""
}

// fieldLabelFromConfig returns the display label for a field key, checking config first.
func fieldLabelFromConfig(endpoint *storage.FormEndpoint, key string) string {
	if endpoint != nil {
		for _, f := range endpoint.Fields {
			if f.Key == key && f.Label != "" {
				return f.Label
			}
		}
	}
	return forms.HumanizeKey(key)
}

// fieldValueString converts an arbitrary JSON value to a display string.
func fieldValueString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return json.Number(fmt.Sprintf("%d", int64(val))).String()
		}
		return json.Number(fmt.Sprintf("%g", val)).String()
	case bool:
		if val {
			return "true"
		}
		return "false"
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}
