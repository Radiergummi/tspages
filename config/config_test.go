package config

import (
	"fmt"
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

func TestLoad_EnvVarOverrides(t *testing.T) {
	// Minimal TOML — no fields set beyond the required section headers.
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	os.WriteFile(path, []byte("[tailscale]\n[server]\n"), 0644)

	tests := []struct {
		envKey string
		envVal string
		check  func(*Config) error
	}{
		{"TSPAGES_HOSTNAME", "myhost", func(c *Config) error {
			if c.Tailscale.Hostname != "myhost" {
				return fmt.Errorf("hostname = %q, want %q", c.Tailscale.Hostname, "myhost")
			}
			return nil
		}},
		{"TSPAGES_STATE_DIR", "/tmp/state", func(c *Config) error {
			if c.Tailscale.StateDir != "/tmp/state" {
				return fmt.Errorf("state_dir = %q, want %q", c.Tailscale.StateDir, "/tmp/state")
			}
			return nil
		}},
		{"TSPAGES_DATA_DIR", "/tmp/data", func(c *Config) error {
			if c.Server.DataDir != "/tmp/data" {
				return fmt.Errorf("data_dir = %q, want %q", c.Server.DataDir, "/tmp/data")
			}
			return nil
		}},
		{"TSPAGES_MAX_UPLOAD_MB", "42", func(c *Config) error {
			if c.Server.MaxUploadMB != 42 {
				return fmt.Errorf("max_upload_mb = %d, want %d", c.Server.MaxUploadMB, 42)
			}
			return nil
		}},
		{"TSPAGES_MAX_SITES", "25", func(c *Config) error {
			if c.Server.MaxSites != 25 {
				return fmt.Errorf("max_sites = %d, want %d", c.Server.MaxSites, 25)
			}
			return nil
		}},
		{"TSPAGES_MAX_DEPLOYMENTS", "3", func(c *Config) error {
			if c.Server.MaxDeployments != 3 {
				return fmt.Errorf("max_deployments = %d, want %d", c.Server.MaxDeployments, 3)
			}
			return nil
		}},
		{"TSPAGES_HIDE_FOOTER", "true", func(c *Config) error {
			if !c.Server.HideFooter {
				return fmt.Errorf("hide_footer = false, want true")
			}
			return nil
		}},
		{"TSPAGES_HIDE_FOOTER", "1", func(c *Config) error {
			if !c.Server.HideFooter {
				return fmt.Errorf("hide_footer = false, want true for value '1'")
			}
			return nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.envKey+"="+tt.envVal, func(t *testing.T) {
			t.Setenv(tt.envKey, tt.envVal)
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if err := tt.check(cfg); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestLoad_ConfigTakesPrecedenceOverEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	os.WriteFile(path, []byte(`
[tailscale]
hostname   = "fromfile"
state_dir  = "/file/state"
auth_key   = "tskey-file"
capability = "file.com/cap/pages"

[server]
data_dir        = "/file/data"
max_upload_mb   = 999
max_sites       = 88
max_deployments = 77
log_level       = "debug"
health_addr     = ":9999"
hide_footer     = true
`), 0644)

	// Set ALL env vars — config file should win for every field.
	t.Setenv("TSPAGES_HOSTNAME", "fromenv")
	t.Setenv("TSPAGES_STATE_DIR", "/env/state")
	t.Setenv("TS_AUTHKEY", "tskey-env")
	t.Setenv("TSPAGES_CAPABILITY", "env.com/cap/pages")
	t.Setenv("TSPAGES_DATA_DIR", "/env/data")
	t.Setenv("TSPAGES_MAX_UPLOAD_MB", "1")
	t.Setenv("TSPAGES_MAX_SITES", "2")
	t.Setenv("TSPAGES_MAX_DEPLOYMENTS", "3")
	t.Setenv("TSPAGES_LOG_LEVEL", "error")
	t.Setenv("TSPAGES_HEALTH_ADDR", ":1111")
	t.Setenv("TSPAGES_HIDE_FOOTER", "false")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"hostname", cfg.Tailscale.Hostname, "fromfile"},
		{"state_dir", cfg.Tailscale.StateDir, "/file/state"},
		{"auth_key", cfg.Tailscale.AuthKey, "tskey-file"},
		{"capability", cfg.Tailscale.Capability, "file.com/cap/pages"},
		{"data_dir", cfg.Server.DataDir, "/file/data"},
		{"max_upload_mb", cfg.Server.MaxUploadMB, 999},
		{"max_sites", cfg.Server.MaxSites, 88},
		{"max_deployments", cfg.Server.MaxDeployments, 77},
		{"log_level", cfg.Server.LogLevel, "debug"},
		{"health_addr", cfg.Server.HealthAddr, ":9999"},
		{"hide_footer", cfg.Server.HideFooter, true},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestLoad_InvalidIntEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tspages.toml")
	os.WriteFile(path, []byte("[tailscale]\n[server]\n"), 0644)

	for _, envKey := range []string{"TSPAGES_MAX_UPLOAD_MB", "TSPAGES_MAX_SITES", "TSPAGES_MAX_DEPLOYMENTS"} {
		t.Run(envKey, func(t *testing.T) {
			t.Setenv(envKey, "notanumber")
			_, err := Load(path)
			if err == nil {
				t.Fatalf("expected error for %s=notanumber", envKey)
			}
		})
	}
}
