# GitHub Actions

You can deploy to tspages from GitHub Actions using the
[tailscale/github-action](https://github.com/tailscale/github-action) to connect to your tailnet.

```yaml
name: Deploy to tspages

on:
  push:
    branches: [ main ]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Build
        run: npm ci && npm run build

      - name: Connect to tailnet
        uses: tailscale/github-action@v2
        with:
          oauth-client-id: ${{ secrets.TS_OAUTH_CLIENT_ID }}
          oauth-secret: ${{ secrets.TS_OAUTH_SECRET }}
          tags: tag:ci

      - name: Deploy
        run: |
          cd dist
          zip -r ../deploy.zip .
          curl -sf \
            --upload-file ../deploy.zip \
            -H "Content-Type: application/zip" \
            https://pages.your-tailnet.ts.net/deploy/docs
```

The runner joins the tailnet as `tag:ci`. The tailnet policy grants deploy access -- no secrets in the `curl` command.

## Grant for CI

Add a grant in your tailnet policy so the CI runner can deploy:

```json
{
  "src": ["tag:ci"],
  "dst": ["tag:pages"],
  "ip": ["443"],
  "app": {
    "tspages.mazetti.me/cap/pages": [
      { "access": "deploy", "sites": ["docs"] }
    ]
  }
}
```

See [Authorization](authorization) for more grant examples.
