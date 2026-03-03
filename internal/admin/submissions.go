package admin

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/storage"
)

// parseRFC3339 parses v as RFC3339 into dst. Returns false and writes a 400 if v is non-empty and invalid.
func parseRFC3339(w http.ResponseWriter, r *http.Request, v, name string, dst *time.Time) bool {
	if v == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		RenderError(w, r, http.StatusBadRequest, fmt.Sprintf("invalid %s: expected RFC3339 format", name))
		return false
	}
	*dst = t
	return true
}

// region GET /sites/{site}/submissions.json

type SubmissionsHandler struct{ handlerDeps }

func (h *SubmissionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	q := r.URL.Query()
	opts := analytics.ListSubmissionsOpts{
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

	subs, err := h.recorder.ListSubmissions(siteName, opts)
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "listing submissions")
		return
	}
	if subs == nil {
		subs = []analytics.Submission{}
	}
	multiKeys := h.multipleFieldKeys(siteName)
	for i := range subs {
		subs[i].Fields = analytics.ExpandFields(subs[i].Fields, multiKeys)
	}
	writeJSON(w, subs)
}

// region GET /sites/{site}/submissions/{id}

type SubmissionDetailHandler struct{ handlerDeps }

func (h *SubmissionDetailHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	id := r.PathValue("id")
	sub, err := h.recorder.GetSubmission(siteName, id)
	if errors.Is(err, sql.ErrNoRows) {
		RenderError(w, r, http.StatusNotFound, "submission not found")
		return
	}
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "fetching submission")
		return
	}
	sub.Fields = analytics.ExpandFields(sub.Fields, h.multipleFieldKeys(siteName))
	writeJSON(w, sub)
}

// region DELETE /sites/{site}/submissions/{id}

type DeleteSubmissionHandler struct{ handlerDeps }

func (h *DeleteSubmissionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.IsAdmin(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	id := r.PathValue("id")

	// Parse form before side effects so a bad body doesn't return 400 after the row is gone.
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			RenderError(w, r, http.StatusBadRequest, "invalid form data")
			return
		}
	}

	deleted, err := h.recorder.DeleteSubmission(siteName, id)
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "deleting submission")
		return
	}
	if !deleted {
		RenderError(w, r, http.StatusNotFound, "submission not found")
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

type BulkDeleteSubmissionsHandler struct{ handlerDeps }

func (h *BulkDeleteSubmissionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.IsAdmin(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	var opts analytics.BulkDeleteOpts
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			RenderError(w, r, http.StatusBadRequest, "invalid form data")
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
		RenderError(w, r, http.StatusBadRequest, "path or before filter is required")
		return
	}

	deleted, err := h.recorder.BulkDeleteSubmissions(siteName, opts)
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "deleting submissions")
		return
	}

	if r.Method == http.MethodPost {
		http.Redirect(w, r, "/sites/"+siteName+"/submissions?path="+url.QueryEscape(opts.Path), http.StatusSeeOther)
		return
	}
	writeJSON(w, map[string]int64{"deleted": deleted})
}

// region GET /sites/{site}/submissions/export

type ExportSubmissionsHandler struct{ handlerDeps }

func (h *ExportSubmissionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	q := r.URL.Query()
	opts := analytics.ListSubmissionsOpts{Path: q.Get("path"), Limit: 10000}
	if !parseRFC3339(w, r, q.Get("since"), "since", &opts.Since) {
		return
	}
	if !parseRFC3339(w, r, q.Get("until"), "until", &opts.Until) {
		return
	}

	subs, err := h.recorder.ListSubmissions(siteName, opts)
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "listing submissions")
		return
	}
	if subs == nil {
		subs = []analytics.Submission{}
	}

	// Expand multi-value fields before format branching so both CSV and JSON
	// get consistent array representation.
	multiKeys := h.multipleFieldKeys(siteName)
	for i := range subs {
		subs[i].Fields = analytics.ExpandFields(subs[i].Fields, multiKeys)
	}

	format := q.Get("format")
	if format == "csv" {
		h.exportCSV(w, siteName, subs)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-submissions.json"`, siteName))
	json.NewEncoder(w).Encode(subs)
}

// region GET /sites/{site}/submissions/analytics.json

type SubmissionAnalyticsHandler struct{ handlerDeps }

type SubmissionAnalyticsData struct {
	Total         int64                  `json:"total"`
	Submitters    int64                  `json:"unique_submitters"`
	TimeSeries    []analytics.TimeBucket `json:"time_series"`
	HourlyPattern []analytics.HourCount  `json:"hourly_pattern"`
	Paths         []analytics.FormPath   `json:"paths"`
}

func (h *SubmissionAnalyticsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := trimSuffix(r.PathValue("site"))
	if !storage.ValidSiteName(siteName) {
		RenderError(w, r, http.StatusBadRequest, "invalid site name")
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, siteName) {
		RenderError(w, r, http.StatusForbidden, "forbidden")
		return
	}

	path := r.URL.Query().Get("path")
	_, from, now := parseRange(r)

	paths, err := h.recorder.FormPaths(siteName)
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "listing form paths")
		return
	}
	total, err := h.recorder.CountSubmissions(siteName, path, from, now)
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "counting submissions")
		return
	}

	ts, err := h.recorder.SubmissionsOverTime(siteName, path, from, now)
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "fetching time series")
		return
	}
	if ts == nil {
		ts = []analytics.TimeBucket{}
	}
	submitters, err := h.recorder.UniqueSubmitters(siteName, path, from, now)
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "fetching submitters")
		return
	}
	hourly, err := h.recorder.HourlySubmissionPattern(siteName, path, from, now)
	if err != nil {
		RenderError(w, r, http.StatusInternalServerError, "fetching hourly pattern")
		return
	}
	if hourly == nil {
		hourly = []analytics.HourCount{}
	}
	if paths == nil {
		paths = []analytics.FormPath{}
	}

	writeJSON(w, SubmissionAnalyticsData{
		Total:         total,
		Submitters:    submitters,
		TimeSeries:    ts,
		HourlyPattern: hourly,
		Paths:         paths,
	})
}

func (h *ExportSubmissionsHandler) exportCSV(w http.ResponseWriter, siteName string, subs []analytics.Submission) {
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
}

// csvFieldValue converts a JSON value (already expanded by ExpandFields) to a
// CSV-friendly string. Arrays are joined with ", ".
func csvFieldValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case []any:
		parts := make([]string, 0, len(val))
		for _, el := range val {
			if s := fmt.Sprintf("%v", el); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprintf("%v", v)
	}
}
