package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// SiteConfig holds per-deployment configuration parsed from tspages.toml.
type SiteConfig struct {
	Public           *bool                        `toml:"public"`
	SPARouting       *bool                        `toml:"spa_routing"`
	HTMLExtensions   *bool                        `toml:"html_extensions"`
	Analytics        *bool                        `toml:"analytics"`
	DirectoryListing *bool                        `toml:"directory_listing"`
	IndexPage        string                       `toml:"index_page"`
	NotFoundPage     string                       `toml:"not_found_page"`
	TrailingSlash    string                       `toml:"trailing_slash"`
	Headers          map[string]map[string]string `toml:"headers"`
	Redirects        []RedirectRule               `toml:"redirects"`
	WebhookURL       string                       `toml:"webhook_url"`
	WebhookEvents    []string                     `toml:"webhook_events"`
	WebhookSecret    string                       `toml:"webhook_secret"`
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
	if c.TrailingSlash != "" && c.TrailingSlash != "add" && c.TrailingSlash != "remove" {
		return fmt.Errorf("trailing_slash: must be \"add\" or \"remove\", got %q", c.TrailingSlash)
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

	if c.WebhookURL != "" && !strings.HasPrefix(c.WebhookURL, "http://") && !strings.HasPrefix(c.WebhookURL, "https://") {
		return fmt.Errorf("webhook_url: must start with http:// or https://, got %q", c.WebhookURL)
	}
	validEvents := map[string]bool{
		"deploy.success": true,
		"deploy.failed":  true,
		"site.created":   true,
		"site.deleted":   true,
	}
	for i, ev := range c.WebhookEvents {
		if !validEvents[ev] {
			return fmt.Errorf("webhook_events[%d]: unknown event %q", i, ev)
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

// ParseRedirectsFile parses a Netlify-style _redirects file into redirect rules.
// Format: /from /to [status]
// Lines starting with # are comments. Blank lines are ignored.
func ParseRedirectsFile(data []byte) ([]RedirectRule, error) {
	var rules []RedirectRule
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("_redirects line %d: need at least <from> <to>", i+1)
		}
		if len(fields) > 3 {
			return nil, fmt.Errorf("_redirects line %d: too many fields (expected: /from /to [status])", i+1)
		}
		rule := RedirectRule{From: fields[0], To: fields[1]}
		if len(fields) == 3 {
			status, err := strconv.Atoi(fields[2])
			if err != nil {
				return nil, fmt.Errorf("_redirects line %d: invalid status %q", i+1, fields[2])
			}
			if status != 301 && status != 302 {
				return nil, fmt.Errorf("_redirects line %d: status must be 301 or 302", i+1)
			}
			rule.Status = status
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// ParseHeadersFile parses a Netlify-style _headers file.
// Format: path on its own line (no leading whitespace), indented header lines below.
// Lines starting with # are comments. Blank lines are ignored.
func ParseHeadersFile(data []byte) (map[string]map[string]string, error) {
	var result map[string]map[string]string
	var currentPath string
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			// Path line
			currentPath = trimmed
			if result == nil {
				result = make(map[string]map[string]string)
			}
			if result[currentPath] == nil {
				result[currentPath] = make(map[string]string)
			}
		} else {
			// Header line
			if currentPath == "" {
				return nil, fmt.Errorf("_headers line %d: header without path", i+1)
			}
			name, value, ok := strings.Cut(trimmed, ":")
			if !ok {
				return nil, fmt.Errorf("_headers line %d: expected Name: Value", i+1)
			}
			result[currentPath][strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}
	return result, nil
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

	if c.Public != nil {
		merged.Public = c.Public
	}
	if c.SPARouting != nil {
		merged.SPARouting = c.SPARouting
	}
	if c.HTMLExtensions != nil {
		merged.HTMLExtensions = c.HTMLExtensions
	}
	if c.Analytics != nil {
		merged.Analytics = c.Analytics
	}
	if c.DirectoryListing != nil {
		merged.DirectoryListing = c.DirectoryListing
	}
	if c.IndexPage != "" {
		merged.IndexPage = c.IndexPage
	}
	if c.NotFoundPage != "" {
		merged.NotFoundPage = c.NotFoundPage
	}
	if c.TrailingSlash != "" {
		merged.TrailingSlash = c.TrailingSlash
	}

	// Deep-copy headers to avoid mutating the defaults map.
	if defaults.Headers != nil || c.Headers != nil {
		merged.Headers = make(map[string]map[string]string)
		for path, hdrs := range defaults.Headers {
			inner := make(map[string]string, len(hdrs))
			for k, v := range hdrs {
				inner[k] = v
			}
			merged.Headers[path] = inner
		}
		for path, hdrs := range c.Headers {
			inner := make(map[string]string, len(hdrs))
			for k, v := range hdrs {
				inner[k] = v
			}
			merged.Headers[path] = inner
		}
	}

	if c.Redirects != nil {
		merged.Redirects = c.Redirects
	}

	if c.WebhookURL != "" {
		merged.WebhookURL = c.WebhookURL
		merged.WebhookEvents = c.WebhookEvents
		merged.WebhookSecret = c.WebhookSecret
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
