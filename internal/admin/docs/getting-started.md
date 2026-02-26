# Getting Started

## Prerequisites

You need a **reusable auth key** tagged with `tag:pages` (or any tag you choose). Create one in the
[Tailscale admin console](https://login.tailscale.com/admin/settings/keys) under Settings > Keys > Generate auth key.
Make sure **Reusable** is checked, since tspages registers multiple devices with the same key.

## 1. Add a tailnet grant

In the Tailscale admin console, go to Access Controls and add a grant so tailnet members can use tspages:

```json
{
  "grants": [
    {
      "src": ["autogroup:member"],
      "dst": ["tag:pages"],
      "ip": ["443"],
      "app": {
        "tspages.mazetti.me/cap/pages": [
          { "access": "admin" }
        ]
      }
    }
  ]
}
```

> Start with `admin` access to set things up. You can narrow permissions later -- see
> [Authorization](authorization) for fine-grained examples.

## 2. Run tspages

### Docker (recommended)

```bash
docker run -d \
  -v tspages-state:/state \
  -v tspages-data:/data \
  -e TS_AUTHKEY=tskey-auth-... \
  ghcr.io/radiergummi/tspages:latest
```

That's it. The default configuration works out of the box -- state is stored in `/state`, site data in `/data`.

### Binary

Download the latest release from [GitHub](https://github.com/Radiergummi/tspages/releases/latest), then run:

```bash
TS_AUTHKEY=tskey-auth-... ./tspages
```

This uses `./state` and `./data` in the current directory. See [Configuration](configuration) for all options.

## 3. Deploy a site

```bash
tspages deploy your-site/dist my-site
```

Or with curl:

```bash
cd your-site/dist
zip -r ../site.zip .
curl -sf --upload-file ../site.zip \
  https://pages.your-tailnet.ts.net/deploy/my-site
```

Your site is live at `https://my-site.your-tailnet.ts.net/`. Open `https://pages.your-tailnet.ts.net/sites` to see
the admin dashboard.
