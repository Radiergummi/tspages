package submissions

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/db"
	"tspages/internal/notify"
	"tspages/internal/storage"
)

// region test helpers

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(db.OpenTestDB(t, analytics.Migrations, Migrations))
}

var (
	adminCaps  = []auth.Cap{{Access: "admin"}}
	deployCaps = []auth.Cap{{Access: "deploy", Sites: []string{"testsite"}}}
	viewCaps   = []auth.Cap{{Access: "view"}}

	adminID  = auth.Identity{LoginName: "admin@test", DisplayName: "Admin"}
	deployID = auth.Identity{LoginName: "deployer@test", DisplayName: "Deployer"}
)

func reqWithAuth(method, url string, caps []auth.Cap, id auth.Identity) *http.Request {
	r := httptest.NewRequest(method, url, nil)
	ctx := auth.ContextWithCaps(r.Context(), caps)
	ctx = auth.ContextWithIdentity(ctx, id)
	return r.WithContext(ctx)
}

func reqWithAuthBody(method, url, contentType, body string, caps []auth.Cap, id auth.Identity) *http.Request {
	r := httptest.NewRequest(method, url, strings.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	ctx := auth.ContextWithCaps(r.Context(), caps)
	ctx = auth.ContextWithIdentity(ctx, id)
	return r.WithContext(ctx)
}

func boolPtr(v bool) *bool { return &v }

func formsEnabledConfig() storage.SiteConfig {
	return storage.SiteConfig{
		Forms: &storage.FormsConfig{Enabled: boolPtr(true)},
	}
}

func formsDisabledConfig() storage.SiteConfig {
	return storage.SiteConfig{
		Forms: &storage.FormsConfig{Enabled: boolPtr(false)},
	}
}

type spyNotifier struct {
	events []string
	data   []map[string]any
}

func (s *spyNotifier) Fire(event string, site string, cfg storage.SiteConfig, data map[string]any) {
	s.events = append(s.events, event)
	s.data = append(s.data, data)
}

var _ notify.Notifier = (*spyNotifier)(nil)

func seedSubmission(t *testing.T, store *Store, id, site, path string, fields map[string]string) {
	t.Helper()
	fieldsJSON, _ := json.Marshal(fields)
	err := store.RecordSubmission(Submission{
		ID:        id,
		Site:      site,
		Path:      path,
		Fields:    fieldsJSON,
		Submitter: "user@test",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
}

// region POST handler tests

func TestPostHandler_URLEncodedForm(t *testing.T) {
	store := newTestStore(t)
	spy := &spyNotifier{}
	cfg := formsEnabledConfig()
	h := NewHandler(store, spy, "testsite", func() storage.SiteConfig { return cfg })

	body := "name=Alice&email=alice%40example.com"
	r := httptest.NewRequest("POST", "/contact", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/contact" {
		t.Errorf("redirect location = %q, want /contact", loc)
	}

	// Verify stored.
	subs, err := store.ListSubmissions("testsite", ListSubmissionsOpts{Path: "/contact"})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 submission, got %d", len(subs))
	}
	var fields map[string]string
	json.Unmarshal(subs[0].Fields, &fields)
	if fields["name"] != "Alice" {
		t.Errorf("name = %q, want Alice", fields["name"])
	}
	if fields["email"] != "alice@example.com" {
		t.Errorf("email = %q, want alice@example.com", fields["email"])
	}
}

func TestPostHandler_SubmitterFromContext(t *testing.T) {
	store := newTestStore(t)
	spy := &spyNotifier{}
	cfg := formsEnabledConfig()
	h := NewHandler(store, spy, "testsite", func() storage.SiteConfig { return cfg })

	body := "msg=hello"
	r := httptest.NewRequest("POST", "/form", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := auth.ContextWithRequestInfo(r.Context(), auth.RequestInfo{
		UserLogin: "alice@example.com",
		UserName:  "Alice Smith",
		NodeName:  "alice-laptop",
		NodeIP:    "100.64.0.1",
	})
	r = r.WithContext(ctx)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}

	subs, _ := store.ListSubmissions("testsite", ListSubmissionsOpts{Path: "/form"})
	if len(subs) != 1 {
		t.Fatalf("expected 1 submission, got %d", len(subs))
	}
	if subs[0].Submitter != "alice@example.com" {
		t.Errorf("submitter = %q, want alice@example.com", subs[0].Submitter)
	}
	if subs[0].SubmitterName != "Alice Smith" {
		t.Errorf("submitter_name = %q, want Alice Smith", subs[0].SubmitterName)
	}
	if subs[0].Node != "alice-laptop" {
		t.Errorf("node = %q, want alice-laptop", subs[0].Node)
	}
}

func TestPostHandler_Multipart(t *testing.T) {
	store := newTestStore(t)
	spy := &spyNotifier{}
	cfg := formsEnabledConfig()
	h := NewHandler(store, spy, "testsite", func() storage.SiteConfig { return cfg })

	boundary := "----TestBoundary"
	body := fmt.Sprintf("--%s\r\nContent-Disposition: form-data; name=\"msg\"\r\n\r\nhello\r\n--%s--\r\n", boundary, boundary)
	r := httptest.NewRequest("POST", "/feedback", strings.NewReader(body))
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}

	subs, _ := store.ListSubmissions("testsite", ListSubmissionsOpts{Path: "/feedback"})
	if len(subs) != 1 {
		t.Fatalf("expected 1 submission, got %d", len(subs))
	}
	var fields map[string]string
	json.Unmarshal(subs[0].Fields, &fields)
	if fields["msg"] != "hello" {
		t.Errorf("msg = %q, want hello", fields["msg"])
	}
}

func TestPostHandler_JSONAccept(t *testing.T) {
	store := newTestStore(t)
	spy := &spyNotifier{}
	cfg := formsEnabledConfig()
	h := NewHandler(store, spy, "testsite", func() storage.SiteConfig { return cfg })

	body := "name=Bob"
	r := httptest.NewRequest("POST", "/api/form", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Errorf("ok = %v, want true", resp["ok"])
	}
	if _, ok := resp["id"]; !ok {
		t.Error("response missing id")
	}
}

func TestPostHandler_UnsupportedContentType(t *testing.T) {
	store := newTestStore(t)
	spy := &spyNotifier{}
	cfg := formsEnabledConfig()
	h := NewHandler(store, spy, "testsite", func() storage.SiteConfig { return cfg })

	r := httptest.NewRequest("POST", "/form", strings.NewReader(`{"name":"test"}`))
	r.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected 415, got %d", w.Code)
	}
}

func TestPostHandler_BodyTooLarge(t *testing.T) {
	store := newTestStore(t)
	spy := &spyNotifier{}
	cfg := formsEnabledConfig()
	h := NewHandler(store, spy, "testsite", func() storage.SiteConfig { return cfg })

	// 64KB + 1 byte should exceed the limit.
	bigBody := "data=" + strings.Repeat("x", 64*1024+1)
	r := httptest.NewRequest("POST", "/form", strings.NewReader(bigBody))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", w.Code)
	}
}

func TestPostHandler_FormsDisabled(t *testing.T) {
	store := newTestStore(t)
	spy := &spyNotifier{}
	cfg := formsDisabledConfig()
	h := NewHandler(store, spy, "testsite", func() storage.SiteConfig { return cfg })

	body := "msg=hello"
	r := httptest.NewRequest("POST", "/form", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestPostHandler_FormsNilConfig(t *testing.T) {
	store := newTestStore(t)
	spy := &spyNotifier{}
	h := NewHandler(store, spy, "testsite", func() storage.SiteConfig { return storage.SiteConfig{} })

	body := "msg=hello"
	r := httptest.NewRequest("POST", "/form", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestPostHandler_WebhookFired(t *testing.T) {
	store := newTestStore(t)
	spy := &spyNotifier{}
	cfg := formsEnabledConfig()
	h := NewHandler(store, spy, "testsite", func() storage.SiteConfig { return cfg })

	body := "name=Alice"
	r := httptest.NewRequest("POST", "/contact", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if len(spy.events) != 1 {
		t.Fatalf("expected 1 webhook event, got %d", len(spy.events))
	}
	if spy.events[0] != "form.submission" {
		t.Errorf("event = %q, want form.submission", spy.events[0])
	}
	data := spy.data[0]
	if data["path"] != "/contact" {
		t.Errorf("webhook path = %v, want /contact", data["path"])
	}
	fields, ok := data["fields"].(map[string]any)
	if !ok {
		t.Fatalf("fields is %T, want map[string]any", data["fields"])
	}
	if fields["name"] != "Alice" {
		t.Errorf("webhook fields[name] = %v, want Alice", fields["name"])
	}
}

func TestPostHandler_BracketSuffixMultiValue(t *testing.T) {
	store := newTestStore(t)
	spy := &spyNotifier{}
	cfg := formsEnabledConfig()
	h := NewHandler(store, spy, "testsite", func() storage.SiteConfig { return cfg })

	// "colors[]" with multiple values should be stored with RS encoding.
	body := "colors%5B%5D=red&colors%5B%5D=blue"
	r := httptest.NewRequest("POST", "/form", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}

	subs, _ := store.ListSubmissions("testsite", ListSubmissionsOpts{Path: "/form"})
	if len(subs) != 1 {
		t.Fatalf("expected 1 submission, got %d", len(subs))
	}

	var fields map[string]string
	json.Unmarshal(subs[0].Fields, &fields)
	// Should be stored under "colors" (bracket stripped), with RS encoding.
	val, ok := fields["colors"]
	if !ok {
		t.Fatal("expected 'colors' key in fields")
	}
	if !strings.Contains(val, FieldSeparator) {
		t.Errorf("expected RS-encoded multi-value, got %q", val)
	}

	// Expand and verify array.
	expanded := ExpandFields(subs[0].Fields, nil)
	var expandedFields map[string]any
	json.Unmarshal(expanded, &expandedFields)
	arr, ok := expandedFields["colors"].([]any)
	if !ok {
		t.Fatalf("expected array for colors, got %T", expandedFields["colors"])
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 values, got %d", len(arr))
	}
	if arr[0] != "red" || arr[1] != "blue" {
		t.Errorf("got %v, want [red blue]", arr)
	}
}

func TestPostHandler_BracketSuffixSingleValue(t *testing.T) {
	store := newTestStore(t)
	spy := &spyNotifier{}
	cfg := formsEnabledConfig()
	h := NewHandler(store, spy, "testsite", func() storage.SiteConfig { return cfg })

	// Single value with [] suffix should still produce an array.
	body := "tag%5B%5D=important"
	r := httptest.NewRequest("POST", "/form", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	subs, _ := store.ListSubmissions("testsite", ListSubmissionsOpts{Path: "/form"})
	if len(subs) != 1 {
		t.Fatal("expected 1 submission")
	}

	expanded := ExpandFields(subs[0].Fields, nil)
	var expandedFields map[string]any
	json.Unmarshal(expanded, &expandedFields)
	arr, ok := expandedFields["tag"].([]any)
	if !ok {
		t.Fatalf("expected array for tag, got %T: %s", expandedFields["tag"], string(expanded))
	}
	if len(arr) != 1 || arr[0] != "important" {
		t.Errorf("got %v, want [important]", arr)
	}
}

// region admin handler tests

func TestSubmissionsHandler_ListJSON(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())

	seedSubmission(t, subStore, "sub_001", "testsite", "/contact", map[string]string{"name": "Alice"})
	seedSubmission(t, subStore, "sub_002", "testsite", "/contact", map[string]string{"name": "Bob"})

	h := &SubmissionsHandler{store: storageStore, submissions: subStore, defaults: storage.SiteConfig{}}

	r := reqWithAuth("GET", "/sites/testsite/submissions.json", adminCaps, adminID)
	r.SetPathValue("site", "testsite")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var subs []Submission
	json.NewDecoder(w.Body).Decode(&subs)
	if len(subs) != 2 {
		t.Fatalf("expected 2 submissions, got %d", len(subs))
	}
}

func TestSubmissionsHandler_PathFilter(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())

	seedSubmission(t, subStore, "sub_001", "testsite", "/contact", map[string]string{"name": "Alice"})
	seedSubmission(t, subStore, "sub_002", "testsite", "/feedback", map[string]string{"msg": "Great"})

	h := &SubmissionsHandler{store: storageStore, submissions: subStore, defaults: storage.SiteConfig{}}

	r := reqWithAuth("GET", "/sites/testsite/submissions.json?path=/contact", adminCaps, adminID)
	r.SetPathValue("site", "testsite")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var subs []Submission
	json.NewDecoder(w.Body).Decode(&subs)
	if len(subs) != 1 {
		t.Fatalf("expected 1 submission, got %d", len(subs))
	}
	if subs[0].ID != "sub_001" {
		t.Errorf("expected sub_001, got %s", subs[0].ID)
	}
}

func TestSubmissionsHandler_Forbidden(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())
	h := &SubmissionsHandler{store: storageStore, submissions: subStore, defaults: storage.SiteConfig{}}

	r := reqWithAuth("GET", "/sites/testsite/submissions.json", viewCaps, adminID)
	r.SetPathValue("site", "testsite")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

// region SubmissionDetailHandler

func TestSubmissionDetailHandler_Found(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())

	seedSubmission(t, subStore, "sub_001", "testsite", "/contact", map[string]string{"name": "Alice"})

	h := &SubmissionDetailHandler{store: storageStore, submissions: subStore, defaults: storage.SiteConfig{}}

	r := reqWithAuth("GET", "/sites/testsite/submissions/sub_001", adminCaps, adminID)
	r.SetPathValue("site", "testsite")
	r.SetPathValue("id", "sub_001")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var sub Submission
	json.NewDecoder(w.Body).Decode(&sub)
	if sub.ID != "sub_001" {
		t.Errorf("id = %q, want sub_001", sub.ID)
	}
}

func TestSubmissionDetailHandler_NotFound(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())
	h := &SubmissionDetailHandler{store: storageStore, submissions: subStore, defaults: storage.SiteConfig{}}

	r := reqWithAuth("GET", "/sites/testsite/submissions/nonexistent", adminCaps, adminID)
	r.SetPathValue("site", "testsite")
	r.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSubmissionDetailHandler_Forbidden(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())
	h := &SubmissionDetailHandler{store: storageStore, submissions: subStore, defaults: storage.SiteConfig{}}

	r := reqWithAuth("GET", "/sites/testsite/submissions/sub_001", viewCaps, adminID)
	r.SetPathValue("site", "testsite")
	r.SetPathValue("id", "sub_001")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

// region DeleteSubmissionHandler

func TestDeleteSubmissionHandler_Delete(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())

	seedSubmission(t, subStore, "sub_001", "testsite", "/contact", map[string]string{"name": "Alice"})

	h := &DeleteSubmissionHandler{store: storageStore, submissions: subStore}

	r := reqWithAuth("DELETE", "/sites/testsite/submissions/sub_001", adminCaps, adminID)
	r.SetPathValue("site", "testsite")
	r.SetPathValue("id", "sub_001")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// Verify it's gone.
	_, err := subStore.GetSubmission("testsite", "sub_001")
	if err != sql.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

func TestDeleteSubmissionHandler_NotFound(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())
	h := &DeleteSubmissionHandler{store: storageStore, submissions: subStore}

	r := reqWithAuth("DELETE", "/sites/testsite/submissions/nonexistent", adminCaps, adminID)
	r.SetPathValue("site", "testsite")
	r.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestDeleteSubmissionHandler_PostForm(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())

	seedSubmission(t, subStore, "sub_001", "testsite", "/contact", map[string]string{"name": "Alice"})

	h := &DeleteSubmissionHandler{store: storageStore, submissions: subStore}

	r := reqWithAuthBody("POST", "/sites/testsite/submissions/sub_001/delete",
		"application/x-www-form-urlencoded", "path=/contact", adminCaps, adminID)
	r.SetPathValue("site", "testsite")
	r.SetPathValue("id", "sub_001")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/sites/testsite/submissions") {
		t.Errorf("redirect location = %q, expected submissions page", loc)
	}
}

func TestDeleteSubmissionHandler_Forbidden(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())
	h := &DeleteSubmissionHandler{store: storageStore, submissions: subStore}

	// deploy caps should not be sufficient for delete (requires admin).
	r := reqWithAuth("DELETE", "/sites/testsite/submissions/sub_001", deployCaps, deployID)
	r.SetPathValue("site", "testsite")
	r.SetPathValue("id", "sub_001")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

// region BulkDeleteSubmissionsHandler

func TestBulkDeleteHandler_ByPath(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())

	seedSubmission(t, subStore, "sub_001", "testsite", "/contact", map[string]string{"name": "A"})
	seedSubmission(t, subStore, "sub_002", "testsite", "/contact", map[string]string{"name": "B"})
	seedSubmission(t, subStore, "sub_003", "testsite", "/feedback", map[string]string{"msg": "C"})

	h := &BulkDeleteSubmissionsHandler{store: storageStore, submissions: subStore}

	r := reqWithAuth("DELETE", "/sites/testsite/submissions?path=/contact", adminCaps, adminID)
	r.SetPathValue("site", "testsite")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]int64
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["deleted"] != 2 {
		t.Errorf("deleted = %d, want 2", resp["deleted"])
	}

	// Feedback submission should remain.
	subs, _ := subStore.ListSubmissions("testsite", ListSubmissionsOpts{})
	if len(subs) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(subs))
	}
	if subs[0].ID != "sub_003" {
		t.Errorf("remaining = %s, want sub_003", subs[0].ID)
	}
}

func TestBulkDeleteHandler_RequiresFilter(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())
	h := &BulkDeleteSubmissionsHandler{store: storageStore, submissions: subStore}

	r := reqWithAuth("DELETE", "/sites/testsite/submissions", adminCaps, adminID)
	r.SetPathValue("site", "testsite")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestBulkDeleteHandler_PostForm(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())

	seedSubmission(t, subStore, "sub_001", "testsite", "/contact", map[string]string{"name": "A"})

	h := &BulkDeleteSubmissionsHandler{store: storageStore, submissions: subStore}

	r := reqWithAuthBody("POST", "/sites/testsite/submissions/delete",
		"application/x-www-form-urlencoded", "path=/contact", adminCaps, adminID)
	r.SetPathValue("site", "testsite")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBulkDeleteHandler_Forbidden(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())
	h := &BulkDeleteSubmissionsHandler{store: storageStore, submissions: subStore}

	r := reqWithAuth("DELETE", "/sites/testsite/submissions?path=/contact", deployCaps, deployID)
	r.SetPathValue("site", "testsite")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

// region ExportSubmissionsHandler

func TestExportHandler_JSON(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())

	seedSubmission(t, subStore, "sub_001", "testsite", "/contact", map[string]string{"name": "Alice"})
	seedSubmission(t, subStore, "sub_002", "testsite", "/contact", map[string]string{"name": "Bob"})

	h := &ExportSubmissionsHandler{store: storageStore, submissions: subStore, defaults: storage.SiteConfig{}}

	r := reqWithAuth("GET", "/sites/testsite/submissions/export?format=json", adminCaps, adminID)
	r.SetPathValue("site", "testsite")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "testsite-submissions.json") {
		t.Errorf("content-disposition = %q, want filename with testsite-submissions.json", cd)
	}

	var subs []Submission
	json.NewDecoder(w.Body).Decode(&subs)
	if len(subs) != 2 {
		t.Errorf("expected 2 submissions, got %d", len(subs))
	}
}

func TestExportHandler_CSV(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())

	seedSubmission(t, subStore, "sub_001", "testsite", "/contact", map[string]string{"name": "Alice", "email": "a@test"})

	h := &ExportSubmissionsHandler{store: storageStore, submissions: subStore, defaults: storage.SiteConfig{}}

	r := reqWithAuth("GET", "/sites/testsite/submissions/export?format=csv", adminCaps, adminID)
	r.SetPathValue("site", "testsite")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("content-type = %q, want text/csv", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "testsite-submissions.csv") {
		t.Errorf("content-disposition = %q, want filename with testsite-submissions.csv", cd)
	}

	body := w.Body.String()
	// Check header row contains expected columns.
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines (header + data), got %d", len(lines))
	}
	header := lines[0]
	for _, col := range []string{"id", "path", "created_at", "submitter"} {
		if !strings.Contains(header, col) {
			t.Errorf("header missing column %q: %s", col, header)
		}
	}
}

func TestExportHandler_PathFilter(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())

	seedSubmission(t, subStore, "sub_001", "testsite", "/contact", map[string]string{"name": "Alice"})
	seedSubmission(t, subStore, "sub_002", "testsite", "/feedback", map[string]string{"msg": "Great"})

	h := &ExportSubmissionsHandler{store: storageStore, submissions: subStore, defaults: storage.SiteConfig{}}

	r := reqWithAuth("GET", "/sites/testsite/submissions/export?format=json&path=/contact", adminCaps, adminID)
	r.SetPathValue("site", "testsite")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var subs []Submission
	json.NewDecoder(w.Body).Decode(&subs)
	if len(subs) != 1 {
		t.Errorf("expected 1, got %d", len(subs))
	}
}

func TestExportHandler_Forbidden(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())
	h := &ExportSubmissionsHandler{store: storageStore, submissions: subStore, defaults: storage.SiteConfig{}}

	r := reqWithAuth("GET", "/sites/testsite/submissions/export", viewCaps, adminID)
	r.SetPathValue("site", "testsite")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

// region SubmissionAnalyticsHandler

func TestAnalyticsHandler_JSON(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())

	seedSubmission(t, subStore, "sub_001", "testsite", "/contact", map[string]string{"name": "Alice"})
	seedSubmission(t, subStore, "sub_002", "testsite", "/contact", map[string]string{"name": "Bob"})

	h := &SubmissionAnalyticsHandler{store: storageStore, submissions: subStore, defaults: storage.SiteConfig{}}

	r := reqWithAuth("GET", "/sites/testsite/submissions/analytics.json?range=all", adminCaps, adminID)
	r.SetPathValue("site", "testsite")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var data SubmissionAnalyticsData
	json.NewDecoder(w.Body).Decode(&data)
	if data.Total != 2 {
		t.Errorf("total = %d, want 2", data.Total)
	}
	if len(data.Paths) != 1 {
		t.Errorf("expected 1 path, got %d", len(data.Paths))
	}
}

func TestAnalyticsHandler_WithPathFilter(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())

	seedSubmission(t, subStore, "sub_001", "testsite", "/contact", map[string]string{"name": "Alice"})
	seedSubmission(t, subStore, "sub_002", "testsite", "/feedback", map[string]string{"msg": "Great"})

	h := &SubmissionAnalyticsHandler{store: storageStore, submissions: subStore, defaults: storage.SiteConfig{}}

	r := reqWithAuth("GET", "/sites/testsite/submissions/analytics.json?path=/contact&range=all", adminCaps, adminID)
	r.SetPathValue("site", "testsite")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var data SubmissionAnalyticsData
	json.NewDecoder(w.Body).Decode(&data)
	if data.Total != 1 {
		t.Errorf("total = %d, want 1", data.Total)
	}
}

func TestAnalyticsHandler_Forbidden(t *testing.T) {
	subStore := newTestStore(t)
	storageStore := storage.New(t.TempDir())
	h := &SubmissionAnalyticsHandler{store: storageStore, submissions: subStore, defaults: storage.SiteConfig{}}

	r := reqWithAuth("GET", "/sites/testsite/submissions/analytics.json", viewCaps, adminID)
	r.SetPathValue("site", "testsite")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

// region ExpandFields

func TestExpandFields_RSEncoded(t *testing.T) {
	raw := json.RawMessage(`{"colors":"red\u001eblue\u001e","name":"Alice"}`)
	expanded := ExpandFields(raw, nil)
	var m map[string]any
	json.Unmarshal(expanded, &m)
	arr, ok := m["colors"].([]any)
	if !ok {
		t.Fatalf("colors is %T, want []any", m["colors"])
	}
	if len(arr) != 2 || arr[0] != "red" || arr[1] != "blue" {
		t.Errorf("colors = %v, want [red blue]", arr)
	}
	if m["name"] != "Alice" {
		t.Errorf("name = %v, want Alice", m["name"])
	}
}

func TestExpandFields_AlwaysArray(t *testing.T) {
	raw := json.RawMessage(`{"tag":"important","name":"Alice"}`)
	always := map[string]bool{"tag": true}
	expanded := ExpandFields(raw, always)
	var m map[string]any
	json.Unmarshal(expanded, &m)
	arr, ok := m["tag"].([]any)
	if !ok {
		t.Fatalf("tag is %T, want []any", m["tag"])
	}
	if len(arr) != 1 || arr[0] != "important" {
		t.Errorf("tag = %v, want [important]", arr)
	}
	// Non-array field should remain a string.
	if m["name"] != "Alice" {
		t.Errorf("name = %v, want Alice", m["name"])
	}
}

func TestExpandFields_NoExpansionNeeded(t *testing.T) {
	raw := json.RawMessage(`{"name":"Alice"}`)
	expanded := ExpandFields(raw, nil)
	// Should return the same bytes (no modification).
	if string(expanded) != string(raw) {
		t.Errorf("expected no expansion, got %s", string(expanded))
	}
}

func TestExpandFields_EmptyInput(t *testing.T) {
	got := ExpandFields(nil, nil)
	if got != nil {
		t.Errorf("expected nil, got %s", string(got))
	}
}

// region Store round-trip

func TestStore_RecordAndGet(t *testing.T) {
	store := newTestStore(t)
	fields, _ := json.Marshal(map[string]string{"name": "Alice"})
	sub := Submission{
		ID:            "sub_test",
		Site:          "mysite",
		Path:          "/contact",
		Fields:        fields,
		Submitter:     "alice@test",
		SubmitterName: "Alice",
		Node:          "alice-laptop",
		IP:            "100.64.0.1",
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := store.RecordSubmission(sub); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetSubmission("mysite", "sub_test")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "sub_test" {
		t.Errorf("id = %q, want sub_test", got.ID)
	}
	if got.Submitter != "alice@test" {
		t.Errorf("submitter = %q, want alice@test", got.Submitter)
	}
	if got.SubmitterName != "Alice" {
		t.Errorf("submitter_name = %q, want Alice", got.SubmitterName)
	}
}

func TestStore_ListAndDelete(t *testing.T) {
	store := newTestStore(t)
	seedSubmission(t, store, "s1", "site", "/a", map[string]string{"x": "1"})
	seedSubmission(t, store, "s2", "site", "/a", map[string]string{"x": "2"})
	seedSubmission(t, store, "s3", "site", "/b", map[string]string{"x": "3"})

	subs, err := store.ListSubmissions("site", ListSubmissionsOpts{Path: "/a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 {
		t.Fatalf("expected 2, got %d", len(subs))
	}

	deleted, err := store.DeleteSubmission("site", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Error("expected deleted=true")
	}

	subs, _ = store.ListSubmissions("site", ListSubmissionsOpts{Path: "/a"})
	if len(subs) != 1 {
		t.Fatalf("expected 1 after delete, got %d", len(subs))
	}
}

func TestStore_FormPaths(t *testing.T) {
	store := newTestStore(t)
	seedSubmission(t, store, "s1", "site", "/contact", map[string]string{"x": "1"})
	seedSubmission(t, store, "s2", "site", "/contact", map[string]string{"x": "2"})
	seedSubmission(t, store, "s3", "site", "/feedback", map[string]string{"x": "3"})

	paths, err := store.FormPaths("site")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}
	// Ordered by count desc: /contact (2) before /feedback (1).
	if paths[0].Path != "/contact" || paths[0].Count != 2 {
		t.Errorf("paths[0] = %+v, want /contact:2", paths[0])
	}
	if paths[1].Path != "/feedback" || paths[1].Count != 1 {
		t.Errorf("paths[1] = %+v, want /feedback:1", paths[1])
	}
}

func TestStore_BulkDelete(t *testing.T) {
	store := newTestStore(t)
	seedSubmission(t, store, "s1", "site", "/contact", map[string]string{"x": "1"})
	seedSubmission(t, store, "s2", "site", "/contact", map[string]string{"x": "2"})
	seedSubmission(t, store, "s3", "site", "/feedback", map[string]string{"x": "3"})

	n, err := store.BulkDeleteSubmissions("site", BulkDeleteOpts{Path: "/contact"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("deleted %d, want 2", n)
	}

	subs, _ := store.ListSubmissions("site", ListSubmissionsOpts{})
	if len(subs) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(subs))
	}
}

func TestStore_PurgeSubmissions(t *testing.T) {
	store := newTestStore(t)
	seedSubmission(t, store, "s1", "site", "/a", map[string]string{"x": "1"})
	seedSubmission(t, store, "s2", "site", "/b", map[string]string{"x": "2"})

	n, err := store.PurgeSubmissions("site")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("purged %d, want 2", n)
	}

	subs, _ := store.ListSubmissions("site", ListSubmissionsOpts{})
	if len(subs) != 0 {
		t.Errorf("expected 0, got %d", len(subs))
	}
}

func TestStore_CountSubmissions(t *testing.T) {
	store := newTestStore(t)
	seedSubmission(t, store, "s1", "site", "/a", map[string]string{"x": "1"})
	seedSubmission(t, store, "s2", "site", "/a", map[string]string{"x": "2"})
	seedSubmission(t, store, "s3", "site", "/b", map[string]string{"x": "3"})

	count, err := store.CountSubmissions("site", "/a", time.Time{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}

	count, _ = store.CountSubmissions("site", "", time.Time{}, time.Now())
	if count != 3 {
		t.Errorf("all count = %d, want 3", count)
	}
}
