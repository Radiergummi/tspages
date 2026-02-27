package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/tspages.toml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_MalformedTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	os.WriteFile(path, []byte(`[[[invalid toml`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for malformed TOML")
	}
}

func TestLoad_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	if err := os.WriteFile(path, []byte(`
[tailscale]
hostname   = "pages"
state_dir  = "/var/lib/tspages"
capability = "example.com/cap/pages"

[server]
data_dir      = "/data"
max_upload_mb = 200
`), 0644); err != nil {
		t.Fatal(err)
	}

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
	if err := os.WriteFile(path, []byte(`
[tailscale]
capability = "example.com/cap/pages"
`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tailscale.Hostname != "pages" {
		t.Errorf("hostname = %q, want %q", cfg.Tailscale.Hostname, "pages")
	}
	if cfg.Tailscale.StateDir != "./state" {
		t.Errorf("state_dir = %q, want %q", cfg.Tailscale.StateDir, "./state")
	}
	if cfg.Server.DataDir != "./data" {
		t.Errorf("data_dir = %q, want %q", cfg.Server.DataDir, "./data")
	}
	if cfg.Server.MaxUploadMB != 500 {
		t.Errorf("max_upload_mb = %d, want %d", cfg.Server.MaxUploadMB, 500)
	}
	if cfg.Server.LogLevel != "warn" {
		t.Errorf("log_level = %q, want %q", cfg.Server.LogLevel, "warn")
	}
	if cfg.Server.MaxDeployments != 10 {
		t.Errorf("max_deployments = %d, want %d", cfg.Server.MaxDeployments, 10)
	}
}

func TestLoad_CapabilityDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	if err := os.WriteFile(path, []byte(`
[tailscale]
hostname = "pages"
`), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TSPAGES_CAPABILITY", "")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tailscale.Capability != "tspages.mazetti.me/cap/pages" {
		t.Errorf("capability = %q, want default", cfg.Tailscale.Capability)
	}
}

func TestLoad_CapabilityFromEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	if err := os.WriteFile(path, []byte(`
[tailscale]
hostname = "pages"
`), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TSPAGES_CAPABILITY", "example.com/cap/from-env")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tailscale.Capability != "example.com/cap/from-env" {
		t.Errorf("capability = %q, want %q", cfg.Tailscale.Capability, "example.com/cap/from-env")
	}
}

func TestLoad_LogLevelFromEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	if err := os.WriteFile(path, []byte(`
[tailscale]
capability = "example.com/cap/pages"
`), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TSPAGES_LOG_LEVEL", "info")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.LogLevel != "info" {
		t.Errorf("log_level = %q, want %q", cfg.Server.LogLevel, "info")
	}
}

func TestLoad_LogLevelFromConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	if err := os.WriteFile(path, []byte(`
[tailscale]
capability = "example.com/cap/pages"

[server]
log_level = "debug"
`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.LogLevel != "debug" {
		t.Errorf("log_level = %q, want %q", cfg.Server.LogLevel, "debug")
	}
}

func TestLoad_SiteDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	os.WriteFile(path, []byte(`
[tailscale]
capability = "example.com/cap/pages"

[defaults]
spa_routing = true
analytics = true
not_found_page = "404.html"

[defaults.headers]
"/*" = { X-Frame-Options = "DENY" }
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Defaults.SPARouting == nil || *cfg.Defaults.SPARouting != true {
		t.Error("defaults.spa should be true")
	}
	if cfg.Defaults.Analytics == nil || *cfg.Defaults.Analytics != true {
		t.Error("defaults.analytics should be true")
	}
	if cfg.Defaults.NotFoundPage != "404.html" {
		t.Errorf("defaults.not_found_page = %q", cfg.Defaults.NotFoundPage)
	}
	if cfg.Defaults.Headers["/*"]["X-Frame-Options"] != "DENY" {
		t.Error("defaults.headers not parsed")
	}
}

func TestLoad_NoDefaults(t *testing.T) {
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
	if cfg.Defaults.SPARouting != nil {
		t.Error("defaults.spa should be nil")
	}
}

func TestLoad_MaxUploadMBExplicitZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	os.WriteFile(path, []byte(`
[tailscale]
capability = "example.com/cap/pages"

[server]
max_upload_mb = 0
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.MaxUploadMB != 0 {
		t.Errorf("max_upload_mb = %d, want 0 (explicitly set)", cfg.Server.MaxUploadMB)
	}
}

func TestLoad_NegativeValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	os.WriteFile(path, []byte(`
[tailscale]
capability = "example.com/cap/pages"

[server]
max_upload_mb = -1
`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for negative max_upload_mb")
	}
}

func TestLoad_HealthAddrFromEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	if err := os.WriteFile(path, []byte(`
[tailscale]
capability = "example.com/cap/pages"
`), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TSPAGES_HEALTH_ADDR", ":8080")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.HealthAddr != ":8080" {
		t.Errorf("health_addr = %q, want %q", cfg.Server.HealthAddr, ":8080")
	}
}

func TestLoad_HealthAddrFromConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	if err := os.WriteFile(path, []byte(`
[tailscale]
capability = "example.com/cap/pages"

[server]
health_addr = ":9090"
`), 0644); err != nil {
		t.Fatal(err)
	}

	// Env should be ignored when config sets the value.
	t.Setenv("TSPAGES_HEALTH_ADDR", ":8080")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.HealthAddr != ":9090" {
		t.Errorf("health_addr = %q, want %q", cfg.Server.HealthAddr, ":9090")
	}
}

func TestLoad_HideFooter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	if err := os.WriteFile(path, []byte(`
[tailscale]
capability = "example.com/cap/pages"

[server]
hide_footer = true
`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Server.HideFooter {
		t.Error("hide_footer = false, want true")
	}
}

func TestLoad_HideFooterDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	if err := os.WriteFile(path, []byte(`
[tailscale]
capability = "example.com/cap/pages"
`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.HideFooter {
		t.Error("hide_footer should default to false")
	}
}

func TestLoad_AuthKeyFromEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	if err := os.WriteFile(path, []byte(`
[tailscale]
capability = "example.com/cap/pages"
`), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TS_AUTHKEY", "tskey-auth-test123")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tailscale.AuthKey != "tskey-auth-test123" {
		t.Errorf("auth_key = %q, want %q", cfg.Tailscale.AuthKey, "tskey-auth-test123")
	}
}
