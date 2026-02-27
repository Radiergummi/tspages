package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"tspages/internal/storage"
)

type Config struct {
	Tailscale TailscaleConfig    `toml:"tailscale"`
	Server    ServerConfig       `toml:"server"`
	Defaults  storage.SiteConfig `toml:"defaults"`
}

type TailscaleConfig struct {
	Hostname   string `toml:"hostname"`
	StateDir   string `toml:"state_dir"`
	AuthKey    string `toml:"auth_key"`
	Capability string `toml:"capability"`
}

type ServerConfig struct {
	DataDir        string `toml:"data_dir"`
	MaxUploadMB    int    `toml:"max_upload_mb"`
	MaxSites       int    `toml:"max_sites"`
	MaxDeployments int    `toml:"max_deployments"`
	LogLevel       string `toml:"log_level"`
	HealthAddr     string `toml:"health_addr"`
	HideFooter     bool   `toml:"hide_footer"`
}

func Load(path string) (*Config, error) {
	var cfg Config
	md, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Warn about unknown keys (likely typos).
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		slog.Warn("unknown keys in config file (check for typos)", "keys", strings.Join(keys, ", "))
	}

	if cfg.Tailscale.Hostname == "" {
		cfg.Tailscale.Hostname = "pages"
	}
	if cfg.Server.DataDir == "" {
		cfg.Server.DataDir = "./data"
	}
	// Only apply defaults for fields not explicitly set in the config file.
	if !md.IsDefined("server", "max_upload_mb") {
		cfg.Server.MaxUploadMB = 500
	}
	if !md.IsDefined("server", "max_sites") {
		cfg.Server.MaxSites = 100
	}
	if !md.IsDefined("server", "max_deployments") {
		cfg.Server.MaxDeployments = 10
	}
	if cfg.Server.LogLevel == "" {
		cfg.Server.LogLevel = os.Getenv("TSPAGES_LOG_LEVEL")
	}
	if cfg.Server.LogLevel == "" {
		cfg.Server.LogLevel = "warn"
	}
	if cfg.Tailscale.StateDir == "" {
		cfg.Tailscale.StateDir = "./state"
	}
	if cfg.Tailscale.AuthKey == "" {
		cfg.Tailscale.AuthKey = os.Getenv("TS_AUTHKEY")
	}
	if cfg.Tailscale.Capability == "" {
		cfg.Tailscale.Capability = os.Getenv("TSPAGES_CAPABILITY")
	}
	if cfg.Tailscale.Capability == "" {
		cfg.Tailscale.Capability = "tspages.mazetti.me/cap/pages"
	}
	if cfg.Server.HealthAddr == "" {
		cfg.Server.HealthAddr = os.Getenv("TSPAGES_HEALTH_ADDR")
	}

	if cfg.Server.MaxUploadMB < 0 {
		return nil, fmt.Errorf("max_upload_mb must be non-negative, got %d", cfg.Server.MaxUploadMB)
	}
	if cfg.Server.MaxSites < 0 {
		return nil, fmt.Errorf("max_sites must be non-negative, got %d", cfg.Server.MaxSites)
	}
	if cfg.Server.MaxDeployments < 0 {
		return nil, fmt.Errorf("max_deployments must be non-negative, got %d", cfg.Server.MaxDeployments)
	}

	return &cfg, nil
}
