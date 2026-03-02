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

func TestDeleteSubmission(t *testing.T) {
	r := newTestRecorder(t)

	if err := r.RecordSubmission(Submission{
		ID: "del1", Site: "docs", Path: "/a", Fields: json.RawMessage(`{}`), CreatedAt: "2026-02-24T10:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	if err := r.DeleteSubmission("docs", "del1"); err != nil {
		t.Fatal(err)
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
