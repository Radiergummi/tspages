package cli

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveServer_FlagWins(t *testing.T) {
	got := resolveServer("https://flag.example.com", "https://env.example.com", nil)
	if got != "https://flag.example.com" {
		t.Errorf("got %q, want flag value", got)
	}
}

func TestResolveServer_EnvFallback(t *testing.T) {
	got := resolveServer("", "https://env.example.com", nil)
	if got != "https://env.example.com" {
		t.Errorf("got %q, want env value", got)
	}
}

func TestResolveServer_AutoDiscover(t *testing.T) {
	discover := func() (string, error) { return "https://pages.test.ts.net", nil }
	got := resolveServer("", "", discover)
	if got != "https://pages.test.ts.net" {
		t.Errorf("got %q, want discovered value", got)
	}
}

func TestResolveServer_NothingFound(t *testing.T) {
	got := resolveServer("", "", nil)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestResolveServer_TrailingSlash(t *testing.T) {
	got := resolveServer("https://example.com/", "", nil)
	if got != "https://example.com" {
		t.Errorf("got %q, want trailing slash stripped", got)
	}
}

func TestPrepareBody_Directory(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi</h1>"), 0644)
	os.MkdirAll(filepath.Join(dir, "css"), 0755)
	os.WriteFile(filepath.Join(dir, "css", "style.css"), []byte("body{}"), 0644)

	body, filename, err := prepareBody(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filename != "" {
		t.Errorf("directory should have no filename, got %q", filename)
	}

	// Verify it's a valid ZIP containing the expected files.
	r, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	names := make(map[string]bool)
	for _, f := range r.File {
		names[f.Name] = true
	}
	if !names["index.html"] {
		t.Error("zip missing index.html")
	}
	if !names["css/style.css"] {
		t.Error("zip missing css/style.css")
	}
}

func TestPrepareBody_File(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "README.md")
	os.WriteFile(p, []byte("# Hello"), 0644)

	body, filename, err := prepareBody(p)
	if err != nil {
		t.Fatal(err)
	}
	if filename != "README.md" {
		t.Errorf("filename = %q, want README.md", filename)
	}
	if string(body) != "# Hello" {
		t.Errorf("body = %q", body)
	}
}

func TestPrepareBody_NotFound(t *testing.T) {
	_, _, err := prepareBody("/nonexistent/path")
	if err == nil {
		t.Error("expected error for missing path")
	}
}
