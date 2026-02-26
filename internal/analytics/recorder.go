package analytics

import (
	"database/sql"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// Event represents a single recorded request.
type Event struct {
	Timestamp     time.Time
	Site          string
	Path          string
	Status        int
	UserLogin     string
	UserName      string
	ProfilePicURL string
	NodeName      string
	NodeIP        string
	OS            string
	OSVersion     string
	Device        string
	Tags          []string
}

// Recorder persists request events to SQLite asynchronously.
type Recorder struct {
	db     *sql.DB
	ch     chan Event
	wg     sync.WaitGroup
	closed atomic.Bool
}

func NewRecorder(dbPath string) (*Recorder, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	r := &Recorder{
		db: db,
		ch: make(chan Event, 1024),
	}
	r.wg.Add(1)
	go r.writer()
	return r, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS requests (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			ts              TEXT NOT NULL,
			site            TEXT NOT NULL,
			path            TEXT NOT NULL,
			status          INTEGER NOT NULL,
			user_login      TEXT NOT NULL DEFAULT '',
			user_name       TEXT NOT NULL DEFAULT '',
			profile_pic_url TEXT NOT NULL DEFAULT '',
			node_name       TEXT NOT NULL DEFAULT '',
			node_ip         TEXT NOT NULL DEFAULT '',
			os              TEXT NOT NULL DEFAULT '',
			os_version      TEXT NOT NULL DEFAULT '',
			device          TEXT NOT NULL DEFAULT '',
			tags            TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_requests_site_ts ON requests(site, ts);
	`)
	if err != nil {
		return err
	}
	// For existing databases: add profile_pic_url if missing.
	_, err = db.Exec(`ALTER TABLE requests ADD COLUMN profile_pic_url TEXT NOT NULL DEFAULT ''`)
	if err != nil && strings.Contains(err.Error(), "duplicate column") {
		err = nil
	}
	return err
}

// Record sends an event to the writer goroutine. Non-blocking; drops on full
// buffer. Safe to call after Close (no-op).
func (r *Recorder) Record(e Event) {
	if r.closed.Load() {
		return
	}
	select {
	case r.ch <- e:
	default:
	}
}

func (r *Recorder) writer() {
	defer r.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var batch []Event
	for {
		select {
		case e, ok := <-r.ch:
			if !ok {
				if len(batch) > 0 {
					r.flush(batch)
				}
				return
			}
			batch = append(batch, e)
			if len(batch) >= 100 {
				r.flush(batch)
				batch = nil
			}
		case <-ticker.C:
			if len(batch) > 0 {
				r.flush(batch)
				batch = nil
			}
		}
	}
}

func (r *Recorder) flush(events []Event) {
	tx, err := r.db.Begin()
	if err != nil {
		log.Printf("analytics: begin tx: %v", err)
		return
	}
	stmt, err := tx.Prepare(`INSERT INTO requests (ts, site, path, status, user_login, user_name, profile_pic_url, node_name, node_ip, os, os_version, device, tags) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		log.Printf("analytics: prepare: %v", err)
		tx.Rollback()
		return
	}
	defer stmt.Close()
	for _, e := range events {
		tags := strings.Join(e.Tags, ",")
		_, err := stmt.Exec(
			e.Timestamp.UTC().Format(time.RFC3339),
			e.Site, e.Path, e.Status,
			e.UserLogin, e.UserName, e.ProfilePicURL,
			e.NodeName, e.NodeIP,
			e.OS, e.OSVersion, e.Device, tags,
		)
		if err != nil {
			log.Printf("analytics: insert: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("analytics: commit: %v", err)
	}
}

// DB returns the underlying database connection for shared use.
func (r *Recorder) DB() *sql.DB { return r.db }

// Ping checks whether the analytics database is reachable.
func (r *Recorder) Ping() error {
	return r.db.Ping()
}

// Close drains the event channel and shuts down the writer.
func (r *Recorder) Close() error {
	r.closed.Store(true)
	close(r.ch)
	r.wg.Wait()
	return r.db.Close()
}

// bucketSQL is the SQL expression that truncates a timestamp to a bucket
// boundary using epoch-based rounding. It requires two int parameters: the
// step size in seconds, passed twice.
const bucketSQL = `strftime('%Y-%m-%dT%H:%M:%SZ', (CAST(strftime('%s', ts) AS INTEGER) / ?) * ?, 'unixepoch')`

// bucketStep returns the largest "nice" step that produces at least 64 buckets.
func bucketStep(from, to time.Time) time.Duration {
	d := to.Sub(from)
	steps := []time.Duration{
		24 * time.Hour,
		12 * time.Hour,
		8 * time.Hour,
		6 * time.Hour,
		4 * time.Hour,
		2 * time.Hour,
		time.Hour,
		30 * time.Minute,
		15 * time.Minute,
	}
	for _, s := range steps {
		if d/s >= 64 {
			return s
		}
	}
	return 15 * time.Minute
}

// fillBuckets takes sparse SQL results and returns a complete series with
// zero-filled gaps from `from` to `to`.
func fillBuckets(sparse []TimeBucket, from, to time.Time) []TimeBucket {
	if from.IsZero() && len(sparse) > 0 {
		// "all" range: derive from from the first bucket
		t, err := time.Parse(time.RFC3339, sparse[0].Time)
		if err == nil {
			from = t
		}
	}
	if from.IsZero() {
		return sparse
	}

	step := bucketStep(from, to)
	from = from.UTC().Truncate(step)

	// Index sparse results by their time key.
	lookup := make(map[string]int64, len(sparse))
	for _, b := range sparse {
		lookup[b.Time] = b.Count
	}

	var out []TimeBucket
	for t := from; !t.After(to.UTC()); t = t.Add(step) {
		key := t.Format(time.RFC3339)
		out = append(out, TimeBucket{Time: key, Count: lookup[key]})
	}
	return out
}

// --- Query types ---

type TimeBucket struct {
	Time  string `json:"time"`
	Count int64  `json:"count"`
}

type StatusTimeBucket struct {
	Time      string `json:"time"`
	OK        int64  `json:"ok"`
	ClientErr int64  `json:"client_err"`
	ServerErr int64  `json:"server_err"`
}

type PathCount struct {
	Path  string `json:"path"`
	Count int64  `json:"count"`
}

type VisitorCount struct {
	UserLogin     string `json:"user_login"`
	UserName      string `json:"user_name"`
	ProfilePicURL string `json:"profile_pic_url,omitempty"`
	Count         int64  `json:"count"`
}

type StatusCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

type HourCount struct {
	Hour  int   `json:"hour"`
	Count int64 `json:"count"`
}

type OSCount struct {
	OS    string `json:"os"`
	Count int64  `json:"count"`
}

type NodeCount struct {
	NodeName string `json:"node_name"`
	OS       string `json:"os"`
	Count    int64  `json:"count"`
}

// --- Query methods ---

func (r *Recorder) TotalRequests(site string, from, to time.Time) (int64, error) {
	var count int64
	err := r.db.QueryRow(
		`SELECT COUNT(*) FROM requests WHERE site = ? AND ts >= ? AND ts <= ?`,
		site, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339),
	).Scan(&count)
	return count, err
}

func (r *Recorder) UniqueVisitors(site string, from, to time.Time) (int64, error) {
	var count int64
	err := r.db.QueryRow(
		`SELECT COUNT(DISTINCT user_login) FROM requests WHERE site = ? AND ts >= ? AND ts <= ? AND user_login != ''`,
		site, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339),
	).Scan(&count)
	return count, err
}

func (r *Recorder) UniquePages(site string, from, to time.Time) (int64, error) {
	var count int64
	err := r.db.QueryRow(
		`SELECT COUNT(DISTINCT path) FROM requests WHERE site = ? AND ts >= ? AND ts <= ?`,
		site, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339),
	).Scan(&count)
	return count, err
}

func (r *Recorder) RequestsOverTime(site string, from, to time.Time) ([]TimeBucket, error) {
	stepSecs := int(bucketStep(from, to).Seconds())
	rows, err := r.db.Query(
		`SELECT `+bucketSQL+` AS bucket, COUNT(*) FROM requests WHERE site = ? AND ts >= ? AND ts <= ? GROUP BY bucket ORDER BY bucket`,
		stepSecs, stepSecs, site, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339),
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
	return fillBuckets(sparse, from, to), nil
}

func fillStatusBuckets(sparse []StatusTimeBucket, from, to time.Time) []StatusTimeBucket {
	if from.IsZero() && len(sparse) > 0 {
		t, err := time.Parse(time.RFC3339, sparse[0].Time)
		if err == nil {
			from = t
		}
	}
	if from.IsZero() {
		return sparse
	}
	step := bucketStep(from, to)
	from = from.UTC().Truncate(step)
	lookup := make(map[string]StatusTimeBucket, len(sparse))
	for _, b := range sparse {
		lookup[b.Time] = b
	}
	var out []StatusTimeBucket
	for t := from; !t.After(to.UTC()); t = t.Add(step) {
		key := t.Format(time.RFC3339)
		if b, ok := lookup[key]; ok {
			out = append(out, b)
		} else {
			out = append(out, StatusTimeBucket{Time: key})
		}
	}
	return out
}

func (r *Recorder) RequestsOverTimeByStatus(site string, from, to time.Time) ([]StatusTimeBucket, error) {
	stepSecs := int(bucketStep(from, to).Seconds())
	rows, err := r.db.Query(
		`SELECT `+bucketSQL+` AS bucket,
			SUM(CASE WHEN status/100 IN (1,2,3) THEN 1 ELSE 0 END),
			SUM(CASE WHEN status/100 = 4 THEN 1 ELSE 0 END),
			SUM(CASE WHEN status/100 = 5 THEN 1 ELSE 0 END)
		FROM requests WHERE site = ? AND ts >= ? AND ts <= ?
		GROUP BY bucket ORDER BY bucket`,
		stepSecs, stepSecs, site, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sparse []StatusTimeBucket
	for rows.Next() {
		var b StatusTimeBucket
		if err := rows.Scan(&b.Time, &b.OK, &b.ClientErr, &b.ServerErr); err != nil {
			return nil, err
		}
		sparse = append(sparse, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return fillStatusBuckets(sparse, from, to), nil
}

func (r *Recorder) TopPages(site string, from, to time.Time, limit int) ([]PathCount, error) {
	rows, err := r.db.Query(
		`SELECT path, COUNT(*) AS c FROM requests WHERE site = ? AND ts >= ? AND ts <= ? GROUP BY path ORDER BY c DESC LIMIT ?`,
		site, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PathCount
	for rows.Next() {
		var p PathCount
		if err := rows.Scan(&p.Path, &p.Count); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Recorder) TopVisitors(site string, from, to time.Time, limit int) ([]VisitorCount, error) {
	rows, err := r.db.Query(
		`SELECT user_login, MAX(user_name), MAX(profile_pic_url), COUNT(*) AS c FROM requests WHERE site = ? AND ts >= ? AND ts <= ? AND user_login != '' GROUP BY user_login ORDER BY c DESC LIMIT ?`,
		site, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VisitorCount
	for rows.Next() {
		var v VisitorCount
		if err := rows.Scan(&v.UserLogin, &v.UserName, &v.ProfilePicURL, &v.Count); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Recorder) StatusBreakdown(site string, from, to time.Time) ([]StatusCount, error) {
	rows, err := r.db.Query(
		`SELECT CAST(status/100 AS TEXT) || 'xx' AS cat, COUNT(*) AS c FROM requests WHERE site = ? AND ts >= ? AND ts <= ? GROUP BY cat ORDER BY cat`,
		site, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatusCount
	for rows.Next() {
		var s StatusCount
		if err := rows.Scan(&s.Status, &s.Count); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Recorder) HourlyPattern(site string, from, to time.Time) ([]HourCount, error) {
	rows, err := r.db.Query(
		`SELECT CAST(strftime('%H', ts) AS INTEGER) AS h, COUNT(*) AS c FROM requests WHERE site = ? AND ts >= ? AND ts <= ? GROUP BY h ORDER BY h`,
		site, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339),
	)
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

func (r *Recorder) OSBreakdown(site string, from, to time.Time) ([]OSCount, error) {
	rows, err := r.db.Query(
		`SELECT os, COUNT(*) AS c FROM requests WHERE site = ? AND ts >= ? AND ts <= ? AND os != '' GROUP BY os ORDER BY c DESC`,
		site, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OSCount
	for rows.Next() {
		var o OSCount
		if err := rows.Scan(&o.OS, &o.Count); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r *Recorder) NodeBreakdown(site string, from, to time.Time) ([]NodeCount, error) {
	rows, err := r.db.Query(
		`SELECT node_name, MAX(os), COUNT(*) AS c FROM requests WHERE site = ? AND ts >= ? AND ts <= ? AND node_name != '' GROUP BY node_name ORDER BY c DESC`,
		site, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeCount
	for rows.Next() {
		var n NodeCount
		if err := rows.Scan(&n.NodeName, &n.OS, &n.Count); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// --- Aggregate query methods (filtered to given sites) ---

type SiteCount struct {
	Site  string `json:"site"`
	Count int64  `json:"count"`
}

// siteFilter builds a "site IN (?, ?, ...)" clause and args for the given sites.
func siteFilter(sites []string) (string, []any) {
	placeholders := make([]string, len(sites))
	args := make([]any, len(sites))
	for i, s := range sites {
		placeholders[i] = "?"
		args[i] = s
	}
	return "site IN (" + strings.Join(placeholders, ",") + ")", args
}

func (r *Recorder) TotalRequestsMulti(sites []string, from, to time.Time) (int64, error) {
	if len(sites) == 0 {
		return 0, nil
	}
	inClause, args := siteFilter(sites)
	args = append(args, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	var count int64
	err := r.db.QueryRow(
		`SELECT COUNT(*) FROM requests WHERE `+inClause+` AND ts >= ? AND ts <= ?`, args...,
	).Scan(&count)
	return count, err
}

func (r *Recorder) UniqueVisitorsMulti(sites []string, from, to time.Time) (int64, error) {
	if len(sites) == 0 {
		return 0, nil
	}
	inClause, args := siteFilter(sites)
	args = append(args, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	var count int64
	err := r.db.QueryRow(
		`SELECT COUNT(DISTINCT user_login) FROM requests WHERE `+inClause+` AND ts >= ? AND ts <= ? AND user_login != ''`, args...,
	).Scan(&count)
	return count, err
}

func (r *Recorder) RequestsOverTimeMulti(sites []string, from, to time.Time) ([]TimeBucket, error) {
	if len(sites) == 0 {
		return nil, nil
	}
	stepSecs := int(bucketStep(from, to).Seconds())
	inClause, args := siteFilter(sites)
	args = append([]any{stepSecs, stepSecs}, args...)
	args = append(args, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	rows, err := r.db.Query(
		`SELECT `+bucketSQL+` AS bucket, COUNT(*) FROM requests WHERE `+inClause+` AND ts >= ? AND ts <= ? GROUP BY bucket ORDER BY bucket`, args...,
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
	return fillBuckets(sparse, from, to), nil
}

func (r *Recorder) RequestsOverTimeByStatusMulti(sites []string, from, to time.Time) ([]StatusTimeBucket, error) {
	if len(sites) == 0 {
		return nil, nil
	}
	stepSecs := int(bucketStep(from, to).Seconds())
	inClause, args := siteFilter(sites)
	args = append([]any{stepSecs, stepSecs}, args...)
	args = append(args, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	rows, err := r.db.Query(
		`SELECT `+bucketSQL+` AS bucket,
			SUM(CASE WHEN status/100 IN (1,2,3) THEN 1 ELSE 0 END),
			SUM(CASE WHEN status/100 = 4 THEN 1 ELSE 0 END),
			SUM(CASE WHEN status/100 = 5 THEN 1 ELSE 0 END)
		FROM requests WHERE `+inClause+` AND ts >= ? AND ts <= ?
		GROUP BY bucket ORDER BY bucket`, args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sparse []StatusTimeBucket
	for rows.Next() {
		var b StatusTimeBucket
		if err := rows.Scan(&b.Time, &b.OK, &b.ClientErr, &b.ServerErr); err != nil {
			return nil, err
		}
		sparse = append(sparse, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return fillStatusBuckets(sparse, from, to), nil
}

func (r *Recorder) SiteBreakdown(sites []string, from, to time.Time) ([]SiteCount, error) {
	if len(sites) == 0 {
		return nil, nil
	}
	inClause, args := siteFilter(sites)
	args = append(args, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	rows, err := r.db.Query(
		`SELECT site, COUNT(*) AS c FROM requests WHERE `+inClause+` AND ts >= ? AND ts <= ? GROUP BY site ORDER BY c DESC`, args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SiteCount
	for rows.Next() {
		var s SiteCount
		if err := rows.Scan(&s.Site, &s.Count); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Recorder) TopVisitorsMulti(sites []string, from, to time.Time, limit int) ([]VisitorCount, error) {
	if len(sites) == 0 {
		return nil, nil
	}
	inClause, args := siteFilter(sites)
	args = append(args, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339), limit)
	rows, err := r.db.Query(
		`SELECT user_login, MAX(user_name), MAX(profile_pic_url), COUNT(*) AS c FROM requests WHERE `+inClause+` AND ts >= ? AND ts <= ? AND user_login != '' GROUP BY user_login ORDER BY c DESC LIMIT ?`, args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VisitorCount
	for rows.Next() {
		var v VisitorCount
		if err := rows.Scan(&v.UserLogin, &v.UserName, &v.ProfilePicURL, &v.Count); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Recorder) StatusBreakdownMulti(sites []string, from, to time.Time) ([]StatusCount, error) {
	if len(sites) == 0 {
		return nil, nil
	}
	inClause, args := siteFilter(sites)
	args = append(args, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	rows, err := r.db.Query(
		`SELECT CAST(status/100 AS TEXT) || 'xx' AS cat, COUNT(*) AS c FROM requests WHERE `+inClause+` AND ts >= ? AND ts <= ? GROUP BY cat ORDER BY cat`, args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatusCount
	for rows.Next() {
		var s StatusCount
		if err := rows.Scan(&s.Status, &s.Count); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Recorder) HourlyPatternMulti(sites []string, from, to time.Time) ([]HourCount, error) {
	if len(sites) == 0 {
		return nil, nil
	}
	inClause, args := siteFilter(sites)
	args = append(args, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	rows, err := r.db.Query(
		`SELECT CAST(strftime('%H', ts) AS INTEGER) AS h, COUNT(*) AS c FROM requests WHERE `+inClause+` AND ts >= ? AND ts <= ? GROUP BY h ORDER BY h`, args...,
	)
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

func (r *Recorder) OSBreakdownMulti(sites []string, from, to time.Time) ([]OSCount, error) {
	if len(sites) == 0 {
		return nil, nil
	}
	inClause, args := siteFilter(sites)
	args = append(args, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	rows, err := r.db.Query(
		`SELECT os, COUNT(*) AS c FROM requests WHERE `+inClause+` AND ts >= ? AND ts <= ? AND os != '' GROUP BY os ORDER BY c DESC`, args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OSCount
	for rows.Next() {
		var o OSCount
		if err := rows.Scan(&o.OS, &o.Count); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r *Recorder) NodeBreakdownMulti(sites []string, from, to time.Time) ([]NodeCount, error) {
	if len(sites) == 0 {
		return nil, nil
	}
	inClause, args := siteFilter(sites)
	args = append(args, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	rows, err := r.db.Query(
		`SELECT node_name, MAX(os), COUNT(*) AS c FROM requests WHERE `+inClause+` AND ts >= ? AND ts <= ? AND node_name != '' GROUP BY node_name ORDER BY c DESC`, args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeCount
	for rows.Next() {
		var n NodeCount
		if err := rows.Scan(&n.NodeName, &n.OS, &n.Count); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (r *Recorder) PurgeSite(site string) (int64, error) {
	res, err := r.db.Exec(`DELETE FROM requests WHERE site = ?`, site)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
