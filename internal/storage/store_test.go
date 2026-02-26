package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateSite(t *testing.T) {
	s := New(t.TempDir())
	if err := s.CreateSite("docs"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// deployments subdir should exist
	info, err := os.Stat(filepath.Join(s.dataDir, "sites", "docs", "deployments"))
	if err != nil {
		t.Fatalf("deployments dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected directory")
	}
}

func TestCreateSite_AlreadyExists(t *testing.T) {
	s := New(t.TempDir())
	if err := s.CreateSite("docs"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := s.CreateSite("docs"); !errors.Is(err, ErrSiteExists) {
		t.Fatalf("got %v, want ErrSiteExists", err)
	}
}

func TestCreateSite_InvalidName(t *testing.T) {
	s := New(t.TempDir())
	if err := s.CreateSite(".."); err == nil {
		t.Fatal("expected error for invalid site name")
	}
}

func TestCreateDeployment(t *testing.T) {
	s := New(t.TempDir())
	path, err := s.CreateDeployment("docs", "abc12345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("deployment dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected directory")
	}
}

func TestActivateAndCurrent(t *testing.T) {
	s := New(t.TempDir())
	path, _ := s.CreateDeployment("docs", "abc12345")
	os.WriteFile(filepath.Join(path, "index.html"), []byte("hello"), 0644)
	s.MarkComplete("docs", "abc12345")

	if err := s.ActivateDeployment("docs", "abc12345"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	cur, err := s.CurrentDeployment("docs")
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if cur != "abc12345" {
		t.Errorf("current = %q, want %q", cur, "abc12345")
	}
}

func TestActivateSwitchesSymlink(t *testing.T) {
	s := New(t.TempDir())
	s.CreateDeployment("docs", "aaa11111")
	s.MarkComplete("docs", "aaa11111")
	s.ActivateDeployment("docs", "aaa11111")

	s.CreateDeployment("docs", "bbb22222")
	s.MarkComplete("docs", "bbb22222")
	s.ActivateDeployment("docs", "bbb22222")

	cur, _ := s.CurrentDeployment("docs")
	if cur != "bbb22222" {
		t.Errorf("current = %q, want %q", cur, "bbb22222")
	}
}

func TestSiteRoot(t *testing.T) {
	s := New(t.TempDir())
	root := s.SiteRoot("docs")
	if filepath.Base(root) != "content" {
		t.Errorf("site root should end in 'content', got %q", root)
	}
	if filepath.Base(filepath.Dir(root)) != "current" {
		t.Errorf("site root parent should be 'current', got %q", root)
	}
}

func TestCurrentDeployment_NoActive(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.CurrentDeployment("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent site")
	}
}

func TestListSites(t *testing.T) {
	s := New(t.TempDir())
	s.CreateDeployment("docs", "aaa11111")
	s.MarkComplete("docs", "aaa11111")
	s.ActivateDeployment("docs", "aaa11111")
	s.CreateDeployment("demo", "bbb22222")
	s.MarkComplete("demo", "bbb22222")
	s.ActivateDeployment("demo", "bbb22222")

	sites, err := s.ListSites()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sites) != 2 {
		t.Fatalf("got %d sites, want 2", len(sites))
	}
}

func TestCleanupOrphans(t *testing.T) {
	s := New(t.TempDir())

	// Complete deployment — should survive cleanup
	path1, _ := s.CreateDeployment("docs", "complete1")
	os.WriteFile(filepath.Join(path1, "index.html"), []byte("hi"), 0644)
	s.MarkComplete("docs", "complete1")

	// Incomplete deployment — should be removed
	s.CreateDeployment("docs", "orphan01")

	s.CleanupOrphans()

	// complete1 should still exist
	if _, err := os.Stat(path1); err != nil {
		t.Errorf("complete deployment was removed: %v", err)
	}
	// orphan should be gone
	orphanPath := filepath.Join(s.dataDir, "sites", "docs", "deployments", "orphan01")
	if _, err := os.Stat(orphanPath); err == nil {
		t.Error("orphan deployment was not removed")
	}
}

func TestDeleteSite(t *testing.T) {
	s := New(t.TempDir())
	s.CreateDeployment("docs", "aaa11111")
	s.MarkComplete("docs", "aaa11111")
	s.ActivateDeployment("docs", "aaa11111")

	if err := s.DeleteSite("docs"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Site directory should be gone
	siteDir := filepath.Join(s.dataDir, "sites", "docs")
	if _, err := os.Stat(siteDir); !os.IsNotExist(err) {
		t.Error("site directory still exists after deletion")
	}
}

func TestDeleteSite_InvalidName(t *testing.T) {
	s := New(t.TempDir())
	if err := s.DeleteSite(".."); err == nil {
		t.Fatal("expected error for invalid site name")
	}
}

func TestDeleteSite_Nonexistent(t *testing.T) {
	s := New(t.TempDir())
	// Should not error — RemoveAll on nonexistent path is a no-op
	if err := s.DeleteSite("nope"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListDeployments(t *testing.T) {
	s := New(t.TempDir())
	s.CreateDeployment("docs", "aaa11111")
	s.MarkComplete("docs", "aaa11111")
	s.CreateDeployment("docs", "bbb22222")
	s.MarkComplete("docs", "bbb22222")
	s.ActivateDeployment("docs", "bbb22222")

	// Incomplete deployment — should not appear
	s.CreateDeployment("docs", "orphan01")

	deps, err := s.ListDeployments("docs")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("got %d deployments, want 2", len(deps))
	}

	active := 0
	for _, d := range deps {
		if d.Active {
			active++
			if d.ID != "bbb22222" {
				t.Errorf("active deployment = %q, want %q", d.ID, "bbb22222")
			}
		}
	}
	if active != 1 {
		t.Errorf("got %d active deployments, want 1", active)
	}
}

func TestListDeployments_NoSite(t *testing.T) {
	s := New(t.TempDir())
	deps, err := s.ListDeployments("nope")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deps != nil {
		t.Errorf("expected nil, got %v", deps)
	}
}

func TestDeleteDeployment(t *testing.T) {
	s := New(t.TempDir())
	s.CreateDeployment("docs", "aaa11111")
	s.MarkComplete("docs", "aaa11111")
	s.CreateDeployment("docs", "bbb22222")
	s.MarkComplete("docs", "bbb22222")
	s.ActivateDeployment("docs", "bbb22222")

	// Can delete inactive deployment
	if err := s.DeleteDeployment("docs", "aaa11111"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	deps, _ := s.ListDeployments("docs")
	if len(deps) != 1 {
		t.Fatalf("got %d deployments, want 1", len(deps))
	}
	if deps[0].ID != "bbb22222" {
		t.Errorf("remaining deployment = %q, want bbb22222", deps[0].ID)
	}
}

func TestDeleteDeployment_Active(t *testing.T) {
	s := New(t.TempDir())
	s.CreateDeployment("docs", "aaa11111")
	s.MarkComplete("docs", "aaa11111")
	s.ActivateDeployment("docs", "aaa11111")

	err := s.DeleteDeployment("docs", "aaa11111")
	if !errors.Is(err, ErrActiveDeployment) {
		t.Fatalf("got %v, want ErrActiveDeployment", err)
	}
}

func TestDeleteDeployment_NotFound(t *testing.T) {
	s := New(t.TempDir())
	s.CreateDeployment("docs", "aaa11111")
	s.MarkComplete("docs", "aaa11111")

	err := s.DeleteDeployment("docs", "nonexistent")
	if !errors.Is(err, ErrDeploymentNotFound) {
		t.Fatalf("got %v, want ErrDeploymentNotFound", err)
	}
}

func TestDeleteDeployment_InvalidSite(t *testing.T) {
	s := New(t.TempDir())
	if err := s.DeleteDeployment("..", "abc"); err == nil {
		t.Fatal("expected error for invalid site name")
	}
}

func TestDeleteDeployment_InvalidID(t *testing.T) {
	s := New(t.TempDir())
	s.CreateDeployment("docs", "aaa11111")
	s.MarkComplete("docs", "aaa11111")

	for _, id := range []string{"", ".", "..", "../other", "a/b", "a\\b"} {
		if err := s.DeleteDeployment("docs", id); err != ErrDeploymentNotFound {
			t.Errorf("DeleteDeployment(%q) = %v, want ErrDeploymentNotFound", id, err)
		}
	}
}

func TestValidSiteName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"docs", true},
		{"my-site", true},
		{"site2", true},
		{"a", true},
		{"", false},
		{"..", false},
		{"../etc", false},
		{"a.b", false},
		{"a/b", false},
		{"a\\b", false},
		{"site_v2", false},    // underscores not allowed
		{"Docs", false},       // uppercase not allowed
		{"-leading", false},   // leading hyphen
		{"trailing-", false},  // trailing hyphen
		{"has space", false},  // spaces not allowed
		{"a\x00b", false},    // null byte
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidSiteName(tt.name); got != tt.want {
				t.Errorf("ValidSiteName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestMaxSiteNameLen(t *testing.T) {
	tests := []struct {
		suffix string
		want   int
	}{
		{"", 63},                    // no suffix → DNS label max
		{"tail1234.ts.net", 63},     // short suffix → still capped at 63
		{"tailnet.ts.net", 63},      // 14 chars → 253-1-14=238, capped at 63
		{string(make([]byte, 200)), 52}, // 200-char suffix → 253-1-200=52
		{string(make([]byte, 252)), 1},  // extreme → floor at 1
	}
	for _, tt := range tests {
		got := MaxSiteNameLen(tt.suffix)
		if got != tt.want {
			t.Errorf("MaxSiteNameLen(%d-char suffix) = %d, want %d", len(tt.suffix), got, tt.want)
		}
	}
}

func TestValidSiteNameForSuffix(t *testing.T) {
	// 200-char suffix → max name len = 52
	longSuffix := string(make([]byte, 200))

	if !ValidSiteNameForSuffix("docs", longSuffix) {
		t.Error("short name should be valid with long suffix")
	}

	longName := strings.Repeat("a", 53)
	if ValidSiteNameForSuffix(longName, longSuffix) {
		t.Error("53-char name should be invalid with 200-char suffix")
	}

	if ValidSiteNameForSuffix("..", "test.ts.net") {
		t.Error("invalid label should still be rejected")
	}
}

func TestWriteReadManifest(t *testing.T) {
	s := New(t.TempDir())
	s.CreateDeployment("docs", "abc12345")

	now := time.Now().Truncate(time.Second)
	m := Manifest{
		Site:      "docs",
		ID:        "abc12345",
		CreatedAt: now,
		CreatedBy: "alice@example.com",
		SizeBytes: 4096,
	}
	if err := s.WriteManifest("docs", "abc12345", m); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.ReadManifest("docs", "abc12345")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Site != "docs" || got.ID != "abc12345" {
		t.Errorf("site/id = %q/%q", got.Site, got.ID)
	}
	if got.CreatedBy != "alice@example.com" {
		t.Errorf("created_by = %q", got.CreatedBy)
	}
	if got.SizeBytes != 4096 {
		t.Errorf("size_bytes = %d", got.SizeBytes)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("created_at = %v, want %v", got.CreatedAt, now)
	}
}

func TestReadManifest_Missing(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.ReadManifest("docs", "nope")
	if err == nil {
		t.Fatal("expected error for missing manifest")
	}
}

func TestListDeployments_WithManifest(t *testing.T) {
	s := New(t.TempDir())
	s.CreateDeployment("docs", "aaa11111")
	s.WriteManifest("docs", "aaa11111", Manifest{
		CreatedBy: "alice@example.com",
		SizeBytes: 1024,
	})
	s.MarkComplete("docs", "aaa11111")
	s.ActivateDeployment("docs", "aaa11111")

	// Deployment without manifest (simulates old deployment)
	s.CreateDeployment("docs", "bbb22222")
	s.MarkComplete("docs", "bbb22222")

	deps, err := s.ListDeployments("docs")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("got %d deployments, want 2", len(deps))
	}

	for _, d := range deps {
		if d.ID == "aaa11111" {
			if d.CreatedBy != "alice@example.com" {
				t.Errorf("aaa11111 created_by = %q", d.CreatedBy)
			}
			if d.SizeBytes != 1024 {
				t.Errorf("aaa11111 size_bytes = %d", d.SizeBytes)
			}
		}
		if d.ID == "bbb22222" {
			// No manifest — should have zero values
			if d.CreatedBy != "" {
				t.Errorf("bbb22222 created_by = %q, want empty", d.CreatedBy)
			}
		}
	}
}

func TestCreateDeployment_InvalidSiteName(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.CreateDeployment("..", "abc12345")
	if err == nil {
		t.Fatal("expected error for invalid site name")
	}
}

func TestDeleteInactiveDeployments(t *testing.T) {
	store := New(t.TempDir())
	store.CreateDeployment("docs", "aaa11111")
	store.MarkComplete("docs", "aaa11111")
	store.CreateDeployment("docs", "bbb22222")
	store.MarkComplete("docs", "bbb22222")
	store.CreateDeployment("docs", "ccc33333")
	store.MarkComplete("docs", "ccc33333")
	store.ActivateDeployment("docs", "bbb22222")

	n, err := store.DeleteInactiveDeployments("docs")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("deleted = %d, want 2", n)
	}

	deps, _ := store.ListDeployments("docs")
	if len(deps) != 1 {
		t.Fatalf("remaining = %d, want 1", len(deps))
	}
	if deps[0].ID != "bbb22222" {
		t.Errorf("remaining ID = %q, want bbb22222", deps[0].ID)
	}
}

func TestListDeploymentFiles(t *testing.T) {
	s := New(t.TempDir())
	dir, _ := s.CreateDeployment("docs", "aaa11111")
	contentDir := filepath.Join(dir, "content")
	os.MkdirAll(filepath.Join(contentDir, "assets"), 0755)
	os.WriteFile(filepath.Join(contentDir, "index.html"), []byte("<h1>hi</h1>"), 0644)
	os.WriteFile(filepath.Join(contentDir, "assets", "style.css"), []byte("body{}"), 0644)
	s.MarkComplete("docs", "aaa11111")

	files, err := s.ListDeploymentFiles("docs", "aaa11111")
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
	// Should be sorted alphabetically
	if files[0].Path != "assets/style.css" {
		t.Errorf("files[0] = %q, want assets/style.css", files[0].Path)
	}
	if files[1].Path != "index.html" {
		t.Errorf("files[1] = %q, want index.html", files[1].Path)
	}
	if files[0].Size != 6 {
		t.Errorf("style.css size = %d, want 6", files[0].Size)
	}
}

func TestListDeploymentFiles_NoContentDir(t *testing.T) {
	s := New(t.TempDir())
	s.CreateDeployment("docs", "aaa11111")
	s.MarkComplete("docs", "aaa11111")

	files, err := s.ListDeploymentFiles("docs", "aaa11111")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("got %d files, want 0", len(files))
	}
}

func TestCleanupOldDeployments(t *testing.T) {
	s := New(t.TempDir())

	// Create 5 deployments with increasing timestamps.
	ids := []string{"aaa11111", "bbb22222", "ccc33333", "ddd44444", "eee55555"}
	for i, id := range ids {
		s.CreateDeployment("docs", id)
		s.WriteManifest("docs", id, Manifest{
			CreatedAt: time.Date(2025, 1, 1+i, 0, 0, 0, 0, time.UTC),
		})
		s.MarkComplete("docs", id)
	}
	// Activate the newest.
	s.ActivateDeployment("docs", "eee55555")

	// Keep 3 → should delete 2 oldest (aaa11111, bbb22222).
	n, err := s.CleanupOldDeployments("docs", 3)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("deleted = %d, want 2", n)
	}

	deps, _ := s.ListDeployments("docs")
	if len(deps) != 3 {
		t.Fatalf("remaining = %d, want 3", len(deps))
	}

	remaining := map[string]bool{}
	for _, d := range deps {
		remaining[d.ID] = true
	}
	for _, want := range []string{"ccc33333", "ddd44444", "eee55555"} {
		if !remaining[want] {
			t.Errorf("expected %s to survive cleanup", want)
		}
	}
}

func TestCleanupOldDeployments_KeepsActive(t *testing.T) {
	s := New(t.TempDir())

	// Create 3 deployments. Activate the oldest.
	ids := []string{"aaa11111", "bbb22222", "ccc33333"}
	for i, id := range ids {
		s.CreateDeployment("docs", id)
		s.WriteManifest("docs", id, Manifest{
			CreatedAt: time.Date(2025, 1, 1+i, 0, 0, 0, 0, time.UTC),
		})
		s.MarkComplete("docs", id)
	}
	s.ActivateDeployment("docs", "aaa11111")

	// Keep 2 → would delete aaa11111 (oldest), but it's active so it survives.
	n, err := s.CleanupOldDeployments("docs", 2)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("deleted = %d, want 0 (active deployment protects oldest)", n)
	}

	deps, _ := s.ListDeployments("docs")
	if len(deps) != 3 {
		t.Fatalf("remaining = %d, want 3", len(deps))
	}
}

func TestCleanupOldDeployments_UnderLimit(t *testing.T) {
	s := New(t.TempDir())
	s.CreateDeployment("docs", "aaa11111")
	s.MarkComplete("docs", "aaa11111")

	n, err := s.CleanupOldDeployments("docs", 10)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("deleted = %d, want 0", n)
	}
}

func TestNewDeploymentID(t *testing.T) {
	id := NewDeploymentID()
	if len(id) != 8 {
		t.Errorf("id length = %d, want 8", len(id))
	}
	// Should be different each time
	id2 := NewDeploymentID()
	if id == id2 {
		t.Error("two generated IDs should differ")
	}
}
