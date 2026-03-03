#!/usr/bin/env bash
#
# Seed the data/ directory with sample sites, deployments, and analytics
# for taking screenshots. Run from the repo root:
#
#   bash scripts/seed-demo-data.sh
#
set -euo pipefail

DATA="data"
DB="$DATA/tspages.db"

rm -rf "$DATA"
mkdir -p "$DATA"

# All timestamps are relative to now so data always looks fresh.
NOW_EPOCH=$(date +%s)
ts_ago() {
  # ts_ago <seconds_before_now> → RFC3339 timestamp
  local epoch=$(( NOW_EPOCH - $1 ))
  date -u -r "$epoch" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null \
    || date -u -d "@$epoch" '+%Y-%m-%dT%H:%M:%SZ'
}

# Precompute offsets (seconds ago)
OFF_7D=$((7 * 86400))      # 7 days ago
OFF_6D=$((6 * 86400))      # 6 days ago
OFF_5D=$((5 * 86400))      # 5 days ago
OFF_4D=$((4 * 86400))      # 4 days ago
OFF_3D=$((3 * 86400))      # 3 days ago
OFF_2D=$((2 * 86400))      # 2 days ago
OFF_1D=$((1 * 86400))      # 1 day ago
OFF_12H=$((12 * 3600))     # 12 hours ago
OFF_6H=$((6 * 3600))       # 6 hours ago
OFF_2H=$((2 * 3600))       # 2 hours ago
OFF_1H=$((1 * 3600))       # 1 hour ago

# ── Helper: create a deployment ──────────────────────────────────────────────
#   create_deployment <site> <id> <timestamp> <user> <size>
create_deployment() {
  local site=$1 id=$2 ts=$3 user=$4 size=$5
  local dir="$DATA/sites/$site/deployments/$id"
  mkdir -p "$dir/content"
  touch "$dir/.complete"
  cat > "$dir/manifest.json" <<MANIFEST
{"site":"$site","id":"$id","created_at":"$ts","created_by":"$user","size_bytes":$size}
MANIFEST
}

# ── Helper: activate a deployment ────────────────────────────────────────────
activate() {
  local site=$1 id=$2
  ln -sfn "deployments/$id" "$DATA/sites/$site/current"
}

# ═══════════════════════════════════════════════════════════════════════════════
# Sites & deployments
# ═══════════════════════════════════════════════════════════════════════════════

# ── docs ─────────────────────────────────────────────────────────────────────
mkdir -p "$DATA/sites/docs/deployments"

create_deployment docs a1b2c3d4 "$(ts_ago $OFF_7D)" "alice@example.com" 245760
mkdir -p "$DATA/sites/docs/deployments/a1b2c3d4/content/guides"
cat > "$DATA/sites/docs/deployments/a1b2c3d4/content/index.html" <<'HTML'
<!DOCTYPE html><html><head><title>Docs</title></head><body><h1>Documentation</h1><p>Welcome to the docs.</p></body></html>
HTML
cat > "$DATA/sites/docs/deployments/a1b2c3d4/content/guides/getting-started.html" <<'HTML'
<!DOCTYPE html><html><head><title>Getting Started</title></head><body><h1>Getting Started</h1></body></html>
HTML

create_deployment docs e5f6a7b8 "$(ts_ago $OFF_5D)" "alice@example.com" 251904
mkdir -p "$DATA/sites/docs/deployments/e5f6a7b8/content/guides"
cat > "$DATA/sites/docs/deployments/e5f6a7b8/content/index.html" <<'HTML'
<!DOCTYPE html><html><head><title>Docs</title></head><body><h1>Documentation</h1><p>Welcome to the docs. Updated with new guides.</p></body></html>
HTML
cat > "$DATA/sites/docs/deployments/e5f6a7b8/content/guides/getting-started.html" <<'HTML'
<!DOCTYPE html><html><head><title>Getting Started</title></head><body><h1>Getting Started</h1><p>Updated guide.</p></body></html>
HTML
cat > "$DATA/sites/docs/deployments/e5f6a7b8/content/guides/configuration.html" <<'HTML'
<!DOCTYPE html><html><head><title>Configuration</title></head><body><h1>Configuration</h1></body></html>
HTML

create_deployment docs 1c2d3e4f "$(ts_ago $OFF_2H)" "bob@example.com" 274432
mkdir -p "$DATA/sites/docs/deployments/1c2d3e4f/content/guides"
mkdir -p "$DATA/sites/docs/deployments/1c2d3e4f/content/api"
cat > "$DATA/sites/docs/deployments/1c2d3e4f/content/index.html" <<'HTML'
<!DOCTYPE html><html><head><title>Docs</title></head><body><h1>Documentation</h1><p>Welcome to the docs. Now with API reference.</p></body></html>
HTML
cat > "$DATA/sites/docs/deployments/1c2d3e4f/content/guides/getting-started.html" <<'HTML'
<!DOCTYPE html><html><head><title>Getting Started</title></head><body><h1>Getting Started</h1><p>Updated guide.</p></body></html>
HTML
cat > "$DATA/sites/docs/deployments/1c2d3e4f/content/guides/configuration.html" <<'HTML'
<!DOCTYPE html><html><head><title>Configuration</title></head><body><h1>Configuration</h1></body></html>
HTML
cat > "$DATA/sites/docs/deployments/1c2d3e4f/content/api/index.html" <<'HTML'
<!DOCTYPE html><html><head><title>API Reference</title></head><body><h1>API Reference</h1></body></html>
HTML

activate docs 1c2d3e4f

# ── blog ─────────────────────────────────────────────────────────────────────
mkdir -p "$DATA/sites/blog/deployments"

create_deployment blog 4a5b6c7d "$(ts_ago $OFF_4D)" "charlie@example.com" 189440
mkdir -p "$DATA/sites/blog/deployments/4a5b6c7d/content/posts"
cat > "$DATA/sites/blog/deployments/4a5b6c7d/content/index.html" <<'HTML'
<!DOCTYPE html><html><head><title>Blog</title></head><body><h1>Engineering Blog</h1></body></html>
HTML
cat > "$DATA/sites/blog/deployments/4a5b6c7d/content/posts/hello-world.html" <<'HTML'
<!DOCTYPE html><html><head><title>Hello World</title></head><body><h1>Hello World</h1><p>Our first post.</p></body></html>
HTML

create_deployment blog 8d9e0f1a "$(ts_ago $OFF_1D)" "charlie@example.com" 215040
mkdir -p "$DATA/sites/blog/deployments/8d9e0f1a/content/posts"
cat > "$DATA/sites/blog/deployments/8d9e0f1a/content/index.html" <<'HTML'
<!DOCTYPE html><html><head><title>Blog</title></head><body><h1>Engineering Blog</h1></body></html>
HTML
cat > "$DATA/sites/blog/deployments/8d9e0f1a/content/posts/hello-world.html" <<'HTML'
<!DOCTYPE html><html><head><title>Hello World</title></head><body><h1>Hello World</h1><p>Our first post.</p></body></html>
HTML
cat > "$DATA/sites/blog/deployments/8d9e0f1a/content/posts/scaling-tailnets.html" <<'HTML'
<!DOCTYPE html><html><head><title>Scaling Tailnets</title></head><body><h1>Scaling Tailnets</h1><p>How we scaled.</p></body></html>
HTML

activate blog 8d9e0f1a

# ── dashboard ────────────────────────────────────────────────────────────────
mkdir -p "$DATA/sites/dashboard/deployments"

create_deployment dashboard b2c3d4e5 "$(ts_ago $OFF_3D)" "alice@example.com" 524288
mkdir -p "$DATA/sites/dashboard/deployments/b2c3d4e5/content/assets"
cat > "$DATA/sites/dashboard/deployments/b2c3d4e5/content/index.html" <<'HTML'
<!DOCTYPE html><html><head><title>Dashboard</title></head><body><h1>Internal Dashboard</h1></body></html>
HTML
printf '%1024s' '' > "$DATA/sites/dashboard/deployments/b2c3d4e5/content/assets/app.js"
printf '%512s' '' > "$DATA/sites/dashboard/deployments/b2c3d4e5/content/assets/style.css"

activate dashboard b2c3d4e5

# ── status-page ──────────────────────────────────────────────────────────────
mkdir -p "$DATA/sites/status-page/deployments"

create_deployment status-page f6a7b8c9 "$(ts_ago $OFF_6D)" "bob@example.com" 81920
cat > "$DATA/sites/status-page/deployments/f6a7b8c9/content/index.html" <<'HTML'
<!DOCTYPE html><html><head><title>Status</title></head><body><h1>System Status</h1><p>All systems operational.</p></body></html>
HTML

activate status-page f6a7b8c9

# ── design-system (no active deployment — newly created) ─────────────────────
mkdir -p "$DATA/sites/design-system/deployments"

# ═══════════════════════════════════════════════════════════════════════════════
# Analytics
# ═══════════════════════════════════════════════════════════════════════════════

# Compute the RFC3339 start time (7 days ago, truncated to the hour) for SQL.
NOW_RFC3339=$(date -u -r "$NOW_EPOCH" '+%Y-%m-%dT%H:00:00' 2>/dev/null \
  || date -u -d "@$NOW_EPOCH" '+%Y-%m-%dT%H:00:00')

sqlite3 "$DB" <<'SQL'
CREATE TABLE IF NOT EXISTS requests (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    ts              TEXT NOT NULL,
    site            TEXT NOT NULL,
    path            TEXT NOT NULL,
    status          INTEGER NOT NULL,
    user_login      TEXT NOT NULL DEFAULT '',
    user_name       TEXT NOT NULL DEFAULT '',
    profile_pic_url TEXT NOT NULL DEFAULT '',
    node_name       TEXT NOT NULL DEFAULT '',
    node_ip         TEXT NOT NULL DEFAULT '',
    os              TEXT NOT NULL DEFAULT '',
    os_version      TEXT NOT NULL DEFAULT '',
    device          TEXT NOT NULL DEFAULT '',
    tags            TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_requests_site_ts ON requests(site, ts);
SQL

# We generate events going backwards from now using pure SQL + CTEs.
# The NOW_RFC3339 variable is injected as the anchor; hours count backwards.
sqlite3 "$DB" <<SEEDSQL
-- Generate 7 days of analytics data ending at now.
-- Hourly slots count back from the current hour; each slot gets a variable
-- number of rows based on time-of-day weighting.

-- docs: busiest site (~3000 requests)
WITH RECURSIVE hours(n) AS (
    SELECT 0 UNION ALL SELECT n+1 FROM hours WHERE n < 167
),
slots AS (
    SELECT
        strftime('%Y-%m-%dT%H:%M:%SZ', '${NOW_RFC3339}', '-' || n || ' hours') AS slot,
        CASE
            WHEN (CAST(strftime('%H', '${NOW_RFC3339}', '-' || n || ' hours') AS INTEGER)) BETWEEN 8 AND 17
                THEN 15 + ABS(CAST(RANDOM() % 25 AS INTEGER))
            WHEN (CAST(strftime('%H', '${NOW_RFC3339}', '-' || n || ' hours') AS INTEGER)) BETWEEN 6 AND 7
              OR (CAST(strftime('%H', '${NOW_RFC3339}', '-' || n || ' hours') AS INTEGER)) BETWEEN 18 AND 20
                THEN 5 + ABS(CAST(RANDOM() % 10 AS INTEGER))
            ELSE ABS(CAST(RANDOM() % 4 AS INTEGER))
        END AS weight
    FROM hours
)
INSERT INTO requests (ts, site, path, status, user_login, user_name, node_name, node_ip, os, os_version, device)
SELECT
    strftime('%Y-%m-%dT%H:%M:%SZ', s.slot, '+' || ABS(CAST(RANDOM() % 3600 AS INTEGER)) || ' seconds') AS ts,
    'docs',
    CASE ABS(CAST(RANDOM() % 10 AS INTEGER))
        WHEN 0 THEN '/'
        WHEN 1 THEN '/guides/getting-started'
        WHEN 2 THEN '/guides/configuration'
        WHEN 3 THEN '/api/'
        WHEN 4 THEN '/api/endpoints'
        WHEN 5 THEN '/guides/deployment'
        WHEN 6 THEN '/guides/authentication'
        WHEN 7 THEN '/changelog'
        WHEN 8 THEN '/guides/networking'
        ELSE '/search'
    END,
    CASE WHEN ABS(CAST(RANDOM() % 100 AS INTEGER)) < 3 THEN 404
         WHEN ABS(CAST(RANDOM() % 100 AS INTEGER)) < 1 THEN 500
         ELSE 200 END,
    CASE ABS(CAST(RANDOM() % 6 AS INTEGER))
        WHEN 0 THEN 'alice@example.com'
        WHEN 1 THEN 'bob@example.com'
        WHEN 2 THEN 'charlie@example.com'
        WHEN 3 THEN 'dana@example.com'
        WHEN 4 THEN 'eve@example.com'
        ELSE 'frank@example.com'
    END,
    CASE ABS(CAST(RANDOM() % 6 AS INTEGER))
        WHEN 0 THEN 'Alice Chen'
        WHEN 1 THEN 'Bob Martinez'
        WHEN 2 THEN 'Charlie Park'
        WHEN 3 THEN 'Dana Singh'
        WHEN 4 THEN 'Eve Johnson'
        ELSE 'Frank Liu'
    END,
    CASE ABS(CAST(RANDOM() % 4 AS INTEGER))
        WHEN 0 THEN 'alice-mbp'
        WHEN 1 THEN 'bob-desktop'
        WHEN 2 THEN 'charlie-linux'
        ELSE 'ci-runner-01'
    END,
    CASE ABS(CAST(RANDOM() % 4 AS INTEGER))
        WHEN 0 THEN '100.64.1.10'
        WHEN 1 THEN '100.64.1.20'
        WHEN 2 THEN '100.64.1.30'
        ELSE '100.64.1.40'
    END,
    CASE ABS(CAST(RANDOM() % 4 AS INTEGER))
        WHEN 0 THEN 'macOS'
        WHEN 1 THEN 'Linux'
        WHEN 2 THEN 'Windows'
        ELSE 'macOS'
    END,
    '',
    ''
FROM slots s, (
    WITH RECURSIVE reps(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM reps WHERE i < 40)
    SELECT i FROM reps
) r
WHERE r.i <= s.weight;

-- blog: moderate traffic (~1200 requests)
WITH RECURSIVE hours(n) AS (
    SELECT 0 UNION ALL SELECT n+1 FROM hours WHERE n < 167
),
slots AS (
    SELECT
        strftime('%Y-%m-%dT%H:%M:%SZ', '${NOW_RFC3339}', '-' || n || ' hours') AS slot,
        CASE
            WHEN (CAST(strftime('%H', '${NOW_RFC3339}', '-' || n || ' hours') AS INTEGER)) BETWEEN 9 AND 17
                THEN 5 + ABS(CAST(RANDOM() % 12 AS INTEGER))
            WHEN (CAST(strftime('%H', '${NOW_RFC3339}', '-' || n || ' hours') AS INTEGER)) BETWEEN 7 AND 8
              OR (CAST(strftime('%H', '${NOW_RFC3339}', '-' || n || ' hours') AS INTEGER)) BETWEEN 18 AND 20
                THEN 2 + ABS(CAST(RANDOM() % 5 AS INTEGER))
            ELSE ABS(CAST(RANDOM() % 2 AS INTEGER))
        END AS weight
    FROM hours
)
INSERT INTO requests (ts, site, path, status, user_login, user_name, node_name, node_ip, os, os_version, device)
SELECT
    strftime('%Y-%m-%dT%H:%M:%SZ', s.slot, '+' || ABS(CAST(RANDOM() % 3600 AS INTEGER)) || ' seconds') AS ts,
    'blog',
    CASE ABS(CAST(RANDOM() % 5 AS INTEGER))
        WHEN 0 THEN '/'
        WHEN 1 THEN '/posts/hello-world'
        WHEN 2 THEN '/posts/scaling-tailnets'
        WHEN 3 THEN '/posts/hello-world'
        ELSE '/feed.xml'
    END,
    CASE WHEN ABS(CAST(RANDOM() % 100 AS INTEGER)) < 2 THEN 404 ELSE 200 END,
    CASE ABS(CAST(RANDOM() % 4 AS INTEGER))
        WHEN 0 THEN 'alice@example.com'
        WHEN 1 THEN 'bob@example.com'
        WHEN 2 THEN 'dana@example.com'
        ELSE 'eve@example.com'
    END,
    CASE ABS(CAST(RANDOM() % 4 AS INTEGER))
        WHEN 0 THEN 'Alice Chen'
        WHEN 1 THEN 'Bob Martinez'
        WHEN 2 THEN 'Dana Singh'
        ELSE 'Eve Johnson'
    END,
    CASE ABS(CAST(RANDOM() % 3 AS INTEGER))
        WHEN 0 THEN 'alice-mbp'
        WHEN 1 THEN 'bob-desktop'
        ELSE 'dana-laptop'
    END,
    '100.64.1.10',
    CASE ABS(CAST(RANDOM() % 3 AS INTEGER))
        WHEN 0 THEN 'macOS'
        WHEN 1 THEN 'Linux'
        ELSE 'Windows'
    END,
    '', ''
FROM slots s, (
    WITH RECURSIVE reps(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM reps WHERE i < 20)
    SELECT i FROM reps
) r
WHERE r.i <= s.weight;

-- dashboard: steady traffic (~1800 requests)
WITH RECURSIVE hours(n) AS (
    SELECT 0 UNION ALL SELECT n+1 FROM hours WHERE n < 167
),
slots AS (
    SELECT
        strftime('%Y-%m-%dT%H:%M:%SZ', '${NOW_RFC3339}', '-' || n || ' hours') AS slot,
        CASE
            WHEN (CAST(strftime('%H', '${NOW_RFC3339}', '-' || n || ' hours') AS INTEGER)) BETWEEN 8 AND 18
                THEN 8 + ABS(CAST(RANDOM() % 15 AS INTEGER))
            WHEN (CAST(strftime('%H', '${NOW_RFC3339}', '-' || n || ' hours') AS INTEGER)) BETWEEN 6 AND 7
              OR (CAST(strftime('%H', '${NOW_RFC3339}', '-' || n || ' hours') AS INTEGER)) BETWEEN 19 AND 21
                THEN 3 + ABS(CAST(RANDOM() % 6 AS INTEGER))
            ELSE ABS(CAST(RANDOM() % 3 AS INTEGER))
        END AS weight
    FROM hours
)
INSERT INTO requests (ts, site, path, status, user_login, user_name, node_name, node_ip, os, os_version, device)
SELECT
    strftime('%Y-%m-%dT%H:%M:%SZ', s.slot, '+' || ABS(CAST(RANDOM() % 3600 AS INTEGER)) || ' seconds') AS ts,
    'dashboard',
    CASE ABS(CAST(RANDOM() % 6 AS INTEGER))
        WHEN 0 THEN '/'
        WHEN 1 THEN '/assets/app.js'
        WHEN 2 THEN '/assets/style.css'
        WHEN 3 THEN '/api/metrics'
        WHEN 4 THEN '/api/health'
        ELSE '/'
    END,
    CASE WHEN ABS(CAST(RANDOM() % 100 AS INTEGER)) < 1 THEN 500 ELSE 200 END,
    CASE ABS(CAST(RANDOM() % 5 AS INTEGER))
        WHEN 0 THEN 'alice@example.com'
        WHEN 1 THEN 'bob@example.com'
        WHEN 2 THEN 'charlie@example.com'
        WHEN 3 THEN 'dana@example.com'
        ELSE 'frank@example.com'
    END,
    CASE ABS(CAST(RANDOM() % 5 AS INTEGER))
        WHEN 0 THEN 'Alice Chen'
        WHEN 1 THEN 'Bob Martinez'
        WHEN 2 THEN 'Charlie Park'
        WHEN 3 THEN 'Dana Singh'
        ELSE 'Frank Liu'
    END,
    CASE ABS(CAST(RANDOM() % 3 AS INTEGER))
        WHEN 0 THEN 'alice-mbp'
        WHEN 1 THEN 'charlie-linux'
        ELSE 'ci-runner-01'
    END,
    '100.64.1.20',
    CASE ABS(CAST(RANDOM() % 3 AS INTEGER))
        WHEN 0 THEN 'macOS'
        WHEN 1 THEN 'Linux'
        ELSE 'macOS'
    END,
    '', ''
FROM slots s, (
    WITH RECURSIVE reps(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM reps WHERE i < 25)
    SELECT i FROM reps
) r
WHERE r.i <= s.weight;

-- status-page: low traffic (~400 requests)
WITH RECURSIVE hours(n) AS (
    SELECT 0 UNION ALL SELECT n+1 FROM hours WHERE n < 167
),
slots AS (
    SELECT
        strftime('%Y-%m-%dT%H:%M:%SZ', '${NOW_RFC3339}', '-' || n || ' hours') AS slot,
        CASE
            WHEN (CAST(strftime('%H', '${NOW_RFC3339}', '-' || n || ' hours') AS INTEGER)) BETWEEN 9 AND 17
                THEN 1 + ABS(CAST(RANDOM() % 4 AS INTEGER))
            ELSE ABS(CAST(RANDOM() % 2 AS INTEGER))
        END AS weight
    FROM hours
)
INSERT INTO requests (ts, site, path, status, user_login, user_name, node_name, node_ip, os, os_version, device)
SELECT
    strftime('%Y-%m-%dT%H:%M:%SZ', s.slot, '+' || ABS(CAST(RANDOM() % 3600 AS INTEGER)) || ' seconds') AS ts,
    'status-page',
    '/',
    200,
    CASE ABS(CAST(RANDOM() % 3 AS INTEGER))
        WHEN 0 THEN 'alice@example.com'
        WHEN 1 THEN 'bob@example.com'
        ELSE 'monitoring@example.com'
    END,
    CASE ABS(CAST(RANDOM() % 3 AS INTEGER))
        WHEN 0 THEN 'Alice Chen'
        WHEN 1 THEN 'Bob Martinez'
        ELSE 'Monitoring Bot'
    END,
    CASE ABS(CAST(RANDOM() % 2 AS INTEGER))
        WHEN 0 THEN 'monitoring-server'
        ELSE 'alice-mbp'
    END,
    '100.64.1.50',
    'Linux', '', ''
FROM slots s, (
    WITH RECURSIVE reps(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM reps WHERE i < 5)
    SELECT i FROM reps
) r
WHERE r.i <= s.weight;
SEEDSQL

# ═══════════════════════════════════════════════════════════════════════════════
# Webhook deliveries
# ═══════════════════════════════════════════════════════════════════════════════

sqlite3 "$DB" <<'SQL'
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    webhook_id  TEXT NOT NULL,
    event       TEXT NOT NULL,
    site        TEXT NOT NULL,
    url         TEXT NOT NULL,
    payload     TEXT NOT NULL,
    attempt     INTEGER NOT NULL,
    status      INTEGER,
    error       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    signed      INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0
);
SQL

# Generate ~80 webhook deliveries spread over 7 days using SQL CTEs.
# This gives a populated time-series chart with visible patterns.
sqlite3 "$DB" <<WEBHOOKSQL
-- Generate deploy.success events: ~10 per day across 4 sites during work hours.
-- Each gets a unique webhook_id. ~15% fail on first attempt and need retries.
WITH RECURSIVE seq(n) AS (
    SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n < 70
),
base AS (
    SELECT
        n,
        'msg_ds' || printf('%03d', n) AS wid,
        -- Spread across 7 days: offset in seconds from now, biased to work hours
        CAST(
            (ABS(CAST(RANDOM() % 7 AS INTEGER))) * 86400
            + 8 * 3600
            + ABS(CAST(RANDOM() % (10 * 3600) AS INTEGER))
        AS INTEGER) AS ago,
        CASE ABS(CAST(RANDOM() % 4 AS INTEGER))
            WHEN 0 THEN 'docs'
            WHEN 1 THEN 'blog'
            WHEN 2 THEN 'dashboard'
            ELSE 'status-page'
        END AS site,
        -- 15% chance of first-attempt failure
        CASE WHEN ABS(CAST(RANDOM() % 100 AS INTEGER)) < 15 THEN 1 ELSE 0 END AS fails_first
    FROM seq
)
INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at, signed, duration_ms)
-- First attempt for all
SELECT wid, 'deploy.success', site,
    'https://hooks.example.com/tspages',
    '{"type":"deploy.success","data":{"site":"' || site || '"}}',
    1,
    CASE WHEN fails_first = 1 THEN 502 ELSE 200 END,
    CASE WHEN fails_first = 1 THEN 'Bad Gateway' ELSE '' END,
    strftime('%Y-%m-%dT%H:%M:%SZ', '${NOW_RFC3339}', '-' || ago || ' seconds'),
    1,
    CASE WHEN fails_first = 1
        THEN 800 + ABS(CAST(RANDOM() % 4200 AS INTEGER))
        ELSE 40 + ABS(CAST(RANDOM() % 260 AS INTEGER))
    END
FROM base
UNION ALL
-- Retry (attempt 2) for the ones that failed: ~60% succeed on retry
SELECT wid, 'deploy.success', site,
    'https://hooks.example.com/tspages',
    '{"type":"deploy.success","data":{"site":"' || site || '"}}',
    2,
    CASE WHEN ABS(CAST(RANDOM() % 100 AS INTEGER)) < 60 THEN 200 ELSE 502 END,
    CASE WHEN ABS(CAST(RANDOM() % 100 AS INTEGER)) < 60 THEN '' ELSE 'Bad Gateway' END,
    strftime('%Y-%m-%dT%H:%M:%SZ', '${NOW_RFC3339}', '-' || (ago - 5) || ' seconds'),
    1,
    50 + ABS(CAST(RANDOM() % 350 AS INTEGER))
FROM base WHERE fails_first = 1;

-- deploy.failed events: ~12 scattered across the week
WITH RECURSIVE seq(n) AS (
    SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n < 12
),
base AS (
    SELECT
        n,
        'msg_df' || printf('%03d', n) AS wid,
        CAST(
            ABS(CAST(RANDOM() % 7 AS INTEGER)) * 86400
            + 8 * 3600
            + ABS(CAST(RANDOM() % (10 * 3600) AS INTEGER))
        AS INTEGER) AS ago,
        CASE ABS(CAST(RANDOM() % 4 AS INTEGER))
            WHEN 0 THEN 'docs'
            WHEN 1 THEN 'blog'
            WHEN 2 THEN 'dashboard'
            ELSE 'status-page'
        END AS site
    FROM seq
)
INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at, duration_ms)
SELECT wid, 'deploy.failed', site,
    'https://hooks.example.com/tspages',
    '{"type":"deploy.failed","data":{"site":"' || site || '","error":"extraction failed"}}',
    1, 200, '',
    strftime('%Y-%m-%dT%H:%M:%SZ', '${NOW_RFC3339}', '-' || ago || ' seconds'),
    30 + ABS(CAST(RANDOM() % 170 AS INTEGER))
FROM base;

-- site.created events: 5 across the week
WITH RECURSIVE seq(n) AS (
    SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n < 5
),
base AS (
    SELECT
        n,
        'msg_sc' || printf('%03d', n) AS wid,
        CASE n
            WHEN 1 THEN 'docs'
            WHEN 2 THEN 'blog'
            WHEN 3 THEN 'dashboard'
            WHEN 4 THEN 'status-page'
            ELSE 'design-system'
        END AS site,
        CASE n
            WHEN 1 THEN $(( OFF_7D + 60 ))
            WHEN 2 THEN $(( OFF_5D + 120 ))
            WHEN 3 THEN $(( OFF_3D + 120 ))
            WHEN 4 THEN $(( OFF_6D + 120 ))
            ELSE $(( OFF_12H ))
        END AS ago
    FROM seq
)
INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at, signed, duration_ms)
SELECT wid, 'site.created', site,
    'https://hooks.example.com/tspages',
    '{"type":"site.created","data":{"site":"' || site || '"}}',
    1, 200, '',
    strftime('%Y-%m-%dT%H:%M:%SZ', '${NOW_RFC3339}', '-' || ago || ' seconds'),
    1,
    35 + ABS(CAST(RANDOM() % 165 AS INTEGER))
FROM base;

-- site.deleted: 2 events
INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at, duration_ms)
VALUES
    ('msg_sd001', 'site.deleted', 'old-project', 'https://hooks.example.com/tspages',
     '{"type":"site.deleted","data":{"site":"old-project"}}', 1, 200, '',
     strftime('%Y-%m-%dT%H:%M:%SZ', '${NOW_RFC3339}', '-$(( OFF_5D + 3600 )) seconds'), 87),
    ('msg_sd002', 'site.deleted', 'staging', 'https://hooks.example.com/tspages',
     '{"type":"site.deleted","data":{"site":"staging"}}', 1, 200, '',
     strftime('%Y-%m-%dT%H:%M:%SZ', '${NOW_RFC3339}', '-$(( OFF_2D + 7200 )) seconds'), 124);
WEBHOOKSQL

echo "Done. Summary:"
echo "  Sites:"
for site in "$DATA"/sites/*/; do
    name=$(basename "$site")
    ndeps=$(find "$site/deployments" -name ".complete" 2>/dev/null | wc -l | tr -d ' ')
    active="none"
    if [ -L "$site/current" ]; then
        active=$(basename "$(readlink "$site/current")")
    fi
    printf "    %-20s %s deployment(s), active: %s\n" "$name" "$ndeps" "$active"
done
echo "  Analytics:"
sqlite3 "$DB" "SELECT '    ' || site || ': ' || COUNT(*) || ' requests' FROM requests GROUP BY site ORDER BY COUNT(*) DESC;"
echo "  Webhooks:"
sqlite3 "$DB" "SELECT '    ' || COUNT(DISTINCT webhook_id) || ' deliveries (' || SUM(CASE WHEN status BETWEEN 200 AND 299 THEN 1 ELSE 0 END) || ' succeeded, ' || SUM(CASE WHEN status NOT BETWEEN 200 AND 299 OR status IS NULL THEN 1 ELSE 0 END) || ' failed attempts)' FROM webhook_deliveries;"
echo "  Time range: $(ts_ago $OFF_7D) .. $(ts_ago 0)"
