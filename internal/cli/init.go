package cli

import (
	"flag"
	"fmt"
	"os"
)

const siteConfigTemplate = `# tspages site configuration
# Include this file as tspages.toml in the root of your deployment archive.
# All fields are optional — uncomment and set only what you need.
# Boolean fields left unset inherit the server default.

# Make this site publicly accessible via Tailscale Funnel.
# Requires the "funnel" node attribute in your tailnet policy.
# public = false

# Enable single-page application routing.
# All non-file requests serve the index page instead of 404.
# spa_routing = false

# Serve clean URLs by stripping .html extensions.
# /about.html becomes accessible at /about.
# html_extensions = true

# Record per-request analytics (page views, visitors, top pages).
# analytics = true

# Show directory listings for folders without an index page.
# directory_listing = false

# Default file to serve for directory requests.
# index_page = "index.html"

# Page to serve for 404 responses (relative to site root).
# not_found_page = ""

# Trailing slash behavior: "add", "remove", or "" (no normalization).
# trailing_slash = ""

# Custom response headers by path pattern.
# [headers."/assets/*"]
# Cache-Control = "public, max-age=31536000, immutable"

# Redirect rules. Each [[redirects]] block defines one rule.
# [[redirects]]
# from = "/old-path"
# to = "/new-path"
# status = 301

# Webhook notifications for deploy and site events.
# webhook_url = "https://example.com/webhook"
# webhook_events = ["deploy.success", "deploy.failed", "site.created", "site.deleted"]
# webhook_secret = ""
`

const serverConfigTemplate = `# tspages server configuration
# Place this file at tspages.toml next to the tspages binary (or pass -config).
# All fields show their default values — uncomment to override.

[tailscale]
# Tailscale hostname for the control plane.
# hostname = "pages"

# Directory for tsnet state files.
# state_dir = "./state"

# Tailscale auth key for headless login (or set TS_AUTHKEY env var).
# auth_key = ""

# Tailscale ACL capability name for authorization.
# capability = "tspages.mazetti.me/cap/pages"

[server]
# Directory for site data (deployments, databases).
# data_dir = "./data"

# Maximum upload size in megabytes.
# max_upload_mb = 500

# Maximum number of sites.
# max_sites = 100

# Maximum deployments retained per site.
# max_deployments = 10

# Log level: debug, info, warn, error.
# log_level = "warn"

# Address for plain-HTTP health checks (e.g. "127.0.0.1:8081"). Empty disables.
# health_addr = ""

# Hide the admin UI footer.
# hide_footer = false

# Default site configuration. These values apply to all sites unless
# overridden by a per-deployment tspages.toml.
# [defaults]
# public = false
# spa_routing = false
# html_extensions = true
# analytics = true
# directory_listing = false
# index_page = "index.html"
# not_found_page = ""
# trailing_slash = ""
`

// Init is the entrypoint for `tspages init`.
func Init(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	server := fs.Bool("server", false, "generate server config template instead of site config")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: tspages init [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Generate an annotated tspages.toml template in the current directory.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	const filename = "tspages.toml"

	if _, err := os.Stat(filename); err == nil {
		return fmt.Errorf("%s already exists", filename)
	}

	tmpl := siteConfigTemplate
	if *server {
		tmpl = serverConfigTemplate
	}

	if err := os.WriteFile(filename, []byte(tmpl), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", filename, err)
	}

	fmt.Fprintf(os.Stderr, "Wrote %s\n", filename)
	return nil
}
