package analytics

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func newTestRecorder(t *testing.T) *Recorder {
	t.Helper()
	r, err := NewRecorder(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestRecordSubmission_And_ListSubmissions(t *testing.T) {
	r := newTestRecorder(t)

	s := Submission{
		ID:        "sub-1",
		Site:      "docs",
		Path:      "/contact",
		Fields:    json.RawMessage(`{"name":"Alice","email":"alice@example.com"}`),
		Submitter: "alice@example.com",
		Node:      "alice-mac",
		IP:        "100.64.0.1",
		CreatedAt: "2026-02-24T10:00:00Z",
	}
	if err := r.RecordSubmission(s); err != nil {
		t.Fatal(err)
	}

	subs, err := r.ListSubmissions("docs", ListSubmissionsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d submissions, want 1", len(subs))
	}
	if subs[0].ID != "sub-1" {
		t.Errorf("ID = %q, want %q", subs[0].ID, "sub-1")
	}
	if string(subs[0].Fields) != `{"name":"Alice","email":"alice@example.com"}` {
		t.Errorf("Fields = %s, want original JSON", subs[0].Fields)
	}
}

func TestGetSubmission(t *testing.T) {
	r := newTestRecorder(t)

	s := Submission{
		ID:        "sub-get",
		Site:      "docs",
		Path:      "/contact",
		Fields:    json.RawMessage(`{"msg":"hello"}`),
		Submitter: "bob@example.com",
		CreatedAt: "2026-02-24T10:00:00Z",
	}
	if err := r.RecordSubmission(s); err != nil {
		t.Fatal(err)
	}

	got, err := r.GetSubmission("docs", "sub-get")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "sub-get" {
		t.Errorf("ID = %q, want %q", got.ID, "sub-get")
	}
	if got.Submitter != "bob@example.com" {
		t.Errorf("Submitter = %q, want %q", got.Submitter, "bob@example.com")
	}

	// Wrong site should return error.
	_, err = r.GetSubmission("other", "sub-get")
	if err == nil {
		t.Error("expected error for wrong site, got nil")
	}
}

func TestListSubmissions_SiteIsolation(t *testing.T) {
	r := newTestRecorder(t)

	for _, s := range []Submission{
		{ID: "s1", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T10:00:00Z"},
		{ID: "s2", Site: "demo", Path: "/b", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T10:00:00Z"},
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}

	subs, err := r.ListSubmissions("docs", ListSubmissionsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("docs: got %d, want 1", len(subs))
	}
	if subs[0].ID != "s1" {
		t.Errorf("ID = %q, want s1", subs[0].ID)
	}
}

func TestListSubmissions_FilterByPath(t *testing.T) {
	r := newTestRecorder(t)

	for _, s := range []Submission{
		{ID: "p1", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T10:00:00Z"},
		{ID: "p2", Site: "docs", Path: "/feedback", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T11:00:00Z"},
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}

	subs, err := r.ListSubmissions("docs", ListSubmissionsOpts{Path: "/contact"})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d, want 1", len(subs))
	}
	if subs[0].ID != "p1" {
		t.Errorf("ID = %q, want p1", subs[0].ID)
	}
}

func TestListSubmissions_FilterBySinceUntil(t *testing.T) {
	r := newTestRecorder(t)

	for _, s := range []Submission{
		{ID: "t1", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T08:00:00Z"},
		{ID: "t2", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T12:00:00Z"},
		{ID: "t3", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T18:00:00Z"},
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}

	since := time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)
	until := time.Date(2026, 2, 24, 14, 0, 0, 0, time.UTC)
	subs, err := r.ListSubmissions("docs", ListSubmissionsOpts{Since: since, Until: until})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d, want 1", len(subs))
	}
	if subs[0].ID != "t2" {
		t.Errorf("ID = %q, want t2", subs[0].ID)
	}
}

func TestListSubmissions_LimitOffset(t *testing.T) {
	r := newTestRecorder(t)

	for _, s := range []Submission{
		{ID: "lo1", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T10:00:00Z"},
		{ID: "lo2", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T11:00:00Z"},
		{ID: "lo3", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T12:00:00Z"},
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}

	// Limit 2.
	subs, err := r.ListSubmissions("docs", ListSubmissionsOpts{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 {
		t.Fatalf("got %d, want 2", len(subs))
	}

	// Offset 1, Limit 2 — should get 2 items.
	subs, err = r.ListSubmissions("docs", ListSubmissionsOpts{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 {
		t.Fatalf("got %d, want 2", len(subs))
	}
}

func TestListSubmissions_OrderDescending(t *testing.T) {
	r := newTestRecorder(t)

	for _, s := range []Submission{
		{ID: "old", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T08:00:00Z"},
		{ID: "new", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T12:00:00Z"},
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}

	subs, err := r.ListSubmissions("docs", ListSubmissionsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 {
		t.Fatalf("got %d, want 2", len(subs))
	}
	if subs[0].ID != "new" {
		t.Errorf("first = %q, want newest first (new)", subs[0].ID)
	}
	if subs[1].ID != "old" {
		t.Errorf("second = %q, want oldest last (old)", subs[1].ID)
	}
}

func TestListSubmissions_SortByDate(t *testing.T) {
	r := newTestRecorder(t)

	for _, s := range []Submission{
		{ID: "old", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T08:00:00Z"},
		{ID: "new", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T12:00:00Z"},
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}

	// Date ASC.
	subs, err := r.ListSubmissions("docs", ListSubmissionsOpts{Sort: []SortField{{Column: "date"}}})
	if err != nil {
		t.Fatal(err)
	}
	if subs[0].ID != "old" {
		t.Errorf("date ASC: first = %q, want old", subs[0].ID)
	}

	// Date DESC.
	subs, err = r.ListSubmissions("docs", ListSubmissionsOpts{Sort: []SortField{{Column: "date", Desc: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if subs[0].ID != "new" {
		t.Errorf("date DESC: first = %q, want new", subs[0].ID)
	}
}

func TestListSubmissions_SortBySubmitter(t *testing.T) {
	r := newTestRecorder(t)

	for _, s := range []Submission{
		{ID: "s1", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), Submitter: "zara@example.com", CreatedAt: "2026-02-24T10:00:00Z"},
		{ID: "s2", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), Submitter: "alice@example.com", CreatedAt: "2026-02-24T11:00:00Z"},
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}

	// Submitter ASC.
	subs, err := r.ListSubmissions("docs", ListSubmissionsOpts{Sort: []SortField{{Column: "submitter"}}})
	if err != nil {
		t.Fatal(err)
	}
	if subs[0].ID != "s2" {
		t.Errorf("submitter ASC: first = %q, want s2 (alice)", subs[0].ID)
	}

	// Submitter DESC.
	subs, err = r.ListSubmissions("docs", ListSubmissionsOpts{Sort: []SortField{{Column: "submitter", Desc: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if subs[0].ID != "s1" {
		t.Errorf("submitter DESC: first = %q, want s1 (zara)", subs[0].ID)
	}
}

func TestListSubmissions_SortByFieldKey(t *testing.T) {
	r := newTestRecorder(t)

	for _, s := range []Submission{
		{ID: "s1", Site: "docs", Path: "/a", Fields: json.RawMessage(`{"email":"zara@test.com"}`), CreatedAt: "2026-02-24T10:00:00Z"},
		{ID: "s2", Site: "docs", Path: "/a", Fields: json.RawMessage(`{"email":"alice@test.com"}`), CreatedAt: "2026-02-24T11:00:00Z"},
		{ID: "s3", Site: "docs", Path: "/a", Fields: json.RawMessage(`{"email":"mike@test.com"}`), CreatedAt: "2026-02-24T12:00:00Z"},
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}

	// Sort by email ASC.
	subs, err := r.ListSubmissions("docs", ListSubmissionsOpts{Sort: []SortField{{Column: "email"}}})
	if err != nil {
		t.Fatal(err)
	}
	if subs[0].ID != "s2" {
		t.Errorf("email ASC: first = %q, want s2 (alice)", subs[0].ID)
	}
	if subs[2].ID != "s1" {
		t.Errorf("email ASC: last = %q, want s1 (zara)", subs[2].ID)
	}

	// Sort by email DESC.
	subs, err = r.ListSubmissions("docs", ListSubmissionsOpts{Sort: []SortField{{Column: "email", Desc: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if subs[0].ID != "s1" {
		t.Errorf("email DESC: first = %q, want s1 (zara)", subs[0].ID)
	}
}

func TestListSubmissions_MultiSort(t *testing.T) {
	r := newTestRecorder(t)

	for _, s := range []Submission{
		{ID: "ms1", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), Submitter: "alice@example.com", CreatedAt: "2026-02-24T10:00:00Z"},
		{ID: "ms2", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), Submitter: "alice@example.com", CreatedAt: "2026-02-24T12:00:00Z"},
		{ID: "ms3", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), Submitter: "bob@example.com", CreatedAt: "2026-02-24T11:00:00Z"},
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}

	// Primary: submitter ASC, secondary: date DESC.
	// alice (ms2 12:00, ms1 10:00), then bob (ms3 11:00).
	subs, err := r.ListSubmissions("docs", ListSubmissionsOpts{
		Sort: []SortField{
			{Column: "submitter"},
			{Column: "date", Desc: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 3 {
		t.Fatalf("got %d, want 3", len(subs))
	}
	want := []string{"ms2", "ms1", "ms3"}
	for i, w := range want {
		if subs[i].ID != w {
			t.Errorf("subs[%d].ID = %q, want %q", i, subs[i].ID, w)
		}
	}
}

func TestDeleteSubmission(t *testing.T) {
	r := newTestRecorder(t)

	if err := r.RecordSubmission(Submission{
		ID: "del1", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T10:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	deleted, err := r.DeleteSubmission("docs", "del1")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Error("expected deleted=true")
	}

	subs, err := r.ListSubmissions("docs", ListSubmissionsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 0 {
		t.Errorf("got %d after delete, want 0", len(subs))
	}
}

func TestBulkDeleteSubmissions(t *testing.T) {
	r := newTestRecorder(t)

	for _, s := range []Submission{
		{ID: "bd1", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-20T10:00:00Z"},
		{ID: "bd2", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-22T10:00:00Z"},
		{ID: "bd3", Site: "docs", Path: "/feedback", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-20T10:00:00Z"},
		{ID: "bd4", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-25T10:00:00Z"},
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}

	before := time.Date(2026, 2, 23, 0, 0, 0, 0, time.UTC)
	deleted, err := r.BulkDeleteSubmissions("docs", BulkDeleteOpts{Path: "/contact", Before: before})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	// bd3 (feedback, old) and bd4 (contact, new) should remain.
	subs, err := r.ListSubmissions("docs", ListSubmissionsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 {
		t.Fatalf("remaining = %d, want 2", len(subs))
	}
}

func TestFormPaths(t *testing.T) {
	r := newTestRecorder(t)

	for _, s := range []Submission{
		{ID: "fp1", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T10:00:00Z"},
		{ID: "fp2", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T11:00:00Z"},
		{ID: "fp3", Site: "docs", Path: "/feedback", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T12:00:00Z"},
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}

	paths, err := r.FormPaths("docs")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("got %d paths, want 2", len(paths))
	}
	// Ordered by count DESC: /contact (2), /feedback (1).
	if paths[0].Path != "/contact" || paths[0].Count != 2 {
		t.Errorf("paths[0] = %+v, want /contact with count 2", paths[0])
	}
	if paths[1].Path != "/feedback" || paths[1].Count != 1 {
		t.Errorf("paths[1] = %+v, want /feedback with count 1", paths[1])
	}
}

func TestRecorder_SubmissionsOverTime(t *testing.T) {
	r := newTestRecorder(t)

	// Insert submissions across several hours and two paths.
	for _, s := range []Submission{
		{ID: "ot1", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T08:00:00Z"},
		{ID: "ot2", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T08:05:00Z"},
		{ID: "ot3", Site: "docs", Path: "/feedback", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T10:00:00Z"},
		{ID: "ot4", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T12:00:00Z"},
		{ID: "ot5", Site: "other", Path: "/contact", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T08:00:00Z"},
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}

	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)

	// All paths for "docs" — should count ot1..ot4 (4 total).
	buckets, err := r.SubmissionsOverTime("docs", "", from, to)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, b := range buckets {
		total += b.Count
	}
	if total != 4 {
		t.Errorf("total count = %d, want 4", total)
	}

	// Filter to /contact only — should count ot1, ot2, ot4 (3 total).
	buckets, err = r.SubmissionsOverTime("docs", "/contact", from, to)
	if err != nil {
		t.Fatal(err)
	}
	total = 0
	for _, b := range buckets {
		total += b.Count
	}
	if total != 3 {
		t.Errorf("path-filtered total = %d, want 3", total)
	}

	// Buckets should be contiguous (FillBuckets fills gaps).
	if len(buckets) < 2 {
		t.Fatalf("expected multiple buckets, got %d", len(buckets))
	}
	for i := 1; i < len(buckets); i++ {
		if buckets[i].Time <= buckets[i-1].Time {
			t.Errorf("buckets not ascending at index %d: %s <= %s", i, buckets[i].Time, buckets[i-1].Time)
		}
	}
}

func TestRecorder_UniqueSubmitters(t *testing.T) {
	r := newTestRecorder(t)

	for _, s := range []Submission{
		{ID: "us1", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), Submitter: "alice@example.com", CreatedAt: "2026-02-24T08:00:00Z"},
		{ID: "us2", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), Submitter: "bob@example.com", CreatedAt: "2026-02-24T09:00:00Z"},
		{ID: "us3", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), Submitter: "alice@example.com", CreatedAt: "2026-02-24T10:00:00Z"}, // duplicate
		{ID: "us4", Site: "docs", Path: "/feedback", Fields: json.RawMessage(`{}`), Submitter: "carol@example.com", CreatedAt: "2026-02-24T11:00:00Z"},
		{ID: "us5", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), Submitter: "", CreatedAt: "2026-02-24T12:00:00Z"},               // empty submitter, excluded
		{ID: "us6", Site: "other", Path: "/contact", Fields: json.RawMessage(`{}`), Submitter: "dave@example.com", CreatedAt: "2026-02-24T08:00:00Z"}, // different site
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}

	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)

	// All paths: alice, bob, carol = 3.
	count, err := r.UniqueSubmitters("docs", "", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("all paths: got %d, want 3", count)
	}

	// /contact only: alice, bob = 2.
	count, err = r.UniqueSubmitters("docs", "/contact", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("/contact: got %d, want 2", count)
	}
}

func TestRecorder_HourlySubmissionPattern(t *testing.T) {
	r := newTestRecorder(t)

	for _, s := range []Submission{
		{ID: "hp1", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T08:00:00Z"},
		{ID: "hp2", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T08:30:00Z"},
		{ID: "hp3", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T14:00:00Z"},
		{ID: "hp4", Site: "other", Path: "/contact", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T08:00:00Z"}, // different site
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}

	from := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC)

	hours, err := r.HourlySubmissionPattern("docs", "", from, to)
	if err != nil {
		t.Fatal(err)
	}

	// Build a map for easy lookup.
	m := make(map[int]int64)
	for _, h := range hours {
		m[h.Hour] = h.Count
	}

	if m[8] != 2 {
		t.Errorf("hour 8: got %d, want 2", m[8])
	}
	if m[14] != 1 {
		t.Errorf("hour 14: got %d, want 1", m[14])
	}
}

func TestPurgeFormSubmissions(t *testing.T) {
	r := newTestRecorder(t)

	for _, s := range []Submission{
		{ID: "pg1", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T10:00:00Z"},
		{ID: "pg2", Site: "docs", Path: "/b", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T11:00:00Z"},
		{ID: "pg3", Site: "demo", Path: "/c", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T12:00:00Z"},
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := r.PurgeFormSubmissions("docs")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	// docs should be empty.
	subs, _ := r.ListSubmissions("docs", ListSubmissionsOpts{})
	if len(subs) != 0 {
		t.Errorf("docs remaining = %d, want 0", len(subs))
	}

	// demo should be untouched.
	subs, _ = r.ListSubmissions("demo", ListSubmissionsOpts{})
	if len(subs) != 1 {
		t.Errorf("demo remaining = %d, want 1", len(subs))
	}
}
