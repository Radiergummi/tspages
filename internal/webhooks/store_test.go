package webhooks

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tspages/internal/storage"
)

func TestNotifier_ListDeliveries(t *testing.T) {
	n, _ := testNotifier(t)

	srv := newTestServer(200)
	defer srv.Close()

	// Fire two webhooks to different sites.
	n.Fire("deploy.success", "docs", storage.SiteConfig{WebhookURL: srv.URL}, map[string]any{"id": "1"})
	n.Fire("site.created", "blog", storage.SiteConfig{WebhookURL: srv.URL}, map[string]any{"id": "2"})

	time.Sleep(500 * time.Millisecond)

	// List all deliveries.
	deliveries, total, err := n.ListDeliveries("", "", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = deliveries
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}

	// Filter by site "docs".
	deliveries, total, err = n.ListDeliveries("docs", "", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = deliveries
	if total != 1 {
		t.Fatalf("total by site = %d, want 1", total)
	}

	// Filter by event "site.created".
	deliveries, total, err = n.ListDeliveries("", "site.created", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = deliveries
	if total != 1 {
		t.Fatalf("total by event = %d, want 1", total)
	}
}

func TestNotifier_ListDeliveries_StatusFilter(t *testing.T) {
	n, db := testNotifier(t)

	// msg_1: 1 attempt, status 200 (succeeded).
	_, err := db.Exec(
		`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"msg_1", "deploy.success", "docs", "http://example.com", "{}", 1, 200, "", "2025-01-01T00:00:00Z",
	)
	if err != nil {
		t.Fatal(err)
	}

	// msg_2: 2 attempts, both status 500 (failed).
	for attempt := 1; attempt <= 2; attempt++ {
		_, err := db.Exec(
			`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"msg_2", "deploy.success", "docs", "http://example.com", "{}", attempt, 500, "server error", "2025-01-01T00:00:01Z",
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Filter succeeded.
	deliveries, total, err := n.ListDeliveries("", "", "succeeded", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = deliveries
	if total != 1 {
		t.Fatalf("succeeded total = %d, want 1", total)
	}

	// Filter failed.
	deliveries, total, err = n.ListDeliveries("", "", "failed", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = deliveries
	if total != 1 {
		t.Fatalf("failed total = %d, want 1", total)
	}
}

func TestNotifier_GetDeliveryAttempts(t *testing.T) {
	n, db := testNotifier(t)

	// Insert 2 rows for the same webhook_id.
	for attempt := 1; attempt <= 2; attempt++ {
		_, err := db.Exec(
			`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"msg_test", "deploy.success", "docs", "http://example.com", `{"v":1}`, attempt, 500+attempt, "", "2025-01-01T00:00:00Z",
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	attempts, err := n.GetDeliveryAttempts("msg_test")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 {
		t.Fatalf("got %d attempts, want 2", len(attempts))
	}
	if attempts[0].Attempt != 1 {
		t.Errorf("first attempt = %d, want 1", attempts[0].Attempt)
	}
	if attempts[1].Attempt != 2 {
		t.Errorf("second attempt = %d, want 2", attempts[1].Attempt)
	}
	if attempts[0].Status != 501 {
		t.Errorf("first status = %d, want 501", attempts[0].Status)
	}
	if attempts[1].Status != 502 {
		t.Errorf("second status = %d, want 502", attempts[1].Status)
	}
	if attempts[0].Payload != `{"v":1}` {
		t.Errorf("payload = %q, want {\"v\":1}", attempts[0].Payload)
	}
}

func TestDeliveryStats(t *testing.T) {
	n, db := testNotifier(t)

	// Insert test data: 2 succeeded (webhook_id msg_1, msg_2), 1 failed (msg_3).
	rows := []struct {
		id      string
		event   string
		site    string
		attempt int
		status  int
		ts      string
	}{
		{"msg_1", "deploy.success", "docs", 1, 200, "2025-06-01T10:00:00Z"},
		{"msg_2", "deploy.success", "blog", 1, 500, "2025-06-01T11:00:00Z"},
		{"msg_2", "deploy.success", "blog", 2, 200, "2025-06-01T11:01:00Z"},
		{"msg_3", "deploy.failed", "docs", 1, 500, "2025-06-01T12:00:00Z"},
		{"msg_3", "deploy.failed", "docs", 2, 500, "2025-06-01T12:01:00Z"},
	}
	for _, r := range rows {
		_, err := db.Exec(
			`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.id, r.event, r.site, "http://example.com", "{}", r.attempt, r.status, "", r.ts,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	from, _ := time.Parse(time.RFC3339, "2025-06-01T00:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2025-06-02T00:00:00Z")

	// All deliveries.
	total, succeeded, failed, err := n.DeliveryStats("", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if succeeded != 2 {
		t.Errorf("succeeded = %d, want 2", succeeded)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}

	// Filter by site.
	total, succeeded, failed, err = n.DeliveryStats("docs", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("docs total = %d, want 2", total)
	}
	if succeeded != 1 {
		t.Errorf("docs succeeded = %d, want 1", succeeded)
	}
	if failed != 1 {
		t.Errorf("docs failed = %d, want 1", failed)
	}

	// Time filter: only first hour.
	earlyTo, _ := time.Parse(time.RFC3339, "2025-06-01T10:30:00Z")
	total, _, _, err = n.DeliveryStats("", from, earlyTo)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("time-filtered total = %d, want 1", total)
	}
}

func TestDeliveriesOverTime(t *testing.T) {
	n, db := testNotifier(t)

	// Insert deliveries at known times.
	rows := []struct {
		id     string
		status int
		ts     string
	}{
		{"msg_1", 200, "2025-06-01T10:00:00Z"},
		{"msg_2", 500, "2025-06-01T10:05:00Z"},
		{"msg_3", 200, "2025-06-01T10:20:00Z"},
	}
	for _, r := range rows {
		_, err := db.Exec(
			`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.id, "deploy.success", "docs", "http://example.com", "{}", 1, r.status, "", r.ts,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	from, _ := time.Parse(time.RFC3339, "2025-06-01T10:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2025-06-01T12:00:00Z")

	buckets, err := n.DeliveriesOverTime("", from, to)
	if err != nil {
		t.Fatal(err)
	}

	// Should have gap-filled buckets (15-min step for 2h range).
	if len(buckets) == 0 {
		t.Fatal("expected non-empty buckets")
	}

	// Verify the first bucket has data.
	first := buckets[0]
	if first.Time != "2025-06-01T10:00:00Z" {
		t.Errorf("first bucket time = %s, want 2025-06-01T10:00:00Z", first.Time)
	}
	// msg_1 (succeeded) and msg_2 (failed) are in the first 15-min bucket.
	if first.Succeeded != 1 {
		t.Errorf("first bucket succeeded = %d, want 1", first.Succeeded)
	}
	if first.Failed != 1 {
		t.Errorf("first bucket failed = %d, want 1", first.Failed)
	}

	// Second bucket (10:15) should have msg_3 (succeeded).
	if len(buckets) > 1 && buckets[1].Succeeded != 1 {
		t.Errorf("second bucket succeeded = %d, want 1", buckets[1].Succeeded)
	}
}

func TestEventBreakdown(t *testing.T) {
	n, db := testNotifier(t)

	rows := []struct {
		id    string
		event string
	}{
		{"msg_1", "deploy.success"},
		{"msg_2", "deploy.success"},
		{"msg_3", "deploy.failed"},
		{"msg_4", "site.created"},
	}
	for _, r := range rows {
		_, err := db.Exec(
			`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.id, r.event, "docs", "http://example.com", "{}", 1, 200, "", "2025-06-01T10:00:00Z",
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	from, _ := time.Parse(time.RFC3339, "2025-06-01T00:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2025-06-02T00:00:00Z")

	events, err := n.EventBreakdown("", from, to)
	if err != nil {
		t.Fatal(err)
	}

	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}

	// Should be ordered by count DESC: deploy.success (2), then the rest (1 each).
	if events[0].Event != "deploy.success" || events[0].Count != 2 {
		t.Errorf("first event = %v, want deploy.success with count 2", events[0])
	}
}

func TestNotifier_LatencyStats(t *testing.T) {
	n, db := testNotifier(t)

	// Insert deliveries with known latencies.
	rows := []struct {
		id         string
		durationMs int64
		ts         string
	}{
		{"msg_l1", 10, "2025-06-01T10:00:00Z"},
		{"msg_l2", 50, "2025-06-01T10:05:00Z"},
		{"msg_l3", 100, "2025-06-01T10:10:00Z"},
		{"msg_l4", 200, "2025-06-01T10:15:00Z"},
		{"msg_l5", 500, "2025-06-01T10:20:00Z"},
	}
	for _, r := range rows {
		_, err := db.Exec(
			`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at, duration_ms)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.id, "deploy.success", "docs", "http://example.com", "{}", 1, 200, "", r.ts, r.durationMs,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	from, _ := time.Parse(time.RFC3339, "2025-06-01T00:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2025-06-02T00:00:00Z")

	stats, err := n.LatencyStats("", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Min != 10 {
		t.Errorf("min = %v, want 10", stats.Min)
	}
	if stats.Max != 500 {
		t.Errorf("max = %v, want 500", stats.Max)
	}
	if stats.P95 == 0 {
		t.Error("p95 should be nonzero")
	}
	if stats.Avg == 0 {
		t.Error("avg should be nonzero")
	}
}

func TestNotifier_LatencyOverTime(t *testing.T) {
	n, db := testNotifier(t)

	// Insert deliveries with known latencies spread across time.
	rows := []struct {
		id         string
		durationMs int64
		ts         string
	}{
		{"msg_lt1", 10, "2025-06-01T10:00:00Z"},
		{"msg_lt2", 50, "2025-06-01T10:05:00Z"},
		{"msg_lt3", 200, "2025-06-01T11:00:00Z"},
	}
	for _, r := range rows {
		_, err := db.Exec(
			`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at, duration_ms)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.id, "deploy.success", "docs", "http://example.com", "{}", 1, 200, "", r.ts, r.durationMs,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	from, _ := time.Parse(time.RFC3339, "2025-06-01T10:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2025-06-01T12:00:00Z")

	buckets, err := n.LatencyOverTime("", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) == 0 {
		t.Fatal("expected non-empty buckets")
	}
	// First bucket should have data from msg_lt1 and msg_lt2.
	if buckets[0].Max == 0 {
		t.Error("first bucket max should be nonzero")
	}
}

// newTestServer returns an httptest server that responds with the given status.
func newTestServer(status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
}
