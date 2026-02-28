package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_SiteConfig(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	if err := Init(nil); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "tspages.toml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	for _, key := range []string{"spa_routing", "html_extensions", "analytics", "index_page", "not_found_page", "trailing_slash", "webhook_url", "redirects", "headers"} {
		if !strings.Contains(content, key) {
			t.Errorf("site config missing key %q", key)
		}
	}
}

func TestInit_ServerConfig(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	if err := Init([]string{"--server"}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "tspages.toml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	for _, key := range []string{"[tailscale]", "hostname", "state_dir", "auth_key", "capability", "[server]", "data_dir", "max_upload_mb", "max_sites", "max_deployments", "log_level", "[defaults]"} {
		if !strings.Contains(content, key) {
			t.Errorf("server config missing key %q", key)
		}
	}
}

func TestInit_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	os.WriteFile("tspages.toml", []byte("existing"), 0644)

	err := Init(nil)
	if err == nil {
		t.Fatal("expected error when file exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want 'already exists'", err)
	}

	// Verify original content was not changed.
	data, _ := os.ReadFile("tspages.toml")
	if string(data) != "existing" {
		t.Error("existing file was modified")
	}
}
