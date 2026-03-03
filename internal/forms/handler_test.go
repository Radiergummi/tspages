package forms

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/storage"
)

func boolPtr(b bool) *bool { return &b }

type mockNotifier struct {
	calls []fireCall
}

type fireCall struct {
	event string
	site  string
	cfg   storage.SiteConfig
	data  map[string]any
}

func (m *mockNotifier) Fire(event string, site string, cfg storage.SiteConfig, data map[string]any) {
	m.calls = append(m.calls, fireCall{event, site, cfg, data})
}

func newTestRecorder(t *testing.T) *analytics.Recorder {
	t.Helper()
	r, err := analytics.NewRecorder(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func formsEnabled() storage.SiteConfig {
	return storage.SiteConfig{
		Forms: &storage.FormsConfig{Enabled: boolPtr(true)},
	}
}

func formsEnabledWithWebhook() storage.SiteConfig {
	return storage.SiteConfig{
		Forms:         &storage.FormsConfig{Enabled: boolPtr(true)},
		WebhookURL:    "https://example.com/hook",
		WebhookEvents: []string{"form.submission"},
	}
}

func TestHandler_URLEncoded_303Redirect(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", formsEnabled)

	body := "name=Alice&email=alice%40example.com"
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}
	if loc := w.Header().Get("Location"); loc != "/contact" {
		t.Errorf("Location = %q, want /contact", loc)
	}

	// Verify stored.
	subs, err := rec.ListSubmissions("docs", analytics.ListSubmissionsOpts{Path: "/contact"})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d submissions, want 1", len(subs))
	}
	if !strings.HasPrefix(subs[0].ID, "sub_") {
		t.Errorf("ID = %q, want sub_ prefix", subs[0].ID)
	}
	var fields map[string]string
	if err := json.Unmarshal(subs[0].Fields, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["name"] != "Alice" {
		t.Errorf("fields[name] = %q, want Alice", fields["name"])
	}
	if fields["email"] != "alice@example.com" {
		t.Errorf("fields[email] = %q, want alice@example.com", fields["email"])
	}
}

func TestHandler_Multipart_303Redirect(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", formsEnabled)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", "Bob")
	mw.WriteField("message", "Hello")
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/feedback", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}
	if loc := w.Header().Get("Location"); loc != "/feedback" {
		t.Errorf("Location = %q, want /feedback", loc)
	}

	subs, err := rec.ListSubmissions("docs", analytics.ListSubmissionsOpts{Path: "/feedback"})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d submissions, want 1", len(subs))
	}
}

func TestHandler_AcceptJSON_ReturnsJSON(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", formsEnabled)

	body := "name=Charlie"
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var resp struct {
		OK bool   `json:"ok"`
		ID string `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Error("ok = false, want true")
	}
	if !strings.HasPrefix(resp.ID, "sub_") {
		t.Errorf("id = %q, want sub_ prefix", resp.ID)
	}
}

func TestHandler_UnsupportedContentType_415(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", formsEnabled)

	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(`{"name":"Alice"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnsupportedMediaType)
	}
}

func TestHandler_BodyTooLarge_413(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", formsEnabled)

	// Create body larger than 64KB.
	bigBody := strings.Repeat("x="+strings.Repeat("a", 1024)+"&", 70)
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(bigBody))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandler_FormsDisabled_NilConfig_405(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", func() storage.SiteConfig {
		return storage.SiteConfig{} // Forms is nil
	})

	body := "name=Alice"
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_FormsDisabled_EnabledFalse_405(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", func() storage.SiteConfig {
		return storage.SiteConfig{
			Forms: &storage.FormsConfig{Enabled: boolPtr(false)},
		}
	})

	body := "name=Alice"
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_WebhookFired(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", formsEnabledWithWebhook)

	body := "name=Alice"
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}
	if len(notif.calls) != 1 {
		t.Fatalf("notifier calls = %d, want 1", len(notif.calls))
	}
	call := notif.calls[0]
	if call.event != "form.submission" {
		t.Errorf("event = %q, want form.submission", call.event)
	}
	if call.site != "docs" {
		t.Errorf("site = %q, want docs", call.site)
	}
	if call.data["path"] != "/contact" {
		t.Errorf("data[path] = %v, want /contact", call.data["path"])
	}
}

func TestHandler_WebhookNotFired_NoURL(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", formsEnabled) // no webhook_url

	body := "name=Alice"
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}
	// Fire is still called, but the notifier implementation (webhook.Notifier)
	// is the one that no-ops when URL is empty. Our mock just records calls.
	// The handler always calls Fire. This test verifies the handler works
	// without a webhook URL configured.
	if len(notif.calls) != 1 {
		t.Fatalf("notifier calls = %d, want 1", len(notif.calls))
	}
}

func TestHandler_SubmitterInfoFromContext(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", formsEnabled)

	body := "msg=hello"
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ctx := auth.ContextWithRequestInfo(req.Context(), auth.RequestInfo{
		UserLogin: "alice@example.com",
		UserName:  "Alice",
		NodeName:  "alice-mac",
		NodeIP:    "100.64.0.1",
	})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}

	subs, err := rec.ListSubmissions("docs", analytics.ListSubmissionsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d submissions, want 1", len(subs))
	}
	if subs[0].Submitter != "alice@example.com" {
		t.Errorf("Submitter = %q, want alice@example.com", subs[0].Submitter)
	}
	if subs[0].Node != "alice-mac" {
		t.Errorf("Node = %q, want alice-mac", subs[0].Node)
	}
	if subs[0].IP != "100.64.0.1" {
		t.Errorf("IP = %q, want 100.64.0.1", subs[0].IP)
	}
}

func TestHandler_FieldsStoredAsJSON(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", formsEnabled)

	body := "name=Alice&email=alice%40example.com&message=Hello+world"
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}

	subs, err := rec.ListSubmissions("docs", analytics.ListSubmissionsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d submissions, want 1", len(subs))
	}

	// Verify the JSON is valid and contains expected fields.
	var fields map[string]string
	if err := json.Unmarshal(subs[0].Fields, &fields); err != nil {
		t.Fatalf("fields not valid JSON: %v", err)
	}
	if fields["name"] != "Alice" {
		t.Errorf("fields[name] = %q, want Alice", fields["name"])
	}
	if fields["email"] != "alice@example.com" {
		t.Errorf("fields[email] = %q, want alice@example.com", fields["email"])
	}
	if fields["message"] != "Hello world" {
		t.Errorf("fields[message] = %q, want Hello world", fields["message"])
	}

	// Verify retrievable via GetSubmission.
	got, err := rec.GetSubmission("docs", subs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Fields) != string(subs[0].Fields) {
		t.Errorf("GetSubmission fields mismatch: %s vs %s", got.Fields, subs[0].Fields)
	}
}

func TestHandler_BracketSuffix_StrippedAndMultiValue(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", formsEnabled)

	// "colors[]" should be stripped to "colors" and always RS-joined.
	body := "name=Alice&colors%5B%5D=red&colors%5B%5D=blue"
	req := httptest.NewRequest(http.MethodPost, "/feedback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}

	subs, err := rec.ListSubmissions("docs", analytics.ListSubmissionsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d submissions, want 1", len(subs))
	}

	var fields map[string]string
	if err := json.Unmarshal(subs[0].Fields, &fields); err != nil {
		t.Fatalf("fields not valid JSON: %v", err)
	}
	if fields["name"] != "Alice" {
		t.Errorf("fields[name] = %q, want Alice", fields["name"])
	}
	// Key should be "colors" (no []), value should contain RS separators.
	colors, ok := fields["colors"]
	if !ok {
		t.Fatal("missing 'colors' key (bracket suffix not stripped?)")
	}
	if !strings.Contains(colors, analytics.FieldSeparator) {
		t.Fatal("colors should contain RS separator")
	}
	// ExpandFields should produce a two-element array (trailing RS filtered).
	expanded := analytics.ExpandFields(subs[0].Fields, nil)
	var out map[string]any
	json.Unmarshal(expanded, &out)
	arr, ok := out["colors"].([]any)
	if !ok || len(arr) != 2 || arr[0] != "red" || arr[1] != "blue" {
		t.Errorf("expanded colors = %v, want [red blue]", out["colors"])
	}
}

func TestHandler_BracketSuffix_SingleValue_StillArray(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", formsEnabled)

	body := "tag%5B%5D=go"
	req := httptest.NewRequest(http.MethodPost, "/feedback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	subs, _ := rec.ListSubmissions("docs", analytics.ListSubmissionsOpts{})
	var fields map[string]string
	json.Unmarshal(subs[0].Fields, &fields)

	// Key should be "tag" (stripped), value should contain trailing RS
	// so ExpandFields detects it as multi-valued.
	tag, ok := fields["tag"]
	if !ok {
		t.Fatal("missing 'tag' key")
	}
	if !strings.Contains(tag, analytics.FieldSeparator) {
		t.Error("single [] value should contain RS for array detection")
	}

	// ExpandFields should produce a single-element array.
	expanded := analytics.ExpandFields(subs[0].Fields, nil)
	var out map[string]any
	json.Unmarshal(expanded, &out)
	arr, ok := out["tag"].([]any)
	if !ok || len(arr) != 1 || arr[0] != "go" {
		t.Errorf("expanded tag = %v, want [go]", out["tag"])
	}
}

func TestHandler_FormsDisabled_NilEnabled_405(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	// FormsConfig exists but Enabled is nil (not explicitly set).
	h := NewHandler(rec, notif, "docs", func() storage.SiteConfig {
		return storage.SiteConfig{
			Forms: &storage.FormsConfig{},
		}
	})

	body := "name=Alice"
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// Ensure context without RequestInfo doesn't break anything.
func TestHandler_NoAuthContext(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", formsEnabled)

	body := "msg=hello"
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// No auth context set — use bare context.
	req = req.WithContext(context.Background())
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}

	subs, err := rec.ListSubmissions("docs", analytics.ListSubmissionsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d submissions, want 1", len(subs))
	}
	// Submitter should be empty when no auth context.
	if subs[0].Submitter != "" {
		t.Errorf("Submitter = %q, want empty", subs[0].Submitter)
	}
}

func TestHandler_BracketAndPlainKeyCollision(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", formsEnabled)

	// Submit both "colors" (plain) and "colors[]" (bracket) — the bracket
	// form should win since it was explicitly declared multi-value.
	body := "colors=injected&colors%5B%5D=red&colors%5B%5D=blue"
	req := httptest.NewRequest(http.MethodPost, "/survey", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}

	subs, err := rec.ListSubmissions("docs", analytics.ListSubmissionsOpts{})
	if err != nil {
		t.Fatal(err)
	}

	expanded := analytics.ExpandFields(subs[0].Fields, nil)
	var out map[string]any
	if err := json.Unmarshal(expanded, &out); err != nil {
		t.Fatal(err)
	}
	arr, ok := out["colors"].([]any)
	if !ok {
		t.Fatalf("colors = %v (%T), want []any", out["colors"], out["colors"])
	}
	if len(arr) != 2 || arr[0] != "red" || arr[1] != "blue" {
		t.Errorf("colors = %v, want [red blue]", arr)
	}
}

func TestHandler_EmptyFormBody(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", formsEnabled)

	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}

	subs, err := rec.ListSubmissions("docs", analytics.ListSubmissionsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d submissions, want 1", len(subs))
	}
	// Empty body should produce empty fields object.
	if string(subs[0].Fields) != "{}" {
		t.Errorf("Fields = %s, want {}", subs[0].Fields)
	}
}

func TestHandler_MultipartBodyTooLarge_413(t *testing.T) {
	rec := newTestRecorder(t)
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "docs", formsEnabled)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// Write a field larger than 64KB.
	mw.WriteField("data", strings.Repeat("x", 70*1024))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/contact", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}
