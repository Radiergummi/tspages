# CLAUDE.md

- The admin UI is embedded from `internal/admin/assets/dist` (gitignored). Run `npx vite build`
  (or `make build`) before `go build`/`go test`, or they fail.
- Log with `log/slog` and pass `"component", "<package name>"` as the first attribute
  (`"tsnet"` for `tsnet.Server.UserLogf`).
- Every user-visible change gets a bullet under `[Unreleased]` in `CHANGELOG.md`, written from
  the user's view.
- New or changed features: update `internal/help/docs/`, add tests. Deploy features must also
  work in the CLI (`internal/cli`).
- Admin pages that show user data need a matching API endpoint, documented in the OpenAPI spec.
- In templates, use the component classes from `web/admin/src/main.css` (`@layer components`),
  not inline utility classes.
- `storage.ValidSiteName` is the only site-name check (name = tsnet hostname = DNS label). The
  serve handler resolves symlinks before its path containment check; keep that order.
- In tests, inject capabilities with `auth.ContextWithCaps` instead of mocking WhoIs.
