# tspages Phase 1 (MVP) Design

## Scope

Phase 1 only: tsnet setup, storage, deploy endpoint, static file serving, auth middleware, config loading, admin status endpoint. Path-based routing (Option A). No build runner, no subdomain mode, no Tailscale Services.

## Decisions

- **Router:** stdlib `net/http` with Go 1.22+ `{param}` patterns. No third-party router.
- **Config:** `tspages.toml` via `BurntSushi/toml`. Capability domain is configurable (required field).
- **Admin:** JSON-only `/_admin/status`. No HTML UI.
- **Testing:** Interfaces for `WhoIs`/tsnet so core logic is unit-testable with mocks.
- **Viewer auth:** Closed by default. All access (view, deploy, admin) requires capabilities. `view: ["*"]` grants broad read access.

## 1. Config (`config/config.go`)

```go
type Config struct {
    Tailscale TailscaleConfig
    Server    ServerConfig
    Routing   RoutingConfig
}

type TailscaleConfig struct {
    Hostname   string // tsnet hostname, default "pages"
    StateDir   string // tsnet state storage
    AuthKey    string // falls back to TS_AUTHKEY env var
    Capability string // required, e.g. "example.com/cap/pages"
}

type ServerConfig struct {
    DataDir     string // default "./data"
    MaxUploadMB int    // default 500
}

type RoutingConfig struct {
    Mode string // "path" only for Phase 1
}
```

## 2. tsnet Setup (`cmd/tspages/main.go`)

- Create `tsnet.Server` with configured hostname and state dir
- Obtain `tsLocalClient` for WhoIs
- Listen on HTTPS via `tsnet`'s TLS
- Route to a single `http.ServeMux`
- On startup, call `storage.CleanupOrphans()` to remove incomplete deployments

## 3. Interfaces for Testability

```go
// WhoIsClient abstracts tailscale LocalClient.WhoIs
type WhoIsClient interface {
    WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
}
```

Handlers and middleware accept this interface. Tests inject mocks returning controlled capability maps.

## 4. Storage Layer (`internal/storage`)

Filesystem layout per spec:

```
data_dir/sites/<site>/current -> deployments/<id>
data_dir/sites/<site>/deployments/<id>/...files...
```

API:
- `CreateDeployment(site, id string) string` — creates deployment dir, returns path
- `ActivateDeployment(site, id string) error` — atomically swaps `current` symlink
- `CurrentDeployment(site string) (string, error)` — resolves current symlink
- `SiteRoot(site string) string` — returns `sites/<site>/current` path for file serving
- `ListSites() []SiteInfo` — lists all sites with active deployment ID and disk usage
- `CleanupOrphans()` — removes deployment dirs lacking `.complete` marker

Deployment IDs: 8 hex chars from `crypto/rand`.

## 5. Auth (`internal/auth`)

### Capability Schema

```go
type Cap struct {
    View   []string `json:"view"`
    Deploy []string `json:"deploy"`
    Admin  bool     `json:"admin"`
}
```

Multiple cap objects in the array are merged (union of lists, OR of booleans).

### Access Rules

| Action | Required |
|--------|----------|
| View site X | `view` or `deploy` contains X or `"*"`, or `admin: true` |
| Deploy to site X | `deploy` contains X or `"*"`, or `admin: true` |
| Admin endpoints | `admin: true` |
| No capability | 403 on everything |

### Helpers

- `CanView(caps []Cap, site string) bool`
- `CanDeploy(caps []Cap, site string) bool`
- `IsAdmin(caps []Cap) bool`

### Middleware

1. Call `WhoIs(remoteAddr)` to get node info
2. Extract `CapMap[configuredCapability]`
3. Parse JSON into `[]Cap`, merge
4. Attach to request context
5. Individual handlers call `CanView`/`CanDeploy`/`IsAdmin`, return 403 if denied

## 6. Deploy Handler (`internal/deploy`)

`POST /deploy/{site}`:

1. Extract site name from URL
2. Check `CanDeploy(caps, site)` — 403 if denied
3. Check `Content-Length` vs `max_upload_mb` — 413 if too large
4. Generate 8-char hex deployment ID
5. Create deployment dir via storage
6. Extract ZIP into deployment dir:
   - `filepath.Clean` every entry
   - Reject if resolved path escapes deployment dir (zip-slip prevention)
   - Track bytes written, abort if exceeds limit
7. Write `.complete` marker
8. Unless `?activate=false`, call `ActivateDeployment`
9. Return JSON: `{"deployment_id", "site", "url"}`

## 7. Static File Server (`internal/serve`)

`GET /{site}/{path...}`:

1. Extract site name from first path segment
2. Check `CanView(caps, site)` — 403 if denied
3. Resolve `current` symlink via storage — 404 if no active deployment
4. Serve file with `http.ServeFile` or `http.FileServer`
5. Directory requests serve `index.html` if present
6. Go's built-in MIME detection for Content-Type
7. Standard `Cache-Control` and `ETag` headers

## 8. Admin Status (`internal/admin`)

`GET /_admin/status`:

1. Check `IsAdmin(caps)` — 403 if denied
2. Call `storage.ListSites()`
3. Return JSON: list of sites, active deployment IDs, total storage

## 9. Routing (`cmd/tspages/main.go`)

```go
mux.HandleFunc("POST /deploy/{site}", withAuth(deployHandler))
mux.HandleFunc("GET /_admin/status", withAuth(adminHandler))
mux.HandleFunc("GET /{site}/{path...}", withAuth(serveHandler))
```

All routes go through auth middleware. Deploy and admin handlers check elevated permissions; serve handler checks view permission.

## Project Structure

```
tspages/
  cmd/tspages/main.go
  internal/
    auth/caps.go, caps_test.go
    deploy/handler.go, extract.go
    serve/handler.go
    admin/handler.go
    storage/store.go, store_test.go
  config/config.go
  tspages.toml.example
  go.mod
  go.sum
```
