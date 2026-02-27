package analytics

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRecorder_RecordAndClose(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	r, err := NewRecorder(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	r.Record(Event{
		Timestamp: time.Now(),
		Site:      "docs",
		Path:      "/index.html",
		Status:    200,
		UserLogin: "alice@example.com",
		UserName:  "Alice",
		OS:        "darwin",
	})

	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen to verify persistence.
	r2, err := NewRecorder(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()

	count, err := r2.TotalRequests("docs", time.Time{}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestRecorder_MultipleEvents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	r, err := NewRecorder(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		r.Record(Event{
			Timestamp: time.Now(),
			Site:      "docs",
			Path:      "/page",
			Status:    200,
		})
	}
	r.Record(Event{
		Timestamp: time.Now(),
		Site:      "other",
		Path:      "/",
		Status:    200,
	})

	r.Close()

	r2, _ := NewRecorder(dbPath)
	defer r2.Close()

	count, _ := r2.TotalRequests("docs", time.Time{}, time.Now().Add(time.Hour))
	if count != 10 {
		t.Errorf("docs count = %d, want 10", count)
	}
	count, _ = r2.TotalRequests("other", time.Time{}, time.Now().Add(time.Hour))
	if count != 1 {
		t.Errorf("other count = %d, want 1", count)
	}
}

func setupTestRecorder(t *testing.T) *Recorder {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	r, err := NewRecorder(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)
	events := []Event{
		{Timestamp: base, Site: "docs", Path: "/", Status: 200, UserLogin: "alice@example.com", UserName: "Alice", OS: "darwin", NodeName: "alice-mac.ts.net."},
		{Timestamp: base.Add(time.Hour), Site: "docs", Path: "/", Status: 200, UserLogin: "alice@example.com", UserName: "Alice", OS: "darwin", NodeName: "alice-mac.ts.net."},
		{Timestamp: base.Add(2 * time.Hour), Site: "docs", Path: "/about", Status: 200, UserLogin: "bob@example.com", UserName: "Bob", OS: "linux", NodeName: "bob-desktop.ts.net."},
		{Timestamp: base.Add(3 * time.Hour), Site: "docs", Path: "/about", Status: 404, UserLogin: "bob@example.com", UserName: "Bob", OS: "linux", NodeName: "bob-desktop.ts.net."},
	}
	for _, e := range events {
		r.Record(e)
	}
	r.Close()

	r2, err := NewRecorder(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r2.Close() })
	return r2
}

func TestRecorder_TotalRequests(t *testing.T) {
	r := setupTestRecorder(t)
	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)

	count, err := r.TotalRequests("docs", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Errorf("count = %d, want 4", count)
	}
}

func TestRecorder_UniqueVisitors(t *testing.T) {
	r := setupTestRecorder(t)
	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)

	count, err := r.UniqueVisitors("docs", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestRecorder_TopPages(t *testing.T) {
	r := setupTestRecorder(t)
	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)

	pages, err := r.TopPages("docs", from, to, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(pages))
	}
	if pages[0].Count != 2 {
		t.Errorf("top page count = %d, want 2", pages[0].Count)
	}
}

func TestRecorder_TopVisitors(t *testing.T) {
	r := setupTestRecorder(t)
	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)

	visitors, err := r.TopVisitors("docs", from, to, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(visitors) != 2 {
		t.Fatalf("got %d visitors, want 2", len(visitors))
	}
	if visitors[0].Count != 2 {
		t.Errorf("top visitor count = %d, want 2", visitors[0].Count)
	}
}

func TestRecorder_OSBreakdown(t *testing.T) {
	r := setupTestRecorder(t)
	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)

	breakdown, err := r.OSBreakdown("docs", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(breakdown) != 2 {
		t.Fatalf("got %d OSes, want 2", len(breakdown))
	}
}

func TestRecorder_NodeBreakdown(t *testing.T) {
	r := setupTestRecorder(t)
	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)

	nodes, err := r.NodeBreakdown("docs", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(nodes))
	}
}

func TestRecorder_RequestsOverTime(t *testing.T) {
	r := setupTestRecorder(t)
	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)

	buckets, err := r.RequestsOverTime("docs", from, to)
	if err != nil {
		t.Fatal(err)
	}
	// 24h range -> 15-min buckets, 0:00 to 0:00 next day = 97 slots
	if len(buckets) != 97 {
		t.Errorf("got %d buckets, want 97", len(buckets))
	}
	// Events at hours 10, 11, 12, 13 — verify non-zero counts
	var nonZero int
	for _, b := range buckets {
		if b.Count > 0 {
			nonZero++
		}
	}
	if nonZero != 4 {
		t.Errorf("got %d non-zero buckets, want 4", nonZero)
	}
}

func TestRecorder_RecordAfterClose(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	r, err := NewRecorder(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()

	// Record after Close must not panic.
	r.Record(Event{
		Timestamp: time.Now(),
		Site:      "docs",
		Path:      "/",
		Status:    200,
	})
}

func TestRecorder_UniquePages(t *testing.T) {
	r := setupTestRecorder(t)
	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)

	count, err := r.UniquePages("docs", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2 (/ and /about)", count)
	}
}

func TestRecorder_RequestsOverTimeByStatus(t *testing.T) {
	r := setupTestRecorder(t)
	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)

	buckets, err := r.RequestsOverTimeByStatus("docs", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) == 0 {
		t.Fatal("expected non-empty buckets")
	}
	// Verify at least one bucket has OK > 0 (3 of 4 events are 200).
	var totalOK, total4xx int64
	for _, b := range buckets {
		totalOK += b.OK
		total4xx += b.ClientErr
	}
	if totalOK != 3 {
		t.Errorf("totalOK = %d, want 3", totalOK)
	}
	if total4xx != 1 {
		t.Errorf("total4xx = %d, want 1", total4xx)
	}
}

func TestRecorder_StatusBreakdown(t *testing.T) {
	r := setupTestRecorder(t)
	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)

	breakdown, err := r.StatusBreakdown("docs", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(breakdown) != 2 {
		t.Fatalf("got %d categories, want 2 (2xx and 4xx)", len(breakdown))
	}
}

func TestRecorder_HourlyPattern(t *testing.T) {
	r := setupTestRecorder(t)
	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)

	hours, err := r.HourlyPattern("docs", from, to)
	if err != nil {
		t.Fatal(err)
	}
	// Events at hours 10, 11, 12, 13.
	if len(hours) != 4 {
		t.Fatalf("got %d hours, want 4", len(hours))
	}
}

func TestRecorder_MultiSiteQueries(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	r, err := NewRecorder(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)
	events := []Event{
		{Timestamp: base, Site: "docs", Path: "/", Status: 200, UserLogin: "alice@example.com", UserName: "Alice", OS: "darwin", NodeName: "alice-mac"},
		{Timestamp: base.Add(time.Hour), Site: "docs", Path: "/about", Status: 404, UserLogin: "bob@example.com", UserName: "Bob", OS: "linux", NodeName: "bob-box"},
		{Timestamp: base.Add(2 * time.Hour), Site: "demo", Path: "/", Status: 200, UserLogin: "alice@example.com", UserName: "Alice", OS: "darwin", NodeName: "alice-mac"},
	}
	for _, e := range events {
		r.Record(e)
	}
	r.Close()

	r2, err := NewRecorder(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()

	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)
	sites := []string{"docs", "demo"}

	total, err := r2.TotalRequestsMulti(sites, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("TotalRequestsMulti = %d, want 3", total)
	}

	visitors, err := r2.UniqueVisitorsMulti(sites, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if visitors != 2 {
		t.Errorf("UniqueVisitorsMulti = %d, want 2", visitors)
	}

	buckets, err := r2.RequestsOverTimeMulti(sites, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) == 0 {
		t.Error("RequestsOverTimeMulti returned empty")
	}

	statusBuckets, err := r2.RequestsOverTimeByStatusMulti(sites, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(statusBuckets) == 0 {
		t.Error("RequestsOverTimeByStatusMulti returned empty")
	}

	siteCounts, err := r2.SiteBreakdown(sites, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(siteCounts) != 2 {
		t.Errorf("SiteBreakdown got %d sites, want 2", len(siteCounts))
	}

	topV, err := r2.TopVisitorsMulti(sites, from, to, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(topV) != 2 {
		t.Errorf("TopVisitorsMulti got %d, want 2", len(topV))
	}

	statuses, err := r2.StatusBreakdownMulti(sites, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 {
		t.Errorf("StatusBreakdownMulti got %d categories, want 2", len(statuses))
	}

	hourly, err := r2.HourlyPatternMulti(sites, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(hourly) == 0 {
		t.Error("HourlyPatternMulti returned empty")
	}

	osB, err := r2.OSBreakdownMulti(sites, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(osB) != 2 {
		t.Errorf("OSBreakdownMulti got %d, want 2", len(osB))
	}

	nodes, err := r2.NodeBreakdownMulti(sites, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Errorf("NodeBreakdownMulti got %d, want 2", len(nodes))
	}
}

func TestRecorder_MultiSiteQueries_EmptySites(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	r, err := NewRecorder(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)

	total, err := r.TotalRequestsMulti(nil, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Errorf("TotalRequestsMulti(nil) = %d, want 0", total)
	}

	visitors, err := r.UniqueVisitorsMulti(nil, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if visitors != 0 {
		t.Errorf("UniqueVisitorsMulti(nil) = %d, want 0", visitors)
	}
}

func TestRecorder_PurgeSite(t *testing.T) {
	r := setupTestRecorder(t)

	deleted, err := r.PurgeSite("docs")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 4 {
		t.Errorf("deleted = %d, want 4", deleted)
	}

	count, _ := r.TotalRequests("docs", time.Time{}, time.Now().Add(time.Hour))
	if count != 0 {
		t.Errorf("count after purge = %d, want 0", count)
	}
}
