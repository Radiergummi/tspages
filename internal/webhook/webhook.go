package webhook

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"tspages/internal/storage"
)

// Notifier sends webhook notifications for site events.
type Notifier struct {
	db          *sql.DB
	client      *http.Client
	retryDelays []time.Duration
	sem         chan struct{}
}

// NewNotifier creates a Notifier and runs the delivery log migration.
func NewNotifier(db *sql.DB) (*Notifier, error) {
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("webhook migration: %w", err)
	}
	return &Notifier{
		db:          db,
		client:      newSafeClient(),
		retryDelays: []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute},
		sem:         make(chan struct{}, 20),
	}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS webhook_deliveries (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			webhook_id TEXT NOT NULL,
			event      TEXT NOT NULL,
			site       TEXT NOT NULL,
			url        TEXT NOT NULL,
			payload    TEXT NOT NULL,
			attempt    INTEGER NOT NULL,
			status     INTEGER,
			error      TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		);
	`)
	return err
}

// Fire sends a webhook notification asynchronously. It is a no-op if the
// config has no WebhookURL or the event is not in the configured event filter.
func (n *Notifier) Fire(event string, site string, cfg storage.SiteConfig, data map[string]any) {
	if cfg.WebhookURL == "" {
		return
	}
	if len(cfg.WebhookEvents) > 0 {
		found := false
		for _, ev := range cfg.WebhookEvents {
			if ev == event {
				found = true
				break
			}
		}
		if !found {
			return
		}
	}
	select {
	case n.sem <- struct{}{}:
		go func() {
			defer func() { <-n.sem }()
			n.deliver(event, site, cfg, data)
		}()
	default:
		log.Printf("webhook: dropping %s event for %s (too many pending deliveries)", event, site)
	}
}

func (n *Notifier) deliver(event, site string, cfg storage.SiteConfig, data map[string]any) {
	msgID := "msg_" + randomHex(16)
	ts := time.Now().UTC()

	payload, err := json.Marshal(map[string]any{
		"type":      event,
		"timestamp": ts.Format(time.RFC3339),
		"data":      data,
	})
	if err != nil {
		log.Printf("webhook: marshal payload: %v", err)
		return
	}

	maxAttempts := 1 + len(n.retryDelays)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		status, sendErr := n.send(cfg.WebhookURL, cfg.WebhookSecret, msgID, ts, payload)

		errStr := ""
		if sendErr != nil {
			errStr = sendErr.Error()
		}
		n.logDelivery(msgID, event, site, cfg.WebhookURL, string(payload), attempt, status, errStr)

		if sendErr == nil && status >= 200 && status < 300 {
			return
		}

		// Don't retry on 406 — the receiver is explicitly rejecting the payload.
		if sendErr == nil && status == http.StatusNotAcceptable {
			return
		}

		if attempt < maxAttempts {
			time.Sleep(n.retryDelays[attempt-1])
		}
	}
}

func (n *Notifier) send(url, secret, msgID string, ts time.Time, payload []byte) (int, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", msgID)
	req.Header.Set("webhook-timestamp", fmt.Sprintf("%d", ts.Unix()))

	if secret != "" {
		wh, err := standardwebhooks.NewWebhook(strings.TrimPrefix(secret, "whsec_"))
		if err != nil {
			return 0, fmt.Errorf("init webhook signer: %w", err)
		}
		sig, err := wh.Sign(msgID, ts, payload)
		if err != nil {
			return 0, fmt.Errorf("sign webhook: %w", err)
		}
		req.Header.Set("webhook-signature", sig)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	return resp.StatusCode, nil
}

func (n *Notifier) logDelivery(webhookID, event, site, url, payload string, attempt, status int, errStr string) {
	_, err := n.db.Exec(
		`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		webhookID, event, site, url, payload, attempt, status, errStr, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		log.Printf("webhook: log delivery: %v", err)
	}
}

// DeliveryTimeBucket represents a time bucket with succeeded/failed counts.
type DeliveryTimeBucket struct {
	Time      string `json:"time"`
	Succeeded int64  `json:"succeeded"`
	Failed    int64  `json:"failed"`
}

// EventCount represents an event type with its delivery count.
type EventCount struct {
	Event string `json:"event"`
	Count int64  `json:"count"`
}

// deliveryBucketSQL is the SQL expression that truncates created_at to a bucket
// boundary using epoch-based rounding. It requires two int parameters: the
// step size in seconds, passed twice.
const deliveryBucketSQL = `strftime('%Y-%m-%dT%H:%M:%SZ', (CAST(strftime('%s', created_at) AS INTEGER) / ?) * ?, 'unixepoch')`

// deliveryBucketStep returns the largest "nice" step that produces at least 64 buckets.
func deliveryBucketStep(from, to time.Time) time.Duration {
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

// fillDeliveryBuckets takes sparse SQL results and returns a complete series
// with zero-filled gaps from `from` to `to`.
func fillDeliveryBuckets(sparse []DeliveryTimeBucket, from, to time.Time) []DeliveryTimeBucket {
	if from.IsZero() && len(sparse) > 0 {
		t, err := time.Parse(time.RFC3339, sparse[0].Time)
		if err == nil {
			from = t
		}
	}
	if from.IsZero() {
		return sparse
	}

	step := deliveryBucketStep(from, to)
	from = from.UTC().Truncate(step)

	type pair struct{ succeeded, failed int64 }
	lookup := make(map[string]pair, len(sparse))
	for _, b := range sparse {
		lookup[b.Time] = pair{b.Succeeded, b.Failed}
	}

	var out []DeliveryTimeBucket
	for t := from; !t.After(to.UTC()); t = t.Add(step) {
		key := t.Format(time.RFC3339)
		p := lookup[key]
		out = append(out, DeliveryTimeBucket{Time: key, Succeeded: p.succeeded, Failed: p.failed})
	}
	return out
}

// DeliveryStats returns aggregate counts for webhook deliveries.
func (n *Notifier) DeliveryStats(site string, from, to time.Time) (total, succeeded, failed int64, err error) {
	var whereConds []string
	var args []any
	if site != "" {
		whereConds = append(whereConds, "site = ?")
		args = append(args, site)
	}
	if !from.IsZero() {
		whereConds = append(whereConds, "created_at >= ?")
		args = append(args, from.UTC().Format(time.RFC3339))
	}
	whereConds = append(whereConds, "created_at <= ?")
	args = append(args, to.UTC().Format(time.RFC3339))

	whereClause := "WHERE " + strings.Join(whereConds, " AND ")

	query := fmt.Sprintf(`SELECT COUNT(*),
		SUM(CASE WHEN succeeded = 1 THEN 1 ELSE 0 END),
		SUM(CASE WHEN succeeded = 0 THEN 1 ELSE 0 END)
		FROM (
			SELECT webhook_id,
				MAX(CASE WHEN status BETWEEN 200 AND 299 THEN 1 ELSE 0 END) AS succeeded
			FROM webhook_deliveries %s GROUP BY webhook_id
		)`, whereClause)

	err = n.db.QueryRow(query, args...).Scan(&total, &succeeded, &failed)
	return
}

// DeliveriesOverTime returns time-bucketed delivery counts.
func (n *Notifier) DeliveriesOverTime(site string, from, to time.Time) ([]DeliveryTimeBucket, error) {
	stepSecs := int(deliveryBucketStep(from, to).Seconds())

	var whereConds []string
	var args []any
	// bucket step args come first
	args = append(args, stepSecs, stepSecs)
	if site != "" {
		whereConds = append(whereConds, "site = ?")
		args = append(args, site)
	}
	if !from.IsZero() {
		whereConds = append(whereConds, "created_at >= ?")
		args = append(args, from.UTC().Format(time.RFC3339))
	}
	whereConds = append(whereConds, "created_at <= ?")
	args = append(args, to.UTC().Format(time.RFC3339))

	whereClause := "WHERE " + strings.Join(whereConds, " AND ")

	query := fmt.Sprintf(`SELECT bucket,
		SUM(CASE WHEN succeeded = 1 THEN 1 ELSE 0 END),
		SUM(CASE WHEN succeeded = 0 THEN 1 ELSE 0 END)
		FROM (
			SELECT webhook_id,
				MIN(%s) AS bucket,
				MAX(CASE WHEN status BETWEEN 200 AND 299 THEN 1 ELSE 0 END) AS succeeded
			FROM webhook_deliveries %s GROUP BY webhook_id
		) GROUP BY bucket ORDER BY bucket`, deliveryBucketSQL, whereClause)

	rows, err := n.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("deliveries over time: %w", err)
	}
	defer rows.Close()

	var buckets []DeliveryTimeBucket
	for rows.Next() {
		var b DeliveryTimeBucket
		if err := rows.Scan(&b.Time, &b.Succeeded, &b.Failed); err != nil {
			return nil, fmt.Errorf("scan bucket: %w", err)
		}
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate buckets: %w", err)
	}

	return fillDeliveryBuckets(buckets, from, to), nil
}

// EventBreakdown returns delivery counts grouped by event type.
func (n *Notifier) EventBreakdown(site string, from, to time.Time) ([]EventCount, error) {
	var whereConds []string
	var args []any
	if site != "" {
		whereConds = append(whereConds, "site = ?")
		args = append(args, site)
	}
	if !from.IsZero() {
		whereConds = append(whereConds, "created_at >= ?")
		args = append(args, from.UTC().Format(time.RFC3339))
	}
	whereConds = append(whereConds, "created_at <= ?")
	args = append(args, to.UTC().Format(time.RFC3339))

	whereClause := "WHERE " + strings.Join(whereConds, " AND ")

	query := fmt.Sprintf(`SELECT event, COUNT(DISTINCT webhook_id)
		FROM webhook_deliveries %s GROUP BY event ORDER BY COUNT(DISTINCT webhook_id) DESC`, whereClause)

	rows, err := n.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("event breakdown: %w", err)
	}
	defer rows.Close()

	var events []EventCount
	for rows.Next() {
		var e EventCount
		if err := rows.Scan(&e.Event, &e.Count); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}

	return events, nil
}

// DeliverySummary represents a grouped webhook delivery (one row per webhook_id).
type DeliverySummary struct {
	WebhookID    string `json:"webhook_id"`
	Event        string `json:"event"`
	Site         string `json:"site"`
	URL          string `json:"url"`
	Attempts     int    `json:"attempts"`
	Succeeded    bool   `json:"succeeded"`
	FirstAttempt string `json:"first_attempt"`
	LastAttempt  string `json:"last_attempt"`
}

// DeliveryAttempt represents a single delivery attempt.
type DeliveryAttempt struct {
	Attempt   int    `json:"attempt"`
	Status    int    `json:"status"`
	Error     string `json:"error"`
	CreatedAt string `json:"created_at"`
	Payload   string `json:"payload"`
}

// ListDeliveries returns grouped webhook deliveries with optional filters.
// It returns the page of results, the total count, and any error.
func (n *Notifier) ListDeliveries(site, event, status string, limit, offset int) ([]DeliverySummary, int, error) {
	var whereConds []string
	var args []any

	if site != "" {
		whereConds = append(whereConds, "site = ?")
		args = append(args, site)
	}
	if event != "" {
		whereConds = append(whereConds, "event = ?")
		args = append(args, event)
	}

	whereClause := ""
	if len(whereConds) > 0 {
		whereClause = "WHERE " + strings.Join(whereConds, " AND ")
	}

	havingClause := ""
	switch status {
	case "succeeded":
		havingClause = "HAVING succeeded = 1"
	case "failed":
		havingClause = "HAVING succeeded = 0"
	}

	innerQuery := fmt.Sprintf(`SELECT webhook_id, event, site, url,
		MAX(attempt) as attempts,
		MAX(CASE WHEN status BETWEEN 200 AND 299 THEN 1 ELSE 0 END) as succeeded,
		MIN(created_at) as first_attempt,
		MAX(created_at) as last_attempt
		FROM webhook_deliveries
		%s
		GROUP BY webhook_id
		%s`, whereClause, havingClause)

	// Get total count.
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM (%s)", innerQuery)
	var total int
	if err := n.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count deliveries: %w", err)
	}

	// Get page of results.
	pageQuery := fmt.Sprintf("%s ORDER BY first_attempt DESC LIMIT ? OFFSET ?", innerQuery)
	pageArgs := append(append([]any{}, args...), limit, offset)

	rows, err := n.db.Query(pageQuery, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list deliveries: %w", err)
	}
	defer rows.Close()

	var deliveries []DeliverySummary
	for rows.Next() {
		var d DeliverySummary
		if err := rows.Scan(&d.WebhookID, &d.Event, &d.Site, &d.URL, &d.Attempts, &d.Succeeded, &d.FirstAttempt, &d.LastAttempt); err != nil {
			return nil, 0, fmt.Errorf("scan delivery: %w", err)
		}
		deliveries = append(deliveries, d)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate deliveries: %w", err)
	}

	return deliveries, total, nil
}

// GetDeliveryAttempts returns all attempts for a given webhook ID, ordered by attempt number.
func (n *Notifier) GetDeliveryAttempts(webhookID string) ([]DeliveryAttempt, error) {
	rows, err := n.db.Query(
		`SELECT attempt, status, error, created_at, payload
		 FROM webhook_deliveries WHERE webhook_id = ? ORDER BY attempt`,
		webhookID,
	)
	if err != nil {
		return nil, fmt.Errorf("get delivery attempts: %w", err)
	}
	defer rows.Close()

	var attempts []DeliveryAttempt
	for rows.Next() {
		var a DeliveryAttempt
		if err := rows.Scan(&a.Attempt, &a.Status, &a.Error, &a.CreatedAt, &a.Payload); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		attempts = append(attempts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate attempts: %w", err)
	}

	return attempts, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func newSafeClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return nil
			}
			if isPrivateIP(ip) {
				return fmt.Errorf("webhook: refusing to connect to private address %s", ip)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
	}
}

func isPrivateIP(ip net.IP) bool {
	privateRanges := []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"100.64.0.0/10",
		"169.254.0.0/16",
		"::1/128",
		"fe80::/10",
		"fc00::/7",
	}
	for _, cidr := range privateRanges {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
