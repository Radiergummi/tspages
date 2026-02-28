package deploy

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"strings"

	"github.com/ulikunitz/xz"
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
	if !bytes.Contains([]byte(got), []byte("<!doctype html>")) {
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
		Body:               body,
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
	if !bytes.Contains([]byte(got), []byte("<!doctype html>")) {
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

func TestExtract_TarXz(t *testing.T) {
	// Build a tar archive with one file.
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	content := []byte("hello from xz")
	tw.WriteHeader(&tar.Header{Name: "index.html", Mode: 0644, Size: int64(len(content))})
	tw.Write(content)
	tw.Close()

	// Compress with xz.
	var xzBuf bytes.Buffer
	xw, err := xz.NewWriter(&xzBuf)
	if err != nil {
		t.Fatal(err)
	}
	xw.Write(tarBuf.Bytes())
	xw.Close()

	dir := t.TempDir()
	n, err := Extract(ExtractRequest{Body: xzBuf.Bytes()}, dir, 10<<20)
	if err != nil {
		t.Fatalf("Extract tar.xz: %v", err)
	}
	if n != int64(len(content)) {
		t.Errorf("wrote %d bytes, want %d", n, len(content))
	}
	got, _ := os.ReadFile(filepath.Join(dir, "index.html"))
	if string(got) != "hello from xz" {
		t.Errorf("content = %q, want %q", got, "hello from xz")
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

func TestIsTar_FalsePositiveAtOffset257(t *testing.T) {
	// A binary blob with "ustar" at offset 257 looks like tar to isTar().
	// This test documents the behavior.
	blob := make([]byte, 300)
	copy(blob[257:], "ustar")
	if !isTar(blob) {
		t.Error("expected isTar to return true for blob with ustar at offset 257")
	}

	// When gzip-wrapped, this would be misrouted to extractTar.
	// Verify that extractTar fails gracefully (returns an error, not panic).
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	gw.Write(blob)
	gw.Close()

	dir := t.TempDir()
	_, err := Extract(ExtractRequest{Body: gzBuf.Bytes()}, dir, 10<<20)
	if err == nil {
		t.Fatal("expected error when extractTar processes non-tar data with ustar magic")
	}
}

func TestExtract_Tar_RejectsUnknownEntryType(t *testing.T) {
	// Exotic tar entry types (FIFO, char device, etc.) should be rejected.
	// Note: PAX headers (TypeXGlobalHeader, TypeXHeader) are handled
	// transparently by Go's archive/tar and never reach this code path.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{
		Name:     "fifo-entry",
		Typeflag: tar.TypeFifo,
	})
	tw.Close()

	dir := t.TempDir()
	_, err := Extract(ExtractRequest{Body: buf.Bytes()}, dir, 10<<20)
	if err == nil {
		t.Fatal("expected unknown entry type to be rejected")
	}
	if !strings.Contains(err.Error(), "unsupported tar entry type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSafePath_DotEntry(t *testing.T) {
	dir := t.TempDir()
	dest, err := safePath(dir, ".")
	if err != nil {
		t.Fatalf("safePath(\".\") returned error: %v", err)
	}
	// safePath allows "." (resolves to destDir itself).
	// Callers must handle this — os.Create on a directory will fail.
	if dest != filepath.Clean(dir) {
		t.Errorf("dest = %q, want %q", dest, filepath.Clean(dir))
	}
}

func TestExtractZip_TrailingSlashNonDir(t *testing.T) {
	// A ZIP entry with a name ending in "/" but with the directory mode bit
	// cleared should be handled as a directory (by the name convention),
	// not fall through to os.Create.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	// Create an entry with explicit header to control the name.
	hdr := &zip.FileHeader{Name: "subdir/"}
	hdr.SetMode(0644) // file mode, not directory
	wr, err := w.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	_ = wr // no content for directory entry
	// Also add a file inside it.
	f, _ := w.Create("subdir/file.txt")
	f.Write([]byte("hello"))
	w.Close()

	dir := t.TempDir()
	_, err = ExtractZip(bytes.NewReader(buf.Bytes()), int64(buf.Len()), dir, 10<<20)
	if err != nil {
		t.Fatalf("ExtractZip error: %v", err)
	}
	// The subdir should exist as a directory.
	info, err := os.Stat(filepath.Join(dir, "subdir"))
	if err != nil {
		t.Fatalf("subdir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("subdir should be a directory")
	}
	got := readFile(t, filepath.Join(dir, "subdir", "file.txt"))
	if got != "hello" {
		t.Errorf("file.txt = %q, want %q", got, "hello")
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

func TestExtract_Xz_SizeLimit(t *testing.T) {
	// Compress 1000 bytes with xz, then try to extract with a 100-byte limit.
	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	xw.Write(make([]byte, 1000))
	xw.Close()

	dir := t.TempDir()
	_, err = Extract(ExtractRequest{Body: buf.Bytes()}, dir, 100)
	if err == nil {
		t.Fatal("expected xz decompression size limit error")
	}
}
