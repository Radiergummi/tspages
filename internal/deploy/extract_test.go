package deploy

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(content))
	}
	w.Close()
	return buf.Bytes()
}

func makeTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(content)),
		})
		tw.Write([]byte(content))
	}
	tw.Close()
	return buf.Bytes()
}

func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	tarBytes := makeTar(t, files)
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write(tarBytes)
	gw.Close()
	return buf.Bytes()
}

func makeGzSingle(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write([]byte(content))
	gw.Close()
	return buf.Bytes()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// --- ExtractZip (existing tests) ---

func TestExtractZip_Valid(t *testing.T) {
	dir := t.TempDir()
	data := makeZip(t, map[string]string{
		"index.html":       "<h1>Hello</h1>",
		"assets/style.css": "body{}",
	})
	n, err := ExtractZip(bytes.NewReader(data), int64(len(data)), dir, 10<<20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n == 0 {
		t.Error("expected nonzero bytes written")
	}
	if got := readFile(t, filepath.Join(dir, "index.html")); got != "<h1>Hello</h1>" {
		t.Errorf("index.html = %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "assets", "style.css")); got != "body{}" {
		t.Errorf("style.css = %q", got)
	}
}

func TestExtractZip_ZipSlip(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("../../../etc/passwd")
	f.Write([]byte("pwned"))
	w.Close()

	dir := t.TempDir()
	_, err := ExtractZip(bytes.NewReader(buf.Bytes()), int64(buf.Len()), dir, 10<<20)
	if err == nil {
		t.Fatal("expected zip-slip to be rejected")
	}
}

func TestExtractZip_SizeLimit(t *testing.T) {
	data := makeZip(t, map[string]string{
		"big.txt": string(make([]byte, 1000)),
	})
	dir := t.TempDir()
	_, err := ExtractZip(bytes.NewReader(data), int64(len(data)), dir, 100)
	if err == nil {
		t.Fatal("expected size limit error")
	}
}

// --- Extract (multi-format) ---

func TestExtract_Zip(t *testing.T) {
	dir := t.TempDir()
	body := makeZip(t, map[string]string{"index.html": "<h1>hi</h1>"})
	_, err := Extract(ExtractRequest{Body: body}, dir, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "index.html")); got != "<h1>hi</h1>" {
		t.Errorf("got %q", got)
	}
}

func TestExtract_Tar(t *testing.T) {
	dir := t.TempDir()
	body := makeTar(t, map[string]string{"index.html": "<p>tar</p>"})
	_, err := Extract(ExtractRequest{Body: body}, dir, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "index.html")); got != "<p>tar</p>" {
		t.Errorf("got %q", got)
	}
}

func TestExtract_TarGz(t *testing.T) {
	dir := t.TempDir()
	body := makeTarGz(t, map[string]string{
		"index.html": "<p>targz</p>",
		"style.css":  "body{}",
	})
	_, err := Extract(ExtractRequest{Body: body}, dir, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "index.html")); got != "<p>targz</p>" {
		t.Errorf("got %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "style.css")); got != "body{}" {
		t.Errorf("got %q", got)
	}
}

func TestExtract_GzipSingleFile(t *testing.T) {
	dir := t.TempDir()
	body := makeGzSingle(t, "<h1>compressed</h1>")
	_, err := Extract(ExtractRequest{Body: body}, dir, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "index.html")); got != "<h1>compressed</h1>" {
		t.Errorf("got %q", got)
	}
}

func TestExtract_Tar_PathTraversal(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: "../../../etc/passwd", Mode: 0644, Size: 5})
	tw.Write([]byte("pwned"))
	tw.Close()

	dir := t.TempDir()
	_, err := Extract(ExtractRequest{Body: buf.Bytes()}, dir, 10<<20)
	if err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}

func TestExtract_Tar_SizeLimit(t *testing.T) {
	body := makeTar(t, map[string]string{"big.txt": string(make([]byte, 1000))})
	dir := t.TempDir()
	_, err := Extract(ExtractRequest{Body: body}, dir, 100)
	if err == nil {
		t.Fatal("expected size limit error")
	}
}

func TestExtract_Tar_RejectsSymlink(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{
		Name:     "evil",
		Typeflag: tar.TypeSymlink,
		Linkname: "/etc/passwd",
	})
	tw.Close()

	dir := t.TempDir()
	_, err := Extract(ExtractRequest{Body: buf.Bytes()}, dir, 10<<20)
	if err == nil {
		t.Fatal("expected symlink to be rejected")
	}
}

func TestExtract_Tar_RejectsHardlink(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{
		Name:     "evil",
		Typeflag: tar.TypeLink,
		Linkname: "/etc/passwd",
	})
	tw.Close()

	dir := t.TempDir()
	_, err := Extract(ExtractRequest{Body: buf.Bytes()}, dir, 10<<20)
	if err == nil {
		t.Fatal("expected hardlink to be rejected")
	}
}

func TestExtract_Markdown_QueryParam(t *testing.T) {
	dir := t.TempDir()
	body := []byte("# Hello\n\nWorld")
	_, err := Extract(ExtractRequest{Body: body, Query: "markdown"}, dir, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, "index.html"))
	if !bytes.Contains([]byte(got), []byte("<h1>Hello</h1>")) {
		t.Errorf("expected rendered markdown heading, got:\n%s", got)
	}
	if !bytes.Contains([]byte(got), []byte("<!DOCTYPE html>")) {
		t.Error("expected HTML wrapper")
	}
}

func TestExtract_Markdown_ContentType(t *testing.T) {
	dir := t.TempDir()
	body := []byte("**bold**")
	_, err := Extract(ExtractRequest{Body: body, ContentType: "text/markdown; charset=utf-8"}, dir, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, "index.html"))
	if !bytes.Contains([]byte(got), []byte("<strong>bold</strong>")) {
		t.Errorf("expected rendered markdown, got:\n%s", got)
	}
}

func TestExtract_Markdown_URLFilename(t *testing.T) {
	dir := t.TempDir()
	body := []byte("# From URL\n\npath based")
	_, err := Extract(ExtractRequest{Body: body, Filename: "readme.md"}, dir, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, "index.html"))
	if !bytes.Contains([]byte(got), []byte("<h1>From URL</h1>")) {
		t.Errorf("expected rendered markdown, got:\n%s", got)
	}
}

func TestExtract_Markdown_ContentDisposition(t *testing.T) {
	dir := t.TempDir()
	body := []byte("- item 1\n- item 2")
	_, err := Extract(ExtractRequest{
		Body:  body,
		ContentDisposition: `attachment; filename="readme.md"`,
	}, dir, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, "index.html"))
	if !bytes.Contains([]byte(got), []byte("<li>item 1</li>")) {
		t.Errorf("expected rendered markdown list, got:\n%s", got)
	}
}

func TestExtract_Markdown_GFMAndExtensions(t *testing.T) {
	input := []byte(`# Features

| Feature | Status |
|---------|--------|
| Tables  | yes    |
| Tasks   | yes    |

- [x] done
- [ ] todo

This has a footnote[^1].

[^1]: The footnote text.

Apple
: A fruit.

She said "hello" ... and left -- goodbye.
`)
	dir := t.TempDir()
	_, err := Extract(ExtractRequest{Body: input, Query: "markdown"}, dir, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, "index.html"))

	checks := []struct {
		name, substr string
	}{
		{"table", "<table>"},
		{"task list checked", `<input checked="" disabled="" type="checkbox"`},
		{"task list unchecked", `<input disabled="" type="checkbox"`},
		{"footnote ref", "fn:1"},
		{"definition list", "<dd>A fruit.</dd>"},
		{"typographer en-dash", "&ndash;"},
		{"typographer ellipsis", "&hellip;"},
	}
	for _, c := range checks {
		if !bytes.Contains([]byte(got), []byte(c.substr)) {
			t.Errorf("%s: expected %q in output, got:\n%s", c.name, c.substr, got)
		}
	}
}

func TestExtract_HTML(t *testing.T) {
	dir := t.TempDir()
	body := []byte("<html><body>hi</body></html>")
	_, err := Extract(ExtractRequest{Body: body}, dir, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, "index.html"))
	if got != "<html><body>hi</body></html>" {
		t.Errorf("got %q", got)
	}
}

func TestExtract_PlainText_Default(t *testing.T) {
	dir := t.TempDir()
	body := []byte("just some text")
	_, err := Extract(ExtractRequest{Body: body}, dir, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, "index.html"))
	if !bytes.Contains([]byte(got), []byte("<!DOCTYPE html>")) {
		t.Error("expected HTML wrapper")
	}
	if !bytes.Contains([]byte(got), []byte("<pre>just some text</pre>")) {
		t.Errorf("expected plain text in <pre>, got:\n%s", got)
	}
}

func TestExtract_Empty(t *testing.T) {
	dir := t.TempDir()
	_, err := Extract(ExtractRequest{Body: nil}, dir, 10<<20)
	if err == nil {
		t.Fatal("expected error for empty upload")
	}
}

func TestExtract_Xz_Unsupported(t *testing.T) {
	// xz magic bytes
	body := []byte{0xfd, '7', 'z', 'X', 'Z', 0x00, 0, 0, 0, 0}
	dir := t.TempDir()
	_, err := Extract(ExtractRequest{Body: body}, dir, 10<<20)
	if err == nil {
		t.Fatal("expected error for xz")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("not supported")) {
		t.Errorf("error = %q, want mention of not supported", err)
	}
}

func TestExtract_TarGz_PathTraversal(t *testing.T) {
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	tw.WriteHeader(&tar.Header{Name: "../../../etc/passwd", Mode: 0644, Size: 5})
	tw.Write([]byte("pwned"))
	tw.Close()

	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	gw.Write(tarBuf.Bytes())
	gw.Close()

	dir := t.TempDir()
	_, err := Extract(ExtractRequest{Body: gzBuf.Bytes()}, dir, 10<<20)
	if err == nil {
		t.Fatal("expected path traversal in tar.gz to be rejected")
	}
}

func TestExtract_Gzip_SizeLimit(t *testing.T) {
	body := makeGzSingle(t, string(make([]byte, 1000)))
	dir := t.TempDir()
	_, err := Extract(ExtractRequest{Body: body}, dir, 100)
	if err == nil {
		t.Fatal("expected gzip decompression size limit error")
	}
}
