# Upload Formats

The deploy endpoint auto-detects the upload format from magic bytes, so `Content-Type` is only
needed as a fallback.

## Archives

| Format     | How to upload                                                 |
| ---------- | ------------------------------------------------------------- |
| **ZIP**    | `zip -r site.zip . && curl --upload-file site.zip ...`        |
| **tar.gz** | `tar czf site.tar.gz . && curl --upload-file site.tar.gz ...` |
| **tar**    | `tar cf site.tar . && curl --upload-file site.tar ...`        |

Archives should contain files directly at the root (not wrapped in a parent directory). tar entries
with symlinks or hardlinks are rejected.

## Single files

For quick, one-off pages you can upload a single file instead of an archive. It is rendered into a
styled HTML page and served as `index.html`.

| Format         | Detection                                                                                  |
| -------------- | ------------------------------------------------------------------------------------------ |
| **Markdown**   | `?format=markdown` query param, `Content-Type: text/markdown`, or `.md`/`.markdown` in URL |
| **HTML**       | Content starts with `<` (after trimming whitespace)                                        |
| **Plain text** | Anything else                                                                              |

## Examples

```bash
# Markdown (detected by filename in the URL)
curl --upload-file README.md https://pages.your-tailnet.ts.net/deploy/notes/README.md

# Markdown (detected by query param)
curl --upload-file notes.txt "https://pages.your-tailnet.ts.net/deploy/notes?format=markdown"

# Plain text
echo "System is under maintenance" | curl -T - https://pages.your-tailnet.ts.net/deploy/status
```
