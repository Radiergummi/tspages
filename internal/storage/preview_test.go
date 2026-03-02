package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractPreview(t *testing.T) {
	dir := t.TempDir()
	html := `<!DOCTYPE html>
<html>
<head>
	<title>Fallback Title</title>
	<meta property="og:title" content="My Site">
	<meta property="og:description" content="A great site">
	<meta property="og:image" content="/img/banner.png">
	<link rel="icon" href="/favicon.svg">
</head>
<body><p>hello</p></body>
</html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(html), 0644); err != nil {
		t.Fatal(err)
	}

	p := ExtractPreview(dir, "index.html")

	if p.Title != "My Site" {
		t.Errorf("Title = %q, want %q", p.Title, "My Site")
	}
	if p.Description != "A great site" {
		t.Errorf("Description = %q, want %q", p.Description, "A great site")
	}
	if p.OGImage != "/img/banner.png" {
		t.Errorf("OGImage = %q, want %q", p.OGImage, "/img/banner.png")
	}
	if p.Favicon != "/favicon.svg" {
		t.Errorf("Favicon = %q, want %q", p.Favicon, "/favicon.svg")
	}
}

func TestExtractPreview_FallbackTitle(t *testing.T) {
	dir := t.TempDir()
	html := `<!DOCTYPE html>
<html>
<head><title>Page Title</title></head>
<body></body>
</html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(html), 0644); err != nil {
		t.Fatal(err)
	}

	p := ExtractPreview(dir, "index.html")

	if p.Title != "Page Title" {
		t.Errorf("Title = %q, want %q", p.Title, "Page Title")
	}
}

func TestExtractPreview_FaviconFallback(t *testing.T) {
	dir := t.TempDir()
	html := `<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body></body>
</html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(html), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "favicon.ico"), []byte("icon"), 0644); err != nil {
		t.Fatal(err)
	}

	p := ExtractPreview(dir, "index.html")

	if p.Favicon != "/favicon.ico" {
		t.Errorf("Favicon = %q, want %q", p.Favicon, "/favicon.ico")
	}
}

func TestExtractPreview_NoIndex(t *testing.T) {
	dir := t.TempDir()

	p := ExtractPreview(dir, "index.html")

	if p != (Preview{}) {
		t.Errorf("expected zero Preview, got %+v", p)
	}
}

func TestExtractPreview_MetaDescription(t *testing.T) {
	dir := t.TempDir()
	html := `<!DOCTYPE html>
<html>
<head>
	<meta name="description" content="Meta description text">
</head>
<body></body>
</html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(html), 0644); err != nil {
		t.Fatal(err)
	}

	p := ExtractPreview(dir, "index.html")

	if p.Description != "Meta description text" {
		t.Errorf("Description = %q, want %q", p.Description, "Meta description text")
	}
}

func TestExtractPreview_AppleTouchIcon(t *testing.T) {
	dir := t.TempDir()
	html := `<!DOCTYPE html>
<html>
<head>
	<link rel="apple-touch-icon" href="/apple-icon.png">
</head>
<body></body>
</html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(html), 0644); err != nil {
		t.Fatal(err)
	}

	p := ExtractPreview(dir, "index.html")

	if p.Favicon != "/apple-icon.png" {
		t.Errorf("Favicon = %q, want %q", p.Favicon, "/apple-icon.png")
	}
}
