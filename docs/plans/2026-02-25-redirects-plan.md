# Redirect Rules Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add configurable redirect rules to `tspages.toml` with exact, named parameter, and splat pattern matching.

**Architecture:** Add a `RedirectRule` type and `Redirects` field to `SiteConfig` (storage layer), then add pattern matching and redirect execution in the serve handler before path resolution. Redirects are checked first-match-wins before any file serving.

**Tech Stack:** Go, TOML config, existing storage/serve packages.

---

### Task 1: Add RedirectRule to SiteConfig with parsing, validation, and merge

**Files:**
- Modify: `internal/storage/siteconfig.go` (add struct, validation, merge)
- Modify: `internal/storage/siteconfig_test.go` (add tests)

**Step 1: Add RedirectRule struct and Redirects field**

In `internal/storage/siteconfig.go`, add after the `SiteConfig` struct:

```go
// RedirectRule defines a single redirect from one path pattern to another.
type RedirectRule struct {
	From   string `toml:"from"`
	To     string `toml:"to"`
	Status int    `toml:"status,omitempty"`
}
```

Add `Redirects` to `SiteConfig`:

```go
type SiteConfig struct {
	SPA          *bool                        `toml:"spa"`
	Analytics    *bool                        `toml:"analytics"`
	IndexPage    string                       `toml:"index_page"`
	NotFoundPage string                       `toml:"not_found_page"`
	Headers      map[string]map[string]string `toml:"headers"`
	Redirects    []RedirectRule               `toml:"redirects"`
}
```

**Step 2: Add redirect validation in `Validate()`**

Add after the header validation block in `Validate()`:

```go
seenFrom := make(map[string]bool, len(c.Redirects))
for i, r := range c.Redirects {
	if r.From == "" {
		return fmt.Errorf("redirect %d: 'from' is required", i)
	}
	if !strings.HasPrefix(r.From, "/") {
		return fmt.Errorf("redirect %d: 'from' must start with /", i)
	}
	if r.To == "" {
		return fmt.Errorf("redirect %d: 'to' is required", i)
	}
	if !strings.HasPrefix(r.To, "/") && !strings.HasPrefix(r.To, "http://") && !strings.HasPrefix(r.To, "https://") {
		return fmt.Errorf("redirect %d: 'to' must start with / or be a full URL", i)
	}
	if r.Status != 0 && r.Status != 301 && r.Status != 302 {
		return fmt.Errorf("redirect %d: status must be 301 or 302", i)
	}
	if seenFrom[r.From] {
		return fmt.Errorf("redirect %d: duplicate 'from' pattern %q", i, r.From)
	}
	seenFrom[r.From] = true

	// Named params in 'to' must exist in 'from'
	fromParams := extractParams(r.From)
	toParams := extractParams(r.To)
	for _, p := range toParams {
		if p == "*" {
			if !strings.Contains(r.From, "*") {
				return fmt.Errorf("redirect %d: 'to' uses * but 'from' has no splat", i)
			}
			continue
		}
		if !fromParams[p] {
			return fmt.Errorf("redirect %d: 'to' references :%s not in 'from'", i, p)
		}
	}
}
```

Add the `extractParams` helper:

```go
// extractParams returns named parameters from a redirect pattern.
// For "from" patterns it returns a set; for "to" patterns use the returned slice.
func extractParams(pattern string) map[string]bool {
	params := make(map[string]bool)
	for _, seg := range strings.Split(pattern, "/") {
		if strings.HasPrefix(seg, ":") {
			params[seg[1:]] = true
		}
	}
	return params
}
```

Note: `extractParams` is used for validation only. The actual matching uses a different function in the serve layer (Task 2).

**Step 3: Add redirect merge logic**

In `Merge()`, add after the headers block:

```go
if c.Redirects != nil {
	merged.Redirects = c.Redirects
}
```

Deployment redirects completely replace defaults.

**Step 4: Write tests**

Add to `internal/storage/siteconfig_test.go`:

```go
func TestParseSiteConfig_Redirects(t *testing.T) {
	input := `
[[redirects]]
from = "/old"
to = "/new"
status = 301

[[redirects]]
from = "/blog/:slug"
to = "/posts/:slug"
`
	cfg, err := ParseSiteConfig([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Redirects) != 2 {
		t.Fatalf("redirects count = %d, want 2", len(cfg.Redirects))
	}
	if cfg.Redirects[0].From != "/old" || cfg.Redirects[0].To != "/new" || cfg.Redirects[0].Status != 301 {
		t.Errorf("redirect 0 = %+v", cfg.Redirects[0])
	}
	if cfg.Redirects[1].From != "/blog/:slug" || cfg.Redirects[1].To != "/posts/:slug" {
		t.Errorf("redirect 1 = %+v", cfg.Redirects[1])
	}
	if cfg.Redirects[1].Status != 0 {
		t.Errorf("redirect 1 status = %d, want 0 (default)", cfg.Redirects[1].Status)
	}
}

func TestValidateSiteConfig_Redirects(t *testing.T) {
	tests := []struct {
		name    string
		cfg     SiteConfig
		wantErr bool
	}{
		{"valid exact", SiteConfig{Redirects: []RedirectRule{{From: "/old", To: "/new"}}}, false},
		{"valid with status", SiteConfig{Redirects: []RedirectRule{{From: "/old", To: "/new", Status: 302}}}, false},
		{"valid named param", SiteConfig{Redirects: []RedirectRule{{From: "/blog/:slug", To: "/posts/:slug"}}}, false},
		{"valid splat", SiteConfig{Redirects: []RedirectRule{{From: "/docs/*", To: "/v2/docs/*"}}}, false},
		{"valid external", SiteConfig{Redirects: []RedirectRule{{From: "/ext", To: "https://example.com"}}}, false},
		{"empty from", SiteConfig{Redirects: []RedirectRule{{From: "", To: "/new"}}}, true},
		{"from no slash", SiteConfig{Redirects: []RedirectRule{{From: "old", To: "/new"}}}, true},
		{"empty to", SiteConfig{Redirects: []RedirectRule{{From: "/old", To: ""}}}, true},
		{"to no slash or url", SiteConfig{Redirects: []RedirectRule{{From: "/old", To: "new"}}}, true},
		{"bad status", SiteConfig{Redirects: []RedirectRule{{From: "/old", To: "/new", Status: 200}}}, true},
		{"duplicate from", SiteConfig{Redirects: []RedirectRule{{From: "/a", To: "/b"}, {From: "/a", To: "/c"}}}, true},
		{"to param not in from", SiteConfig{Redirects: []RedirectRule{{From: "/blog/:slug", To: "/posts/:id"}}}, true},
		{"to splat without from splat", SiteConfig{Redirects: []RedirectRule{{From: "/docs/:slug", To: "/v2/*"}}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSiteConfig_Merge_Redirects(t *testing.T) {
	defaults := SiteConfig{
		Redirects: []RedirectRule{{From: "/default", To: "/new-default"}},
	}
	deploy := SiteConfig{
		Redirects: []RedirectRule{{From: "/old", To: "/new"}},
	}
	merged := deploy.Merge(defaults)
	if len(merged.Redirects) != 1 || merged.Redirects[0].From != "/old" {
		t.Errorf("deployment redirects should replace defaults, got %+v", merged.Redirects)
	}
}

func TestSiteConfig_Merge_RedirectsInheritDefaults(t *testing.T) {
	defaults := SiteConfig{
		Redirects: []RedirectRule{{From: "/default", To: "/new-default"}},
	}
	deploy := SiteConfig{} // nil Redirects
	merged := deploy.Merge(defaults)
	if len(merged.Redirects) != 1 || merged.Redirects[0].From != "/default" {
		t.Errorf("nil deployment redirects should inherit defaults, got %+v", merged.Redirects)
	}
}
```

**Step 5: Run tests**

Run: `go test ./internal/storage/...`
Expected: PASS

**Step 6: Commit**

```
feat: add redirect rules to site config
```

---

### Task 2: Add redirect matching to serve handler

**Files:**
- Modify: `internal/serve/handler.go` (add matching + wire into ServeHTTP)
- Modify: `internal/serve/handler_test.go` (add tests)

**Step 1: Add the redirect matching function**

In `internal/serve/handler.go`, add after `matchHeaderPath`:

```go
// matchRedirect checks if reqPath matches a redirect rule's From pattern.
// Returns the substituted target URL and true if matched, or ("", false).
// Patterns: "/exact", "/blog/:slug" (named param), "/docs/*" (splat).
func matchRedirect(rule storage.RedirectRule, reqPath string) (string, bool) {
	fromSegs := strings.Split(rule.From, "/")
	pathSegs := strings.Split(reqPath, "/")

	params := make(map[string]string)

	for i, seg := range fromSegs {
		if seg == "*" {
			// Splat: capture everything remaining
			params["*"] = strings.Join(pathSegs[i:], "/")
			break
		}
		if i >= len(pathSegs) {
			return "", false
		}
		if strings.HasPrefix(seg, ":") {
			params[seg[1:]] = pathSegs[i]
		} else if seg != pathSegs[i] {
			return "", false
		}
	}

	// If no splat, segment counts must match exactly
	if !strings.Contains(rule.From, "*") && len(fromSegs) != len(pathSegs) {
		return "", false
	}

	// Substitute params into target
	target := rule.To
	for name, value := range params {
		if name == "*" {
			target = strings.Replace(target, "*", value, 1)
		} else {
			target = strings.Replace(target, ":"+name, value, 1)
		}
	}
	return target, true
}
```

**Step 2: Add redirect check in ServeHTTP**

In `ServeHTTP`, add right after `cfg := h.loadConfig(deploymentID)` (line 89), before the `indexPage` logic:

```go
// Check redirects before file resolution (first match wins).
if target, status, ok := h.checkRedirects(r.URL.Path, cfg); ok {
	http.Redirect(w, r, target, status)
	return
}
```

Add the `checkRedirects` method:

```go
func (h *Handler) checkRedirects(reqPath string, cfg storage.SiteConfig) (target string, status int, matched bool) {
	for _, rule := range cfg.Redirects {
		if target, ok := matchRedirect(rule, reqPath); ok {
			status := rule.Status
			if status == 0 {
				status = 301
			}
			return target, status, true
		}
	}
	return "", 0, false
}
```

**Step 3: Write tests**

Add to `internal/serve/handler_test.go`:

```go
func TestMatchRedirect(t *testing.T) {
	tests := []struct {
		name     string
		rule     storage.RedirectRule
		path     string
		want     string
		wantOK   bool
	}{
		{"exact match", storage.RedirectRule{From: "/old", To: "/new"}, "/old", "/new", true},
		{"exact no match", storage.RedirectRule{From: "/old", To: "/new"}, "/other", "", false},
		{"named param", storage.RedirectRule{From: "/blog/:slug", To: "/posts/:slug"}, "/blog/hello", "/posts/hello", true},
		{"named param no match", storage.RedirectRule{From: "/blog/:slug", To: "/posts/:slug"}, "/other/hello", "", false},
		{"named param too few segments", storage.RedirectRule{From: "/blog/:slug", To: "/posts/:slug"}, "/blog", "", false},
		{"named param too many segments", storage.RedirectRule{From: "/blog/:slug", To: "/posts/:slug"}, "/blog/a/b", "", false},
		{"multiple params", storage.RedirectRule{From: "/a/:x/b/:y", To: "/c/:y/:x"}, "/a/1/b/2", "/c/2/1", true},
		{"splat", storage.RedirectRule{From: "/docs/*", To: "/v2/docs/*"}, "/docs/getting-started", "/v2/docs/getting-started", true},
		{"splat deep", storage.RedirectRule{From: "/docs/*", To: "/v2/*"}, "/docs/a/b/c", "/v2/a/b/c", true},
		{"splat root", storage.RedirectRule{From: "/docs/*", To: "/v2/*"}, "/docs/", "/v2/", true},
		{"external", storage.RedirectRule{From: "/ext", To: "https://example.com"}, "/ext", "https://example.com", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := matchRedirect(tt.rule, tt.path)
			if ok != tt.wantOK {
				t.Errorf("matched = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("target = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandler_Redirect_Exact(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Docs</h1>",
	})
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{
		Redirects: []storage.RedirectRule{
			{From: "/old", To: "/new", Status: 302},
		},
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/old", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "old")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/new" {
		t.Errorf("Location = %q, want /new", loc)
	}
}

func TestHandler_Redirect_NamedParam(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "hi",
	})
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{
		Redirects: []storage.RedirectRule{
			{From: "/blog/:slug", To: "/posts/:slug"},
		},
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/blog/hello-world", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "blog/hello-world")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/posts/hello-world" {
		t.Errorf("Location = %q, want /posts/hello-world", loc)
	}
}

func TestHandler_Redirect_Splat(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "hi",
	})
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{
		Redirects: []storage.RedirectRule{
			{From: "/docs/*", To: "/v2/docs/*"},
		},
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/docs/getting-started", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "docs/getting-started")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/v2/docs/getting-started" {
		t.Errorf("Location = %q, want /v2/docs/getting-started", loc)
	}
}

func TestHandler_Redirect_FirstMatchWins(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "hi",
	})
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{
		Redirects: []storage.RedirectRule{
			{From: "/a", To: "/first", Status: 302},
			{From: "/a", To: "/second", Status: 302},
		},
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/a", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "a")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if loc := rec.Header().Get("Location"); loc != "/first" {
		t.Errorf("Location = %q, want /first (first match wins)", loc)
	}
}

func TestHandler_Redirect_NoMatchServesFile(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Docs</h1>",
		"about.html": "<h1>About</h1>",
	})
	store.WriteSiteConfig("docs", "aaa11111", storage.SiteConfig{
		Redirects: []storage.RedirectRule{
			{From: "/old", To: "/new"},
		},
	})

	h := NewHandler(store, "docs", nil, storage.SiteConfig{})
	req := httptest.NewRequest("GET", "/about.html", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "about.html")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no redirect match)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "About") {
		t.Error("should serve the file normally when no redirect matches")
	}
}

func TestHandler_Redirect_FromDefaults(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "hi",
	})

	defaults := storage.SiteConfig{
		Redirects: []storage.RedirectRule{
			{From: "/old", To: "/new", Status: 301},
		},
	}
	h := NewHandler(store, "docs", nil, defaults)

	req := httptest.NewRequest("GET", "/old", nil)
	req = withCaps(req, []auth.Cap{{Access: "view", Sites: []string{"docs"}}})
	req.SetPathValue("path", "old")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301 (redirect from defaults)", rec.Code)
	}
}
```

**Step 4: Run tests**

Run: `go build ./cmd/tspages && go test ./...`
Expected: PASS, binary builds

**Step 5: Commit**

```
feat: add redirect matching to serve handler
```
