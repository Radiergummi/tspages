package storage

import (
	"testing"
)

func TestParseSiteConfig_Full(t *testing.T) {
	input := `
spa_routing = true
analytics = false
index_page = "home.html"
not_found_page = "errors/404.html"

[headers]
"/*" = { Cache-Control = "public, max-age=3600" }
"/api/*" = { Access-Control-Allow-Origin = "*", Access-Control-Allow-Methods = "GET, POST" }
`
	cfg, err := ParseSiteConfig([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.SPARouting == nil || *cfg.SPARouting != true {
		t.Error("spa should be true")
	}
	if cfg.Analytics == nil || *cfg.Analytics != false {
		t.Error("analytics should be false")
	}
	if cfg.IndexPage != "home.html" {
		t.Errorf("index_page = %q, want home.html", cfg.IndexPage)
	}
	if cfg.NotFoundPage != "errors/404.html" {
		t.Errorf("not_found_page = %q, want errors/404.html", cfg.NotFoundPage)
	}
	if len(cfg.Headers) != 2 {
		t.Fatalf("headers count = %d, want 2", len(cfg.Headers))
	}
	if cfg.Headers["/*"]["Cache-Control"] != "public, max-age=3600" {
		t.Errorf("Cache-Control = %q", cfg.Headers["/*"]["Cache-Control"])
	}
}

func TestParseSiteConfig_Empty(t *testing.T) {
	cfg, err := ParseSiteConfig([]byte(""))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.SPARouting != nil {
		t.Error("spa should default to nil")
	}
	if cfg.Analytics != nil {
		t.Error("analytics should default to nil")
	}
	if cfg.IndexPage != "" {
		t.Error("index_page should default to empty")
	}
	if cfg.NotFoundPage != "" {
		t.Error("not_found_page should default to empty")
	}
	if cfg.Headers != nil {
		t.Error("headers should default to nil")
	}
}

func TestParseSiteConfig_Invalid(t *testing.T) {
	_, err := ParseSiteConfig([]byte(`spa_routing = "not a bool"`))
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

func boolPtr(b bool) *bool { return &b }

func TestWriteReadSiteConfig(t *testing.T) {
	s := New(t.TempDir())
	s.CreateDeployment("docs", "aaa11111")

	analytics := true
	cfg := SiteConfig{
		SPARouting:   boolPtr(true),
		Analytics:    &analytics,
		NotFoundPage: "404.html",
		Headers: map[string]map[string]string{
			"/*": {"Cache-Control": "public, max-age=3600"},
		},
	}
	if err := s.WriteSiteConfig("docs", "aaa11111", cfg); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.ReadSiteConfig("docs", "aaa11111")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.SPARouting == nil || *got.SPARouting != true {
		t.Error("spa should be true")
	}
	if got.Analytics == nil || *got.Analytics != true {
		t.Error("analytics should be true")
	}
	if got.NotFoundPage != "404.html" {
		t.Errorf("not_found_page = %q", got.NotFoundPage)
	}
	if got.Headers["/*"]["Cache-Control"] != "public, max-age=3600" {
		t.Errorf("Cache-Control = %q", got.Headers["/*"]["Cache-Control"])
	}
}

func TestReadSiteConfig_Missing(t *testing.T) {
	s := New(t.TempDir())
	s.CreateDeployment("docs", "aaa11111")

	cfg, err := s.ReadSiteConfig("docs", "aaa11111")
	if err != nil {
		t.Fatalf("missing config should not error: %v", err)
	}
	if cfg.SPARouting != nil {
		t.Error("spa should be nil")
	}
	if cfg.Headers != nil {
		t.Error("headers should be nil")
	}
}

func TestReadCurrentSiteConfig(t *testing.T) {
	s := New(t.TempDir())
	s.CreateDeployment("docs", "aaa11111")
	s.MarkComplete("docs", "aaa11111")
	s.ActivateDeployment("docs", "aaa11111")

	cfg := SiteConfig{SPARouting: boolPtr(true)}
	if err := s.WriteSiteConfig("docs", "aaa11111", cfg); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.ReadCurrentSiteConfig("docs")
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	if got.SPARouting == nil || *got.SPARouting != true {
		t.Error("spa should be true")
	}
}

func TestReadCurrentSiteConfig_NoDeployment(t *testing.T) {
	s := New(t.TempDir())

	cfg, err := s.ReadCurrentSiteConfig("docs")
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}
	if cfg.SPARouting != nil {
		t.Error("spa should be nil for missing deployment")
	}
}

func TestSiteConfig_Merge(t *testing.T) {
	defaults := SiteConfig{
		SPARouting:   boolPtr(true),
		Analytics:    boolPtr(true),
		NotFoundPage: "404.html",
		Headers: map[string]map[string]string{
			"/*": {"X-Frame-Options": "DENY", "Cache-Control": "no-store"},
		},
	}

	// Deployment overrides SPA and analytics, adds a header path
	deploy := SiteConfig{
		SPARouting: boolPtr(false),
		Analytics:  boolPtr(false),
		Headers: map[string]map[string]string{
			"/assets/*": {"Cache-Control": "public, max-age=86400"},
		},
	}

	merged := deploy.Merge(defaults)
	if merged.SPARouting == nil || *merged.SPARouting != false {
		t.Error("deploy should override spa to false")
	}
	if merged.Analytics == nil || *merged.Analytics != false {
		t.Error("deploy should override analytics to false")
	}
	if merged.NotFoundPage != "404.html" {
		t.Errorf("not_found_page should inherit from defaults, got %q", merged.NotFoundPage)
	}
	// Headers: deployment paths override defaults; default-only paths are kept
	if merged.Headers["/*"]["X-Frame-Options"] != "DENY" {
		t.Error("should inherit default /* headers")
	}
	if merged.Headers["/assets/*"]["Cache-Control"] != "public, max-age=86400" {
		t.Error("should have deployment /assets/* headers")
	}
}

func TestSiteConfig_Merge_EmptyDeployment(t *testing.T) {
	defaults := SiteConfig{
		SPARouting: boolPtr(true),
		Analytics:  boolPtr(true),
		IndexPage:  "home.html",
	}
	deploy := SiteConfig{} // all zero values

	merged := deploy.Merge(defaults)
	if merged.SPARouting == nil || *merged.SPARouting != true {
		t.Error("should inherit spa from defaults")
	}
	if merged.Analytics == nil || *merged.Analytics != true {
		t.Error("should inherit analytics from defaults")
	}
	if merged.IndexPage != "home.html" {
		t.Errorf("index_page = %q, want home.html", merged.IndexPage)
	}
}

func TestSiteConfig_Merge_EmptyDefaults(t *testing.T) {
	deploy := SiteConfig{SPARouting: boolPtr(true), IndexPage: "app.html"}
	defaults := SiteConfig{}

	merged := deploy.Merge(defaults)
	if merged.SPARouting == nil || *merged.SPARouting != true {
		t.Error("spa should be true")
	}
	if merged.IndexPage != "app.html" {
		t.Errorf("index_page = %q", merged.IndexPage)
	}
}

func TestSiteConfig_Merge_DoesNotMutateDefaults(t *testing.T) {
	defaults := SiteConfig{
		Headers: map[string]map[string]string{
			"/*": {"X-Frame-Options": "DENY"},
		},
	}
	deploy := SiteConfig{
		Headers: map[string]map[string]string{
			"/assets/*": {"Cache-Control": "public"},
		},
	}

	deploy.Merge(defaults)

	// defaults.Headers should not have been mutated
	if _, ok := defaults.Headers["/assets/*"]; ok {
		t.Error("Merge mutated the defaults Headers map")
	}
	if len(defaults.Headers) != 1 {
		t.Errorf("defaults.Headers has %d entries, want 1", len(defaults.Headers))
	}
}

func TestValidateSiteConfig_ValidPaths(t *testing.T) {
	cfg := SiteConfig{IndexPage: "index.html", NotFoundPage: "errors/404.html"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSiteConfig_PathTraversal(t *testing.T) {
	tests := []SiteConfig{
		{IndexPage: "../etc/passwd"},
		{NotFoundPage: "../../secret.html"},
		{IndexPage: "/absolute/path.html"},
		{NotFoundPage: "foo/../../bar.html"},
	}
	for _, cfg := range tests {
		if err := cfg.Validate(); err == nil {
			t.Errorf("expected error for %+v", cfg)
		}
	}
}

func TestValidateSiteConfig_HeaderPaths(t *testing.T) {
	cfg := SiteConfig{
		Headers: map[string]map[string]string{
			"/*":        {"X-Frame-Options": "DENY"},
			"/assets/*": {"Cache-Control": "public"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSiteConfig_EmptyHeaderValue(t *testing.T) {
	cfg := SiteConfig{
		Headers: map[string]map[string]string{
			"/*": {"X-Frame-Options": ""},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty header value")
	}
}

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

func TestParseRedirectsFile(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []RedirectRule
		wantErr bool
	}{
		{
			name:  "basic rules",
			input: "/old /new 301\n/blog/:slug /posts/:slug\n",
			want: []RedirectRule{
				{From: "/old", To: "/new", Status: 301},
				{From: "/blog/:slug", To: "/posts/:slug"},
			},
		},
		{
			name:  "comments and blank lines",
			input: "# redirect rules\n\n/old /new\n  # indented comment\n\n",
			want:  []RedirectRule{{From: "/old", To: "/new"}},
		},
		{
			name:  "splat",
			input: "/docs/* /v2/docs/* 302\n",
			want:  []RedirectRule{{From: "/docs/*", To: "/v2/docs/*", Status: 302}},
		},
		{
			name:  "external url",
			input: "/ext https://example.com 302\n",
			want:  []RedirectRule{{From: "/ext", To: "https://example.com", Status: 302}},
		},
		{
			name:    "missing to",
			input:   "/old\n",
			wantErr: true,
		},
		{
			name:    "invalid status",
			input:   "/old /new 200\n",
			wantErr: true,
		},
		{
			name:    "extra fields rejected",
			input:   "/old /new 301 force\n",
			wantErr: true,
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "trailing whitespace",
			input: "/old  /new  301  \n",
			want:  []RedirectRule{{From: "/old", To: "/new", Status: 301}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRedirectsFile([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d rules, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("rule %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseHeadersFile(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]map[string]string
		wantErr bool
	}{
		{
			name:  "single path",
			input: "/*\n  X-Frame-Options: DENY\n  X-Content-Type-Options: nosniff\n",
			want: map[string]map[string]string{
				"/*": {"X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff"},
			},
		},
		{
			name:  "multiple paths",
			input: "/*\n  X-Frame-Options: DENY\n/*.css\n  Cache-Control: public, max-age=86400\n",
			want: map[string]map[string]string{
				"/*":     {"X-Frame-Options": "DENY"},
				"/*.css": {"Cache-Control": "public, max-age=86400"},
			},
		},
		{
			name:  "comments and blank lines",
			input: "# global headers\n/*\n  X-Frame-Options: DENY\n\n# css headers\n/*.css\n  Cache-Control: public\n",
			want: map[string]map[string]string{
				"/*":     {"X-Frame-Options": "DENY"},
				"/*.css": {"Cache-Control": "public"},
			},
		},
		{
			name:  "header value with colon",
			input: "/*\n  Link: </style.css>; rel=preload; as=style\n",
			want: map[string]map[string]string{
				"/*": {"Link": "</style.css>; rel=preload; as=style"},
			},
		},
		{
			name:    "header line without path",
			input:   "  X-Frame-Options: DENY\n",
			wantErr: true,
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseHeadersFile([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d paths, want %d", len(got), len(tt.want))
			}
			for path, wantHdrs := range tt.want {
				gotHdrs, ok := got[path]
				if !ok {
					t.Errorf("missing path %q", path)
					continue
				}
				for k, v := range wantHdrs {
					if gotHdrs[k] != v {
						t.Errorf("path %q header %q = %q, want %q", path, k, gotHdrs[k], v)
					}
				}
			}
		})
	}
}

func TestParseHeadersFile_PathWithNoHeaders(t *testing.T) {
	// A path line with no subsequent header lines should produce an empty map for that path.
	got, err := ParseHeadersFile([]byte("/*\n  X-Frame-Options: DENY\n/empty\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d paths, want 2", len(got))
	}
	if hdrs := got["/empty"]; len(hdrs) != 0 {
		t.Errorf("expected empty headers for /empty, got %v", hdrs)
	}
}

func TestValidateSiteConfig_HeaderPathMissingSlash(t *testing.T) {
	cfg := SiteConfig{
		Headers: map[string]map[string]string{
			"assets/*": {"Cache-Control": "public"},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for header path without leading /")
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

func TestParseSiteConfig_DirectoryListing(t *testing.T) {
	cfg, err := ParseSiteConfig([]byte(`directory_listing = true`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.DirectoryListing == nil || *cfg.DirectoryListing != true {
		t.Error("directory_listing should be true")
	}
}

func TestSiteConfig_Merge_DirectoryListing(t *testing.T) {
	defaults := SiteConfig{DirectoryListing: boolPtr(true)}
	deploy := SiteConfig{}
	merged := deploy.Merge(defaults)
	if merged.DirectoryListing == nil || *merged.DirectoryListing != true {
		t.Error("should inherit directory_listing from defaults")
	}

	deploy2 := SiteConfig{DirectoryListing: boolPtr(false)}
	merged2 := deploy2.Merge(defaults)
	if merged2.DirectoryListing == nil || *merged2.DirectoryListing != false {
		t.Error("deployment should override directory_listing")
	}
}

func TestParseSiteConfig_TrailingSlash(t *testing.T) {
	cfg, err := ParseSiteConfig([]byte(`trailing_slash = "add"`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.TrailingSlash != "add" {
		t.Errorf("trailing_slash = %q, want add", cfg.TrailingSlash)
	}
}

func TestValidateSiteConfig_TrailingSlash(t *testing.T) {
	tests := []struct {
		value   string
		wantErr bool
	}{
		{"", false},
		{"add", false},
		{"remove", false},
		{"strip", true},
		{"yes", true},
	}
	for _, tt := range tests {
		cfg := SiteConfig{TrailingSlash: tt.value}
		err := cfg.Validate()
		if (err != nil) != tt.wantErr {
			t.Errorf("TrailingSlash=%q: error=%v, wantErr=%v", tt.value, err, tt.wantErr)
		}
	}
}

func TestSiteConfig_Merge_TrailingSlash(t *testing.T) {
	defaults := SiteConfig{TrailingSlash: "add"}
	deploy := SiteConfig{}
	merged := deploy.Merge(defaults)
	if merged.TrailingSlash != "add" {
		t.Errorf("should inherit trailing_slash, got %q", merged.TrailingSlash)
	}

	deploy2 := SiteConfig{TrailingSlash: "remove"}
	merged2 := deploy2.Merge(defaults)
	if merged2.TrailingSlash != "remove" {
		t.Errorf("deployment should override trailing_slash, got %q", merged2.TrailingSlash)
	}
}

func TestParseSiteConfig_Webhook(t *testing.T) {
	input := `
webhook_url = "https://example.com/hook"
webhook_events = ["deploy.success", "deploy.failed"]
webhook_secret = "s3cret"
`
	cfg, err := ParseSiteConfig([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.WebhookURL != "https://example.com/hook" {
		t.Errorf("webhook_url = %q", cfg.WebhookURL)
	}
	if len(cfg.WebhookEvents) != 2 || cfg.WebhookEvents[0] != "deploy.success" || cfg.WebhookEvents[1] != "deploy.failed" {
		t.Errorf("webhook_events = %v", cfg.WebhookEvents)
	}
	if cfg.WebhookSecret != "s3cret" {
		t.Errorf("webhook_secret = %q", cfg.WebhookSecret)
	}
}

func TestValidateSiteConfig_WebhookURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"empty", "", false},
		{"https", "https://example.com/hook", false},
		{"http", "http://internal.ts.net/hook", false},
		{"ftp", "ftp://example.com", true},
		{"no scheme", "example.com/hook", true},
		{"just text", "not-a-url", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := SiteConfig{WebhookURL: tt.url}
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSiteConfig_WebhookEvents(t *testing.T) {
	tests := []struct {
		name    string
		events  []string
		wantErr bool
	}{
		{"nil", nil, false},
		{"empty", []string{}, false},
		{"valid single", []string{"deploy.success"}, false},
		{"valid all", []string{"deploy.success", "deploy.failed", "site.created", "site.deleted"}, false},
		{"unknown event", []string{"deploy.success", "deploy.started"}, true},
		{"empty string event", []string{""}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := SiteConfig{WebhookEvents: tt.events}
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSiteConfig_Merge_WebhookOverride(t *testing.T) {
	defaults := SiteConfig{
		WebhookURL:    "https://global.example.com/hook",
		WebhookEvents: []string{"deploy.success", "deploy.failed"},
		WebhookSecret: "global-secret",
	}
	deploy := SiteConfig{
		WebhookURL:    "https://site.example.com/hook",
		WebhookEvents: []string{"site.created"},
		// WebhookSecret intentionally empty
	}
	merged := deploy.Merge(defaults)
	if merged.WebhookURL != "https://site.example.com/hook" {
		t.Errorf("webhook_url = %q, want site URL", merged.WebhookURL)
	}
	if len(merged.WebhookEvents) != 1 || merged.WebhookEvents[0] != "site.created" {
		t.Errorf("webhook_events = %v, want [site.created]", merged.WebhookEvents)
	}
	if merged.WebhookSecret != "" {
		t.Errorf("webhook_secret = %q, want empty (per-site override replaces all)", merged.WebhookSecret)
	}
}

func TestParseSiteConfig_Public(t *testing.T) {
	cfg, err := ParseSiteConfig([]byte(`public = true`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Public == nil || *cfg.Public != true {
		t.Error("public should be true")
	}
}

func TestParseSiteConfig_PublicEmpty(t *testing.T) {
	cfg, err := ParseSiteConfig([]byte(""))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Public != nil {
		t.Error("public should default to nil")
	}
}

func TestSiteConfig_Merge_Public(t *testing.T) {
	defaults := SiteConfig{Public: boolPtr(false)}
	deploy := SiteConfig{}
	merged := deploy.Merge(defaults)
	if merged.Public == nil || *merged.Public != false {
		t.Error("should inherit public from defaults")
	}

	deploy2 := SiteConfig{Public: boolPtr(true)}
	merged2 := deploy2.Merge(defaults)
	if merged2.Public == nil || *merged2.Public != true {
		t.Error("deployment should override public")
	}
}

func TestSiteConfig_Merge_WebhookInherit(t *testing.T) {
	defaults := SiteConfig{
		WebhookURL:    "https://global.example.com/hook",
		WebhookEvents: []string{"deploy.success"},
		WebhookSecret: "global-secret",
	}
	deploy := SiteConfig{} // empty webhook_url → inherit global
	merged := deploy.Merge(defaults)
	if merged.WebhookURL != "https://global.example.com/hook" {
		t.Errorf("webhook_url = %q, want global URL", merged.WebhookURL)
	}
	if len(merged.WebhookEvents) != 1 || merged.WebhookEvents[0] != "deploy.success" {
		t.Errorf("webhook_events = %v, want [deploy.success]", merged.WebhookEvents)
	}
	if merged.WebhookSecret != "global-secret" {
		t.Errorf("webhook_secret = %q, want global-secret", merged.WebhookSecret)
	}
}
