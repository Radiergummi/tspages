package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// SiteConfig holds configuration for a site, used for both per-deployment
// config (tspages.toml) and site-level overrides (data/sites/{site}/config.toml).
type SiteConfig struct {
	Public           *bool                        `toml:"public" json:"public,omitempty"`
	SPARouting       *bool                        `toml:"spa_routing" json:"spa_routing,omitempty"`
	HTMLExtensions   *bool                        `toml:"html_extensions" json:"html_extensions,omitempty"`
	Analytics        *bool                        `toml:"analytics" json:"analytics,omitempty"`
	DirectoryListing *bool                        `toml:"directory_listing" json:"directory_listing,omitempty"`
	IndexPage        string                       `toml:"index_page" json:"index_page,omitempty"`
	NotFoundPage     string                       `toml:"not_found_page" json:"not_found_page,omitempty"`
	TrailingSlash    string                       `toml:"trailing_slash" json:"trailing_slash,omitempty"`
	Headers          map[string]map[string]string `toml:"headers" json:"headers,omitempty"`
	Redirects        []RedirectRule               `toml:"redirects" json:"redirects,omitempty"`
	Forms            *FormsConfig                 `toml:"forms" json:"forms,omitempty"`
	WebhookURL       string                       `toml:"webhook_url" json:"webhook_url,omitempty"`
	WebhookEvents    []string                     `toml:"webhook_events" json:"webhook_events,omitempty"`
	WebhookSecret    string                       `toml:"webhook_secret" json:"webhook_secret,omitempty"`
}

// RedirectRule defines a single redirect from one path pattern to another.
type RedirectRule struct {
	From   string `toml:"from" json:"from"`
	To     string `toml:"to" json:"to"`
	Status int    `toml:"status,omitempty" json:"status,omitempty"`
}

// FormsConfig holds form collection configuration for a site.
// In TOML: [forms] enabled = true with [[forms.endpoints]] for endpoints.
type FormsConfig struct {
	Enabled   *bool          `toml:"enabled" json:"enabled,omitempty"`
	Endpoints []FormEndpoint `toml:"endpoints" json:"endpoints,omitempty"`
}

// FormEndpoint defines a single form collection endpoint.
type FormEndpoint struct {
	Path        string      `toml:"path" json:"path"`
	Name        string      `toml:"name" json:"name,omitempty"`
	Description string      `toml:"description" json:"description,omitempty"`
	Fields      []FormField `toml:"fields" json:"fields,omitempty"`
}

// FormField describes an expected field in a form endpoint.
type FormField struct {
	Key       string `toml:"key" json:"key"`
	Label     string `toml:"label" json:"label,omitempty"`
	Type      string `toml:"type" json:"type,omitempty"`
	Aggregate string `toml:"aggregate" json:"aggregate,omitempty"`
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

	if c.Forms != nil {
		seenPaths := make(map[string]bool, len(c.Forms.Endpoints))
		for i, ep := range c.Forms.Endpoints {
			if ep.Path == "" {
				return fmt.Errorf("forms.endpoints[%d]: path is required", i)
			}
			if !strings.HasPrefix(ep.Path, "/") {
				return fmt.Errorf("forms.endpoints[%d]: path must start with /", i)
			}
			if seenPaths[ep.Path] {
				return fmt.Errorf("forms.endpoints[%d]: duplicate path %q", i, ep.Path)
			}
			seenPaths[ep.Path] = true
			validFieldTypes := map[string]bool{
				"":       true,
				"text":   true,
				"email":  true,
				"number": true,
				"url":    true,
				"tel":    true,
				"date":   true,
			}
			validAggregates := map[string]bool{
				"": true,
				// General (any type).
				"unique": true, "total": true,
				"count_empty": true, "count_not_empty": true,
				"percent_empty": true, "percent_not_empty": true,
				// Numeric.
				"sum": true, "avg": true, "mean": true, "median": true,
				"min": true, "max": true, "range": true,
			}
			for j, f := range ep.Fields {
				if f.Key == "" {
					return fmt.Errorf("forms.endpoints[%d].fields[%d]: key is required", i, j)
				}
				if !validFieldTypes[f.Type] {
					return fmt.Errorf("forms.endpoints[%d].fields[%d]: invalid type %q", i, j, f.Type)
				}
				if !validAggregates[f.Aggregate] {
					return fmt.Errorf("forms.endpoints[%d].fields[%d]: invalid aggregate %q", i, j, f.Aggregate)
				}
			}
		}
	}

	if c.WebhookURL != "" && !strings.HasPrefix(c.WebhookURL, "http://") && !strings.HasPrefix(c.WebhookURL, "https://") {
		return fmt.Errorf("webhook_url: must start with http:// or https://, got %q", c.WebhookURL)
	}
	validEvents := map[string]bool{
		"deploy.success":  true,
		"deploy.failed":   true,
		"site.created":    true,
		"site.deleted":    true,
		"form.submission": true,
	}
	for i, ev := range c.WebhookEvents {
		if !validEvents[ev] {
			return fmt.Errorf("webhook_events[%d]: unknown event %q", i, ev)
		}
	}

	if c.SPARouting != nil && *c.SPARouting && c.DirectoryListing != nil && *c.DirectoryListing {
		return fmt.Errorf("spa_routing and directory_listing are mutually exclusive")
	}
	if c.WebhookURL != "" && len(c.WebhookEvents) == 0 {
		return fmt.Errorf("webhook_url requires at least one webhook_events entry")
	}
	if c.WebhookURL == "" && len(c.WebhookEvents) > 0 {
		return fmt.Errorf("webhook_events requires webhook_url")
	}
	if c.WebhookURL == "" && c.WebhookSecret != "" {
		return fmt.Errorf("webhook_secret requires webhook_url")
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

// ReadSiteConfig reads site-level config overrides from data/sites/{site}/config.toml.
// Returns a zero SiteConfig if the file does not exist.
func (s *Store) ReadSiteConfig(site string) (SiteConfig, error) {
	if !ValidSiteName(site) {
		return SiteConfig{}, fmt.Errorf("invalid site name: %q", site)
	}
	path := filepath.Join(s.dataDir, "sites", site, siteConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SiteConfig{}, nil
		}
		return SiteConfig{}, err
	}
	return ParseSiteConfig(data)
}

// WriteSiteConfig validates and writes site-level config overrides to data/sites/{site}/config.toml.
func (s *Store) WriteSiteConfig(site string, cfg SiteConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal site config: %w", err)
	}
	path := filepath.Join(s.dataDir, "sites", site, siteConfigFile)
	return os.WriteFile(path, data, 0644)
}

// WriteDeploymentConfig writes per-deployment config to the deployment directory.
func (s *Store) WriteDeploymentConfig(site, id string, cfg SiteConfig) error {
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal site config: %w", err)
	}
	path := filepath.Join(s.dataDir, "sites", site, "deployments", id, siteConfigFile)
	return os.WriteFile(path, data, 0644)
}

// ReadDeploymentConfig reads per-deployment config from the deployment directory.
func (s *Store) ReadDeploymentConfig(site, id string) (SiteConfig, error) {
	if !ValidSiteName(site) {
		return SiteConfig{}, fmt.Errorf("invalid site name: %q", site)
	}
	if !ValidDeploymentID(id) {
		return SiteConfig{}, fmt.Errorf("invalid deployment ID: %q", id)
	}
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

	if c.Forms != nil {
		merged.Forms = c.Forms
	}

	if c.WebhookURL != "" {
		merged.WebhookURL = c.WebhookURL
		merged.WebhookEvents = c.WebhookEvents
		merged.WebhookSecret = c.WebhookSecret
	}

	return merged
}

// ReadCurrentDeploymentConfig reads per-deployment config for the currently active deployment.
func (s *Store) ReadCurrentDeploymentConfig(site string) (SiteConfig, error) {
	id, err := s.CurrentDeployment(site)
	if err != nil {
		return SiteConfig{}, nil //nolint:nilerr // no deployment → zero config (use defaults)
	}
	return s.ReadDeploymentConfig(site, id)
}
