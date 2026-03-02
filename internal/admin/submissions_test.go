package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"tspages/internal/analytics"
	"tspages/internal/auth"

	_ "modernc.org/sqlite"
)

func newSubmissionsRecorder(t *testing.T) *analytics.Recorder {
	t.Helper()
	r, err := analytics.NewRecorder(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func seedSubmissions(t *testing.T, r *analytics.Recorder) {
	t.Helper()
	for _, s := range []analytics.Submission{
		{ID: "s1", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{"name":"Alice","email":"alice@example.com"}`), Submitter: "alice@ts", Node: "alice-mac", IP: "100.64.0.1", CreatedAt: "2026-02-24T10:00:00Z"},
		{ID: "s2", Site: "docs", Path: "/feedback", Fields: json.RawMessage(`{"rating":"5"}`), Submitter: "bob@ts", Node: "bob-pc", IP: "100.64.0.2", CreatedAt: "2026-02-24T12:00:00Z"},
		{ID: "s3", Site: "docs", Path: "/contact", Fields: json.RawMessage(`{"name":"Charlie"}`), Submitter: "charlie@ts", Node: "charlie-mac", IP: "100.64.0.3", CreatedAt: "2026-02-24T14:00:00Z"},
	} {
		if err := r.RecordSubmission(s); err != nil {
			t.Fatal(err)
		}
	}
}

// Use package-level adminCaps, adminID, viewerCaps, viewerID from handler_test.go.

func TestSubmissions_ListJSON(t *testing.T) {
	rec := newSubmissionsRecorder(t)
	seedSubmissions(t, rec)

	h := &SubmissionsHandler{handlerDeps{recorder: rec}}
	r := reqWithAuth("GET", "/sites/docs/submissions.json", adminCaps, adminID)
	r.SetPathValue("site", "docs")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var subs []analytics.Submission
	if err := json.NewDecoder(w.Body).Decode(&subs); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(subs) != 3 {
		t.Fatalf("got %d submissions, want 3", len(subs))
	}
	// Newest first.
	if subs[0].ID != "s3" {
		t.Errorf("first = %q, want s3", subs[0].ID)
	}
}

func TestSubmissions_ListPathFilter(t *testing.T) {
	rec := newSubmissionsRecorder(t)
	seedSubmissions(t, rec)

	h := &SubmissionsHandler{handlerDeps{recorder: rec}}
	r := reqWithAuth("GET", "/sites/docs/submissions.json?path=/contact", adminCaps, adminID)
	r.SetPathValue("site", "docs")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var subs []analytics.Submission
	json.NewDecoder(w.Body).Decode(&subs)
	if len(subs) != 2 {
		t.Fatalf("got %d, want 2 (only /contact)", len(subs))
	}
}

func TestSubmissionDetail_Found(t *testing.T) {
	rec := newSubmissionsRecorder(t)
	seedSubmissions(t, rec)

	h := &SubmissionDetailHandler{handlerDeps{recorder: rec}}
	r := reqWithAuth("GET", "/sites/docs/submissions/s1", adminCaps, adminID)
	r.SetPathValue("site", "docs")
	r.SetPathValue("id", "s1")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var sub analytics.Submission
	json.NewDecoder(w.Body).Decode(&sub)
	if sub.ID != "s1" {
		t.Errorf("ID = %q, want s1", sub.ID)
	}
	if sub.Submitter != "alice@ts" {
		t.Errorf("Submitter = %q, want alice@ts", sub.Submitter)
	}
}

func TestSubmissionDetail_NotFound(t *testing.T) {
	rec := newSubmissionsRecorder(t)

	h := &SubmissionDetailHandler{handlerDeps{recorder: rec}}
	r := reqWithAuth("GET", "/sites/docs/submissions/nonexistent", adminCaps, adminID)
	r.SetPathValue("site", "docs")
	r.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDeleteSubmission(t *testing.T) {
	rec := newSubmissionsRecorder(t)
	seedSubmissions(t, rec)

	h := &DeleteSubmissionHandler{handlerDeps{recorder: rec}}
	r := reqWithAuth("DELETE", "/sites/docs/submissions/s1", adminCaps, adminID)
	r.SetPathValue("site", "docs")
	r.SetPathValue("id", "s1")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	// Verify deleted.
	subs, _ := rec.ListSubmissions("docs", analytics.ListSubmissionsOpts{})
	if len(subs) != 2 {
		t.Fatalf("remaining = %d, want 2", len(subs))
	}
}

func TestBulkDeleteSubmissions_PathFilter(t *testing.T) {
	rec := newSubmissionsRecorder(t)
	seedSubmissions(t, rec)

	h := &BulkDeleteSubmissionsHandler{handlerDeps{recorder: rec}}
	r := reqWithAuth("DELETE", "/sites/docs/submissions?path=/contact", adminCaps, adminID)
	r.SetPathValue("site", "docs")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var resp map[string]int64
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["deleted"] != 2 {
		t.Fatalf("deleted = %d, want 2", resp["deleted"])
	}

	// Only /feedback should remain.
	subs, _ := rec.ListSubmissions("docs", analytics.ListSubmissionsOpts{})
	if len(subs) != 1 {
		t.Fatalf("remaining = %d, want 1", len(subs))
	}
}

func TestBulkDeleteSubmissions_PostAlias(t *testing.T) {
	rec := newSubmissionsRecorder(t)
	seedSubmissions(t, rec)

	h := &BulkDeleteSubmissionsHandler{handlerDeps{recorder: rec}}
	r := httptest.NewRequest("POST", "/sites/docs/submissions/delete", strings.NewReader("path=/contact"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := auth.ContextWithCaps(r.Context(), adminCaps)
	ctx = auth.ContextWithIdentity(ctx, adminID)
	r = r.WithContext(ctx)
	r.SetPathValue("site", "docs")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body = %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc != "/sites/docs/submissions?path=/contact" {
		t.Errorf("Location = %q", loc)
	}

	// Verify the delete actually happened.
	subs, _ := rec.ListSubmissions("docs", analytics.ListSubmissionsOpts{})
	if len(subs) != 1 {
		t.Fatalf("remaining = %d, want 1", len(subs))
	}
}

func TestBulkDeleteSubmissions_RequiresFilter(t *testing.T) {
	rec := newSubmissionsRecorder(t)
	seedSubmissions(t, rec)

	h := &BulkDeleteSubmissionsHandler{handlerDeps{recorder: rec}}
	r := reqWithAuth("DELETE", "/sites/docs/submissions", adminCaps, adminID)
	r.SetPathValue("site", "docs")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}

	// Verify nothing was deleted.
	subs, _ := rec.ListSubmissions("docs", analytics.ListSubmissionsOpts{})
	if len(subs) != 3 {
		t.Fatalf("remaining = %d, want 3", len(subs))
	}
}

func TestDeleteSubmission_NotFound(t *testing.T) {
	rec := newSubmissionsRecorder(t)

	h := &DeleteSubmissionHandler{handlerDeps{recorder: rec}}
	r := reqWithAuth("DELETE", "/sites/docs/submissions/nonexistent", adminCaps, adminID)
	r.SetPathValue("site", "docs")
	r.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDeleteSubmission_PostAlias(t *testing.T) {
	rec := newSubmissionsRecorder(t)
	seedSubmissions(t, rec)

	h := &DeleteSubmissionHandler{handlerDeps{recorder: rec}}
	r := httptest.NewRequest("POST", "/sites/docs/submissions/s1/delete", strings.NewReader("path=/contact"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := auth.ContextWithCaps(r.Context(), adminCaps)
	ctx = auth.ContextWithIdentity(ctx, adminID)
	r = r.WithContext(ctx)
	r.SetPathValue("site", "docs")
	r.SetPathValue("id", "s1")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body = %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc != "/sites/docs/submissions?path=/contact" {
		t.Errorf("Location = %q", loc)
	}

	// Verify deleted.
	subs, _ := rec.ListSubmissions("docs", analytics.ListSubmissionsOpts{})
	if len(subs) != 2 {
		t.Fatalf("remaining = %d, want 2", len(subs))
	}
}

func TestExportSubmissions_JSON(t *testing.T) {
	rec := newSubmissionsRecorder(t)
	seedSubmissions(t, rec)

	h := &ExportSubmissionsHandler{handlerDeps{recorder: rec}}
	r := reqWithAuth("GET", "/sites/docs/submissions/export?format=json", adminCaps, adminID)
	r.SetPathValue("site", "docs")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "docs-submissions.json") {
		t.Errorf("Content-Disposition = %q, want filename", cd)
	}

	var subs []analytics.Submission
	json.NewDecoder(w.Body).Decode(&subs)
	if len(subs) != 3 {
		t.Fatalf("got %d, want 3", len(subs))
	}
}

func TestExportSubmissions_CSV(t *testing.T) {
	rec := newSubmissionsRecorder(t)
	seedSubmissions(t, rec)

	h := &ExportSubmissionsHandler{handlerDeps{recorder: rec}}
	r := reqWithAuth("GET", "/sites/docs/submissions/export?format=csv", adminCaps, adminID)
	r.SetPathValue("site", "docs")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("Content-Type = %q", ct)
	}

	body := w.Body.String()
	lines := strings.Split(strings.TrimSpace(body), "\n")
	// Header + 3 data rows.
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4:\n%s", len(lines), body)
	}
	header := lines[0]
	// Fixed columns + dynamic field columns (email, name, rating).
	if !strings.Contains(header, "id") || !strings.Contains(header, "email") || !strings.Contains(header, "name") || !strings.Contains(header, "rating") {
		t.Errorf("header missing expected columns: %s", header)
	}
}

func TestSubmissionAnalyticsHandler(t *testing.T) {
	rec := newSubmissionsRecorder(t)
	seedSubmissions(t, rec)

	h := &SubmissionAnalyticsHandler{handlerDeps{recorder: rec}}
	r := reqWithAuth("GET", "/sites/docs/submissions/analytics.json", adminCaps, adminID)
	r.SetPathValue("site", "docs")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var data SubmissionAnalyticsData
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if data.Total != 3 {
		t.Errorf("total = %d, want 3", data.Total)
	}
	if data.Submitters == 0 {
		t.Error("unique_submitters = 0, want > 0")
	}
	if data.TimeSeries == nil {
		t.Error("time_series is nil, want non-nil array")
	}
	if data.HourlyPattern == nil {
		t.Error("hourly_pattern is nil, want non-nil array")
	}
	if len(data.Paths) != 2 {
		t.Errorf("paths length = %d, want 2", len(data.Paths))
	}
}

func TestSubmissionAnalyticsHandler_PathFilter(t *testing.T) {
	rec := newSubmissionsRecorder(t)
	seedSubmissions(t, rec)

	h := &SubmissionAnalyticsHandler{handlerDeps{recorder: rec}}
	r := reqWithAuth("GET", "/sites/docs/submissions/analytics.json?path=/contact", adminCaps, adminID)
	r.SetPathValue("site", "docs")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var data SubmissionAnalyticsData
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if data.Total != 2 {
		t.Errorf("total = %d, want 2 (only /contact)", data.Total)
	}
}

func TestSubmissions_Forbidden(t *testing.T) {
	rec := newSubmissionsRecorder(t)
	deps := handlerDeps{recorder: rec}

	tests := []struct {
		name    string
		handler http.Handler
		method  string
		path    string
	}{
		{"list", &SubmissionsHandler{deps}, "GET", "/sites/docs/submissions.json"},
		{"detail", &SubmissionDetailHandler{deps}, "GET", "/sites/docs/submissions/s1"},
		{"delete", &DeleteSubmissionHandler{deps}, "DELETE", "/sites/docs/submissions/s1"},
		{"bulk-delete", &BulkDeleteSubmissionsHandler{deps}, "DELETE", "/sites/docs/submissions?path=/x"},
		{"export", &ExportSubmissionsHandler{deps}, "GET", "/sites/docs/submissions/export"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := reqWithAuth(tt.method, tt.path, viewerCaps, viewerID)
			r.SetPathValue("site", "docs")
			r.SetPathValue("id", "s1")
			w := httptest.NewRecorder()

			tt.handler.ServeHTTP(w, r)

			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", w.Code)
			}
		})
	}
}
