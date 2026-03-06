package webhooks

import (
	"fmt"
	"strings"
	"time"

	"tspages/internal/timeseries"
)

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

var deliveryBucketSQL = timeseries.BucketSQL("created_at")

// deliveryFilter builds a WHERE clause and args for webhook delivery queries.
// Extra conditions (e.g. "duration_ms > 0") can be appended via extra.
func deliveryFilter(site string, from, to time.Time, extra ...string) (string, []any) {
	var conds []string
	var args []any
	if site != "" {
		conds = append(conds, "site = ?")
		args = append(args, site)
	}
	if !from.IsZero() {
		conds = append(conds, "created_at >= ?")
		args = append(args, from.UTC().Format(time.RFC3339))
	}
	conds = append(conds, "created_at <= ?")
	args = append(args, to.UTC().Format(time.RFC3339))
	conds = append(conds, extra...)
	return "WHERE " + strings.Join(conds, " AND "), args
}

// DeliveryStats returns aggregate counts for webhook deliveries.
func (n *Notifier) DeliveryStats(site string, from, to time.Time) (total, succeeded, failed int64, err error) {
	whereClause, args := deliveryFilter(site, from, to)

	query := fmt.Sprintf(`SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN succeeded = 1 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN succeeded = 0 THEN 1 ELSE 0 END), 0)
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
	stepSecs := int(timeseries.BucketStep(from, to).Seconds())
	whereClause, filterArgs := deliveryFilter(site, from, to)
	// bucket step args come first, then filter args
	args := append([]any{stepSecs, stepSecs}, filterArgs...)

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

	return timeseries.FillBuckets(buckets, from, to,
		func(b DeliveryTimeBucket) string { return b.Time },
		func(t string) DeliveryTimeBucket { return DeliveryTimeBucket{Time: t} },
	), nil
}

// LatencyStats holds aggregate latency values across a time range.
type LatencyStats struct {
	Min float64 `json:"min"`
	Avg float64 `json:"avg"`
	P95 float64 `json:"p95"`
	Max float64 `json:"max"`
}

// LatencyStats returns min/avg/p95/max latency in ms for the given time range.
func (n *Notifier) LatencyStats(site string, from, to time.Time) (LatencyStats, error) {
	whereClause, args := deliveryFilter(site, from, to, "duration_ms > 0")

	var s LatencyStats
	err := n.db.QueryRow(fmt.Sprintf(
		`SELECT COALESCE(MIN(duration_ms), 0), COALESCE(AVG(duration_ms), 0), COALESCE(MAX(duration_ms), 0)
		 FROM webhook_deliveries %s`, whereClause), args...,
	).Scan(&s.Min, &s.Avg, &s.Max)
	if err != nil {
		return s, fmt.Errorf("latency stats: %w", err)
	}
	// Approximate p95: use the value at the 95th percentile rank.
	var p95 float64
	err = n.db.QueryRow(fmt.Sprintf(
		`SELECT COALESCE(duration_ms, 0) FROM webhook_deliveries %s
		 ORDER BY duration_ms ASC LIMIT 1 OFFSET (
			SELECT CAST(COUNT(*) * 0.95 AS INTEGER) FROM webhook_deliveries %s
		 )`, whereClause, whereClause), append(append([]any{}, args...), args...)...,
	).Scan(&p95)
	if err == nil {
		s.P95 = p95
	} else {
		s.P95 = s.Max
	}
	return s, nil
}

// LatencyTimeBucket represents a time bucket with avg/p95/max latency in ms.
type LatencyTimeBucket struct {
	Time string  `json:"time"`
	Avg  float64 `json:"avg"`
	P95  float64 `json:"p95"`
	Max  float64 `json:"max"`
}

// LatencyOverTime returns time-bucketed latency percentiles.
func (n *Notifier) LatencyOverTime(site string, from, to time.Time) ([]LatencyTimeBucket, error) {
	stepSecs := int(timeseries.BucketStep(from, to).Seconds())
	whereClause, filterArgs := deliveryFilter(site, from, to, "duration_ms > 0")
	args := append([]any{stepSecs, stepSecs}, filterArgs...)

	// Use a CTE with PERCENT_RANK to compute real per-bucket p95.
	query := fmt.Sprintf(`WITH bucketed AS (
			SELECT %s AS bucket, duration_ms
			FROM webhook_deliveries %s
		),
		ranked AS (
			SELECT bucket, duration_ms,
				PERCENT_RANK() OVER (PARTITION BY bucket ORDER BY duration_ms) AS pct_rank
			FROM bucketed
		)
		SELECT bucket,
			CAST(AVG(duration_ms) AS REAL) AS avg_ms,
			CAST(COALESCE(MIN(CASE WHEN pct_rank >= 0.95 THEN duration_ms END), MAX(duration_ms)) AS REAL) AS p95_ms,
			CAST(MAX(duration_ms) AS REAL) AS max_ms
		FROM ranked
		GROUP BY bucket ORDER BY bucket`, deliveryBucketSQL, whereClause)

	rows, err := n.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("latency over time: %w", err)
	}
	defer rows.Close()

	var buckets []LatencyTimeBucket
	for rows.Next() {
		var b LatencyTimeBucket
		if err := rows.Scan(&b.Time, &b.Avg, &b.P95, &b.Max); err != nil {
			return nil, fmt.Errorf("scan latency bucket: %w", err)
		}
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latency buckets: %w", err)
	}

	return buckets, nil
}

// EventBreakdown returns delivery counts grouped by event type.
func (n *Notifier) EventBreakdown(site string, from, to time.Time) ([]EventCount, error) {
	whereClause, args := deliveryFilter(site, from, to)

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
	Signed       bool   `json:"signed"`
	FirstAttempt string `json:"first_attempt"`
	LastAttempt  string `json:"last_attempt"`
}

// DeliveryAttempt represents a single delivery attempt.
type DeliveryAttempt struct {
	Attempt    int    `json:"attempt"`
	Status     int    `json:"status"`
	Error      string `json:"error"`
	CreatedAt  string `json:"created_at"`
	Payload    string `json:"payload"`
	DurationMs int64  `json:"duration_ms"`
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
		MAX(signed) as signed,
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
		if err := rows.Scan(&d.WebhookID, &d.Event, &d.Site, &d.URL, &d.Attempts, &d.Succeeded, &d.Signed, &d.FirstAttempt, &d.LastAttempt); err != nil {
			return nil, 0, fmt.Errorf("scan delivery: %w", err)
		}
		deliveries = append(deliveries, d)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate deliveries: %w", err)
	}

	return deliveries, total, nil
}

// GetDelivery returns the summary for a single webhook delivery.
func (n *Notifier) GetDelivery(webhookID string) (DeliverySummary, error) {
	var d DeliverySummary
	err := n.db.QueryRow(
		`SELECT webhook_id, event, site, url,
			MAX(attempt) as attempts,
			MAX(CASE WHEN status BETWEEN 200 AND 299 THEN 1 ELSE 0 END) as succeeded,
			MAX(signed) as signed,
			MIN(created_at) as first_attempt,
			MAX(created_at) as last_attempt
		 FROM webhook_deliveries WHERE webhook_id = ? GROUP BY webhook_id`,
		webhookID,
	).Scan(&d.WebhookID, &d.Event, &d.Site, &d.URL, &d.Attempts, &d.Succeeded, &d.Signed, &d.FirstAttempt, &d.LastAttempt)
	if err != nil {
		return d, fmt.Errorf("get delivery: %w", err)
	}
	return d, nil
}

// GetDeliveryAttempts returns all attempts for a given webhook ID, ordered by attempt number.
func (n *Notifier) GetDeliveryAttempts(webhookID string) ([]DeliveryAttempt, error) {
	rows, err := n.db.Query(
		`SELECT attempt, status, error, created_at, payload, duration_ms
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
		if err := rows.Scan(&a.Attempt, &a.Status, &a.Error, &a.CreatedAt, &a.Payload, &a.DurationMs); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		attempts = append(attempts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate attempts: %w", err)
	}

	return attempts, nil
}
