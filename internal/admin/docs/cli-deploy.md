# CLI Deploy

The `tspages` binary doubles as a deploy client:

```bash
tspages deploy <path> <site> [flags]
```

`<path>` can be a directory (automatically zipped) or a file (ZIP, tar.gz, Markdown, etc.).

## Server discovery

The command finds the control plane automatically by querying the local Tailscale daemon for the
tailnet's DNS suffix and constructing `https://pages.<suffix>`. Override with:

- `--server URL` flag
- `TSPAGES_SERVER` environment variable

## Flags

| Flag            | Description                             |
| --------------- | --------------------------------------- |
| `--server`      | Control plane URL (overrides discovery) |
| `--no-activate` | Upload without switching live traffic   |

## Examples

```bash
# Deploy a build directory
tspages deploy ./dist my-site

# Deploy a single Markdown file
tspages deploy README.md notes

# Deploy without activating
tspages deploy ./dist staging --no-activate

# Explicit server URL
tspages deploy ./dist my-site --server https://pages.my-tailnet.ts.net
```
