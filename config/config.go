package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"tspages/internal/storage"
)

type Config struct {
	Tailscale  TailscaleConfig    `toml:"tailscale"`
	Server     ServerConfig       `toml:"server"`
	Defaults   storage.SiteConfig `toml:"defaults"`
	RateLimits RateLimitsConfig   `toml:"rate_limits"`
}

type RateLimitsConfig struct {
	MutateRate  float64 `toml:"mutate_rate"`
	MutateBurst int     `toml:"mutate_burst"`
	SubmitRate  float64 `toml:"submit_rate"`
	SubmitBurst int     `toml:"submit_burst"`
	QueryRate   float64 `toml:"query_rate"`
	QueryBurst  int     `toml:"query_burst"`
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
		slog.Warn("unknown keys in config file (check for typos)", "component", "config", "keys", strings.Join(keys, ", "))
	}

	// All fields follow TOML > env var > default precedence.
	strDefault(&cfg.Tailscale.Hostname, "TSPAGES_HOSTNAME", "pages")
	strDefault(&cfg.Tailscale.StateDir, "TSPAGES_STATE_DIR", "./state")
	strDefault(&cfg.Tailscale.AuthKey, "TS_AUTHKEY", "")
	strDefault(&cfg.Tailscale.Capability, "TSPAGES_CAPABILITY", "tspages.mazetti.me/cap/pages")
	strDefault(&cfg.Server.DataDir, "TSPAGES_DATA_DIR", "./data")
	strDefault(&cfg.Server.LogLevel, "TSPAGES_LOG_LEVEL", "warn")
	strDefault(&cfg.Server.HealthAddr, "TSPAGES_HEALTH_ADDR", "")

	if err := intDefault(md, &cfg.Server.MaxUploadMB, "TSPAGES_MAX_UPLOAD_MB", 500, "server", "max_upload_mb"); err != nil {
		return nil, err
	}
	if err := intDefault(md, &cfg.Server.MaxSites, "TSPAGES_MAX_SITES", 100, "server", "max_sites"); err != nil {
		return nil, err
	}
	if err := intDefault(md, &cfg.Server.MaxDeployments, "TSPAGES_MAX_DEPLOYMENTS", 10, "server", "max_deployments"); err != nil {
		return nil, err
	}

	boolDefault(md, &cfg.Server.HideFooter, "TSPAGES_HIDE_FOOTER", false, "server", "hide_footer")

	if err := floatDefault(md, &cfg.RateLimits.MutateRate, "TSPAGES_RATE_MUTATE_RATE", 2, "rate_limits", "mutate_rate"); err != nil {
		return nil, err
	}
	if err := intDefault(md, &cfg.RateLimits.MutateBurst, "TSPAGES_RATE_MUTATE_BURST", 5, "rate_limits", "mutate_burst"); err != nil {
		return nil, err
	}
	if err := floatDefault(md, &cfg.RateLimits.SubmitRate, "TSPAGES_RATE_SUBMIT_RATE", 5, "rate_limits", "submit_rate"); err != nil {
		return nil, err
	}
	if err := intDefault(md, &cfg.RateLimits.SubmitBurst, "TSPAGES_RATE_SUBMIT_BURST", 10, "rate_limits", "submit_burst"); err != nil {
		return nil, err
	}
	if err := floatDefault(md, &cfg.RateLimits.QueryRate, "TSPAGES_RATE_QUERY_RATE", 5, "rate_limits", "query_rate"); err != nil {
		return nil, err
	}
	if err := intDefault(md, &cfg.RateLimits.QueryBurst, "TSPAGES_RATE_QUERY_BURST", 10, "rate_limits", "query_burst"); err != nil {
		return nil, err
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

	if cfg.RateLimits.MutateRate < 0 {
		return nil, fmt.Errorf("mutate_rate must be non-negative, got %g", cfg.RateLimits.MutateRate)
	}
	if cfg.RateLimits.MutateBurst < 0 {
		return nil, fmt.Errorf("mutate_burst must be non-negative, got %d", cfg.RateLimits.MutateBurst)
	}
	if cfg.RateLimits.SubmitRate < 0 {
		return nil, fmt.Errorf("submit_rate must be non-negative, got %g", cfg.RateLimits.SubmitRate)
	}
	if cfg.RateLimits.SubmitBurst < 0 {
		return nil, fmt.Errorf("submit_burst must be non-negative, got %d", cfg.RateLimits.SubmitBurst)
	}
	if cfg.RateLimits.QueryRate < 0 {
		return nil, fmt.Errorf("query_rate must be non-negative, got %g", cfg.RateLimits.QueryRate)
	}
	if cfg.RateLimits.QueryBurst < 0 {
		return nil, fmt.Errorf("query_burst must be non-negative, got %d", cfg.RateLimits.QueryBurst)
	}

	return &cfg, nil
}

// strDefault fills *dst from envKey if *dst is empty (not set in TOML),
// then falls back to def.
func strDefault(dst *string, envKey, def string) {
	if *dst == "" {
		*dst = os.Getenv(envKey)
	}
	if *dst == "" {
		*dst = def
	}
}

// intDefault fills *dst from envKey if the TOML key was not defined,
// then falls back to def.
func intDefault(md toml.MetaData, dst *int, envKey string, def int, tomlPath ...string) error {
	if md.IsDefined(tomlPath...) {
		return nil
	}
	if v := os.Getenv(envKey); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%s: %w", envKey, err)
		}
		*dst = n
		return nil
	}
	*dst = def
	return nil
}

// floatDefault fills *dst from envKey if the TOML key was not defined,
// then falls back to def.
func floatDefault(md toml.MetaData, dst *float64, envKey string, def float64, tomlPath ...string) error {
	if md.IsDefined(tomlPath...) {
		return nil
	}
	if v := os.Getenv(envKey); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("%s: %w", envKey, err)
		}
		*dst = f
		return nil
	}
	*dst = def
	return nil
}

// boolDefault fills *dst from envKey if the TOML key was not defined,
// then falls back to def. Accepts "true" and "1" as truthy values.
func boolDefault(md toml.MetaData, dst *bool, envKey string, def bool, tomlPath ...string) {
	if md.IsDefined(tomlPath...) {
		return
	}
	if v := os.Getenv(envKey); v != "" {
		*dst = v == "true" || v == "1"
		return
	}
	*dst = def
}
