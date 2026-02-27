# Analytics

tspages records per-request analytics for all sites (enabled by default). Data is stored in a local
SQLite database at `{data_dir}/analytics.db`.

Each event captures: timestamp, site, path, HTTP status, user identity (login name, display name),
node info (name, IP, OS, tags), and device type. Recording is async and non-blocking -- events are
dropped rather than queued if the system is under heavy load.

## Viewing analytics

Admins can view analytics via the dashboard:

- **Cross-site**: `GET /analytics` -- overview of all sites
- **Per-site**: `GET /sites/{site}/analytics`

Both views support a `?range=` parameter with ISO 8601 durations: `PT24H` (default), `P7D`, `P30D`,
`P1Y`, or `all`.

## Disabling analytics

Per-site in the deployment's `tspages.toml`:

```toml
analytics = false
```

Or as a server-wide default:

```toml
[defaults]
analytics = false
```

See [Per-Site Configuration](per-site-config) and [Configuration](configuration) for more details.

## Purging analytics data

Admins can delete all analytics data for a site:

```
POST /sites/{site}/analytics/purge
```
