package serve

import (
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/andybalholm/brotli"
)

const compressMinBytes = 256

// acceptsEncoding reports whether the request accepts the given encoding
// (e.g. "gzip", "br"), respecting q=0 to explicitly refuse an encoding.
func acceptsEncoding(r *http.Request, encoding string) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.TrimSpace(name) != encoding {
			continue
		}
		if _, qval, ok := strings.Cut(params, "q="); ok {
			q := strings.TrimSpace(qval)
			if q == "0" || q == "0.0" || q == "0.00" || q == "0.000" {
				return false
			}
		}
		return true
	}
	return false
}

// acceptsGzip reports whether the request accepts gzip encoding.
func acceptsGzip(r *http.Request) bool {
	return acceptsEncoding(r, "gzip")
}

// acceptsBrotli reports whether the request accepts brotli encoding.
func acceptsBrotli(r *http.Request) bool {
	return acceptsEncoding(r, "br")
}

// isCompressible reports whether the given Content-Type benefits from compression.
func isCompressible(contentType string) bool {
	ct := contentType
	if i := strings.IndexByte(ct, ';'); i > 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	switch ct {
	case "application/javascript", "application/json", "application/xml",
		"application/xhtml+xml", "application/wasm", "application/manifest+json",
		"image/svg+xml":
		return true
	}
	return false
}

// brotliLevel is the compression level for on-the-fly brotli.
// Level 4 balances compression ratio with CPU cost for dynamic content.
const brotliLevel = 4

// compressWriter wraps an http.ResponseWriter to transparently compress
// responses (gzip or brotli) when the content type is compressible and
// the body is large enough to benefit.
type compressWriter struct {
	http.ResponseWriter
	enc           io.WriteCloser // gzip or brotli writer, nil until first compressible Write
	encoding      string         // "gzip" or "br"
	headerWritten bool
	statusCode    int
}

// WriteHeader defers the actual header write for 200 responses so that
// Write can inspect Content-Type and decide whether to compress.
func (cw *compressWriter) WriteHeader(code int) {
	if cw.headerWritten {
		return
	}
	cw.statusCode = code
	// Only potentially compress full 200 responses.
	// For 304, 206, etc., pass through immediately.
	if code != http.StatusOK {
		cw.headerWritten = true
		cw.ResponseWriter.WriteHeader(code)
	}
}

func (cw *compressWriter) Write(b []byte) (int, error) {
	if !cw.headerWritten {
		cw.headerWritten = true
		if cw.statusCode == 0 {
			cw.statusCode = http.StatusOK
		}
		ct := cw.Header().Get("Content-Type")
		if isCompressible(ct) {
			cw.Header().Set("Vary", "Accept-Encoding")
			clStr := cw.Header().Get("Content-Length")
			cl, err := strconv.ParseInt(clStr, 10, 64)
			if err != nil || cl >= compressMinBytes {
				switch cw.encoding {
				case "br":
					cw.enc = brotli.NewWriterLevel(cw.ResponseWriter, brotliLevel)
				default:
					cw.enc = gzip.NewWriter(cw.ResponseWriter)
				}
				cw.Header().Del("Content-Length")
				cw.Header().Set("Content-Encoding", cw.encoding)
			}
		}
		cw.ResponseWriter.WriteHeader(cw.statusCode)
	}
	if cw.enc != nil {
		return cw.enc.Write(b)
	}
	return cw.ResponseWriter.Write(b)
}

// Close flushes the compressed stream. Must be called via defer.
func (cw *compressWriter) Close() error {
	if !cw.headerWritten {
		cw.headerWritten = true
		if cw.statusCode == 0 {
			cw.statusCode = http.StatusOK
		}
		cw.ResponseWriter.WriteHeader(cw.statusCode)
	}
	if cw.enc != nil {
		return cw.enc.Close()
	}
	return nil
}

func (cw *compressWriter) Flush() {
	type flusher interface{ Flush() error }
	if f, ok := cw.enc.(flusher); ok {
		f.Flush()
	}
	if f, ok := cw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (cw *compressWriter) Unwrap() http.ResponseWriter {
	return cw.ResponseWriter
}
