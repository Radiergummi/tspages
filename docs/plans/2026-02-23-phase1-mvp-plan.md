# tspages Phase 1 (MVP) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a working static site hosting platform on Tailscale with deploy, serve, and admin status endpoints — all access controlled via Tailscale application grant capabilities.

**Architecture:** Single Go binary using `tsnet` for HTTPS. Auth via `WhoIs` + capability parsing. Storage is filesystem with atomic symlink swaps. Stdlib `net/http` router with Go 1.22+ patterns.

**Tech Stack:** Go 1.23+, `tailscale.com/tsnet`, `BurntSushi/toml`

**Design doc:** `docs/plans/2026-02-23-phase1-mvp-design.md`

---

### Task 1: Project Scaffolding

**Files:**
- Create: `go.mod`
- Create: `cmd/tspages/main.go` (placeholder)
- Create: `tspages.toml.example`

**Step 1: Initialize Go module**

```bash
cd /Users/moritz/Projects/tailscale-static-sites
go mod init tspages
```

**Step 2: Create directory structure**

```bash
mkdir -p cmd/tspages internal/auth internal/deploy internal/serve internal/admin internal/storage config
```

**Step 3: Create placeholder main.go**

Create `cmd/tspages/main.go`:
```go
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("tspages")
	os.Exit(0)
}
```

**Step 4: Verify it compiles**

```bash
go build ./cmd/tspages
```

Expected: builds successfully, produces `tspages` binary.

**Step 5: Add dependencies**

```bash
go get tailscale.com@latest
go get github.com/BurntSushi/toml@latest
```

**Step 6: Create example config**

Create `tspages.toml.example`:
```toml
[tailscale]
hostname     = "pages"
state_dir    = "/var/lib/tspages"
auth_key     = ""                          # leave empty to use TS_AUTHKEY env var
capability   = "your-company.com/cap/pages"

[server]
data_dir      = "/data"
max_upload_mb = 500

[routing]
mode = "path"
```

**Step 7: Create .gitignore**

Create `.gitignore`:
```
tspages
/data/
*.toml
!tspages.toml.example
```

**Step 8: Commit**

```bash
git init
git add go.mod go.sum cmd/ internal/ config/ tspages.toml.example .gitignore docs/
git commit -m "feat: scaffold tspages project"
```

---

### Task 2: Config Package

**Files:**
- Create: `config/config.go`
- Create: `config/config_test.go`

**Step 1: Write the failing test**

Create `config/config_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	os.WriteFile(path, []byte(`
[tailscale]
hostname   = "pages"
state_dir  = "/var/lib/tspages"
capability = "example.com/cap/pages"

[server]
data_dir      = "/data"
max_upload_mb = 200

[routing]
mode = "path"
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tailscale.Hostname != "pages" {
		t.Errorf("hostname = %q, want %q", cfg.Tailscale.Hostname, "pages")
	}
	if cfg.Tailscale.Capability != "example.com/cap/pages" {
		t.Errorf("capability = %q, want %q", cfg.Tailscale.Capability, "example.com/cap/pages")
	}
	if cfg.Server.DataDir != "/data" {
		t.Errorf("data_dir = %q, want %q", cfg.Server.DataDir, "/data")
	}
	if cfg.Server.MaxUploadMB != 200 {
		t.Errorf("max_upload_mb = %d, want %d", cfg.Server.MaxUploadMB, 200)
	}
}

func TestLoad_Defaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	os.WriteFile(path, []byte(`
[tailscale]
capability = "example.com/cap/pages"
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tailscale.Hostname != "pages" {
		t.Errorf("hostname = %q, want %q", cfg.Tailscale.Hostname, "pages")
	}
	if cfg.Server.DataDir != "./data" {
		t.Errorf("data_dir = %q, want %q", cfg.Server.DataDir, "./data")
	}
	if cfg.Server.MaxUploadMB != 500 {
		t.Errorf("max_upload_mb = %d, want %d", cfg.Server.MaxUploadMB, 500)
	}
}

func TestLoad_MissingCapability(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	os.WriteFile(path, []byte(`
[tailscale]
hostname = "pages"
`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing capability")
	}
}

func TestLoad_AuthKeyFromEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	os.WriteFile(path, []byte(`
[tailscale]
capability = "example.com/cap/pages"
`), 0644)

	t.Setenv("TS_AUTHKEY", "tskey-auth-test123")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tailscale.AuthKey != "tskey-auth-test123" {
		t.Errorf("auth_key = %q, want %q", cfg.Tailscale.AuthKey, "tskey-auth-test123")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./config/
```

Expected: FAIL — `Load` not defined.

**Step 3: Implement config package**

Create `config/config.go`:
```go
package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Tailscale TailscaleConfig `toml:"tailscale"`
	Server    ServerConfig    `toml:"server"`
	Routing   RoutingConfig   `toml:"routing"`
}

type TailscaleConfig struct {
	Hostname   string `toml:"hostname"`
	StateDir   string `toml:"state_dir"`
	AuthKey    string `toml:"auth_key"`
	Capability string `toml:"capability"`
}

type ServerConfig struct {
	DataDir     string `toml:"data_dir"`
	MaxUploadMB int    `toml:"max_upload_mb"`
}

type RoutingConfig struct {
	Mode string `toml:"mode"`
}

func Load(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.Tailscale.Hostname == "" {
		cfg.Tailscale.Hostname = "pages"
	}
	if cfg.Server.DataDir == "" {
		cfg.Server.DataDir = "./data"
	}
	if cfg.Server.MaxUploadMB == 0 {
		cfg.Server.MaxUploadMB = 500
	}
	if cfg.Routing.Mode == "" {
		cfg.Routing.Mode = "path"
	}
	if cfg.Tailscale.AuthKey == "" {
		cfg.Tailscale.AuthKey = os.Getenv("TS_AUTHKEY")
	}
	if cfg.Tailscale.Capability == "" {
		return nil, fmt.Errorf("tailscale.capability is required")
	}
	return &cfg, nil
}
```

**Step 4: Run tests**

```bash
go test ./config/ -v
```

Expected: all 4 tests PASS.

**Step 5: Commit**

```bash
git add config/
git commit -m "feat: add config package with TOML loading and defaults"
```

---

### Task 3: Storage Layer

**Files:**
- Create: `internal/storage/store.go`
- Create: `internal/storage/store_test.go`

**Step 1: Write the failing tests**

Create `internal/storage/store_test.go`:
```go
package storage

import (
	"os"
	"path/filepath"
	"testing"
)

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
	if filepath.Base(root) != "current" {
		t.Errorf("site root should end in 'current', got %q", root)
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
```

**Step 2: Run tests to verify they fail**

```bash
go test ./internal/storage/
```

Expected: FAIL — `New`, `NewDeploymentID`, etc. not defined.

**Step 3: Implement storage package**

Create `internal/storage/store.go`:
```go
package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

type Store struct {
	dataDir string
}

type SiteInfo struct {
	Name               string `json:"name"`
	ActiveDeploymentID string `json:"active_deployment_id"`
}

func New(dataDir string) *Store {
	return &Store{dataDir: dataDir}
}

func NewDeploymentID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) CreateDeployment(site, id string) (string, error) {
	dir := filepath.Join(s.dataDir, "sites", site, "deployments", id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create deployment dir: %w", err)
	}
	return dir, nil
}

func (s *Store) MarkComplete(site, id string) error {
	marker := filepath.Join(s.dataDir, "sites", site, "deployments", id, ".complete")
	return os.WriteFile(marker, nil, 0644)
}

func (s *Store) ActivateDeployment(site, id string) error {
	link := filepath.Join(s.dataDir, "sites", site, "current")
	target := filepath.Join("deployments", id)

	// Atomic swap: create temp symlink, rename over current
	tmp := link + ".tmp"
	os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}
	if err := os.Rename(tmp, link); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("swap symlink: %w", err)
	}
	return nil
}

func (s *Store) CurrentDeployment(site string) (string, error) {
	link := filepath.Join(s.dataDir, "sites", site, "current")
	target, err := os.Readlink(link)
	if err != nil {
		return "", fmt.Errorf("no active deployment for site %q: %w", site, err)
	}
	return filepath.Base(target), nil
}

func (s *Store) SiteRoot(site string) string {
	return filepath.Join(s.dataDir, "sites", site, "current")
}

func (s *Store) ListSites() ([]SiteInfo, error) {
	sitesDir := filepath.Join(s.dataDir, "sites")
	entries, err := os.ReadDir(sitesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sites []SiteInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info := SiteInfo{Name: e.Name()}
		if id, err := s.CurrentDeployment(e.Name()); err == nil {
			info.ActiveDeploymentID = id
		}
		sites = append(sites, info)
	}
	return sites, nil
}

func (s *Store) CleanupOrphans() {
	sitesDir := filepath.Join(s.dataDir, "sites")
	siteEntries, err := os.ReadDir(sitesDir)
	if err != nil {
		return
	}
	for _, site := range siteEntries {
		if !site.IsDir() {
			continue
		}
		deploymentsDir := filepath.Join(sitesDir, site.Name(), "deployments")
		depEntries, err := os.ReadDir(deploymentsDir)
		if err != nil {
			continue
		}
		for _, dep := range depEntries {
			if !dep.IsDir() {
				continue
			}
			marker := filepath.Join(deploymentsDir, dep.Name(), ".complete")
			if _, err := os.Stat(marker); os.IsNotExist(err) {
				os.RemoveAll(filepath.Join(deploymentsDir, dep.Name()))
			}
		}
	}
}
```

**Step 4: Run tests**

```bash
go test ./internal/storage/ -v
```

Expected: all 8 tests PASS.

**Step 5: Commit**

```bash
git add internal/storage/
git commit -m "feat: add storage layer with deployment CRUD and symlink management"
```

---

### Task 4: Auth Package

**Files:**
- Create: `internal/auth/caps.go`
- Create: `internal/auth/caps_test.go`

**Step 1: Write the failing tests**

Create `internal/auth/caps_test.go`:
```go
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCanView(t *testing.T) {
	tests := []struct {
		name string
		caps []Cap
		site string
		want bool
	}{
		{"view grant", []Cap{{View: []string{"docs"}}}, "docs", true},
		{"view wildcard", []Cap{{View: []string{"*"}}}, "docs", true},
		{"deploy implies view", []Cap{{Deploy: []string{"docs"}}}, "docs", true},
		{"admin implies view", []Cap{{Admin: true}}, "docs", true},
		{"no grant", []Cap{{View: []string{"other"}}}, "docs", false},
		{"empty caps", []Cap{}, "docs", false},
		{"nil caps", nil, "docs", false},
		{"multi cap merge", []Cap{{View: []string{"a"}}, {View: []string{"docs"}}}, "docs", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanView(tt.caps, tt.site); got != tt.want {
				t.Errorf("CanView() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCanDeploy(t *testing.T) {
	tests := []struct {
		name string
		caps []Cap
		site string
		want bool
	}{
		{"deploy grant", []Cap{{Deploy: []string{"docs"}}}, "docs", true},
		{"deploy wildcard", []Cap{{Deploy: []string{"*"}}}, "docs", true},
		{"admin implies deploy", []Cap{{Admin: true}}, "docs", true},
		{"view does not imply deploy", []Cap{{View: []string{"docs"}}}, "docs", false},
		{"no grant", []Cap{{Deploy: []string{"other"}}}, "docs", false},
		{"empty caps", []Cap{}, "docs", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanDeploy(tt.caps, tt.site); got != tt.want {
				t.Errorf("CanDeploy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAdmin(t *testing.T) {
	tests := []struct {
		name string
		caps []Cap
		want bool
	}{
		{"admin true", []Cap{{Admin: true}}, true},
		{"admin false", []Cap{{Admin: false}}, false},
		{"multi merge", []Cap{{Admin: false}, {Admin: true}}, true},
		{"empty", []Cap{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAdmin(tt.caps); got != tt.want {
				t.Errorf("IsAdmin() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseCaps(t *testing.T) {
	raw := []json.RawMessage{
		json.RawMessage(`{"view":["docs","demo"],"deploy":["docs"]}`),
		json.RawMessage(`{"admin":true}`),
	}
	caps, err := ParseCaps(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(caps) != 2 {
		t.Fatalf("got %d caps, want 2", len(caps))
	}
	if !CanView(caps, "demo") {
		t.Error("expected view access to demo")
	}
	if !CanDeploy(caps, "docs") {
		t.Error("expected deploy access to docs")
	}
	if !IsAdmin(caps) {
		t.Error("expected admin")
	}
}

func TestMiddleware_NoCaps(t *testing.T) {
	// Mock WhoIs client that returns no capabilities
	client := &mockWhoIs{caps: nil}
	handler := Middleware(client, "example.com/cap/pages")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		}),
	)
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "100.64.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Handler should still be called — individual handlers decide on 403
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	caps := CapsFromContext(req.Context())
	if len(caps) != 0 {
		t.Errorf("expected no caps, got %d", len(caps))
	}
}

func TestMiddleware_WithCaps(t *testing.T) {
	raw := []json.RawMessage{json.RawMessage(`{"view":["*"]}`)}
	client := &mockWhoIs{caps: raw}

	var gotCaps []Cap
	handler := Middleware(client, "example.com/cap/pages")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotCaps = CapsFromContext(r.Context())
			w.WriteHeader(200)
		}),
	)
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "100.64.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if len(gotCaps) != 1 {
		t.Fatalf("expected 1 cap, got %d", len(gotCaps))
	}
	if !CanView(gotCaps, "anything") {
		t.Error("expected wildcard view access")
	}
}

// mockWhoIs implements WhoIsClient for testing
type mockWhoIs struct {
	caps []json.RawMessage
	err  error
}

func (m *mockWhoIs) WhoIs(ctx context.Context, remoteAddr string) (*WhoIsResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	capMap := make(map[string][]json.RawMessage)
	if m.caps != nil {
		capMap["example.com/cap/pages"] = m.caps
	}
	return &WhoIsResult{CapMap: capMap}, nil
}
```

**Step 2: Run tests to verify they fail**

```bash
go test ./internal/auth/
```

Expected: FAIL — types and functions not defined.

**Step 3: Implement auth package**

Create `internal/auth/caps.go`:
```go
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Cap represents a single capability object from the tailnet policy.
type Cap struct {
	View   []string `json:"view"`
	Deploy []string `json:"deploy"`
	Admin  bool     `json:"admin"`
}

// WhoIsResult is the subset of WhoIs response data we need.
// This decouples us from the tailscale types for testability.
type WhoIsResult struct {
	CapMap map[string][]json.RawMessage
}

// WhoIsClient abstracts the tailscale LocalClient.WhoIs call.
type WhoIsClient interface {
	WhoIs(ctx context.Context, remoteAddr string) (*WhoIsResult, error)
}

type capsKey struct{}

// ParseCaps unmarshals raw JSON capability objects into Cap structs.
func ParseCaps(raw []json.RawMessage) ([]Cap, error) {
	caps := make([]Cap, 0, len(raw))
	for _, r := range raw {
		var c Cap
		if err := json.Unmarshal(r, &c); err != nil {
			return nil, fmt.Errorf("parsing capability: %w", err)
		}
		caps = append(caps, c)
	}
	return caps, nil
}

func matchesSite(list []string, site string) bool {
	for _, s := range list {
		if s == "*" || s == site {
			return true
		}
	}
	return false
}

// CanView reports whether caps grant view access to the named site.
func CanView(caps []Cap, site string) bool {
	for _, c := range caps {
		if c.Admin || matchesSite(c.View, site) || matchesSite(c.Deploy, site) {
			return true
		}
	}
	return false
}

// CanDeploy reports whether caps grant deploy access to the named site.
func CanDeploy(caps []Cap, site string) bool {
	for _, c := range caps {
		if c.Admin || matchesSite(c.Deploy, site) {
			return true
		}
	}
	return false
}

// IsAdmin reports whether any cap grants admin access.
func IsAdmin(caps []Cap) bool {
	for _, c := range caps {
		if c.Admin {
			return true
		}
	}
	return false
}

// CapsFromContext retrieves parsed caps from the request context.
func CapsFromContext(ctx context.Context) []Cap {
	caps, _ := ctx.Value(capsKey{}).([]Cap)
	return caps
}

// Middleware returns HTTP middleware that calls WhoIs, parses capabilities,
// and attaches them to the request context. It does NOT enforce permissions —
// individual handlers decide what access level is required.
func Middleware(client WhoIsClient, capName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			result, err := client.WhoIs(r.Context(), r.RemoteAddr)
			if err != nil {
				http.Error(w, "identity check failed", http.StatusForbidden)
				return
			}

			var caps []Cap
			if raw, ok := result.CapMap[capName]; ok && len(raw) > 0 {
				parsed, err := ParseCaps(raw)
				if err != nil {
					http.Error(w, "invalid capabilities", http.StatusInternalServerError)
					return
				}
				caps = parsed
			}

			ctx := context.WithValue(r.Context(), capsKey{}, caps)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
```

**Step 4: Run tests**

```bash
go test ./internal/auth/ -v
```

Expected: all tests PASS.

**Step 5: Commit**

```bash
git add internal/auth/
git commit -m "feat: add auth package with capability parsing and middleware"
```

---

### Task 5: Deploy Handler (ZIP extraction + HTTP handler)

**Files:**
- Create: `internal/deploy/extract.go`
- Create: `internal/deploy/extract_test.go`
- Create: `internal/deploy/handler.go`
- Create: `internal/deploy/handler_test.go`

**Step 1: Write ZIP extraction tests**

Create `internal/deploy/extract_test.go`:
```go
package deploy

import (
	"archive/zip"
	"bytes"
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

func TestExtractZip_Valid(t *testing.T) {
	dir := t.TempDir()
	data := makeZip(t, map[string]string{
		"index.html":      "<h1>Hello</h1>",
		"assets/style.css": "body{}",
	})
	n, err := ExtractZip(bytes.NewReader(data), int64(len(data)), dir, 10<<20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n == 0 {
		t.Error("expected nonzero bytes written")
	}
	content, _ := os.ReadFile(filepath.Join(dir, "index.html"))
	if string(content) != "<h1>Hello</h1>" {
		t.Errorf("index.html = %q", content)
	}
	content, _ = os.ReadFile(filepath.Join(dir, "assets", "style.css"))
	if string(content) != "body{}" {
		t.Errorf("style.css = %q", content)
	}
}

func TestExtractZip_ZipSlip(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	// Manually create entry with path traversal
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
	_, err := ExtractZip(bytes.NewReader(data), int64(len(data)), dir, 100) // 100 byte limit
	if err == nil {
		t.Fatal("expected size limit error")
	}
}
```

**Step 2: Run tests to verify they fail**

```bash
go test ./internal/deploy/ -run TestExtract
```

Expected: FAIL — `ExtractZip` not defined.

**Step 3: Implement ZIP extraction**

Create `internal/deploy/extract.go`:
```go
package deploy

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExtractZip extracts a ZIP archive into destDir.
// It rejects entries that escape destDir (zip-slip) and enforces maxBytes.
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
			os.MkdirAll(dest, 0755)
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
```

**Step 4: Run extraction tests**

```bash
go test ./internal/deploy/ -run TestExtract -v
```

Expected: all 3 tests PASS.

**Step 5: Write deploy handler tests**

Create `internal/deploy/handler_test.go`:
```go
package deploy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

func withCaps(r *http.Request, caps []auth.Cap) *http.Request {
	ctx := auth.ContextWithCaps(r.Context(), caps)
	return r.WithContext(ctx)
}

func TestHandler_Success(t *testing.T) {
	store := storage.New(t.TempDir())
	h := NewHandler(store, "https://pages.test.ts.net", 10)

	body := makeZip(t, map[string]string{"index.html": "<h1>Hi</h1>"})
	req := httptest.NewRequest("POST", "/deploy/docs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/zip")
	req = withCaps(req, []auth.Cap{{Deploy: []string{"docs"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp DeployResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Site != "docs" {
		t.Errorf("site = %q, want %q", resp.Site, "docs")
	}
	if resp.DeploymentID == "" {
		t.Error("expected deployment_id")
	}
}

func TestHandler_Forbidden(t *testing.T) {
	store := storage.New(t.TempDir())
	h := NewHandler(store, "https://pages.test.ts.net", 10)

	body := makeZip(t, map[string]string{"index.html": "hi"})
	req := httptest.NewRequest("POST", "/deploy/docs", bytes.NewReader(body))
	req = withCaps(req, []auth.Cap{{Deploy: []string{"other"}}}) // wrong site
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestHandler_ActivateFalse(t *testing.T) {
	store := storage.New(t.TempDir())
	h := NewHandler(store, "https://pages.test.ts.net", 10)

	body := makeZip(t, map[string]string{"index.html": "hi"})
	req := httptest.NewRequest("POST", "/deploy/docs?activate=false", bytes.NewReader(body))
	req = withCaps(req, []auth.Cap{{Deploy: []string{"docs"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	// Should have no active deployment
	_, err := store.CurrentDeployment("docs")
	if err == nil {
		t.Error("expected no active deployment when activate=false")
	}
}
```

**Step 6: Implement deploy handler**

First, add `ContextWithCaps` to the auth package. Add to `internal/auth/caps.go`:
```go
// ContextWithCaps adds caps to a context. Used by tests.
func ContextWithCaps(ctx context.Context, caps []Cap) context.Context {
	return context.WithValue(ctx, capsKey{}, caps)
}
```

Create `internal/deploy/handler.go`:
```go
package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

type DeployResponse struct {
	DeploymentID string `json:"deployment_id"`
	Site         string `json:"site"`
	URL          string `json:"url"`
}

type Handler struct {
	store       *storage.Store
	baseURL     string
	maxUploadMB int
}

func NewHandler(store *storage.Store, baseURL string, maxUploadMB int) *Handler {
	return &Handler{store: store, baseURL: baseURL, maxUploadMB: maxUploadMB}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site := r.PathValue("site")
	if site == "" {
		http.Error(w, "missing site name", http.StatusBadRequest)
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanDeploy(caps, site) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	maxBytes := int64(h.maxUploadMB) << 20
	if r.ContentLength > maxBytes {
		http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		http.Error(w, "reading body", http.StatusInternalServerError)
		return
	}
	if int64(len(body)) > maxBytes {
		http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
		return
	}

	id := storage.NewDeploymentID()
	deployDir, err := h.store.CreateDeployment(site, id)
	if err != nil {
		http.Error(w, "creating deployment", http.StatusInternalServerError)
		return
	}

	_, err = ExtractZip(bytes.NewReader(body), int64(len(body)), deployDir, maxBytes)
	if err != nil {
		http.Error(w, fmt.Sprintf("extracting zip: %v", err), http.StatusBadRequest)
		return
	}

	if err := h.store.MarkComplete(site, id); err != nil {
		http.Error(w, "finalizing deployment", http.StatusInternalServerError)
		return
	}

	if r.URL.Query().Get("activate") != "false" {
		if err := h.store.ActivateDeployment(site, id); err != nil {
			http.Error(w, "activating deployment", http.StatusInternalServerError)
			return
		}
	}

	resp := DeployResponse{
		DeploymentID: id,
		Site:         site,
		URL:          fmt.Sprintf("%s/%s/", h.baseURL, site),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
```

**Step 7: Run all deploy tests**

```bash
go test ./internal/deploy/ -v
```

Expected: all 6 tests PASS.

**Step 8: Commit**

```bash
git add internal/deploy/ internal/auth/caps.go
git commit -m "feat: add deploy handler with ZIP extraction and authorization"
```

---

### Task 6: Static File Server

**Files:**
- Create: `internal/serve/handler.go`
- Create: `internal/serve/handler_test.go`

**Step 1: Write the failing tests**

Create `internal/serve/handler_test.go`:
```go
package serve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

func setupSite(t *testing.T, store *storage.Store, site, id string, files map[string]string) {
	t.Helper()
	dir, _ := store.CreateDeployment(site, id)
	for name, content := range files {
		path := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(path), 0755)
		os.WriteFile(path, []byte(content), 0644)
	}
	store.MarkComplete(site, id)
	store.ActivateDeployment(site, id)
}

func withCaps(r *http.Request, caps []auth.Cap) *http.Request {
	return r.WithContext(auth.ContextWithCaps(r.Context(), caps))
}

func TestHandler_ServesFile(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Docs</h1>",
		"style.css":  "body{}",
	})

	h := NewHandler(store)
	req := httptest.NewRequest("GET", "/docs/style.css", nil)
	req = withCaps(req, []auth.Cap{{View: []string{"docs"}}})
	req.SetPathValue("site", "docs")
	req.SetPathValue("path", "style.css")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "body{}" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestHandler_IndexFallback(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "<h1>Index</h1>",
	})

	h := NewHandler(store)
	// Request the directory (trailing slash)
	req := httptest.NewRequest("GET", "/docs/", nil)
	req = withCaps(req, []auth.Cap{{View: []string{"docs"}}})
	req.SetPathValue("site", "docs")
	req.SetPathValue("path", "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Forbidden(t *testing.T) {
	store := storage.New(t.TempDir())
	setupSite(t, store, "docs", "aaa11111", map[string]string{
		"index.html": "hi",
	})

	h := NewHandler(store)
	req := httptest.NewRequest("GET", "/docs/index.html", nil)
	req = withCaps(req, []auth.Cap{{View: []string{"other"}}}) // wrong site
	req.SetPathValue("site", "docs")
	req.SetPathValue("path", "index.html")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestHandler_NoDeployment(t *testing.T) {
	store := storage.New(t.TempDir())

	h := NewHandler(store)
	req := httptest.NewRequest("GET", "/nonexistent/index.html", nil)
	req = withCaps(req, []auth.Cap{{View: []string{"*"}}})
	req.SetPathValue("site", "nonexistent")
	req.SetPathValue("path", "index.html")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
```

**Step 2: Run tests to verify they fail**

```bash
go test ./internal/serve/
```

Expected: FAIL — `NewHandler` not defined.

**Step 3: Implement serve handler**

Create `internal/serve/handler.go`:
```go
package serve

import (
	"net/http"
	"path/filepath"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

type Handler struct {
	store *storage.Store
}

func NewHandler(store *storage.Store) *Handler {
	return &Handler{store: store}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site := r.PathValue("site")
	if site == "" {
		http.NotFound(w, r)
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.CanView(caps, site) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	_, err := h.store.CurrentDeployment(site)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	root := h.store.SiteRoot(site)
	filePath := r.PathValue("path")
	if filePath == "" {
		filePath = "index.html"
	}

	fullPath := filepath.Join(root, filePath)
	http.ServeFile(w, r, fullPath)
}
```

**Step 4: Run tests**

```bash
go test ./internal/serve/ -v
```

Expected: all 4 tests PASS.

**Step 5: Commit**

```bash
git add internal/serve/
git commit -m "feat: add static file server with view authorization"
```

---

### Task 7: Admin Status Handler

**Files:**
- Create: `internal/admin/handler.go`
- Create: `internal/admin/handler_test.go`

**Step 1: Write the failing tests**

Create `internal/admin/handler_test.go`:
```go
package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

func withCaps(r *http.Request, caps []auth.Cap) *http.Request {
	return r.WithContext(auth.ContextWithCaps(r.Context(), caps))
}

func TestStatusHandler_Admin(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateDeployment("docs", "aaa11111")
	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")

	h := NewStatusHandler(store)
	req := httptest.NewRequest("GET", "/_admin/status", nil)
	req = withCaps(req, []auth.Cap{{Admin: true}})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp StatusResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Sites) != 1 {
		t.Fatalf("got %d sites, want 1", len(resp.Sites))
	}
	if resp.Sites[0].Name != "docs" {
		t.Errorf("site name = %q, want %q", resp.Sites[0].Name, "docs")
	}
}

func TestStatusHandler_Forbidden(t *testing.T) {
	store := storage.New(t.TempDir())
	h := NewStatusHandler(store)

	req := httptest.NewRequest("GET", "/_admin/status", nil)
	req = withCaps(req, []auth.Cap{{View: []string{"*"}}}) // view only, not admin

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}
```

**Step 2: Run tests to verify they fail**

```bash
go test ./internal/admin/
```

Expected: FAIL — types not defined.

**Step 3: Implement admin handler**

Create `internal/admin/handler.go`:
```go
package admin

import (
	"encoding/json"
	"net/http"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

type StatusResponse struct {
	Sites []storage.SiteInfo `json:"sites"`
}

type StatusHandler struct {
	store *storage.Store
}

func NewStatusHandler(store *storage.Store) *StatusHandler {
	return &StatusHandler{store: store}
}

func (h *StatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())
	if !auth.IsAdmin(caps) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	sites, err := h.store.ListSites()
	if err != nil {
		http.Error(w, "listing sites", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(StatusResponse{Sites: sites})
}
```

**Step 4: Run tests**

```bash
go test ./internal/admin/ -v
```

Expected: all 2 tests PASS.

**Step 5: Commit**

```bash
git add internal/admin/
git commit -m "feat: add admin status endpoint with admin-only authorization"
```

---

### Task 8: Main Wiring + tsnet Adapter

**Files:**
- Create: `internal/tsadapter/adapter.go` (wraps real tsnet WhoIs into our interface)
- Modify: `cmd/tspages/main.go`

**Step 1: Create tsnet adapter**

This bridges the real tailscale `LocalClient` to our `auth.WhoIsClient` interface.

Create `internal/tsadapter/adapter.go`:
```go
package tsadapter

import (
	"context"
	"encoding/json"

	"tspages/internal/auth"

	"tailscale.com/client/tailscale"
)

// Adapter wraps a real tailscale LocalClient to implement auth.WhoIsClient.
type Adapter struct {
	client *tailscale.LocalClient
}

func New(client *tailscale.LocalClient) *Adapter {
	return &Adapter{client: client}
}

func (a *Adapter) WhoIs(ctx context.Context, remoteAddr string) (*auth.WhoIsResult, error) {
	who, err := a.client.WhoIs(ctx, remoteAddr)
	if err != nil {
		return nil, err
	}

	result := &auth.WhoIsResult{
		CapMap: make(map[string][]json.RawMessage),
	}
	for k, v := range who.Node.CapMap {
		raw := make([]json.RawMessage, len(v))
		for i, msg := range v {
			raw[i] = json.RawMessage(msg)
		}
		result.CapMap[string(k)] = raw
	}
	return result, nil
}
```

**Step 2: Wire up main.go**

Replace `cmd/tspages/main.go`:
```go
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"tspages/config"
	"tspages/internal/admin"
	"tspages/internal/auth"
	"tspages/internal/deploy"
	"tspages/internal/serve"
	"tspages/internal/storage"
	"tspages/internal/tsadapter"

	"tailscale.com/tsnet"
)

func main() {
	configPath := flag.String("config", "tspages.toml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	store := storage.New(cfg.Server.DataDir)
	store.CleanupOrphans()

	srv := &tsnet.Server{
		Hostname: cfg.Tailscale.Hostname,
		Dir:      cfg.Tailscale.StateDir,
		AuthKey:  cfg.Tailscale.AuthKey,
	}
	defer srv.Close()

	lc, err := srv.LocalClient()
	if err != nil {
		log.Fatalf("getting local client: %v", err)
	}

	whoIsClient := tsadapter.New(lc)
	withAuth := auth.Middleware(whoIsClient, cfg.Tailscale.Capability)

	baseURL := fmt.Sprintf("https://%s", cfg.Tailscale.Hostname)
	deployHandler := deploy.NewHandler(store, baseURL, cfg.Server.MaxUploadMB)
	serveHandler := serve.NewHandler(store)
	statusHandler := admin.NewStatusHandler(store)

	mux := http.NewServeMux()
	mux.Handle("POST /deploy/{site}", withAuth(deployHandler))
	mux.Handle("GET /_admin/status", withAuth(statusHandler))
	mux.Handle("GET /{site}/{path...}", withAuth(serveHandler))

	ln, err := srv.ListenTLS("tcp", ":443")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	log.Printf("tspages listening on https://%s", cfg.Tailscale.Hostname)
	log.Fatal(http.Serve(ln, mux))
}
```

**Step 3: Verify it compiles**

```bash
go build ./cmd/tspages
```

Expected: compiles successfully. Note: The tsadapter may need import path adjustments depending on exact tailscale module API — fix any compile errors with the actual tailscale types.

**Step 4: Run all tests**

```bash
go test ./...
```

Expected: all tests across all packages PASS.

**Step 5: Commit**

```bash
git add internal/tsadapter/ cmd/tspages/main.go
git commit -m "feat: wire up main with tsnet, auth middleware, and all handlers"
```

---

## Summary

| Task | What | Key files |
|------|------|-----------|
| 1 | Scaffolding | `go.mod`, `cmd/tspages/main.go`, `.gitignore` |
| 2 | Config | `config/config.go`, `config/config_test.go` |
| 3 | Storage | `internal/storage/store.go`, `internal/storage/store_test.go` |
| 4 | Auth | `internal/auth/caps.go`, `internal/auth/caps_test.go` |
| 5 | Deploy | `internal/deploy/extract.go`, `handler.go`, tests |
| 6 | Serve | `internal/serve/handler.go`, tests |
| 7 | Admin | `internal/admin/handler.go`, tests |
| 8 | Main | `internal/tsadapter/adapter.go`, `cmd/tspages/main.go` |

Total: 8 tasks, ~15 source files, ~20 tests.
