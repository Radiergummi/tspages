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
