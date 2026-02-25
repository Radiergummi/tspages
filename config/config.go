package config

import (
	"fmt"
	"os"

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
	if cfg.Server.MaxSites == 0 {
		cfg.Server.MaxSites = 100
	}
	if cfg.Server.MaxDeployments == 0 {
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
	return &cfg, nil
}
