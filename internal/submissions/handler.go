package submissions

import (
	"database/sql"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"tspages/internal/auth"
	"tspages/internal/id"
	"tspages/internal/notify"
	"tspages/internal/storage"
	"tspages/internal/web"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

var submissionsTmpl = web.NewTmpl("submissions", templateFS, "templates/submissions.gohtml")
var submissionDetailTmpl = web.NewTmpl("submissions", templateFS, "templates/submission.gohtml")

// RegisterRoutes registers all submission-related HTTP routes on the given mux.
func RegisterRoutes(mux *http.ServeMux, withAuth, withMutate, withQuery func(http.Handler) http.Handler,
	store *storage.Store, submissionStore *Store, defaults storage.SiteConfig, vapidPub string) {

	sp := &SubmissionsPageHandler{store: store, submissions: submissionStore, defaults: defaults, vapidPub: vapidPub}
	sl := &SubmissionsHandler{store: store, submissions: submissionStore, defaults: defaults}
	sd := &SubmissionDetailHandler{store: store, submissions: submissionStore, defaults: defaults}
	del := &DeleteSubmissionHandler{store: store, submissions: submissionStore}
	bulk := &BulkDeleteSubmissionsHandler{store: store, submissions: submissionStore}
	exp := &ExportSubmissionsHandler{store: store, submissions: submissionStore, defaults: defaults}
	sa := &SubmissionAnalyticsHandler{store: store, submissions: submissionStore, defaults: defaults}

	mux.Handle("GET /sites/{site}/submissions", withAuth(sp))
	mux.Handle("GET /sites/{site}/submissions.json", withAuth(sl))
	mux.Handle("GET /sites/{site}/submissions/{id}", withAuth(sd))
	mux.Handle("DELETE /sites/{site}/submissions/{id}", withMutate(del))
	mux.Handle("POST /sites/{site}/submissions/{id}/delete", withMutate(del))
	mux.Handle("DELETE /sites/{site}/submissions", withMutate(bulk))
	mux.Handle("POST /sites/{site}/submissions/delete", withMutate(bulk))
	mux.Handle("GET /sites/{site}/submissions/analytics.json", withQuery(sa))
	mux.Handle("GET /sites/{site}/submissions/export", withQuery(exp))
}

// region POST handler (per-site, created by multihost)

// Handler processes POST requests to site paths when forms are enabled.
type Handler struct {
	submissions *Store
	notifier    notify.Notifier
	site        string
	configFunc  func() storage.SiteConfig
}

// NewHandler creates a form submission handler for the given site.
func NewHandler(submissions *Store, notifier notify.Notifier, site string, configFunc func() storage.SiteConfig) *Handler {
	return &Handler{
		submissions: submissions,
		notifier:    notifier,
		site:        site,
		configFunc:  configFunc,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cfg := h.configFunc()

	// Check forms enabled.
	if cfg.Forms == nil || cfg.Forms.Enabled == nil || !*cfg.Forms.Enabled {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit total body size to 64 KB.
	const maxBodySize = 64 << 10
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)

	// Parse based on Content-Type.
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch ct {
	case "application/x-www-form-urlencoded":
		if err := r.ParseForm(); err != nil {
			if errors.As(err, new(*http.MaxBytesError)) {
				http.Error(w, "Payload Too Large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
	case "multipart/form-data":
		// maxMemory controls how much form data Go buffers in RAM vs disk;
		// the total body size is already enforced by MaxBytesReader above.
		if err := r.ParseMultipartForm(maxBodySize); err != nil {
			if errors.As(err, new(*http.MaxBytesError)) {
				http.Error(w, "Payload Too Large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "Unsupported Media Type", http.StatusUnsupportedMediaType)
		return
	}

	// Collect text fields. Multi-value keys (checkboxes, multi-selects) are
	// joined with the Record Separator so they round-trip as arrays in JSON.
	// Keys ending in "[]" are treated as multi-value: the suffix is stripped
	// and even a single value is RS-encoded so it always expands to an array.
	// Any RS characters in the raw input are stripped first to prevent forgery.
	fields := make(map[string]string, len(r.Form))
	for key, values := range r.Form {
		if len(values) == 0 {
			continue
		}
		multi := strings.HasSuffix(key, "[]")
		if multi {
			key = strings.TrimSuffix(key, "[]")
		}
		// If both "foo" and "foo[]" exist in the form, the [] variant wins
		// (it was explicitly declared multi-value). Skip if already set by
		// the bracket form.
		if _, exists := fields[key]; exists {
			if !multi {
				continue
			}
		}
		clean := make([]string, len(values))
		for i, v := range values {
			clean[i] = strings.ReplaceAll(v, FieldSeparator, "")
		}
		if multi || len(clean) > 1 {
			// Trailing RS ensures even a single [] value is detected as
			// multi-valued by ExpandFields (which filters empty parts).
			fields[key] = strings.Join(clean, FieldSeparator) + FieldSeparator
		} else {
			fields[key] = clean[0]
		}
	}

	fieldsJSON, _ := json.Marshal(fields) // map[string]string cannot fail

	id := id.New("sub")

	info := auth.RequestInfoFromContext(r.Context())

	sub := Submission{
		ID:              id,
		Site:            h.site,
		Path:            r.URL.Path,
		Fields:          fieldsJSON,
		Submitter:       info.UserLogin,
		SubmitterName:   info.UserName,
		SubmitterPicURL: info.ProfilePicURL,
		Node:            info.NodeName,
		IP:              info.NodeIP,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}

	if err := h.submissions.RecordSubmission(sub); err != nil {
		slog.Error("recording form submission", "component", "submissions", "site", h.site, "path", r.URL.Path, "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Fire webhook with expanded fields (RS-encoded multi-values → arrays).
	var webhookFields map[string]any
	expanded := ExpandFields(fieldsJSON, multipleFieldKeys(cfg))
	if err := json.Unmarshal(expanded, &webhookFields); err != nil {
		webhookFields = make(map[string]any, len(fields))
		for k, v := range fields {
			webhookFields[k] = v
		}
	}
	h.notifier.Fire("form.submission", h.site, cfg, map[string]any{
		"id":        id,
		"path":      r.URL.Path,
		"fields":    webhookFields,
		"submitter": info.UserLogin,
	})

	// Respond based on Accept header.
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		web.WriteJSON(w, map[string]any{
			"ok": true,
			"id": id,
		})
		return
	}

	http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
}

// region data types

// SubmissionsPageData holds all data for the submissions page template and JSON API.
type SubmissionsPageData struct {
	User          web.UserInfo      `json:"user"`
	SiteName      string            `json:"site_name"`
	Paths         []PathInfo        `json:"form_paths_nav,omitempty"`
	CurrentPath   string            `json:"current_path"`
	FormName      string            `json:"form_name,omitempty"`
	FormDesc      string            `json:"form_desc,omitempty"`
	Submissions   []submissionView  `json:"submissions,omitempty"`
	FieldKeys     []string          `json:"field_keys,omitempty"`
	FieldLabels   map[string]string `json:"field_labels,omitempty"`
	NumericFields map[string]bool   `json:"numeric_fields,omitempty"` // fields where all non-empty values are numeric
	Summary       map[string]string `json:"summary,omitempty"`        // per-field summary (auto-detected or configured)
	HasSummary    bool              `json:"has_summary,omitempty"`
	DateSummary   string            `json:"date_summary,omitempty"`
	TotalCount    int64             `json:"total_count"`
	Admin         bool              `json:"admin,omitempty"`
	Sort          []SortField       `json:"sort,omitempty"` // current sort (up to 2 fields)
	PageSize      int64             `json:"page_size"`

	VAPIDKey string `json:"-"`

	// Analytics fields.
	TimeSeriesByForm []formTimeSeries `json:"time_series_by_form,omitempty"`
	Submitters       int64            `json:"unique_submitters,omitempty"`
	FormPaths        []FormPath       `json:"paths,omitempty"`
}

type formTimeSeries struct {
	Path   string       `json:"path"`
	Name   string       `json:"name"`
	Series []TimeBucket `json:"series"`
}

// PathInfo holds a form path with display name and count for the sidebar nav.
type PathInfo struct {
	Path  string
	Name  string
	Count int64
}

// SubmissionDetailData holds data for the submission detail page template.
type SubmissionDetailData struct {
	User        web.UserInfo
	SiteName    string
	Admin       bool
	Submission  submissionView
	FieldKeys   []string
	FieldLabels map[string]string
	FormName    string
}

type submissionView struct {
	Submission
	ParsedFields map[string]string
}

// DisplayName returns the submitter's display name, falling back to their login.
func (v submissionView) DisplayName() string {
	if v.SubmitterName != "" {
		return v.SubmitterName
	}
	return v.Submitter
}

// SortState holds the sort direction and position for a column, used by templates.
type SortState struct {
	Desc     bool   // true if sorted descending
	Asc      bool   // true if sorted ascending
	Position string // "1", "2" when multi-sort active; empty for single sort
}

// sortPosition returns the 0-based index of col in the sort list, or -1 if absent.
func (d SubmissionsPageData) sortPosition(col string) int {
	for i, s := range d.Sort {
		if s.Column == col {
			return i
		}
	}
	return -1
}

// SortHref returns the href for a column sort link implementing multi-sort click logic.
// Clicking a column not in the sort list prepends it as primary (desc), pushing old primary
// to secondary and dropping any old secondary. Clicking a desc column flips to asc (keeping
// position). Clicking an asc column removes it (others shift up).
func (d SubmissionsPageData) SortHref(col string) string {
	q := "?path=" + url.QueryEscape(d.CurrentPath)

	pos := d.sortPosition(col)
	var next []SortField

	switch {
	case pos < 0:
		// Not in list → prepend as primary desc.
		next = append(next, SortField{Column: col, Desc: true})
		if len(d.Sort) > 0 {
			next = append(next, d.Sort[0])
		}
	case d.Sort[pos].Desc:
		// Currently desc → flip to asc, keep position.
		next = make([]SortField, len(d.Sort))
		copy(next, d.Sort)
		next[pos].Desc = false
	default:
		// Currently asc → remove, others shift up.
		for i, s := range d.Sort {
			if i != pos {
				next = append(next, s)
			}
		}
	}

	if s := formatSortParam(next); s != "" {
		q += "&sort=" + url.QueryEscape(s)
	}
	return q
}

// SortInfo returns the sort state for a column header.
func (d SubmissionsPageData) SortInfo(col string) SortState {
	pos := d.sortPosition(col)
	if pos < 0 {
		return SortState{}
	}
	s := SortState{
		Desc: d.Sort[pos].Desc,
		Asc:  !d.Sort[pos].Desc,
	}
	if len(d.Sort) > 1 {
		s.Position = fmt.Sprintf("%d", pos+1)
	}
	return s
}

// SubmissionAnalyticsData holds analytics data for the submissions analytics JSON endpoint.
type SubmissionAnalyticsData struct {
	Total         int64        `json:"total"`
	Submitters    int64        `json:"unique_submitters"`
	TimeSeries    []TimeBucket `json:"time_series"`
	HourlyPattern []HourCount  `json:"hourly_pattern"`
	Paths         []FormPath   `json:"paths"`
}

// region admin UI handlers

// region GET /sites/{site}/submissions

// SubmissionsPageHandler renders the submissions page (HTML or JSON).
type SubmissionsPageHandler struct {
	store       *storage.Store
	submissions *Store
	defaults    storage.SiteConfig
	vapidPub    string
}

func (h *SubmissionsPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := web.TrimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		web.RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	admin := auth.IsAdmin(caps, siteName)

	if !auth.CanDeploy(caps, siteName) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	// Load form paths.
	formPaths, err := h.submissions.FormPaths(siteName)
	if err != nil {
		slog.Error("listing form paths", "component", "submissions", "site", siteName, "err", err)
		web.RenderError(w, r, http.StatusInternalServerError, "listing form paths")
		return
	}

	// Read merged config for label/name lookups.
	merged, _ := h.store.ResolveConfig(siteName, h.defaults)

	// Build path infos with display names.
	paths := make([]PathInfo, len(formPaths))
	for i, fp := range formPaths {
		paths[i] = PathInfo{
			Path:  fp.Path,
			Name:  formNameFromConfig(merged, fp.Path),
			Count: fp.Count,
		}
	}

	const pageSize int64 = 20
	data := SubmissionsPageData{
		User:     web.NewUserInfo(identity, caps),
		SiteName: siteName,
		Paths:    paths,
		Admin:    admin,
		PageSize: pageSize,
		VAPIDKey: h.vapidPub,
	}

	// Select current path.
	currentPath := r.URL.Query().Get("path")
	if currentPath == "" && len(formPaths) > 0 {
		currentPath = formPaths[0].Path
	}
	data.CurrentPath = currentPath

	// Analytics — per-form time series for the chart, other stats scoped to selected path.
	now := time.Now()
	var from time.Time // zero = all time
	byForm, err := h.submissions.SubmissionsOverTimeByForm(siteName, from, now)
	if err != nil {
		slog.Error("fetching submission time series", "component", "submissions", "site", siteName, "err", err)
		web.RenderError(w, r, http.StatusInternalServerError, "fetching time series")
		return
	}
	for _, p := range paths {
		if series, ok := byForm[p.Path]; ok {
			data.TimeSeriesByForm = append(data.TimeSeriesByForm, formTimeSeries{
				Path:   p.Path,
				Name:   p.Name,
				Series: series,
			})
		}
	}
	data.Submitters, err = h.submissions.UniqueSubmitters(siteName, currentPath, from, now)
	if err != nil {
		slog.Error("fetching unique submitters", "component", "submissions", "site", siteName, "err", err)
		web.RenderError(w, r, http.StatusInternalServerError, "fetching submitters")
		return
	}
	data.FormPaths = formPaths

	if currentPath != "" {
		data.FormName = formNameFromConfig(merged, currentPath)
		data.FormDesc = formDescFromConfig(merged, currentPath)

		// Parse sort param. Comma-separated, minus prefix = descending.
		// Empty means default (date desc, no indicator).
		sortFields := parseSortParam(r.URL.Query().Get("sort"))
		data.Sort = sortFields

		// Load submissions (limit 20). Sort fields referencing nonexistent
		// JSON keys are harmless (json_extract returns NULL), so we pass them
		// through and strip unknown columns from the UI indicators below.
		subs, err := h.submissions.ListSubmissions(siteName, ListSubmissionsOpts{
			Path:  currentPath,
			Sort:  sortFields,
			Limit: int(pageSize),
		})
		if err != nil {
			slog.Error("listing submissions", "component", "submissions", "site", siteName, "path", currentPath, "err", err)
			web.RenderError(w, r, http.StatusInternalServerError, "listing submissions")
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

		// Parse fields and collect all field keys.
		// Use ExpandFields to consistently convert RS-encoded multi-value fields
		// to arrays, matching the JSON API path. fieldValueString then flattens
		// arrays to newline-joined strings for display.
		multiKeys := multipleFieldKeys(merged)
		views := make([]submissionView, len(subs))
		seen := map[string]bool{}
		var fieldKeys []string
		for i, s := range subs {
			expanded := ExpandFields(s.Fields, multiKeys)
			parsed := map[string]string{}
			if len(expanded) > 0 {
				var m map[string]any
				json.Unmarshal(expanded, &m) // best-effort: show partial data rather than fail the page
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

		// Stabilize column order: config-declared fields first (in declaration
		// order), then any remaining keys sorted alphabetically.
		fieldKeys = stableFieldOrder(endpoint, fieldKeys)

		// Validate sort keys — drop entries with unknown column names.
		var validSort []SortField
		for _, s := range data.Sort {
			if s.Column == "submitter" || s.Column == "date" || seen[s.Column] {
				validSort = append(validSort, s)
			}
		}
		data.Sort = validSort

		// Compute field labels.
		fieldLabels := make(map[string]string, len(fieldKeys))
		for _, k := range fieldKeys {
			fieldLabels[k] = fieldLabelFromConfig(endpoint, k)
		}

		// Compute per-field summary and detect numeric fields (for right-alignment).
		summary := make(map[string]string, len(fieldKeys))
		numericFields := make(map[string]bool, len(fieldKeys))
		for _, k := range fieldKeys {
			values := make([]string, len(views))
			for i, v := range views {
				values[i] = v.ParsedFields[k]
			}
			agg := fieldAggregateFromConfig(endpoint, k)
			summary[k], numericFields[k] = computeFieldSummary(values, agg)
		}

		dateSummary := computeDateSummary(views)
		hasSummary := dateSummary != ""
		if !hasSummary {
			for _, v := range summary {
				if v != "" {
					hasSummary = true
					break
				}
			}
		}

		data.Submissions = views
		data.FieldKeys = fieldKeys
		data.FieldLabels = fieldLabels
		data.NumericFields = numericFields
		data.Summary = summary
		data.HasSummary = hasSummary
		data.DateSummary = dateSummary
	}

	if web.WantsJSON(r) {
		web.WriteJSON(w, data)
		return
	}

	web.RenderPage(w, r, submissionsTmpl, "sites", data)
}

// region GET /sites/{site}/submissions.json

// SubmissionsHandler returns submissions as JSON.
type SubmissionsHandler struct {
	store       *storage.Store
	submissions *Store
	defaults    storage.SiteConfig
}

func (h *SubmissionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := web.TrimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		web.RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, siteName) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	q := r.URL.Query()
	opts := ListSubmissionsOpts{
		Path:   q.Get("path"),
		Limit:  50,
		Offset: 0,
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			opts.Limit = min(n, 1000)
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			opts.Offset = n
		}
	}
	if !parseRFC3339(w, r, q.Get("since"), "since", &opts.Since) {
		return
	}
	if !parseRFC3339(w, r, q.Get("until"), "until", &opts.Until) {
		return
	}

	subs, err := h.submissions.ListSubmissions(siteName, opts)
	if err != nil {
		slog.Error("listing submissions", "component", "submissions", "site", siteName, "err", err)
		web.RenderError(w, r, http.StatusInternalServerError, "listing submissions")
		return
	}
	if subs == nil {
		subs = []Submission{}
	}
	merged, _ := h.store.ResolveConfig(siteName, h.defaults)
	multiKeys := multipleFieldKeys(merged)
	for i := range subs {
		subs[i].Fields = ExpandFields(subs[i].Fields, multiKeys)
	}
	web.WriteJSON(w, subs)
}

// region GET /sites/{site}/submissions/{id}

// SubmissionDetailHandler returns a single submission as JSON.
type SubmissionDetailHandler struct {
	store       *storage.Store
	submissions *Store
	defaults    storage.SiteConfig
}

func (h *SubmissionDetailHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := web.TrimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		web.RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, siteName) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	id := web.TrimSuffix(r.PathValue("id"))
	sub, err := h.submissions.GetSubmission(siteName, id)
	if errors.Is(err, sql.ErrNoRows) {
		web.RenderError(w, r, http.StatusNotFound, "submission not found")
		return
	}
	if err != nil {
		slog.Error("fetching submission", "component", "submissions", "site", siteName, "id", id, "err", err)
		web.RenderError(w, r, http.StatusInternalServerError, "fetching submission")
		return
	}
	merged, _ := h.store.ResolveConfig(siteName, h.defaults)
	sub.Fields = ExpandFields(sub.Fields, multipleFieldKeys(merged))

	if web.WantsJSON(r) {
		web.WriteJSON(w, sub)
		return
	}

	// Build view with parsed fields.
	parsed := map[string]string{}
	var fieldKeys []string
	if len(sub.Fields) > 0 {
		var m map[string]any
		json.Unmarshal(sub.Fields, &m) // best-effort: show partial data rather than fail the page
		for k, v := range m {
			parsed[k] = fieldValueString(v)
			fieldKeys = append(fieldKeys, k)
		}
	}

	// Find endpoint config for field labels and ordering.
	var endpoint *storage.FormEndpoint
	if merged.Forms != nil {
		for i := range merged.Forms.Endpoints {
			if merged.Forms.Endpoints[i].Path == sub.Path {
				endpoint = &merged.Forms.Endpoints[i]
				break
			}
		}
	}
	fieldKeys = stableFieldOrder(endpoint, fieldKeys)

	fieldLabels := make(map[string]string, len(fieldKeys))
	for _, k := range fieldKeys {
		fieldLabels[k] = fieldLabelFromConfig(endpoint, k)
	}

	identity := auth.IdentityFromContext(r.Context())
	admin := auth.IsAdmin(caps, siteName)

	data := SubmissionDetailData{
		User:        web.NewUserInfo(identity, caps),
		SiteName:    siteName,
		Admin:       admin,
		Submission:  submissionView{Submission: sub, ParsedFields: parsed},
		FieldKeys:   fieldKeys,
		FieldLabels: fieldLabels,
		FormName:    formNameFromConfig(merged, sub.Path),
	}

	web.RenderPage(w, r, submissionDetailTmpl, "sites", data)
}

// region DELETE /sites/{site}/submissions/{id}

// DeleteSubmissionHandler deletes a single submission.
type DeleteSubmissionHandler struct {
	store       *storage.Store
	submissions *Store
}

func (h *DeleteSubmissionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := web.TrimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		web.RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.IsAdmin(caps, siteName) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	id := web.TrimSuffix(r.PathValue("id"))

	// Parse form before side effects so a bad body doesn't return 400 after the row is gone.
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			web.RenderError(w, r, http.StatusBadRequest, "invalid form data")
			return
		}
	}

	deleted, err := h.submissions.DeleteSubmission(siteName, id)
	if err != nil {
		slog.Error("deleting submission", "component", "submissions", "site", siteName, "id", id, "err", err)
		web.RenderError(w, r, http.StatusInternalServerError, "deleting submission")
		return
	}
	if !deleted {
		web.RenderError(w, r, http.StatusNotFound, "submission not found")
		return
	}
	if r.Method == http.MethodPost {
		path := r.FormValue("path")
		http.Redirect(w, r, "/sites/"+siteName+"/submissions?path="+url.QueryEscape(path), http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// region DELETE /sites/{site}/submissions
// region POST /sites/{site}/submissions/delete (HTML form alias)

// BulkDeleteSubmissionsHandler deletes submissions matching filters.
type BulkDeleteSubmissionsHandler struct {
	store       *storage.Store
	submissions *Store
}

func (h *BulkDeleteSubmissionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := web.TrimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		web.RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.IsAdmin(caps, siteName) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	var opts BulkDeleteOpts
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			web.RenderError(w, r, http.StatusBadRequest, "invalid form data")
			return
		}
		opts.Path = r.FormValue("path")
		if !parseRFC3339(w, r, r.FormValue("before"), "before", &opts.Before) {
			return
		}
	} else {
		q := r.URL.Query()
		opts.Path = q.Get("path")
		if !parseRFC3339(w, r, q.Get("before"), "before", &opts.Before) {
			return
		}
	}

	if opts.Path == "" && opts.Before.IsZero() {
		web.RenderError(w, r, http.StatusBadRequest, "path or before filter is required")
		return
	}

	deleted, err := h.submissions.BulkDeleteSubmissions(siteName, opts)
	if err != nil {
		slog.Error("bulk deleting submissions", "component", "submissions", "site", siteName, "err", err)
		web.RenderError(w, r, http.StatusInternalServerError, "deleting submissions")
		return
	}

	if r.Method == http.MethodPost {
		http.Redirect(w, r, "/sites/"+siteName+"/submissions?path="+url.QueryEscape(opts.Path), http.StatusSeeOther)
		return
	}
	web.WriteJSON(w, map[string]int64{"deleted": deleted})
}

// region GET /sites/{site}/submissions/export

// ExportSubmissionsHandler exports submissions as JSON or CSV.
type ExportSubmissionsHandler struct {
	store       *storage.Store
	submissions *Store
	defaults    storage.SiteConfig
}

func (h *ExportSubmissionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := web.TrimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		web.RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, siteName) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	q := r.URL.Query()
	opts := ListSubmissionsOpts{Path: q.Get("path"), Limit: 10000}
	if !parseRFC3339(w, r, q.Get("since"), "since", &opts.Since) {
		return
	}
	if !parseRFC3339(w, r, q.Get("until"), "until", &opts.Until) {
		return
	}

	subs, err := h.submissions.ListSubmissions(siteName, opts)
	if err != nil {
		slog.Error("listing submissions for export", "component", "submissions", "site", siteName, "err", err)
		web.RenderError(w, r, http.StatusInternalServerError, "listing submissions")
		return
	}
	if subs == nil {
		subs = []Submission{}
	}

	// Expand multi-value fields before format branching so both CSV and JSON
	// get consistent array representation.
	merged, _ := h.store.ResolveConfig(siteName, h.defaults)
	multiKeys := multipleFieldKeys(merged)
	for i := range subs {
		subs[i].Fields = ExpandFields(subs[i].Fields, multiKeys)
	}

	format := q.Get("format")
	if format == "csv" {
		h.exportCSV(w, siteName, subs)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-submissions.json"`, siteName))
	web.WriteJSON(w, subs)
}

func (h *ExportSubmissionsHandler) exportCSV(w http.ResponseWriter, siteName string, subs []Submission) {
	// Collect all unique field keys across submissions.
	fieldKeySet := map[string]struct{}{}
	type parsedFields = map[string]any
	parsed := make([]parsedFields, len(subs))
	for i, s := range subs {
		var m map[string]any
		if len(s.Fields) > 0 {
			json.Unmarshal(s.Fields, &m) // best-effort: export partial data rather than fail
		}
		parsed[i] = m
		for k := range m {
			fieldKeySet[k] = struct{}{}
		}
	}
	fieldKeys := make([]string, 0, len(fieldKeySet))
	for k := range fieldKeySet {
		fieldKeys = append(fieldKeys, k)
	}
	sort.Strings(fieldKeys)

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-submissions.csv"`, siteName))

	cw := csv.NewWriter(w)
	// Header row: fixed columns + dynamic field columns.
	header := append([]string{"id", "path", "created_at", "submitter", "submitter_name", "node", "ip"}, fieldKeys...)
	cw.Write(header)

	for i, s := range subs {
		row := []string{s.ID, s.Path, s.CreatedAt, s.Submitter, s.SubmitterName, s.Node, s.IP}
		for _, k := range fieldKeys {
			val := ""
			if v, ok := parsed[i][k]; ok {
				val = csvFieldValue(v)
			}
			row = append(row, val)
		}
		cw.Write(row)
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		slog.Warn("csv export flush failed", "component", "submissions", "site", siteName, "err", err)
	}
}

// region GET /sites/{site}/submissions/analytics.json

// SubmissionAnalyticsHandler returns submission analytics as JSON.
type SubmissionAnalyticsHandler struct {
	store       *storage.Store
	submissions *Store
	defaults    storage.SiteConfig
}

func (h *SubmissionAnalyticsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := web.TrimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		web.RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, siteName) {
		web.RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	path := r.URL.Query().Get("path")
	_, from, now := web.ParseRange(r)

	paths, err := h.submissions.FormPaths(siteName)
	if err != nil {
		slog.Error("listing form paths", "component", "submissions", "site", siteName, "err", err)
		web.RenderError(w, r, http.StatusInternalServerError, "listing form paths")
		return
	}
	total, err := h.submissions.CountSubmissions(siteName, path, from, now)
	if err != nil {
		slog.Error("counting submissions", "component", "submissions", "site", siteName, "err", err)
		web.RenderError(w, r, http.StatusInternalServerError, "counting submissions")
		return
	}

	ts, err := h.submissions.SubmissionsOverTime(siteName, path, from, now)
	if err != nil {
		slog.Error("fetching submission time series", "component", "submissions", "site", siteName, "err", err)
		web.RenderError(w, r, http.StatusInternalServerError, "fetching time series")
		return
	}
	if ts == nil {
		ts = []TimeBucket{}
	}
	submitters, err := h.submissions.UniqueSubmitters(siteName, path, from, now)
	if err != nil {
		slog.Error("fetching unique submitters", "component", "submissions", "site", siteName, "err", err)
		web.RenderError(w, r, http.StatusInternalServerError, "fetching submitters")
		return
	}
	hourly, err := h.submissions.HourlySubmissionPattern(siteName, path, from, now)
	if err != nil {
		slog.Error("fetching hourly pattern", "component", "submissions", "site", siteName, "err", err)
		web.RenderError(w, r, http.StatusInternalServerError, "fetching hourly pattern")
		return
	}
	if hourly == nil {
		hourly = []HourCount{}
	}
	if paths == nil {
		paths = []FormPath{}
	}

	web.WriteJSON(w, SubmissionAnalyticsData{
		Total:         total,
		Submitters:    submitters,
		TimeSeries:    ts,
		HourlyPattern: hourly,
		Paths:         paths,
	})
}
