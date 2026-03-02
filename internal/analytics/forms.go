package analytics

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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
	query += " ORDER BY " + strings.Join(orderParts, ", ")

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

// PurgeFormSubmissions deletes all form submissions for a site and returns the
// number of rows deleted.
func (r *Recorder) PurgeFormSubmissions(site string) (int64, error) {
	res, err := r.db.Exec(`DELETE FROM form_submissions WHERE site = ?`, site)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
