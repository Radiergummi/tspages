# Frontend Build Process Design

**Date:** 2026-02-25
**Status:** Draft

## Goal

Replace the admin panel's hand-written CSS and vanilla JS with a proper build pipeline: Vite + TypeScript + Tailwind CSS v4. Keep Go templates as the rendering layer — this is a build tooling change, not an architecture change.

## Motivation

- **Developer experience:** TypeScript types, Tailwind IntelliSense, Vite HMR
- **Design consistency:** Tailwind utilities instead of 700 lines of bespoke CSS
- **Code quality:** TypeScript modules with proper imports instead of inline `<script>` blocks and global functions
- **Maintainability:** npm dependencies for Chart.js and fflate instead of vendored minified files

## Current State

```
internal/admin/
├── render.go               ← //go:embed assets/* and templates/*.html
├── templates/
│   ├── layout.html         ← <link href="/assets/style.css">
│   ├── sites.html          ← inline <script> for modal
│   ├── site.html           ← <script src="/assets/deploy-drop.js"> + inline scripts
│   ├── deployment.html     ← inline <script> for activate/delete
│   ├── deployments.html    ← no JS
│   └── analytics.html      ← <script src="/assets/chart.min.js"> + analytics.js
└── assets/
    ├── style.css           ← 697 lines, custom CSS properties, dark/light
    ├── deploy-drop.js      ← drag-and-drop upload (175 lines)
    ├── analytics.js        ← Chart.js init (268 lines)
    ├── chart.min.js        ← vendored Chart.js (201KB)
    └── fflate.min.js       ← vendored fflate (32KB)
```

Assets are embedded via `//go:embed assets/*` and served at `/assets/` by `AssetHandler()`.

## Design

### File layout

```
/                                       ← repo root
├── Makefile                            ← build orchestration
├── package.json                        ← npm deps (vite, tailwindcss, chart.js, fflate, typescript)
├── vite.config.ts                      ← multi-entry build config
├── tsconfig.json                       ← strict TypeScript config
├── web/admin/src/
│   ├── main.css                        ← @import "tailwindcss" + @theme
│   ├── pages/
│   │   ├── sites.ts                    ← new-site modal logic
│   │   ├── site.ts                     ← deploy modal, activate, delete, cleanup
│   │   ├── deployment.ts               ← activate, delete deployment
│   │   ├── deployments.ts              ← (minimal — pagination is server-rendered)
│   │   └── analytics.ts               ← Chart.js init + data fetch
│   └── lib/
│       ├── modal.ts                    ← shared modal open/close/backdrop-dismiss
│       ├── deploy-drop.ts             ← drag-and-drop + fflate ZIP upload
│       └── api.ts                      ← shared fetch helpers (confirm + POST/DELETE + reload)
├── internal/admin/
│   ├── assets/dist/                    ← Vite build output (gitignored)
│   │   ├── manifest.json              ← maps entry names → hashed filenames
│   │   ├── sites-[hash].js
│   │   ├── site-[hash].js
│   │   ├── deployment-[hash].js
│   │   ├── analytics-[hash].js
│   │   └── main-[hash].css
│   ├── render.go                       ← updated embed + asset() template func
│   └── templates/                      ← updated with Tailwind classes
```

### Vite configuration

`vite.config.ts` at repo root:

- Uses `@tailwindcss/vite` plugin (Tailwind v4 CSS-first config)
- `build.rollupOptions.input`: one entry per page (`web/admin/src/pages/*.ts`) plus `web/admin/src/main.css`
- `build.outDir`: `internal/admin/assets/dist`
- `build.manifest`: `true` (generates `manifest.json` for Go to resolve hashed filenames)
- `build.emptyOutDir`: `true`

### Tailwind CSS configuration

All theming in `web/admin/src/main.css` using Tailwind v4's CSS-first approach:

```css
@import "tailwindcss";

@theme {
  --color-surface: ...;
  --color-accent: ...;
  /* custom tokens as needed */
}
```

No `tailwind.config.ts`. The `@theme` block defines custom design tokens. Use Tailwind defaults as the base; accept visual divergence from the current design.

### Template content scanning

Vite with Tailwind needs to know where classes are used. Since templates live in `internal/admin/templates/*.html`, vite config's `css.transformer` or tailwind's `@source` directive will point at that directory:

```css
@source "../../../internal/admin/templates";
```

This ensures Tailwind scans Go templates for utility classes.

### TypeScript entries

Each page gets its own entry point. Entries import from `lib/` as needed:

| Entry | Imports | Responsibility |
|-------|---------|---------------|
| `sites.ts` | `modal.ts` | New-site modal open/close/form |
| `site.ts` | `modal.ts`, `deploy-drop.ts`, `api.ts` | Deploy modal, activate, delete site, cleanup |
| `deployment.ts` | `api.ts` | Activate deployment, delete deployment |
| `deployments.ts` | — | Minimal (no interactive JS currently) |
| `analytics.ts` | `chart.js` (npm) | Fetch JSON, render charts |

Each entry also imports `main.css` so Vite includes the CSS bundle.

### Shared libraries

- **`modal.ts`**: Generic `openModal(id)` / `closeModal(id)` + backdrop click dismiss. Replaces inline modal scripts duplicated across sites.html, site.html.
- **`deploy-drop.ts`**: Port of current `deploy-drop.js` to TypeScript. Imports `fflate` from npm instead of vendored global. Exports `initDeployDrop(siteName: string, host: string)`.
- **`api.ts`**: Small helpers for the confirm→fetch→reload pattern used by activate/delete/cleanup actions. Replaces duplicated inline `async function activate(...)` etc.

### Go-side changes

**`render.go`:**

1. Change embed directive from `//go:embed assets/*` to `//go:embed assets/dist/*`
2. Add an `asset` template function that reads `manifest.json` (once at startup) and resolves `"sites"` → `/assets/dist/sites-abc123.js`
3. `AssetHandler()` updated to serve from `assets/dist/` subdirectory

**`layout.html`:**

```html
<link rel="stylesheet" href="{{asset "main.css"}}">
```

**Page templates** (e.g. `site.html`):

```html
{{define "script"}}
<script type="module" src="{{asset "pages/site.ts"}}"></script>
{{end}}
```

Inline `<script>` blocks are removed entirely. Template-injected data (site name, host) is passed via `data-*` attributes on a known element, read by the TS entry:

```html
<main data-site="{{.Site.Name}}" data-host="{{.Host}}">
```

```typescript
const main = document.querySelector("main")!;
const siteName = main.dataset.site!;
```

### Makefile

```makefile
.PHONY: all build dev clean frontend go

all: build

build: frontend go

frontend: node_modules
	npx vite build

go:
	go build -o tspages ./cmd/tspages

dev:
	npx vite build --watch &
	# Or: npx vite dev for HMR proxy setup

node_modules: package.json
	npm install
	@touch node_modules

clean:
	rm -rf internal/admin/assets/dist node_modules tspages
```

`make` builds everything. `make frontend` builds just the frontend. `make go` builds just the Go binary (assumes frontend already built).

### .gitignore additions

```
node_modules/
internal/admin/assets/dist/
```

### What gets deleted

- `internal/admin/assets/style.css` — replaced by Tailwind utilities in templates + `main.css`
- `internal/admin/assets/deploy-drop.js` — ported to `web/admin/src/lib/deploy-drop.ts`
- `internal/admin/assets/analytics.js` — ported to `web/admin/src/pages/analytics.ts`
- `internal/admin/assets/chart.min.js` — replaced by npm `chart.js`
- `internal/admin/assets/fflate.min.js` — replaced by npm `fflate`
- All inline `<script>` blocks in templates

### What stays the same

- Go templates as the rendering layer (`html/template`)
- Server-rendered HTML with dual JSON/HTML content negotiation
- Template functions (`reltime`, `abstime`, `bytes`, etc.)
- Template structure (layout + page defines `title`, `content`, `script`, `nav-*`)
- `//go:embed` for bundling assets into the binary
- Route structure and handler logic

### Dev workflow

1. **Frontend iteration:** `npx vite build --watch` rebuilds on file changes. Restart Go server to pick up new embedded assets (or use a dev mode that serves from disk).
2. **Full build:** `make` — builds frontend then Go binary.
3. **CI:** `make build` — requires Node.js + Go.

## Migration strategy

Port one page at a time. Each page migration:

1. Create the TS entry, move inline JS into it
2. Replace CSS classes in the template with Tailwind utilities
3. Update the `{{define "script"}}` block to use `{{asset ...}}`
4. Test the page works

Order: `deployments` (simplest, no JS) → `sites` → `deployment` → `site` → `analytics` (most complex).

Start with the build infrastructure (Vite config, Makefile, Go-side `asset` function, layout template update) before migrating any pages.
