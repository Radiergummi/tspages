package analytics

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tspages/internal/timeseries"
)

var formBucketSQL = timeseries.BucketSQL("created_at")

// FieldSeparator is the Unicode Record Separator (U+001E), used to encode
// multi-value form fields (e.g. checkboxes, multi-selects) as a single string.
const FieldSeparator = "\x1e"

// ExpandFields rewrites stored field JSON so that any string containing
// FieldSeparator is split into a JSON array. Keys in alwaysArray are
// wrapped in a single-element array even without a separator, so API
// consumers get a consistent type for fields configured as multiple.
func ExpandFields(raw json.RawMessage, alwaysArray map[string]bool) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}

	// Copy-on-write: only allocate when a field actually needs expansion.
	var out map[string]any
	for k, v := range m {
		if strings.Contains(v, FieldSeparator) {
			if out == nil {
				out = make(map[string]any, len(m))
				for k2, v2 := range m {
					out[k2] = v2
				}
			}
			parts := strings.Split(v, FieldSeparator)
			clean := make([]string, 0, len(parts))
			for _, p := range parts {
				if p != "" {
					clean = append(clean, p)
				}
			}
			out[k] = clean
		} else if alwaysArray[k] {
			if out == nil {
				out = make(map[string]any, len(m))
				for k2, v2 := range m {
					out[k2] = v2
				}
			}
			out[k] = []string{v}
		}
	}
	if out == nil {
		return raw
	}
	expanded, err := json.Marshal(out)
	if err != nil {
		return raw
	}
	return expanded
}

// Submission represents a single form submission stored in the database.
type Submission struct {
	ID              string          `json:"id"`
	Site            string          `json:"site"`
	Path            string          `json:"path"`
	Fields          json.RawMessage `json:"fields"`
	Submitter       string          `json:"submitter"`
	SubmitterName   string          `json:"submitter_name,omitempty"`
	SubmitterPicURL string          `json:"submitter_pic_url,omitempty"`
	Node            string          `json:"node"`
	IP              string          `json:"ip"`
	CreatedAt       string          `json:"created_at"`
}

// FormPath holds a form path and the number of submissions to it.
type FormPath struct {
	Path  string `json:"path"`
	Count int64  `json:"count"`
}

// SortField specifies a single column sort with direction.
type SortField struct {
	Column string // "submitter", "date", or a field key for json_extract
	Desc   bool
}

// ListSubmissionsOpts controls filtering, sorting, and pagination for ListSubmissions.
type ListSubmissionsOpts struct {
	Path   string
	Since  time.Time
	Until  time.Time
	Sort   []SortField // up to 2; default: date desc
	Limit  int
	Offset int
}

// BulkDeleteOpts controls filtering for BulkDeleteSubmissions.
type BulkDeleteOpts struct {
	Path   string
	Before time.Time
}

// RecordSubmission inserts a single form submission. Unlike page view recording,
// this is synchronous because submissions are infrequent and callers need the
// ID back immediately.
func (r *Recorder) RecordSubmission(s Submission) error {
	_, err := r.db.Exec(
		`INSERT INTO form_submissions (id, site, path, fields, submitter, submitter_name, submitter_pic_url, node, ip, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Site, s.Path, string(s.Fields), s.Submitter, s.SubmitterName, s.SubmitterPicURL, s.Node, s.IP, s.CreatedAt,
	)
	return err
}

// GetSubmission returns a single submission by site and ID.
func (r *Recorder) GetSubmission(site, id string) (Submission, error) {
	var s Submission
	var fields string
	err := r.db.QueryRow(
		`SELECT id, site, path, fields, submitter, submitter_name, submitter_pic_url, node, ip, created_at
		 FROM form_submissions WHERE site = ? AND id = ?`, site, id,
	).Scan(&s.ID, &s.Site, &s.Path, &fields, &s.Submitter, &s.SubmitterName, &s.SubmitterPicURL, &s.Node, &s.IP, &s.CreatedAt)
	if err != nil {
		return Submission{}, err
	}
	s.Fields = json.RawMessage(fields)
	return s, nil
}

// ListSubmissions returns submissions for a site, with optional filters.
// Default sort is created_at DESC (newest first).
func (r *Recorder) ListSubmissions(site string, opts ListSubmissionsOpts) ([]Submission, error) {
	if len(opts.Sort) == 0 {
		opts.Sort = []SortField{{Column: "date", Desc: true}}
	} else if len(opts.Sort) > 2 {
		opts.Sort = opts.Sort[:2]
	}

	query := `SELECT id, site, path, fields, submitter, submitter_name, submitter_pic_url, node, ip, created_at
		FROM form_submissions WHERE site = ?`
	args := []any{site}

	if opts.Path != "" {
		query += ` AND path = ?`
		args = append(args, opts.Path)
	}
	if !opts.Since.IsZero() {
		query += ` AND created_at >= ?`
		args = append(args, opts.Since.UTC().Format(time.RFC3339))
	}
	if !opts.Until.IsZero() {
		query += ` AND created_at <= ?`
		args = append(args, opts.Until.UTC().Format(time.RFC3339))
	}

	var orderParts []string
	for _, s := range opts.Sort {
		var clause string
		switch s.Column {
		case "submitter":
			clause = "submitter"
		case "date":
			clause = "created_at"
		default:
			clause = "json_extract(fields, ?)"
			args = append(args, "$."+s.Column)
		}
		if s.Desc {
			clause += " DESC"
		} else {
			clause += " ASC"
		}
		orderParts = append(orderParts, clause)
	}
	query += " ORDER BY " + strings.Join(orderParts, ", ") + ", id ASC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, opts.Limit)
	}
	if opts.Offset > 0 {
		query += fmt.Sprintf(` OFFSET %d`, opts.Offset)
	}

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Submission
	for rows.Next() {
		var s Submission
		var fields string
		if err := rows.Scan(&s.ID, &s.Site, &s.Path, &fields, &s.Submitter, &s.SubmitterName, &s.SubmitterPicURL, &s.Node, &s.IP, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.Fields = json.RawMessage(fields)
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteSubmission removes a single submission by site and ID.
// Returns false if no matching row was found.
func (r *Recorder) DeleteSubmission(site, id string) (bool, error) {
	res, err := r.db.Exec(`DELETE FROM form_submissions WHERE site = ? AND id = ?`, site, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// BulkDeleteSubmissions deletes submissions matching the given filters and
// returns the number of rows affected.
func (r *Recorder) BulkDeleteSubmissions(site string, opts BulkDeleteOpts) (int64, error) {
	query := `DELETE FROM form_submissions WHERE site = ?`
	args := []any{site}

	if opts.Path != "" {
		query += ` AND path = ?`
		args = append(args, opts.Path)
	}
	if !opts.Before.IsZero() {
		query += ` AND created_at < ?`
		args = append(args, opts.Before.UTC().Format(time.RFC3339))
	}

	res, err := r.db.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// FormPaths returns the distinct form paths for a site with submission counts,
// ordered by count descending.
func (r *Recorder) FormPaths(site string) ([]FormPath, error) {
	rows, err := r.db.Query(
		`SELECT path, COUNT(*) AS c FROM form_submissions WHERE site = ? GROUP BY path ORDER BY c DESC`,
		site,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FormPath
	for rows.Next() {
		var fp FormPath
		if err := rows.Scan(&fp.Path, &fp.Count); err != nil {
			return nil, err
		}
		out = append(out, fp)
	}
	return out, rows.Err()
}

// CountSubmissions returns the number of form submissions for a site, optionally
// filtered by path and time range.
func (r *Recorder) CountSubmissions(site, path string, from, to time.Time) (int64, error) {
	query := `SELECT COUNT(*) FROM form_submissions WHERE site = ?`
	args := []any{site}
	if path != "" {
		query += ` AND path = ?`
		args = append(args, path)
	}
	if !from.IsZero() {
		query += ` AND created_at >= ?`
		args = append(args, from.UTC().Format(time.RFC3339))
	}
	if !to.IsZero() {
		query += ` AND created_at <= ?`
		args = append(args, to.UTC().Format(time.RFC3339))
	}
	var count int64
	err := r.db.QueryRow(query, args...).Scan(&count)
	return count, err
}

// PurgeFormSubmissions deletes all form submissions for a site and returns the
// number of rows deleted.
func (r *Recorder) PurgeFormSubmissions(site string) (int64, error) {
	res, err := r.db.Exec(`DELETE FROM form_submissions WHERE site = ?`, site)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// UniqueSubmitters returns the number of distinct non-empty submitters for a site.
// If path is non-empty, results are filtered to that path. Zero from/to values
// are treated as unbounded on that side.
func (r *Recorder) UniqueSubmitters(site, path string, from, to time.Time) (int64, error) {
	if to.IsZero() {
		to = time.Now()
	}
	query := `SELECT COUNT(DISTINCT submitter) FROM form_submissions WHERE site = ? AND submitter != ''`
	args := []any{site}

	if path != "" {
		query += ` AND path = ?`
		args = append(args, path)
	}
	if !from.IsZero() {
		query += ` AND created_at >= ?`
		args = append(args, from.UTC().Format(time.RFC3339))
	}
	query += ` AND created_at <= ?`
	args = append(args, to.UTC().Format(time.RFC3339))

	var count int64
	err := r.db.QueryRow(query, args...).Scan(&count)
	return count, err
}

// HourlySubmissionPattern returns submission counts grouped by hour-of-day for a site.
// If path is non-empty, results are filtered to that path. Zero from/to values
// are treated as unbounded on that side.
func (r *Recorder) HourlySubmissionPattern(site, path string, from, to time.Time) ([]HourCount, error) {
	if to.IsZero() {
		to = time.Now()
	}
	query := `SELECT CAST(strftime('%H', created_at) AS INTEGER) AS hour, COUNT(*) FROM form_submissions WHERE site = ?`
	args := []any{site}

	if path != "" {
		query += ` AND path = ?`
		args = append(args, path)
	}
	if !from.IsZero() {
		query += ` AND created_at >= ?`
		args = append(args, from.UTC().Format(time.RFC3339))
	}
	query += ` AND created_at <= ?`
	args = append(args, to.UTC().Format(time.RFC3339))

	query += ` GROUP BY hour ORDER BY hour`

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []HourCount
	for rows.Next() {
		var h HourCount
		if err := rows.Scan(&h.Hour, &h.Count); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// SubmissionsOverTimeByForm returns time-bucketed submission counts for a site,
// grouped by form path. All series share the same bucket boundaries.
func (r *Recorder) SubmissionsOverTimeByForm(site string, from, to time.Time) (map[string][]TimeBucket, error) {
	if to.IsZero() {
		to = time.Now()
	}
	stepSecs := int(timeseries.BucketStep(from, to).Seconds())
	args := []any{stepSecs, stepSecs, site}
	where := `site = ?`

	if !from.IsZero() {
		where += ` AND created_at >= ?`
		args = append(args, from.UTC().Format(time.RFC3339))
	}
	where += ` AND created_at <= ?`
	args = append(args, to.UTC().Format(time.RFC3339))

	rows, err := r.db.Query(
		`SELECT path, `+formBucketSQL+` AS bucket, COUNT(*) FROM form_submissions WHERE `+where+` GROUP BY path, bucket ORDER BY path, bucket`, args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sparse := make(map[string][]TimeBucket)
	for rows.Next() {
		var path string
		var b TimeBucket
		if err := rows.Scan(&path, &b.Time, &b.Count); err != nil {
			return nil, err
		}
		sparse[path] = append(sparse[path], b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make(map[string][]TimeBucket, len(sparse))
	for path, buckets := range sparse {
		result[path] = timeseries.FillBuckets(buckets, from, to,
			func(b TimeBucket) string { return b.Time },
			func(t string) TimeBucket { return TimeBucket{Time: t} },
		)
	}
	return result, nil
}

// SubmissionsOverTime returns time-bucketed submission counts for a site.
// If path is non-empty, results are filtered to that path. Zero from/to values
// are treated as unbounded on that side.
func (r *Recorder) SubmissionsOverTime(site, path string, from, to time.Time) ([]TimeBucket, error) {
	if to.IsZero() {
		to = time.Now()
	}
	stepSecs := int(timeseries.BucketStep(from, to).Seconds())
	args := []any{stepSecs, stepSecs, site}
	where := `site = ?`

	if path != "" {
		where += ` AND path = ?`
		args = append(args, path)
	}
	if !from.IsZero() {
		where += ` AND created_at >= ?`
		args = append(args, from.UTC().Format(time.RFC3339))
	}
	where += ` AND created_at <= ?`
	args = append(args, to.UTC().Format(time.RFC3339))

	rows, err := r.db.Query(
		`SELECT `+formBucketSQL+` AS bucket, COUNT(*) FROM form_submissions WHERE `+where+` GROUP BY bucket ORDER BY bucket`, args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sparse []TimeBucket
	for rows.Next() {
		var b TimeBucket
		if err := rows.Scan(&b.Time, &b.Count); err != nil {
			return nil, err
		}
		sparse = append(sparse, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return timeseries.FillBuckets(sparse, from, to,
		func(b TimeBucket) string { return b.Time },
		func(t string) TimeBucket { return TimeBucket{Time: t} },
	), nil
}
