package serve

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

// benchSetup creates a store, deploys files, optionally writes a deployment
// config, and returns a ready-to-use Handler. The config cache is primed so
// the first benchmark iteration is not an outlier.
func benchSetup(b *testing.B, files map[string]string, cfg *storage.SiteConfig) *Handler {
	b.Helper()
	store := storage.New(b.TempDir())

	dir, err := store.CreateDeployment("bench", "aaa11111")
	if err != nil {
		b.Fatal(err)
	}
	for name, content := range files {
		p := filepath.Join(dir, "content", name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			b.Fatal(err)
		}
	}
	store.MarkComplete("bench", "aaa11111")
	store.ActivateDeployment("bench", "aaa11111")

	if cfg != nil {
		if err := store.WriteDeploymentConfig("bench", "aaa11111", *cfg); err != nil {
			b.Fatal(err)
		}
	}

	h := NewHandler(store, "bench", "", storage.SiteConfig{})

	// Prime the config cache.
	req := httptest.NewRequest("GET", "/", nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", "")
	h.ServeHTTP(httptest.NewRecorder(), req)

	return h
}

// benchReq builds a GET request for the given path with view capabilities.
func benchReq(path string) *http.Request {
	req := httptest.NewRequest("GET", "/"+path, nil)
	req = withCaps(req, []auth.Cap{{Access: "view"}})
	req.SetPathValue("path", path)
	return req
}

func BenchmarkServeSmallHTML(b *testing.B) {
	h := benchSetup(b, map[string]string{
		"index.html": "<html><body><h1>Hello</h1></body></html>",
	}, nil)

	b.ResetTimer()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), benchReq(""))
	}
}

func BenchmarkServeSmallHTML_Gzip(b *testing.B) {
	// Must exceed 256 bytes to trigger on-the-fly compression.
	content := strings.Repeat("<p>Hello world paragraph</p>\n", 20)
	h := benchSetup(b, map[string]string{
		"index.html": content,
	}, nil)

	b.ResetTimer()
	for b.Loop() {
		req := benchReq("")
		req.Header.Set("Accept-Encoding", "gzip")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkServeSmallHTML_Brotli(b *testing.B) {
	content := strings.Repeat("<p>Hello world paragraph</p>\n", 20)
	h := benchSetup(b, map[string]string{
		"index.html": content,
	}, nil)

	b.ResetTimer()
	for b.Loop() {
		req := benchReq("")
		req.Header.Set("Accept-Encoding", "br")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkServeLargeAsset(b *testing.B) {
	content := strings.Repeat("var x = 'hello';\n", 6000) // ~102KB
	h := benchSetup(b, map[string]string{
		"app.js": content,
	}, nil)

	b.ResetTimer()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), benchReq("app.js"))
	}
}

func BenchmarkServeLargeAsset_Gzip(b *testing.B) {
	content := strings.Repeat("var x = 'hello';\n", 6000)
	h := benchSetup(b, map[string]string{
		"app.js": content,
	}, nil)

	b.ResetTimer()
	for b.Loop() {
		req := benchReq("app.js")
		req.Header.Set("Accept-Encoding", "gzip")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkServeLargeAsset_Precompressed(b *testing.B) {
	content := strings.Repeat("var x = 'hello';\n", 6000)
	// The .br file doesn't need valid brotli — servePrecompressed serves
	// whatever bytes are on disk.
	h := benchSetup(b, map[string]string{
		"app.js":    content,
		"app.js.br": strings.Repeat("compressed", 1000),
	}, nil)

	b.ResetTimer()
	for b.Loop() {
		req := benchReq("app.js")
		req.Header.Set("Accept-Encoding", "br")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkServeETag304(b *testing.B) {
	h := benchSetup(b, map[string]string{
		"style.css": strings.Repeat("body { color: red; }\n", 20),
	}, nil)

	b.ResetTimer()
	for b.Loop() {
		req := benchReq("style.css")
		req.Header.Set("If-None-Match", `"aaa11111:style.css"`)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkServeSPAFallback(b *testing.B) {
	spa := true
	h := benchSetup(b, map[string]string{
		"index.html": "<html><body><div id='app'></div></body></html>",
	}, &storage.SiteConfig{SPARouting: &spa})

	b.ResetTimer()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), benchReq("dashboard/settings"))
	}
}

func BenchmarkServeCustom404(b *testing.B) {
	h := benchSetup(b, map[string]string{
		"index.html": "<h1>Home</h1>",
		"404.html":   "<h1>Not Found</h1>",
	}, nil)

	b.ResetTimer()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), benchReq("nonexistent"))
	}
}

func BenchmarkServeRedirect(b *testing.B) {
	h := benchSetup(b, map[string]string{
		"index.html": "<h1>Home</h1>",
	}, &storage.SiteConfig{
		Redirects: []storage.RedirectRule{
			{From: "/old", To: "/new", Status: 302},
		},
	})

	b.ResetTimer()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), benchReq("old"))
	}
}

func BenchmarkServeCleanURL(b *testing.B) {
	// Clean URLs are on by default — /about resolves to about.html.
	h := benchSetup(b, map[string]string{
		"index.html": "<h1>Home</h1>",
		"about.html": "<h1>About</h1>",
	}, nil)

	b.ResetTimer()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), benchReq("about"))
	}
}

func BenchmarkServeDirectoryListing(b *testing.B) {
	files := map[string]string{
		"index.html": "<h1>Home</h1>",
	}
	for i := range 10 {
		files[fmt.Sprintf("docs/file%d.txt", i)] = fmt.Sprintf("content %d", i)
	}
	dl := true
	h := benchSetup(b, files, &storage.SiteConfig{DirectoryListing: &dl})

	b.ResetTimer()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), benchReq("docs/"))
	}
}

func BenchmarkServeCustomHeaders(b *testing.B) {
	h := benchSetup(b, map[string]string{
		"index.html": "<h1>Home</h1>",
		"style.css":  strings.Repeat("body { color: red; }\n", 20),
	}, &storage.SiteConfig{
		Headers: map[string]map[string]string{
			"/*": {"X-Frame-Options": "DENY"},
		},
	})

	b.ResetTimer()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), benchReq("style.css"))
	}
}
