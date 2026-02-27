package deploy

import (
	"archive/tar"
	"archive/zip"
	"errors"
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/ulikunitz/xz"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

//go:embed templates/markdown.html
var markdownTmplStr string

//go:embed templates/plaintext.html
var plaintextTmplStr string

var (
	markdownTmpl  = template.Must(template.New("markdown").Parse(markdownTmplStr))
	plaintextTmpl = template.Must(template.New("plaintext").Parse(plaintextTmplStr))
)

// ExtractRequest carries the upload body and request metadata needed for
// format detection.
type ExtractRequest struct {
	Body               []byte
	Query              string // r.URL.Query().Get("format")
	ContentType        string
	ContentDisposition string
	Filename           string // from URL path, e.g. PUT /deploy/{site}/{filename}
}

// Extract detects the upload format and extracts/writes content into destDir.
// Archives are detected by magic bytes. For non-archive content, the text
// format is resolved from: query param → Content-Type → Content-Disposition
// filename → default (plain text).
func Extract(req ExtractRequest, destDir string, maxBytes int64) (int64, error) {
	body := req.Body
	if len(body) == 0 {
		return 0, fmt.Errorf("empty upload")
	}

	// Archive detection by magic bytes.
	switch {
	case isZip(body):
		return ExtractZip(bytes.NewReader(body), int64(len(body)), destDir, maxBytes)
	case isGzip(body):
		return extractGzip(body, destDir, maxBytes)
	case isXz(body):
		return extractXz(body, destDir, maxBytes)
	case isTar(body):
		return extractTar(bytes.NewReader(body), destDir, maxBytes)
	}

	// Non-archive: determine text format.
	if isMarkdown(req) {
		return writeMarkdown(body, destDir)
	}
	if !looksLikeHTML(body) {
		return writePlaintext(body, destDir)
	}
	return writeSingleFile(body, destDir, "index.html")
}

// Magic byte checks.

func isZip(b []byte) bool {
	return len(b) >= 4 && b[0] == 'P' && b[1] == 'K' && b[2] == 0x03 && b[3] == 0x04
}

func isGzip(b []byte) bool {
	return len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b
}

func isXz(b []byte) bool {
	return len(b) >= 6 && b[0] == 0xfd && b[1] == '7' && b[2] == 'z' && b[3] == 'X' && b[4] == 'Z' && b[5] == 0x00
}

func isTar(b []byte) bool {
	// The "ustar" magic appears at offset 257 in a tar file.
	return len(b) >= 263 && string(b[257:262]) == "ustar"
}

// Format resolution for non-archive uploads.

func isMarkdown(req ExtractRequest) bool {
	if req.Query == "markdown" {
		return true
	}
	if mediaType(req.ContentType) == "text/markdown" {
		return true
	}
	// Check filename from URL path (PUT /deploy/{site}/{filename})
	if hasMarkdownExt(req.Filename) {
		return true
	}
	// Check filename from Content-Disposition header
	if req.ContentDisposition != "" {
		_, params, _ := mime.ParseMediaType(req.ContentDisposition)
		if hasMarkdownExt(params["filename"]) {
			return true
		}
	}
	return false
}

func hasMarkdownExt(name string) bool {
	if name == "" {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".md" || ext == ".markdown"
}

func mediaType(ct string) string {
	mt, _, _ := mime.ParseMediaType(ct)
	return mt
}

// Archive extractors.

func extractGzip(body []byte, destDir string, maxBytes int64) (int64, error) {
	gr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("reading gzip: %w", err)
	}
	defer gr.Close()

	// Read decompressed content (up to limit).
	inner, err := io.ReadAll(io.LimitReader(gr, maxBytes+1))
	if err != nil {
		return 0, fmt.Errorf("decompressing gzip: %w", err)
	}
	if int64(len(inner)) > maxBytes {
		return 0, fmt.Errorf("decompressed size exceeds limit of %d bytes", maxBytes)
	}

	// Check if inner content is a tar archive.
	if isTar(inner) {
		return extractTar(bytes.NewReader(inner), destDir, maxBytes)
	}

	// Single compressed file.
	return writeSingleFile(inner, destDir, "index.html")
}

func extractXz(body []byte, destDir string, maxBytes int64) (int64, error) {
	inner, err := decompressXz(bytes.NewReader(body), maxBytes)
	if err != nil {
		return 0, err
	}

	if isTar(inner) {
		return extractTar(bytes.NewReader(inner), destDir, maxBytes)
	}

	return writeSingleFile(inner, destDir, "index.html")
}

func extractTar(r io.Reader, destDir string, maxBytes int64) (int64, error) {
	tr := tar.NewReader(r)
	var totalWritten int64

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return totalWritten, fmt.Errorf("reading tar: %w", err)
		}

		name := filepath.Clean(hdr.Name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return totalWritten, fmt.Errorf("path traversal detected: %q", hdr.Name)
		}
		dest := filepath.Join(destDir, name)
		if !strings.HasPrefix(dest, filepath.Clean(destDir)+string(os.PathSeparator)) && dest != filepath.Clean(destDir) {
			return totalWritten, fmt.Errorf("path traversal detected: %q", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeSymlink, tar.TypeLink:
			// Symlinks and hardlinks are rejected — they could escape the
			// deployment directory or be used for write-through attacks.
			return totalWritten, fmt.Errorf("unsupported tar entry type (symlink/hardlink): %q", hdr.Name)
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0755); err != nil {
				return totalWritten, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return totalWritten, err
			}
			out, err := os.Create(dest)
			if err != nil {
				return totalWritten, err
			}
			n, err := io.Copy(out, io.LimitReader(tr, maxBytes-totalWritten+1))
			out.Close()
			totalWritten += n
			if err != nil {
				return totalWritten, err
			}
			if totalWritten > maxBytes {
				return totalWritten, fmt.Errorf("extracted size exceeds limit of %d bytes", maxBytes)
			}
		}
	}
	return totalWritten, nil
}

// Single file writers.

func writeSingleFile(content []byte, destDir, filename string) (int64, error) {
	dest := filepath.Join(destDir, filename)
	if err := os.WriteFile(dest, content, 0644); err != nil {
		return 0, err
	}
	return int64(len(content)), nil
}

var md = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.DefinitionList,
		extension.Footnote,
		extension.Typographer,
	),
)

func writeMarkdown(body []byte, destDir string) (int64, error) {
	var rendered bytes.Buffer
	if err := md.Convert(body, &rendered); err != nil {
		return 0, fmt.Errorf("rendering markdown: %w", err)
	}
	var buf bytes.Buffer
	if err := markdownTmpl.Execute(&buf, struct{ Body template.HTML }{template.HTML(rendered.Bytes())}); err != nil {
		return 0, fmt.Errorf("rendering markdown wrapper: %w", err)
	}
	return writeSingleFile(buf.Bytes(), destDir, "index.html")
}

func writePlaintext(body []byte, destDir string) (int64, error) {
	var buf bytes.Buffer
	if err := plaintextTmpl.Execute(&buf, struct{ Body string }{string(body)}); err != nil {
		return 0, fmt.Errorf("rendering plaintext wrapper: %w", err)
	}
	return writeSingleFile(buf.Bytes(), destDir, "index.html")
}

func looksLikeHTML(b []byte) bool {
	return bytes.HasPrefix(bytes.TrimLeft(b, " \t\r\n"), []byte("<"))
}

// --- ZIP (unchanged) ---

func ExtractZip(r io.ReaderAt, size int64, destDir string, maxBytes int64) (int64, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return 0, fmt.Errorf("reading zip: %w", err)
	}

	var totalWritten int64
	for _, f := range zr.File {
		name := filepath.Clean(f.Name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return totalWritten, fmt.Errorf("zip-slip detected: %q", f.Name)
		}
		dest := filepath.Join(destDir, name)
		if !strings.HasPrefix(dest, filepath.Clean(destDir)+string(os.PathSeparator)) && dest != filepath.Clean(destDir) {
			return totalWritten, fmt.Errorf("zip-slip detected: %q", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0755); err != nil {
				return totalWritten, err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return totalWritten, err
		}

		rc, err := f.Open()
		if err != nil {
			return totalWritten, err
		}

		out, err := os.Create(dest)
		if err != nil {
			rc.Close()
			return totalWritten, err
		}

		n, err := io.Copy(out, io.LimitReader(rc, maxBytes-totalWritten+1))
		rc.Close()
		out.Close()

		totalWritten += n
		if err != nil {
			return totalWritten, err
		}
		if totalWritten > maxBytes {
			return totalWritten, fmt.Errorf("extracted size exceeds limit of %d bytes", maxBytes)
		}
	}
	return totalWritten, nil
}

func decompressXz(r io.Reader, maxBytes int64) ([]byte, error) {
	xr, err := xz.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("reading xz: %w", err)
	}
	inner, err := io.ReadAll(io.LimitReader(xr, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("decompressing xz: %w", err)
	}
	if int64(len(inner)) > maxBytes {
		return nil, fmt.Errorf("decompressed size exceeds limit of %d bytes", maxBytes)
	}
	return inner, nil
}

