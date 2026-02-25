package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// SiteConfig holds per-deployment configuration parsed from tspages.toml.
type SiteConfig struct {
	SPA          *bool                        `toml:"spa"`
	Analytics    *bool                        `toml:"analytics"`
	IndexPage    string                       `toml:"index_page"`
	NotFoundPage string                       `toml:"not_found_page"`
	Headers      map[string]map[string]string `toml:"headers"`
	Redirects    []RedirectRule               `toml:"redirects"`
}

// RedirectRule defines a single redirect from one path pattern to another.
type RedirectRule struct {
	From   string `toml:"from"`
	To     string `toml:"to"`
	Status int    `toml:"status,omitempty"`
}

const siteConfigFile = "config.toml"

func (c SiteConfig) Validate() error {
	if err := validateConfigPath(c.IndexPage, "index_page"); err != nil {
		return err
	}
	if err := validateConfigPath(c.NotFoundPage, "not_found_page"); err != nil {
		return err
	}
	for pattern, hdrs := range c.Headers {
		if !strings.HasPrefix(pattern, "/") {
			return fmt.Errorf("header path %q must start with /", pattern)
		}
		for name, value := range hdrs {
			if value == "" {
				return fmt.Errorf("header %q in path %q has empty value", name, pattern)
			}
		}
	}

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
		for p := range toParams {
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

	return nil
}

func extractParams(pattern string) map[string]bool {
	params := make(map[string]bool)
	for _, seg := range strings.Split(pattern, "/") {
		if strings.HasPrefix(seg, ":") {
			params[seg[1:]] = true
		} else if seg == "*" {
			params["*"] = true
		}
	}
	return params
}

func validateConfigPath(p, field string) error {
	if p == "" {
		return nil
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("%s: absolute path not allowed: %q", field, p)
	}
	cleaned := filepath.Clean(p)
	if strings.HasPrefix(cleaned, "..") {
		return fmt.Errorf("%s: path traversal not allowed: %q", field, p)
	}
	return nil
}

// ParseSiteConfig parses a tspages.toml file body into a SiteConfig.
func ParseSiteConfig(data []byte) (SiteConfig, error) {
	var cfg SiteConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return SiteConfig{}, err
	}
	return cfg, nil
}

func (s *Store) WriteSiteConfig(site, id string, cfg SiteConfig) error {
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal site config: %w", err)
	}
	path := filepath.Join(s.dataDir, "sites", site, "deployments", id, siteConfigFile)
	return os.WriteFile(path, data, 0644)
}

func (s *Store) ReadSiteConfig(site, id string) (SiteConfig, error) {
	path := filepath.Join(s.dataDir, "sites", site, "deployments", id, siteConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SiteConfig{}, nil
		}
		return SiteConfig{}, err
	}
	return ParseSiteConfig(data)
}

// Merge returns a new SiteConfig with deployment values (c) taking priority over defaults.
// For *bool fields, nil means "use default", non-nil overrides.
// For string fields, empty means "use default", non-empty overrides.
// For Headers, deployment paths override default paths; default-only paths are kept.
func (c SiteConfig) Merge(defaults SiteConfig) SiteConfig {
	merged := defaults

	if c.SPA != nil {
		merged.SPA = c.SPA
	}
	if c.Analytics != nil {
		merged.Analytics = c.Analytics
	}
	if c.IndexPage != "" {
		merged.IndexPage = c.IndexPage
	}
	if c.NotFoundPage != "" {
		merged.NotFoundPage = c.NotFoundPage
	}

	// Deep-copy headers to avoid mutating the defaults map.
	if defaults.Headers != nil || c.Headers != nil {
		merged.Headers = make(map[string]map[string]string)
		for path, hdrs := range defaults.Headers {
			merged.Headers[path] = hdrs
		}
		for path, hdrs := range c.Headers {
			merged.Headers[path] = hdrs
		}
	}

	if c.Redirects != nil {
		merged.Redirects = c.Redirects
	}

	return merged
}

func (s *Store) ReadCurrentSiteConfig(site string) (SiteConfig, error) {
	id, err := s.CurrentDeployment(site)
	if err != nil {
		return SiteConfig{}, nil
	}
	return s.ReadSiteConfig(site, id)
}
