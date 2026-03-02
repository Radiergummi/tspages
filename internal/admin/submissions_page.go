package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/forms"
	"tspages/internal/storage"
)

// --- GET /sites/{site}/submissions ---

type SubmissionsPageHandler struct{ handlerDeps }

type SubmissionsPageData struct {
	User          UserInfo
	SiteName      string
	Paths         []PathInfo
	CurrentPath   string
	FormName      string
	FormDesc      string
	Submissions   []submissionView
	FieldKeys     []string
	FieldLabels   map[string]string
	NumericFields map[string]bool   // fields where all non-empty values are numeric
	Summary       map[string]string // per-field summary (auto-detected or configured)
	HasSummary    bool
	DateSummary   string
	TotalCount    int64
	Admin         bool
	Sort          []analytics.SortField // current sort (up to 2 fields)

	// Analytics fields.
	TimeSeriesByForm []formTimeSeries      `json:"time_series_by_form,omitempty"`
	Submitters       int64                 `json:"unique_submitters"`
	HourlyPattern    []analytics.HourCount `json:"hourly_pattern,omitempty"`
	FormPaths        []analytics.FormPath  `json:"paths,omitempty"`
}

type formTimeSeries struct {
	Path   string                 `json:"path"`
	Name   string                 `json:"name"`
	Series []analytics.TimeBucket `json:"series"`
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
	q := "?path=" + d.CurrentPath

	pos := d.sortPosition(col)
	var next []analytics.SortField

	switch {
	case pos < 0:
		// Not in list → prepend as primary desc.
		next = append(next, analytics.SortField{Column: col, Desc: true})
		if len(d.Sort) > 0 {
			next = append(next, d.Sort[0])
		}
	case d.Sort[pos].Desc:
		// Currently desc → flip to asc, keep position.
		next = make([]analytics.SortField, len(d.Sort))
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
		q += "&sort=" + s
	}
	return q
}

// SortState holds the sort direction and position for a column, used by templates.
type SortState struct {
	Desc     bool   // true if sorted descending
	Asc      bool   // true if sorted ascending
	Position string // "1", "2" when multi-sort active; empty for single sort
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

// parseSortParam parses a comma-separated sort parameter into SortFields (max 2).
// Minus prefix means descending: "-date,email" → [{date desc}, {email asc}].
func parseSortParam(raw string) []analytics.SortField {
	if raw == "" {
		return nil
	}
	parts := strings.SplitN(raw, ",", 2)
	var out []analytics.SortField
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		desc := strings.HasPrefix(p, "-")
		col := strings.TrimPrefix(p, "-")
		if col == "" {
			continue
		}
		out = append(out, analytics.SortField{Column: col, Desc: desc})
	}
	return out
}

// formatSortParam is the inverse of parseSortParam.
func formatSortParam(sorts []analytics.SortField) string {
	var parts []string
	for _, s := range sorts {
		if s.Desc {
			parts = append(parts, "-"+s.Column)
		} else {
			parts = append(parts, s.Column)
		}
	}
	return strings.Join(parts, ",")
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

// DisplayName returns the submitter's display name, falling back to their login.
func (v submissionView) DisplayName() string {
	if v.SubmitterName != "" {
		return v.SubmitterName
	}
	return v.Submitter
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

	// Analytics — per-form time series for the chart, other stats scoped to selected path.
	now := time.Now()
	var from time.Time // zero = all time
	byForm, _ := h.recorder.SubmissionsOverTimeByForm(siteName, from, now)
	for _, p := range paths {
		if series, ok := byForm[p.Path]; ok {
			data.TimeSeriesByForm = append(data.TimeSeriesByForm, formTimeSeries{
				Path:   p.Path,
				Name:   p.Name,
				Series: series,
			})
		}
	}
	data.Submitters, _ = h.recorder.UniqueSubmitters(siteName, currentPath, from, now)
	data.HourlyPattern, _ = h.recorder.HourlySubmissionPattern(siteName, currentPath, from, now)
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
		subs, err := h.recorder.ListSubmissions(siteName, analytics.ListSubmissionsOpts{
			Path:  currentPath,
			Sort:  sortFields,
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

		// Parse fields and collect all field keys.
		views := make([]submissionView, len(subs))
		seen := map[string]bool{}
		var fieldKeys []string
		for i, s := range subs {
			parsed := map[string]string{}
			if len(s.Fields) > 0 {
				var m map[string]any
				json.Unmarshal(s.Fields, &m) // best-effort: show partial data rather than fail the page
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
		var validSort []analytics.SortField
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

	if wantsJSON(r) {
		writeJSON(w, data)
		return
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

// fieldAggregateFromConfig returns the configured aggregate function for a field, if any.
func fieldAggregateFromConfig(endpoint *storage.FormEndpoint, key string) string {
	if endpoint != nil {
		for _, f := range endpoint.Fields {
			if f.Key == key {
				return f.Aggregate
			}
		}
	}
	return ""
}

// computeFieldSummary returns the summary string and whether the column is numeric.
// If aggregate is set (from config), it overrides auto-detection.
func computeFieldSummary(values []string, aggregate string) (summary string, isNumeric bool) {
	nonEmpty := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			nonEmpty = append(nonEmpty, v)
		}
	}
	if len(nonEmpty) == 0 {
		return "", false
	}

	// Check if all non-empty values are numeric (used for column alignment
	// and auto-detected numeric summaries).
	nums := make([]float64, 0, len(nonEmpty))
	isNumeric = true
	for _, v := range nonEmpty {
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			isNumeric = false
			break
		}
		nums = append(nums, n)
	}

	if aggregate != "" {
		return computeExplicitAggregate(len(values), nonEmpty, aggregate), isNumeric
	}

	if isNumeric {
		mn, mx := nums[0], nums[0]
		sum := 0.0
		for _, n := range nums {
			if n < mn {
				mn = n
			}
			if n > mx {
				mx = n
			}
			sum += n
		}
		if mn == mx {
			return formatNum(mn), true
		}
		avg := sum / float64(len(nums))
		return formatNum(mn) + "–" + formatNum(mx) + ", Ø " + formatNum(avg), true
	}

	// All unique → omit (meaningless for free-text).
	unique := make(map[string]struct{}, len(nonEmpty))
	for _, v := range nonEmpty {
		unique[v] = struct{}{}
	}
	if len(unique) == len(nonEmpty) {
		return "", false
	}

	return fmt.Sprintf("%d unique", len(unique)), false
}

// computeExplicitAggregate computes a configured aggregate function.
func computeExplicitAggregate(totalRows int, nonEmpty []string, aggregate string) string {
	switch aggregate {
	case "unique":
		unique := make(map[string]struct{}, len(nonEmpty))
		for _, v := range nonEmpty {
			unique[v] = struct{}{}
		}
		return fmt.Sprintf("%d unique", len(unique))
	case "total":
		return strconv.Itoa(totalRows)
	case "count_empty":
		return strconv.Itoa(totalRows - len(nonEmpty))
	case "count_not_empty":
		return strconv.Itoa(len(nonEmpty))
	case "percent_empty":
		if totalRows == 0 {
			return "0%"
		}
		return fmt.Sprintf("%.0f%%", float64(totalRows-len(nonEmpty))/float64(totalRows)*100)
	case "percent_not_empty":
		if totalRows == 0 {
			return "0%"
		}
		return fmt.Sprintf("%.0f%%", float64(len(nonEmpty))/float64(totalRows)*100)
	case "sum", "avg", "mean", "median", "min", "max", "range":
		return computeNumericAggregate(nonEmpty, aggregate)
	default:
		return ""
	}
}

// computeNumericAggregate parses values as numbers and computes the requested aggregate.
func computeNumericAggregate(nonEmpty []string, aggregate string) string {
	nums := make([]float64, 0, len(nonEmpty))
	for _, v := range nonEmpty {
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			nums = append(nums, n)
		}
	}
	if len(nums) == 0 {
		return ""
	}

	sort.Float64s(nums)
	mn, mx := nums[0], nums[len(nums)-1]
	sum := 0.0
	for _, n := range nums {
		sum += n
	}

	switch aggregate {
	case "sum":
		return formatNum(sum)
	case "avg", "mean":
		return "Ø " + formatNum(sum/float64(len(nums)))
	case "median":
		n := len(nums)
		if n%2 == 0 {
			return formatNum((nums[n/2-1] + nums[n/2]) / 2)
		}
		return formatNum(nums[n/2])
	case "min":
		return formatNum(mn)
	case "max":
		return formatNum(mx)
	case "range":
		return formatNum(mn) + "–" + formatNum(mx)
	}
	return ""
}

// formatNum formats a float for display: integers show no decimals, others get up to 2.
func formatNum(v float64) string {
	if v == float64(int64(v)) && v >= -1e15 && v <= 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

// computeDateSummary returns a date range string from the submissions' timestamps.
func computeDateSummary(views []submissionView) string {
	var earliest, latest time.Time
	for _, v := range views {
		t, err := time.Parse(time.RFC3339, v.CreatedAt)
		if err != nil {
			continue
		}
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
		if latest.IsZero() || t.After(latest) {
			latest = t
		}
	}
	if earliest.IsZero() {
		return ""
	}
	if earliest.Format("Jan 2, 2006") == latest.Format("Jan 2, 2006") {
		return earliest.Format("Jan 2, 2006")
	}
	if earliest.Year() == latest.Year() {
		return earliest.Format("Jan 2") + " – " + latest.Format("Jan 2, 2006")
	}
	return earliest.Format("Jan 2, 2006") + " – " + latest.Format("Jan 2, 2006")
}

// stableFieldOrder returns field keys in a deterministic order: config-declared
// fields first (in declaration order), then remaining keys sorted alphabetically.
func stableFieldOrder(endpoint *storage.FormEndpoint, keys []string) []string {
	if len(keys) == 0 {
		return keys
	}

	present := make(map[string]bool, len(keys))
	for _, k := range keys {
		present[k] = true
	}

	var ordered []string
	used := make(map[string]bool, len(keys))

	// Config-declared fields first.
	if endpoint != nil {
		for _, f := range endpoint.Fields {
			if present[f.Key] && !used[f.Key] {
				ordered = append(ordered, f.Key)
				used[f.Key] = true
			}
		}
	}

	// Remaining keys, sorted.
	var rest []string
	for _, k := range keys {
		if !used[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(ordered, rest...)
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
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
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
