# Phase 5: Slim Down Admin — Checklist

## Remove migration aliases from `admin/render.go`
- [x] Remove all `var renderPage = web.RenderPage` etc. aliases
- [x] Replace alias usage with direct `web.*` calls in all admin handlers

## Restructure `admin/handler.go`
- [x] Remove `handlerDeps` struct — each handler gets explicit fields
- [x] Remove `Handlers` struct and `NewHandlers`
- [x] Add `RegisterRoutes()` function

## Rename admin files to singular form
- [x] `sites.go` → `site.go`
- [x] `deployments.go` → `deployment.go`
- [x] `docs.go` → `doc.go`
- [x] `docs_test.go` → `doc_test.go`

## Update test files
- [x] Replace `handlerDeps{...}` with direct field initialization in `config_test.go`
- [x] Replace `NewHandlers`/`Handlers` with `testHandlers`/`newTestHandlers` in `handler_test.go`
- [x] Replace `NewHandlers` with `newTestHandlers` in `feed_test.go`
- [x] Remove unused `dnsSuffix` variables from SiteHealthHandler tests

## Update `cmd/tspages/main.go`
- [x] Replace `h *admin.Handlers` pattern with `admin.RegisterRoutes()`

## Verification
- [x] `go build ./cmd/tspages`
- [x] `go test ./...`
