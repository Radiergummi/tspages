package submissions

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"tspages/internal/storage"
	"tspages/internal/web"
)

// region humanize

// HumanizePath returns a display name from a URL path by humanizing the last segment.
// Splits on "-" and "_", title-cases each word. Returns "Home" for root or empty paths.
func HumanizePath(path string) string {
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "Home"
	}
	seg := path[strings.LastIndex(path, "/")+1:]
	if seg == "" {
		return "Home"
	}
	words := splitDelimiters(seg)
	return titleJoin(words)
}

// HumanizeKey returns a display label from a form field key.
// Splits on "_", "-", and camelCase boundaries, title-cases each word.
// All-uppercase runs (ID, HTML, URL) stay grouped as single tokens.
func HumanizeKey(key string) string {
	if key == "" {
		return ""
	}
	parts := splitDelimiters(key)
	var words []string
	for _, part := range parts {
		words = append(words, splitCamel(part)...)
	}
	return titleJoin(words)
}

// splitDelimiters splits s on "-" and "_".
func splitDelimiters(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '_'
	})
}

// splitCamel splits a string on camelCase boundaries.
// Uppercase runs are kept together: "HTMLContent" -> ["HTML", "Content"],
// "userID" -> ["user", "ID"].
func splitCamel(s string) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	var words []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev := runes[i-1]
		cur := runes[i]
		if unicode.IsLower(prev) && unicode.IsUpper(cur) {
			words = append(words, string(runes[start:i]))
			start = i
			continue
		}
		if i >= 2 && unicode.IsUpper(runes[i-2]) && unicode.IsUpper(prev) && unicode.IsLower(cur) {
			words = append(words, string(runes[start:i-1]))
			start = i - 1
		}
	}
	words = append(words, string(runes[start:]))
	return words
}

// titleJoin title-cases each word and joins with spaces.
// Words that are all uppercase and len >= 2 are kept as-is (ID, HTML, URL).
func titleJoin(words []string) string {
	var b strings.Builder
	for i, w := range words {
		if i > 0 {
			b.WriteByte(' ')
		}
		if len(w) >= 2 && isAllUpper(w) {
			b.WriteString(w)
		} else {
			b.WriteString(titleCase(w))
		}
	}
	return b.String()
}

func isAllUpper(s string) bool {
	for _, r := range s {
		if !unicode.IsUpper(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func titleCase(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	for i := 1; i < len(runes); i++ {
		runes[i] = unicode.ToLower(runes[i])
	}
	return string(runes)
}

// endregion

// region CSV helpers

// parseRFC3339 parses v as RFC3339 into dst. Returns false and writes a 400 if v is non-empty and invalid.
func parseRFC3339(w http.ResponseWriter, r *http.Request, v, name string, dst *time.Time) bool {
	if v == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		web.RenderError(w, r, http.StatusBadRequest, fmt.Sprintf("invalid %s: expected RFC3339 format", name))
		return false
	}
	*dst = t
	return true
}

// csvFieldValue converts a JSON value (already expanded by ExpandFields) to a
// CSV-friendly string. Arrays are joined with ", ".
func csvFieldValue(v any) string {
	switch val := v.(type) {
	case string:
		return csvSanitize(val)
	case []any:
		parts := make([]string, 0, len(val))
		for _, el := range val {
			if s := fmt.Sprintf("%v", el); s != "" {
				parts = append(parts, s)
			}
		}
		return csvSanitize(strings.Join(parts, ", "))
	default:
		return csvSanitize(fmt.Sprintf("%v", v))
	}
}

// csvSanitize prefixes values that start with characters spreadsheet
// applications interpret as formula indicators, preventing CSV injection
// (CWE-1236).
func csvSanitize(s string) string {
	if len(s) > 0 {
		switch s[0] {
		case '=', '+', '-', '@', '\t', '\r':
			return "'" + s
		}
	}
	return s
}

// endregion

// region sort helpers

// parseSortParam parses a comma-separated sort parameter into SortFields (max 2).
// Minus prefix means descending: "-date,email" -> [{date desc}, {email asc}].
func parseSortParam(raw string) []SortField {
	if raw == "" {
		return nil
	}
	parts := strings.SplitN(raw, ",", 3)
	if len(parts) > 2 {
		parts = parts[:2]
	}
	var out []SortField
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
		out = append(out, SortField{Column: col, Desc: desc})
	}
	return out
}

// formatSortParam is the inverse of parseSortParam.
func formatSortParam(sorts []SortField) string {
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

// endregion

// region config helpers

// formNameFromConfig returns the display name for a form path, checking config first.
func formNameFromConfig(cfg storage.SiteConfig, path string) string {
	if cfg.Forms != nil {
		for _, ep := range cfg.Forms.Endpoints {
			if ep.Path == path && ep.Name != "" {
				return ep.Name
			}
		}
	}
	return HumanizePath(path)
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
	return HumanizeKey(key)
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

// endregion

// region field summary and aggregation

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

	// Explicit aggregate configured — delegate without detecting numeric type,
	// since computeNumericAggregate does its own parsing.
	if aggregate != "" {
		// Still detect isNumeric for column alignment.
		isNumeric = true
		for _, v := range nonEmpty {
			if _, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err != nil {
				isNumeric = false
				break
			}
		}
		return computeExplicitAggregate(len(values), nonEmpty, aggregate), isNumeric
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
		return formatNum(mn) + "\u2013" + formatNum(mx) + ", \u00d8 " + formatNum(avg), true
	}

	// All unique -> omit (meaningless for free-text).
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
		return "\u00d8 " + formatNum(sum/float64(len(nums)))
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
		return formatNum(mn) + "\u2013" + formatNum(mx)
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
		return earliest.Format("Jan 2") + " \u2013 " + latest.Format("Jan 2, 2006")
	}
	return earliest.Format("Jan 2, 2006") + " \u2013 " + latest.Format("Jan 2, 2006")
}

// stableFieldOrder returns field keys in a deterministic order: config-declared
// fields first (in declaration order), then remaining keys sorted alphabetically.
func stableFieldOrder(endpoint *storage.FormEndpoint, keys []string) []string {
	if len(keys) == 0 {
		return keys
	}

	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}

	var ordered []string

	// Config-declared fields first.
	if endpoint != nil {
		for _, f := range endpoint.Fields {
			if keySet[f.Key] {
				ordered = append(ordered, f.Key)
				delete(keySet, f.Key)
			}
		}
	}

	// Remaining keys, sorted.
	var rest []string
	for _, k := range keys {
		if keySet[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(ordered, rest...)
}

// fieldValueString converts an arbitrary JSON value to a display string.
// Arrays (from ExpandFields) are joined with newlines for display.
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
	case []any:
		parts := make([]string, 0, len(val))
		for _, el := range val {
			if s := fieldValueString(el); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// endregion

// region multipleFieldKeys

// multipleFieldKeys returns the set of field keys configured as multiple
// across all form endpoints for the given site. It reads config from disk.
func multipleFieldKeys(store *storage.Store, defaults storage.SiteConfig, site string) map[string]bool {
	if store == nil {
		return nil
	}
	deployCfg, _ := store.ReadCurrentDeploymentConfig(site)
	siteCfg, _ := store.ReadSiteConfig(site)
	merged := deployCfg.Merge(siteCfg).Merge(defaults)
	return merged.MultipleFieldKeys()
}

// endregion
