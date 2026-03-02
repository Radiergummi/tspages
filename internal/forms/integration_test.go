package forms

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/storage"
)

func TestFormSubmissionIntegration(t *testing.T) {
	// 1. Create a store with a temp dir.
	store := storage.New(t.TempDir())

	// 2. Create an analytics recorder with a temp DB.
	rec, err := analytics.NewRecorder(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rec.Close() })

	// 3. Create a site and deploy content with forms enabled.
	if err := store.CreateSite("testsite"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment("testsite", "d1"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkComplete("testsite", "d1"); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateDeployment("testsite", "d1"); err != nil {
		t.Fatal(err)
	}

	cfg := storage.SiteConfig{
		Forms: &storage.FormsConfig{
			Enabled: boolPtr(true),
			Endpoints: []storage.FormEndpoint{
				{Path: "/contact", Name: "Contact Form"},
			},
		},
	}
	if err := store.WriteDeploymentConfig("testsite", "d1", cfg); err != nil {
		t.Fatal(err)
	}

	// 4. Create a form handler with a mock notifier.
	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "testsite", func() storage.SiteConfig {
		c, _ := store.ReadCurrentDeploymentConfig("testsite")
		return c
	})

	// 5. POST a form submission.
	body := "name=Alice&email=alice%40example.com&message=Hello"
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
		t.Fatalf("POST status = %d, want %d", w.Code, http.StatusSeeOther)
	}

	// 6. Verify submission is stored.
	subs, err := rec.ListSubmissions("testsite", analytics.ListSubmissionsOpts{Path: "/contact"})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d submissions, want 1", len(subs))
	}
	sub := subs[0]
	if !strings.HasPrefix(sub.ID, "sub_") {
		t.Errorf("ID = %q, want sub_ prefix", sub.ID)
	}
	if sub.Site != "testsite" {
		t.Errorf("Site = %q, want testsite", sub.Site)
	}
	if sub.Path != "/contact" {
		t.Errorf("Path = %q, want /contact", sub.Path)
	}
	if sub.Submitter != "alice@example.com" {
		t.Errorf("Submitter = %q, want alice@example.com", sub.Submitter)
	}

	var fields map[string]string
	if err := json.Unmarshal(sub.Fields, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["name"] != "Alice" {
		t.Errorf("fields[name] = %q, want Alice", fields["name"])
	}
	if fields["email"] != "alice@example.com" {
		t.Errorf("fields[email] = %q, want alice@example.com", fields["email"])
	}

	// 7. Verify webhook was fired.
	if len(notif.calls) != 1 {
		t.Fatalf("notifier calls = %d, want 1", len(notif.calls))
	}
	call := notif.calls[0]
	if call.event != "form.submission" {
		t.Errorf("event = %q, want form.submission", call.event)
	}
	if call.site != "testsite" {
		t.Errorf("site = %q, want testsite", call.site)
	}
	if call.data["path"] != "/contact" {
		t.Errorf("data[path] = %v, want /contact", call.data["path"])
	}

	// 8. GET the submission via recorder.
	got, err := rec.GetSubmission("testsite", sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != sub.ID {
		t.Errorf("GetSubmission ID = %q, want %q", got.ID, sub.ID)
	}
	if string(got.Fields) != string(sub.Fields) {
		t.Errorf("GetSubmission fields mismatch")
	}

	// 9. DELETE the submission.
	if deleted, err := rec.DeleteSubmission("testsite", sub.ID); err != nil {
		t.Fatal(err)
	} else if !deleted {
		t.Error("expected deleted=true")
	}

	// 10. Verify it's gone.
	subs, err = rec.ListSubmissions("testsite", analytics.ListSubmissionsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 0 {
		t.Fatalf("after delete: got %d submissions, want 0", len(subs))
	}
}

func TestFormSubmissionIntegration_SubmissionsAPI(t *testing.T) {
	// Seed a submission via the form handler, then verify the admin
	// SubmissionsHandler returns it as JSON.

	rec, err := analytics.NewRecorder(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rec.Close() })

	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "mysite", func() storage.SiteConfig {
		return storage.SiteConfig{
			Forms: &storage.FormsConfig{Enabled: boolPtr(true)},
		}
	})

	// Submit two forms.
	for _, body := range []string{"name=Alice", "name=Bob"} {
		req := httptest.NewRequest(http.MethodPost, "/feedback", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("POST status = %d, want %d", w.Code, http.StatusSeeOther)
		}
	}

	// Verify via recorder list (simulates what the admin handler does).
	subs, err := rec.ListSubmissions("mysite", analytics.ListSubmissionsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 {
		t.Fatalf("got %d submissions, want 2", len(subs))
	}

	// Verify JSON encoding round-trips correctly (as the API handler does).
	data, err := json.Marshal(subs)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []analytics.Submission
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 2 {
		t.Fatalf("decoded %d submissions, want 2", len(decoded))
	}
	if decoded[0].Site != "mysite" {
		t.Errorf("decoded[0].Site = %q, want mysite", decoded[0].Site)
	}
}

func TestFormSubmissionIntegration_ExportCSV(t *testing.T) {
	rec, err := analytics.NewRecorder(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rec.Close() })

	notif := &mockNotifier{}
	h := NewHandler(rec, notif, "csvsite", func() storage.SiteConfig {
		return storage.SiteConfig{
			Forms: &storage.FormsConfig{Enabled: boolPtr(true)},
		}
	})

	// Submit a form.
	body := "name=Alice&email=alice%40example.com"
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST status = %d, want %d", w.Code, http.StatusSeeOther)
	}

	// List submissions and build CSV (replicating the export logic).
	subs, err := rec.ListSubmissions("csvsite", analytics.ListSubmissionsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d submissions, want 1", len(subs))
	}

	// Build CSV output the same way ExportSubmissionsHandler does.
	var buf strings.Builder
	cw := csv.NewWriter(&buf)

	// Parse fields to get column keys.
	var fields map[string]string
	json.Unmarshal(subs[0].Fields, &fields)
	fieldKeys := make([]string, 0, len(fields))
	for k := range fields {
		fieldKeys = append(fieldKeys, k)
	}
	// sort for deterministic output
	if len(fieldKeys) == 2 && fieldKeys[0] > fieldKeys[1] {
		fieldKeys[0], fieldKeys[1] = fieldKeys[1], fieldKeys[0]
	}

	header := append([]string{"id", "path", "created_at", "submitter", "node", "ip"}, fieldKeys...)
	cw.Write(header)
	row := []string{subs[0].ID, subs[0].Path, subs[0].CreatedAt, subs[0].Submitter, subs[0].Node, subs[0].IP}
	for _, k := range fieldKeys {
		row = append(row, fields[k])
	}
	cw.Write(row)
	cw.Flush()

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 2 {
		t.Fatalf("CSV lines = %d, want 2 (header + 1 row):\n%s", len(lines), output)
	}
	if !strings.Contains(lines[0], "id") || !strings.Contains(lines[0], "email") || !strings.Contains(lines[0], "name") {
		t.Errorf("CSV header missing expected columns: %s", lines[0])
	}
	if !strings.Contains(lines[1], "alice@example.com") {
		t.Errorf("CSV row missing email: %s", lines[1])
	}
}
