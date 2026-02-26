# Configuration

tspages is configured via a TOML file (default: `tspages.toml`). Specify a different path with
`tspages -config /path/to/tspages.toml`.

## Full reference

```toml
[tailscale]
hostname = "pages"                              # control plane tsnet hostname (default: "pages")
state_dir = "/var/lib/tspages"                  # tsnet state directory (default: "./state")
auth_key = ""                                   # reusable, tagged key; or set TS_AUTHKEY env var
capability = "tspages.mazetti.me/cap/pages"     # default; or set TSPAGES_CAPABILITY env

[server]
data_dir = "/data"         # site storage root (default: "./data")
max_upload_mb = 500        # max upload size in MB (default: 500)
max_sites = 100            # max concurrent site servers (default: 100)
max_deployments = 10       # max deployments kept per site (default: 10)
log_level = "warn"         # "debug", "info", "warn", "error" (default: "warn")
health_addr = ":9091"      # local health check listener (default: off; see Telemetry)

# Server-wide defaults for per-site config. Deployments can override these
# via their own tspages.toml included in the archive.
[defaults]
spa_routing = false
html_extensions = false
analytics = true
index_page = "index.html"
not_found_page = "404.html"

[defaults.headers]
"/*" = { X-Frame-Options = "DENY" }
```

## Environment variables

| Variable             | Overrides                | Notes                           |
|----------------------|--------------------------|---------------------------------|
| `TS_AUTHKEY`         | `tailscale.auth_key`     | Reusable, tagged auth key       |
| `TSPAGES_CAPABILITY` | `tailscale.capability`   | Capability name for grants      |
| `TSPAGES_LOG_LEVEL`  | `server.log_level`       | Config file takes precedence    |
| `TSPAGES_HEALTH_ADDR`| `server.health_addr`     | Local health check listener     |
| `TSPAGES_SERVER`     | --                       | Used by the CLI deploy command  |

## Docker

When running with Docker, the default paths work with volume mounts:

```bash
docker run -d \
  -v tspages-state:/state \
  -v tspages-data:/data \
  -e TS_AUTHKEY=tskey-auth-... \
  ghcr.io/radiergummi/tspages:latest
```

You can mount a config file if you need custom settings:

```bash
docker run -d \
  -v tspages-state:/state \
  -v tspages-data:/data \
  -v ./tspages.toml:/etc/tspages.toml:ro \
  -e TS_AUTHKEY=tskey-auth-... \
  ghcr.io/radiergummi/tspages:latest -config /etc/tspages.toml
```

## Local development

To work on the admin frontend with hot reloading:

```bash
# Terminal 1: Vite dev server
npx vite

# Terminal 2: Go server with dev mode
go run ./cmd/tspages -dev
```

Open http://localhost:8080 in your browser. The `-dev` flag:

- Serves CSS/JS from the Vite dev server with hot module replacement
- Re-parses Go templates from disk on every request (refresh to see changes)
- Provides a localhost listener with mock admin auth (no Tailscale required to browse the UI)

The tsnet control plane still starts normally alongside the dev server. Production builds use `npx vite build`, which
outputs to `internal/admin/assets/dist/` (embedded at compile time).

## Security notes

- **Archive extraction** rejects path traversal (zip-slip and tar equivalents), symlinks, hardlinks, and enforces size
  limits on both compressed and decompressed content
- **Site names** must be valid DNS labels (lowercase alphanumeric and hyphens, max 63 characters)
- **Auth** uses the local Tailscale daemon's WhoIs -- identity is verified by Tailscale, not forgeable by the remote
  peer
- **Deployments** are atomic: files are fully written before the `current` symlink is swapped
- **State directory** (`state_dir`) should be `0700` -- it contains the node key and certificates
