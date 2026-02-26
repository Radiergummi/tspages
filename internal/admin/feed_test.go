package admin

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

// atomFeed is the minimal Atom structure we need for assertions.
type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Links   []atomLink  `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title   string     `xml:"title"`
	ID      string     `xml:"id"`
	Updated string     `xml:"updated"`
	Author  atomAuthor `xml:"author"`
	Links   []atomLink `xml:"link"`
	Content string     `xml:"content"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

// --- GET /feed.atom ---

func TestFeedHandler_AdminSeesAllSites(t *testing.T) {
	hs, _ := setupHandlers(t)
	req := reqWithAuth("GET", "/feed.atom", adminCaps, adminID)

	rec := httptest.NewRecorder()
	hs.Feed.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/atom+xml; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}

	var feed atomFeed
	if err := xml.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("invalid XML: %v", err)
	}
	if feed.Title != "tspages deployments" {
		t.Errorf("title = %q", feed.Title)
	}
	// setupStore creates: docs/aaa11111 (Jan 15), demo/bbb22222 (Feb 1), staging/ccc33333 (no manifest)
	// staging has no manifest so CreatedAt is zero — should still appear.
	if len(feed.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(feed.Entries))
	}
	// Sorted newest first: demo (Feb 1), docs (Jan 15), staging (zero time)
	if feed.Entries[0].Author.Name != "Bob" {
		t.Errorf("first entry author = %q, want Bob", feed.Entries[0].Author.Name)
	}
}

func TestFeedHandler_FilteredByViewAccess(t *testing.T) {
	hs, _ := setupHandlers(t)
	// viewerCaps only grants view on "docs"
	req := reqWithAuth("GET", "/feed.atom", viewerCaps, viewerID)

	rec := httptest.NewRecorder()
	hs.Feed.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var feed atomFeed
	if err := xml.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("invalid XML: %v", err)
	}
	if len(feed.Entries) != 1 {
		t.Fatalf("got %d entries, want 1 (docs only)", len(feed.Entries))
	}
	if feed.Entries[0].Author.Name != "Alice" {
		t.Errorf("entry author = %q, want Alice", feed.Entries[0].Author.Name)
	}
}

func TestFeedHandler_EntryContent(t *testing.T) {
	hs, _ := setupHandlers(t)
	req := reqWithAuth("GET", "/feed.atom", adminCaps, adminID)

	rec := httptest.NewRecorder()
	hs.Feed.ServeHTTP(rec, req)

	var feed atomFeed
	xml.Unmarshal(rec.Body.Bytes(), &feed)

	// Check the demo entry (first, newest)
	entry := feed.Entries[0]
	if entry.Title == "" {
		t.Error("entry title is empty")
	}
	if entry.ID == "" {
		t.Error("entry id is empty")
	}
	if entry.Updated == "" {
		t.Error("entry updated is empty")
	}
	// Verify the updated time parses as RFC3339
	if _, err := time.Parse(time.RFC3339, entry.Updated); err != nil {
		t.Errorf("entry updated %q is not RFC3339: %v", entry.Updated, err)
	}
	// Should have a link to the deployment page
	var hasLink bool
	for _, l := range entry.Links {
		if l.Rel == "alternate" && l.Href != "" {
			hasLink = true
		}
	}
	if !hasLink {
		t.Error("entry missing alternate link")
	}
}

// --- GET /sites/{site}/feed.atom ---

func TestSiteFeedHandler_ReturnsOnlySiteDeployments(t *testing.T) {
	hs, store := setupHandlers(t)

	// Add a second deployment to docs
	store.CreateDeployment("docs", "ddd44444")
	store.WriteManifest("docs", "ddd44444", storage.Manifest{
		Site: "docs", ID: "ddd44444",
		CreatedBy: "Alice",
		CreatedAt: time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
		SizeBytes: 512,
	})
	store.MarkComplete("docs", "ddd44444")

	req := reqWithAuth("GET", "/sites/docs/feed.atom", adminCaps, adminID)
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	hs.SiteFeed.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/atom+xml; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}

	var feed atomFeed
	if err := xml.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("invalid XML: %v", err)
	}
	// Only docs deployments
	if len(feed.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(feed.Entries))
	}
	if feed.Title != "tspages: docs" {
		t.Errorf("title = %q", feed.Title)
	}
}

func TestSiteFeedHandler_Forbidden(t *testing.T) {
	hs, _ := setupHandlers(t)
	req := reqWithAuth("GET", "/sites/demo/feed.atom", viewerCaps, viewerID)
	req.SetPathValue("site", "demo")

	rec := httptest.NewRecorder()
	hs.SiteFeed.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestSiteFeedHandler_InvalidSite(t *testing.T) {
	hs, _ := setupHandlers(t)
	req := reqWithAuth("GET", "/sites/BAD!/feed.atom", adminCaps, adminID)
	req.SetPathValue("site", "BAD!")

	rec := httptest.NewRecorder()
	hs.SiteFeed.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestFeedHandler_NoAccess(t *testing.T) {
	hs, _ := setupHandlers(t)
	// Caps with no view/deploy/admin for any site
	noCaps := []auth.Cap{{Access: "view", Sites: []string{"other"}}}
	req := reqWithAuth("GET", "/feed.atom", noCaps, viewerID)

	rec := httptest.NewRecorder()
	hs.Feed.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var feed atomFeed
	xml.Unmarshal(rec.Body.Bytes(), &feed)
	if len(feed.Entries) != 0 {
		t.Errorf("got %d entries, want 0", len(feed.Entries))
	}
}
