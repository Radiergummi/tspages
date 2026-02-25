# Frontend Build Process Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a Vite + TypeScript + Tailwind CSS v4 build pipeline for the admin panel, replacing hand-written CSS and vanilla JS with proper frontend tooling.

**Architecture:** Vite builds TypeScript entries and Tailwind CSS from `web/admin/src/` into `internal/admin/assets/dist/`. Go embeds the build output and resolves hashed filenames via Vite's manifest.json. Templates keep Go `html/template` rendering but use Tailwind utility classes and `<script type="module">` tags pointing at built bundles.

**Tech Stack:** Vite 6, TypeScript 5, Tailwind CSS v4 (`@tailwindcss/vite`), Chart.js, fflate

**Design doc:** `docs/plans/2026-02-25-frontend-build-design.md`

---

## Task 1: Initialize npm project and Vite config

**Files:**
- Create: `package.json`
- Create: `vite.config.ts`
- Create: `tsconfig.json`
- Create: `web/admin/src/main.css`
- Create: `web/admin/src/pages/deployments.ts` (minimal placeholder)
- Modify: `.gitignore`

**Step 1: Initialize package.json**

Run:
```bash
npm init -y
```

Then edit `package.json` to set it up:

```json
{
  "name": "tspages-admin",
  "private": true,
  "type": "module",
  "scripts": {
    "build": "vite build",
    "dev": "vite build --watch"
  }
}
```

**Step 2: Install dependencies**

Run:
```bash
npm install -D vite typescript @tailwindcss/vite
npm install tailwindcss chart.js fflate
```

**Step 3: Create tsconfig.json**

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "isolatedModules": true,
    "skipLibCheck": true
  },
  "include": ["web/admin/src"]
}
```

**Step 4: Create vite.config.ts**

```typescript
import { resolve } from "path";
import { defineConfig } from "vite";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [tailwindcss()],
  build: {
    outDir: resolve(__dirname, "internal/admin/assets/dist"),
    emptyOutDir: true,
    manifest: true,
    rollupOptions: {
      input: {
        main: resolve(__dirname, "web/admin/src/main.css"),
        sites: resolve(__dirname, "web/admin/src/pages/sites.ts"),
        site: resolve(__dirname, "web/admin/src/pages/site.ts"),
        deployment: resolve(__dirname, "web/admin/src/pages/deployment.ts"),
        deployments: resolve(__dirname, "web/admin/src/pages/deployments.ts"),
        analytics: resolve(__dirname, "web/admin/src/pages/analytics.ts"),
      },
    },
  },
});
```

**Step 5: Create main.css**

```css
@import "tailwindcss";
@source "../../../internal/admin/templates";
```

This is the minimal starting point. Theme customization comes later during page migration.

**Step 6: Create placeholder entry**

Create `web/admin/src/pages/deployments.ts`:

```typescript
// Deployments page — no interactive JS needed.
// This entry exists so Vite produces a bundle that imports main.css.
import "../main.css";
```

**Step 7: Update .gitignore**

Add to `.gitignore`:

```
node_modules/
internal/admin/assets/dist/
```

**Step 8: Run the build to verify**

Run:
```bash
npx vite build
```

Expected: Build succeeds, `internal/admin/assets/dist/` contains `.manifest.json`, a CSS file, and a JS file.

**Step 9: Commit**

```bash
git add package.json package-lock.json vite.config.ts tsconfig.json .gitignore web/admin/src/
git commit -m "feat: initialize Vite + TypeScript + Tailwind build pipeline"
```

---

## Task 2: Create Makefile

**Files:**
- Create: `Makefile`

**Step 1: Create Makefile**

```makefile
.PHONY: all build dev clean frontend go

all: build

build: frontend go

frontend: node_modules
	npx vite build

go: frontend
	go build -o tspages ./cmd/tspages

dev: node_modules
	npx vite build --watch

node_modules: package.json
	npm install
	@touch node_modules

clean:
	rm -rf internal/admin/assets/dist node_modules tspages
```

Note: the indentation in Makefile MUST be tabs, not spaces.

**Step 2: Test make build**

Run:
```bash
make clean && make build
```

Expected: npm install runs, vite builds, go builds the binary. All three succeed.

**Step 3: Commit**

```bash
git add Makefile
git commit -m "feat: add Makefile for frontend + Go build"
```

---

## Task 3: Go-side asset manifest resolution

This wires up the Go side to read Vite's `manifest.json` and resolve hashed filenames in templates.

**Files:**
- Modify: `internal/admin/render.go`

**Step 1: Write test for asset resolution**

Create `internal/admin/render_test.go` (or add to existing test file):

```go
package admin

import "testing"

func TestResolveAsset(t *testing.T) {
	m := &manifest{
		entries: map[string]string{
			"web/admin/src/main.css":             "assets/main-abc123.css",
			"web/admin/src/pages/deployments.ts": "assets/deployments-def456.js",
		},
	}

	tests := []struct {
		key  string
		want string
	}{
		{"main.css", "/assets/dist/assets/main-abc123.css"},
		{"pages/deployments.ts", "/assets/dist/assets/deployments-def456.js"},
		{"nonexistent.ts", ""},
	}

	for _, tt := range tests {
		got := m.resolve(tt.key)
		if got != tt.want {
			t.Errorf("resolve(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run:
```bash
go test -run TestResolveAsset ./internal/admin/...
```

Expected: FAIL — `manifest` type doesn't exist yet.

**Step 3: Implement manifest resolution in render.go**

Add to `render.go`:

```go
// manifest maps Vite entry keys to their built output paths.
type manifest struct {
	entries map[string]string // input path → output "file" value
}

// viteManifest is loaded once at init from the embedded assets.
var viteManifest *manifest

func init() {
	data, err := assetFS.ReadFile("assets/dist/.vite/manifest.json")
	if err != nil {
		// No manifest means frontend hasn't been built yet.
		// Fail at template render time, not at startup.
		viteManifest = &manifest{entries: map[string]string{}}
		return
	}

	var raw map[string]struct {
		File string `json:"file"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		panic("admin: bad manifest.json: " + err.Error())
	}

	m := &manifest{entries: make(map[string]string, len(raw))}
	for k, v := range raw {
		m.entries[k] = v.File
	}
	viteManifest = m
}

const srcPrefix = "web/admin/src/"

// resolve maps a short key like "main.css" or "pages/sites.ts"
// to the full served path like "/assets/dist/assets/sites-abc123.js".
func (m *manifest) resolve(key string) string {
	full := srcPrefix + key
	f, ok := m.entries[full]
	if !ok {
		return ""
	}
	return "/assets/dist/" + f
}
```

**Step 4: Add "asset" template function**

Update the `funcs` map in `render.go`:

```go
"asset": func(key string) string {
    return viteManifest.resolve(key)
},
```

**Step 5: Update embed directive and AssetHandler**

Change:
```go
//go:embed assets/*
var assetFS embed.FS
```

To:
```go
//go:embed all:assets/dist
var assetFS embed.FS
```

Note: `all:` prefix is needed because `.vite/manifest.json` starts with a dot.

Update `AssetHandler()`:
```go
func AssetHandler() http.Handler {
	sub, _ := fs.Sub(assetFS, "assets/dist")
	return http.StripPrefix("/assets/dist/", http.FileServerFS(sub))
}
```

**Step 6: Run test to verify it passes**

Run:
```bash
go test -run TestResolveAsset ./internal/admin/...
```

Expected: PASS

**Step 7: Update layout.html to use asset function**

Change `layout.html` line 7:
```html
<link rel="stylesheet" href="/assets/style.css">
```
To:
```html
<link rel="stylesheet" href="{{asset "main.css"}}">
```

**Step 8: Update the route prefix in main.go**

Find where `/assets/` is mounted in `cmd/tspages/main.go` and change it to `/assets/dist/`. The route registration likely looks like:

```go
mux.Handle("GET /assets/{file...}", admin.AssetHandler())
```

Change to:

```go
mux.Handle("GET /assets/dist/{file...}", admin.AssetHandler())
```

**Step 9: Build and verify**

Run:
```bash
make build
```

Expected: Build succeeds. The Go binary embeds the Vite output.

**Step 10: Commit**

```bash
git add internal/admin/render.go internal/admin/render_test.go internal/admin/templates/layout.html cmd/tspages/main.go
git commit -m "feat: add Vite manifest resolution and asset template function"
```

---

## Task 4: Create shared TypeScript libraries

**Files:**
- Create: `web/admin/src/lib/modal.ts`
- Create: `web/admin/src/lib/api.ts`
- Create: `web/admin/src/lib/deploy-drop.ts`

**Step 1: Create modal.ts**

```typescript
export function openModal(id: string): void {
  const el = document.getElementById(id);
  if (el) el.classList.add("open");
}

export function closeModal(id: string): void {
  const el = document.getElementById(id);
  if (el) el.classList.remove("open");
}

/**
 * Set up a modal: close on backdrop click, close on Escape key.
 */
export function initModal(id: string): void {
  const el = document.getElementById(id);
  if (!el) return;

  el.addEventListener("click", (e) => {
    if (e.target === el) closeModal(id);
  });

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && el.classList.contains("open")) {
      closeModal(id);
    }
  });
}
```

**Step 2: Create api.ts**

```typescript
/**
 * Confirm with the user, then POST/DELETE to the given URL.
 * On success, redirect or reload. On failure, alert the error.
 */
export async function confirmAction(opts: {
  message: string;
  url: string;
  method: "POST" | "DELETE";
  onSuccess?: string; // URL to redirect to; omit to reload
}): Promise<void> {
  if (!confirm(opts.message)) return;

  const res = await fetch(opts.url, { method: opts.method });
  if (res.ok) {
    if (opts.onSuccess) {
      location.href = opts.onSuccess;
    } else {
      location.reload();
    }
  } else {
    alert(`Failed: ${(await res.text()).trim()}`);
  }
}

export function copyToClipboard(elementId: string): void {
  const el = document.getElementById(elementId);
  if (el?.textContent) {
    navigator.clipboard.writeText(el.textContent);
  }
}
```

**Step 3: Create deploy-drop.ts**

Port `internal/admin/assets/deploy-drop.js` to TypeScript, importing fflate from npm:

```typescript
import { zipSync } from "fflate";

export function initDeployDrop(siteName: string): void {
  const overlay = document.getElementById("deploy-overlay");
  if (!overlay) return;

  const overlayText = overlay.querySelector<HTMLElement>(".deploy-overlay-text");
  const progressBar = overlay.querySelector<HTMLElement>(".deploy-progress-bar");
  let dragCount = 0;

  function setState(state: string, message?: string): void {
    overlay!.className = `deploy-overlay ${state}`;
    if (message && overlayText) overlayText.textContent = message;
    if (progressBar) progressBar.hidden = state !== "uploading";
  }

  // Modal dropzone
  const modalDropzone = document.getElementById("modal-dropzone");
  const fileInput = document.getElementById("deploy-file-input") as HTMLInputElement | null;

  if (modalDropzone && fileInput) {
    modalDropzone.addEventListener("click", (e) => {
      const target = e.target as HTMLElement;
      if (target.tagName !== "BUTTON" && target.tagName !== "INPUT") {
        fileInput.click();
      }
    });

    modalDropzone.addEventListener("dragover", (e) => {
      e.preventDefault();
      modalDropzone.classList.add("dragover");
    });

    modalDropzone.addEventListener("dragleave", () => {
      modalDropzone.classList.remove("dragover");
    });

    modalDropzone.addEventListener("drop", (e) => {
      e.preventDefault();
      e.stopPropagation();
      modalDropzone.classList.remove("dragover");
      document.getElementById("deploy-modal")?.classList.remove("open");
      if (e.dataTransfer) handleDrop(e.dataTransfer);
    });
  }

  if (fileInput) {
    fileInput.addEventListener("change", async () => {
      if (!fileInput.files?.length) return;
      document.getElementById("deploy-modal")?.classList.remove("open");

      if (fileInput.files.length === 1) {
        upload(fileInput.files[0], fileInput.files[0].name);
        fileInput.value = "";
        return;
      }

      setState("uploading", "Zipping files\u2026");
      const fileMap: Record<string, Uint8Array> = {};
      await Promise.all(
        Array.from(fileInput.files).map(async (file) => {
          fileMap[file.name] = new Uint8Array(await file.arrayBuffer());
        })
      );
      fileInput.value = "";
      upload(new Blob([zipSync(fileMap)], { type: "application/zip" }));
    });
  }

  // Full-document drag-and-drop
  document.addEventListener("dragenter", (e) => {
    e.preventDefault();
    if (++dragCount === 1) setState("dragging", `Drop to deploy to ${siteName}`);
  });

  document.addEventListener("dragover", (e) => e.preventDefault());

  document.addEventListener("dragleave", (e) => {
    e.preventDefault();
    if (--dragCount <= 0) {
      dragCount = 0;
      setState("idle", "");
    }
  });

  document.addEventListener("drop", (e) => {
    e.preventDefault();
    dragCount = 0;
    if (e.dataTransfer) handleDrop(e.dataTransfer);
  });

  async function handleDrop(dataTransfer: DataTransfer): Promise<void> {
    const { items } = dataTransfer;
    if (!items?.length) {
      setState("idle", "");
      return;
    }

    if (items.length === 1 && items[0].kind === "file") {
      const file = items[0].getAsFile();
      if (file) {
        upload(file, file.name);
        return;
      }
    }

    setState("uploading", "Zipping files\u2026");
    const entries = [...items]
      .filter((item) => item.kind === "file")
      .map((item) => item.webkitGetAsEntry?.())
      .filter((e): e is FileSystemEntry => e !== null && e !== undefined);

    if (!entries.length) {
      setState("idle", "");
      return;
    }

    try {
      const fileMap = await readAllEntries(entries);
      if (!Object.keys(fileMap).length) {
        setState("error", "No files found");
        return;
      }
      upload(new Blob([zipSync(fileMap)], { type: "application/zip" }));
    } catch (err) {
      setState("error", `Error: ${(err as Error).message}`);
    }
  }

  async function readAllEntries(
    entries: FileSystemEntry[]
  ): Promise<Record<string, Uint8Array>> {
    const topEntries =
      entries.length === 1 && entries[0].isDirectory
        ? await readDir(entries[0] as FileSystemDirectoryEntry)
        : entries;
    return collectFiles(topEntries, "");
  }

  function readDir(dirEntry: FileSystemDirectoryEntry): Promise<FileSystemEntry[]> {
    return new Promise((resolve, reject) => {
      const reader = dirEntry.createReader();
      const all: FileSystemEntry[] = [];
      (function read() {
        reader.readEntries((batch) => {
          if (!batch.length) {
            resolve(all);
            return;
          }
          all.push(...batch);
          read();
        }, reject);
      })();
    });
  }

  async function collectFiles(
    entries: FileSystemEntry[],
    prefix: string
  ): Promise<Record<string, Uint8Array>> {
    const fileMap: Record<string, Uint8Array> = {};
    await Promise.all(
      entries.map(async (entry) => {
        const path = prefix ? `${prefix}/${entry.name}` : entry.name;
        if (entry.isFile) {
          const file = await new Promise<File>((resolve, reject) =>
            (entry as FileSystemFileEntry).file(resolve, reject)
          );
          fileMap[path] = new Uint8Array(await file.arrayBuffer());
        } else if (entry.isDirectory) {
          Object.assign(
            fileMap,
            await collectFiles(
              await readDir(entry as FileSystemDirectoryEntry),
              path
            )
          );
        }
      })
    );
    return fileMap;
  }

  async function upload(blob: Blob, filename?: string): Promise<void> {
    setState("uploading", `Deploying to ${siteName}\u2026`);
    let url = `/deploy/${encodeURIComponent(siteName)}`;
    if (filename) url += `/${encodeURIComponent(filename)}`;

    try {
      const resp = await fetch(url, { method: "POST", body: blob });
      if (resp.ok) {
        setState("success", "Deployed!");
        setTimeout(() => location.reload(), 800);
      } else {
        setState("error", `Deploy failed: ${(await resp.text()).trim()}`);
      }
    } catch {
      setState("error", "Network error");
    }
  }
}
```

**Step 4: Verify the build still works**

Run:
```bash
npx vite build
```

Expected: Build succeeds. The lib files are tree-shaken into page bundles — they don't produce separate output files.

**Step 5: Commit**

```bash
git add web/admin/src/lib/
git commit -m "feat: add shared TS libraries (modal, api, deploy-drop)"
```

---

## Task 5: Create all page entry points

**Files:**
- Create: `web/admin/src/pages/sites.ts`
- Create: `web/admin/src/pages/site.ts`
- Create: `web/admin/src/pages/deployment.ts`
- Create: `web/admin/src/pages/analytics.ts`
- Modify: `web/admin/src/pages/deployments.ts` (already exists from Task 1)

**Step 1: Create sites.ts**

```typescript
import "../main.css";
import { openModal, closeModal, initModal } from "../lib/modal";

const modal = document.getElementById("new-site-modal");
if (modal) {
  initModal("new-site-modal");

  document.querySelector<HTMLButtonElement>("[data-action='new-site']")
    ?.addEventListener("click", () => {
      openModal("new-site-modal");
      setTimeout(() => document.getElementById("site-name")?.focus(), 0);
    });

  modal.querySelector<HTMLButtonElement>(".modal-close")
    ?.addEventListener("click", () => closeModal("new-site-modal"));
}
```

**Step 2: Create site.ts**

```typescript
import "../main.css";
import { openModal, closeModal, initModal } from "../lib/modal";
import { confirmAction, copyToClipboard } from "../lib/api";
import { initDeployDrop } from "../lib/deploy-drop";

const main = document.querySelector<HTMLElement>("main")!;
const siteName = main.dataset.site!;

// Deploy modal
const deployModal = document.getElementById("deploy-modal");
if (deployModal) {
  initModal("deploy-modal");

  document.querySelector<HTMLButtonElement>("[data-action='deploy']")
    ?.addEventListener("click", () => openModal("deploy-modal"));

  deployModal.querySelector<HTMLButtonElement>(".modal-close")
    ?.addEventListener("click", () => closeModal("deploy-modal"));

  initDeployDrop(siteName);
}

// Copy deploy command
document.querySelector<HTMLButtonElement>("[data-action='copy-cmd']")
  ?.addEventListener("click", () => copyToClipboard("deploy-cmd"));

// Activate deployment
document.querySelectorAll<HTMLButtonElement>("[data-action='activate']").forEach((btn) => {
  btn.addEventListener("click", () => {
    const id = btn.dataset.deploymentId!;
    confirmAction({
      message: `Activate deployment "${id}"?`,
      url: `/deploy/${encodeURIComponent(siteName)}/${encodeURIComponent(id)}/activate`,
      method: "POST",
    });
  });
});

// Delete site
document.querySelector<HTMLButtonElement>("[data-action='delete-site']")
  ?.addEventListener("click", () => {
    confirmAction({
      message: `Delete site "${siteName}" and all its deployments?`,
      url: `/deploy/${encodeURIComponent(siteName)}`,
      method: "DELETE",
      onSuccess: "/sites",
    });
  });

// Cleanup old deployments
document.querySelector<HTMLButtonElement>("[data-action='cleanup']")
  ?.addEventListener("click", async () => {
    if (!confirm("Delete all inactive deployments? This cannot be undone.")) return;

    const res = await fetch(`/deploy/${encodeURIComponent(siteName)}/deployments`, {
      method: "DELETE",
    });

    if (res.ok) {
      const data = await res.json();
      alert(`Deleted ${data.deleted} deployment(s).`);
      location.reload();
    } else {
      alert(`Cleanup failed: ${(await res.text()).trim()}`);
    }
  });
```

**Step 3: Create deployment.ts**

```typescript
import "../main.css";
import { confirmAction } from "../lib/api";

const main = document.querySelector<HTMLElement>("main")!;
const siteName = main.dataset.site!;

// Activate deployment
document.querySelector<HTMLButtonElement>("[data-action='activate']")
  ?.addEventListener("click", () => {
    const id = main.dataset.deploymentId!;
    confirmAction({
      message: `Activate deployment "${id}"?`,
      url: `/deploy/${encodeURIComponent(siteName)}/${encodeURIComponent(id)}/activate`,
      method: "POST",
    });
  });

// Delete deployment
document.querySelector<HTMLButtonElement>("[data-action='delete']")
  ?.addEventListener("click", () => {
    const id = main.dataset.deploymentId!;
    confirmAction({
      message: `Delete deployment "${id}"? This cannot be undone.`,
      url: `/deploy/${encodeURIComponent(siteName)}/${encodeURIComponent(id)}`,
      method: "DELETE",
      onSuccess: `/sites/${encodeURIComponent(siteName)}`,
    });
  });
```

**Step 4: Create analytics.ts**

Port `internal/admin/assets/analytics.js` to TypeScript with Chart.js as npm import:

```typescript
import "../main.css";
import {
  Chart,
  LineController,
  BarController,
  DoughnutController,
  LineElement,
  BarElement,
  ArcElement,
  PointElement,
  LinearScale,
  CategoryScale,
  Filler,
  Tooltip,
  type ChartConfiguration,
} from "chart.js";

// Register only the components we use (tree-shaking)
Chart.register(
  LineController, BarController, DoughnutController,
  LineElement, BarElement, ArcElement, PointElement,
  LinearScale, CategoryScale, Filler, Tooltip
);

const palette = [
  "#4b70e5", "#f59e0b", "#10b981", "#f87171", "#a78bfa",
  "#06b6d4", "#ec4899", "#84cc16", "#f97316", "#6366f1",
];

async function main(): Promise<void> {
  const style = getComputedStyle(document.documentElement);
  const textColor = style.getPropertyValue("--text-muted").trim() || "#71717a";
  const gridColor = style.getPropertyValue("--border").trim() || "#2a2a32";
  const accent = style.getPropertyValue("--accent").trim() || "#4b70e5";
  const mainText = style.getPropertyValue("--text").trim() || "#e4e4e7";

  Chart.defaults.color = textColor;
  Chart.defaults.borderColor = gridColor;
  Chart.defaults.font.family = style.getPropertyValue("--sans").trim();
  Chart.defaults.font.size = 11;

  const resp = await fetch(window.location.href, {
    headers: { Accept: "application/json" },
  });
  const data = await resp.json();

  if (data.time_series?.length) {
    lineChart("requests-chart", data.time_series, data.range, accent, gridColor);
  }
  if (data.status_time_series?.length) {
    stackedBar("status-chart", data.status_time_series, data.range);
  }
  if (data.sites?.length) {
    doughnut("sites-chart", pluck(data.sites, "site"), pluck(data.sites, "count"), mainText);
  }
  if (data.os?.length) {
    doughnut("os-chart", pluck(data.os, "os"), pluck(data.os, "count"), mainText);
  }
  if (data.nodes?.length) {
    doughnut("nodes-chart", pluck(data.nodes, "node_name"), pluck(data.nodes, "count"), mainText);
  }
}

function lineChart(
  id: string,
  buckets: { time: string; count: number }[],
  range: string,
  accent: string,
  gridColor: string,
): void {
  const node = document.getElementById(id) as HTMLCanvasElement | null;
  if (!node) return;

  const counts = pluck(buckets, "count");
  const max = Math.max(...counts) || 1;

  new Chart(node, {
    type: "line",
    data: {
      labels: buckets.map((b) => formatLabel(b.time, range)),
      datasets: [{
        backgroundColor: accent + "18",
        borderColor: accent,
        borderWidth: 1.5,
        data: counts,
        fill: "start",
        pointHitRadius: 8,
        pointRadius: 0,
        tension: 0.35,
      }],
    },
    options: {
      interaction: { intersect: false, mode: "index" },
      maintainAspectRatio: false,
      plugins: {
        legend: { display: false },
        tooltip: {
          backgroundColor: "#1c1c22",
          bodyColor: "#e4e4e7",
          borderColor: "#2a2a32",
          borderWidth: 1,
          cornerRadius: 6,
          padding: 10,
          titleColor: "#e4e4e7",
        },
      },
      responsive: true,
      scales: {
        x: { display: false, offset: false, grid: { color: gridColor + "40" } },
        y: {
          display: false,
          min: -max * 0.05,
          grace: "5%",
          afterFit: (axis) => { axis.paddingBottom = 0; },
        },
      },
    },
  } as ChartConfiguration);
}

function stackedBar(
  id: string,
  buckets: { time: string; ok: number; client_err: number; server_err: number }[],
  range: string,
): void {
  const node = document.getElementById(id) as HTMLCanvasElement | null;
  if (!node) return;

  const labels = buckets.map((b) => formatLabel(b.time, range));
  const barDataset = (label: string, key: string, color: string) => ({
    label,
    data: pluck(buckets, key),
    backgroundColor: color,
    borderRadius: 1,
    borderSkipped: false as const,
    borderWidth: { top: 0, right: 0, bottom: 1, left: 0 },
  });

  new Chart(node, {
    type: "bar",
    data: {
      labels,
      datasets: [
        barDataset("1/2/3xx", "ok", "#71717a"),
        barDataset("4xx", "client_err", "#f59e0b"),
        barDataset("5xx", "server_err", "#f87171"),
      ],
    },
    options: {
      maintainAspectRatio: false,
      plugins: {
        legend: { display: false },
        tooltip: {
          backgroundColor: "#1c1c22",
          bodyColor: "#e4e4e7",
          borderColor: "#2a2a32",
          borderWidth: 1,
          cornerRadius: 6,
          padding: 10,
          titleColor: "#e4e4e7",
        },
      },
      responsive: true,
      scales: {
        x: { display: false, stacked: true },
        y: { display: false, stacked: true, beginAtZero: true, grace: "5%" },
      },
    },
  } as ChartConfiguration);
}

// Custom plugin for doughnut center labels
const doughnutLabelsPlugin = {
  id: "doughnutLabels",
  afterDraw(chart: Chart): void {
    if (chart.config.type !== "doughnut") return;

    const { ctx, chartArea } = chart;
    const cx = (chartArea.left + chartArea.right) / 2;
    const cy = (chartArea.top + chartArea.bottom) / 2;
    const meta = chart.getDatasetMeta(0);
    const total = (chart.data.datasets[0].data as number[]).reduce(
      (a, b) => a + b,
      0
    );

    ctx.save();
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    ctx.font = `600 1.5rem ${Chart.defaults.font.family}`;
    ctx.fillStyle = chart.options.plugins?.tooltip?.titleColor as string || "#e4e4e7";
    ctx.fillText(fmtnum(total), cx, cy);
    ctx.restore();

    meta.data.forEach((arc, i) => {
      if (!chart.getDataVisibility(i)) return;
      const props = arc.getProps(["startAngle", "endAngle", "outerRadius"]);
      const span = props.endAngle - props.startAngle;
      if (span < 0.25) return;
      const mid = (props.startAngle + props.endAngle) / 2;
      const r = props.outerRadius + 14;
      const x = cx + Math.cos(mid) * r;
      const y = cy + Math.sin(mid) * r;
      const right = mid > -Math.PI / 2 && mid < Math.PI / 2;

      ctx.save();
      ctx.font = `600 0.6875rem ${Chart.defaults.font.family}`;
      ctx.fillStyle = chart.options.plugins?.tooltip?.titleColor as string || "#e4e4e7";
      ctx.textAlign = right ? "left" : "right";
      ctx.textBaseline = "middle";
      ctx.fillText(chart.data.labels![i] as string, x, y);
      ctx.restore();
    });
  },
};

Chart.register(doughnutLabelsPlugin);

function doughnut(
  id: string,
  labels: string[],
  data: number[],
  mainText: string,
): void {
  const node = document.getElementById(id) as HTMLCanvasElement | null;
  if (!node) return;

  new Chart(node, {
    type: "doughnut",
    data: {
      labels,
      datasets: [{
        backgroundColor: palette.slice(0, labels.length),
        borderWidth: 0,
        borderRadius: 4,
        spacing: 2,
        data,
        hoverOffset: 6,
      }],
    },
    options: {
      responsive: true,
      cutout: "62%",
      layout: { padding: 32 },
      plugins: {
        legend: { display: false },
        tooltip: { enabled: false, titleColor: mainText },
      },
    },
  } as ChartConfiguration);
}

function formatLabel(iso: string, range: string): string {
  const date = new Date(iso);
  if (range === "24h") {
    return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
  }
  return date.toLocaleDateString([], { month: "short", day: "numeric" });
}

function fmtnum(n: number): string {
  if (n >= 1_000_000) {
    const v = n / 1_000_000;
    return (v === Math.floor(v) ? v.toFixed(0) : v.toFixed(1)) + "M";
  }
  if (n >= 1_000) {
    const v = n / 1_000;
    return (v === Math.floor(v) ? v.toFixed(0) : v.toFixed(1)) + "k";
  }
  return String(n);
}

function pluck<T, K extends keyof T>(items: T[], key: K): T[K][] {
  return items.map((item) => item[key]);
}

document.addEventListener("DOMContentLoaded", main);
```

**Step 5: Verify the build**

Run:
```bash
npx vite build
```

Expected: Build succeeds. `internal/admin/assets/dist/.vite/manifest.json` contains entries for all 5 pages + main.css.

**Step 6: Commit**

```bash
git add web/admin/src/pages/
git commit -m "feat: add all page TypeScript entry points"
```

---

## Task 6: Migrate deployments page (simplest — no JS)

This is the first template migration. It validates the full pipeline end-to-end.

**Files:**
- Modify: `internal/admin/templates/deployments.html`

**Step 1: Replace CSS classes with Tailwind utilities**

Rewrite `deployments.html` using Tailwind utility classes. The template structure and Go template directives stay identical — only the HTML classes change, and the `{{define "script"}}` block loads the built JS entry.

Replace the content of `deployments.html`. Key mappings from old CSS → Tailwind:

- `.page-content` → `flex flex-col gap-8`
- `.page-header` → `flex items-center justify-between`
- `.page-title` → `text-2xl font-semibold tracking-tight`
- `.mono` → `font-mono text-sm`
- `.muted` → `text-base-500`
- `.num` → `font-mono tabular-nums`
- `.r` → `text-right`
- `.badge` → `inline-block text-xs font-semibold uppercase tracking-wide px-2 py-0.5 rounded-full bg-blue-500/10 text-blue-500`
- `.btn.btn-secondary` → `inline-block px-3 py-1.5 text-sm font-medium border border-base-700 rounded-md bg-base-900 text-base-200 hover:border-blue-500`
- `.empty` → `text-center py-12 px-8 text-base-500 text-sm bg-base-900 border border-base-800 rounded-md`
- Table: use standard Tailwind table utilities

Also add `{{define "script"}}` block:
```html
{{define "script"}}
<script type="module" src="{{asset "pages/deployments.ts"}}"></script>
{{end}}
```

**Step 2: Build and verify**

Run:
```bash
make build
```

Expected: Build succeeds. The deployments page renders with Tailwind styles.

**Step 3: Commit**

```bash
git add internal/admin/templates/deployments.html
git commit -m "feat: migrate deployments page to Tailwind"
```

---

## Task 7: Migrate sites page

**Files:**
- Modify: `internal/admin/templates/sites.html`

**Step 1: Convert template to Tailwind utilities**

Same pattern as Task 6. Additionally:
- Remove inline `onclick="openNewSiteModal()"` handlers — replace with `data-action="new-site"` attributes
- Move the modal HTML out of `{{define "script"}}` into `{{define "content"}}` (modals are content, not script)
- The `{{define "script"}}` block becomes just the module import
- Remove all inline `<script>` blocks
- Replace inline `style=` attributes in the form with Tailwind classes

**Step 2: Build and verify**

Run:
```bash
make build
```

**Step 3: Commit**

```bash
git add internal/admin/templates/sites.html
git commit -m "feat: migrate sites page to Tailwind + TS"
```

---

## Task 8: Migrate deployment (single) page

**Files:**
- Modify: `internal/admin/templates/deployment.html`

**Step 1: Convert template to Tailwind utilities**

- Replace CSS classes with Tailwind
- Remove inline `onclick` handlers — use `data-action="activate"`, `data-action="delete"`
- Add `data-site` and `data-deployment-id` attributes to `<main>` for the TS entry to read
- Replace inline `style=` attributes with Tailwind classes
- Remove all `<script>` blocks from `{{define "script"}}`
- `{{define "script"}}` becomes just the module import:
  ```html
  <script type="module" src="{{asset "pages/deployment.ts"}}"></script>
  ```

**Step 2: Build and verify**

Run:
```bash
make build
```

**Step 3: Commit**

```bash
git add internal/admin/templates/deployment.html
git commit -m "feat: migrate deployment page to Tailwind + TS"
```

---

## Task 9: Migrate site (detail) page

This is the most complex migration due to the deploy modal and multiple action types.

**Files:**
- Modify: `internal/admin/templates/site.html`

**Step 1: Convert template to Tailwind utilities**

- Replace all CSS classes with Tailwind
- Remove inline `onclick` handlers — use `data-action` attributes:
  - `data-action="deploy"` on the Deploy button
  - `data-action="delete-site"` on the Delete site button
  - `data-action="activate"` + `data-deployment-id="{{.ID}}"` on each Activate button
  - `data-action="cleanup"` on the Clean old deployments button
  - `data-action="copy-cmd"` on the copy button
- Add `data-site="{{.Site.Name}}"` and `data-host="{{.Host}}"` to `<main>`
- Move modal HTML from `{{define "script"}}` into `{{define "content"}}`
- Move deploy overlay HTML from `{{define "script"}}` into `{{define "content"}}`
- Remove all `<script>` tags (including `fflate.min.js`, `deploy-drop.js`)
- `{{define "script"}}` becomes just:
  ```html
  <script type="module" src="{{asset "pages/site.ts"}}"></script>
  ```

**Step 2: Build and verify**

Run:
```bash
make build
```

**Step 3: Commit**

```bash
git add internal/admin/templates/site.html
git commit -m "feat: migrate site page to Tailwind + TS"
```

---

## Task 10: Migrate analytics page

**Files:**
- Modify: `internal/admin/templates/analytics.html`

**Step 1: Convert template to Tailwind utilities**

- Replace CSS classes with Tailwind
- Replace `style="grid-column:span 3"` etc. with Tailwind grid utilities (`col-span-3`)
- Remove `<script src="/assets/chart.min.js">` and `<script src="/assets/analytics.js">`
- `{{define "script"}}` becomes:
  ```html
  <script type="module" src="{{asset "pages/analytics.ts"}}"></script>
  ```

For the analytics TS to read CSS custom properties, we need to ensure our `@theme` block defines them. If the analytics page needs `--text-muted`, `--border`, `--accent`, `--text`, `--sans` — add these to `main.css`'s `@theme` block as part of this task.

**Step 2: Build and verify**

Run:
```bash
make build
```

**Step 3: Commit**

```bash
git add internal/admin/templates/analytics.html web/admin/src/main.css
git commit -m "feat: migrate analytics page to Tailwind + TS"
```

---

## Task 11: Migrate layout and delete old assets

**Files:**
- Modify: `internal/admin/templates/layout.html`
- Delete: `internal/admin/assets/style.css`
- Delete: `internal/admin/assets/deploy-drop.js`
- Delete: `internal/admin/assets/analytics.js`
- Delete: `internal/admin/assets/chart.min.js`
- Delete: `internal/admin/assets/fflate.min.js`

**Step 1: Update layout.html with Tailwind classes**

Replace the CSS class-based layout (topbar, nav-tabs, etc.) with Tailwind utilities. The `<link>` tag was already updated in Task 3 to use `{{asset "main.css"}}`.

**Step 2: Delete old asset files**

```bash
rm internal/admin/assets/style.css
rm internal/admin/assets/deploy-drop.js
rm internal/admin/assets/analytics.js
rm internal/admin/assets/chart.min.js
rm internal/admin/assets/fflate.min.js
```

**Step 3: Remove old embed directive**

In `render.go`, the embed was already changed in Task 3 to point at `assets/dist`. If there's still a reference to the old `assets/*` glob, remove it. The `assets/` directory (minus `dist/`) should now be empty and can be deleted if desired.

**Step 4: Build and verify**

Run:
```bash
make clean && make build
```

Expected: Full clean build succeeds. No references to old asset files remain.

**Step 5: Run Go tests**

Run:
```bash
go test ./...
```

Expected: All tests pass. Handler tests should still work — they test route logic, not CSS.

**Step 6: Commit**

```bash
git add -A
git commit -m "feat: remove old CSS/JS assets, complete Tailwind migration"
```

---

## Task 12: Final verification and cleanup

**Step 1: Verify no stale references**

Search for any remaining references to old asset paths:

```bash
grep -r "style.css\|deploy-drop.js\|analytics.js\|chart.min.js\|fflate.min.js" internal/admin/
```

Expected: No matches.

**Step 2: Verify all pages load correct assets**

Search templates to confirm every page has a script module tag:

```bash
grep -l 'asset "pages/' internal/admin/templates/*.html
```

Expected: All 5 page templates (sites, site, deployment, deployments, analytics).

**Step 3: Run full test suite**

```bash
go test ./...
```

Expected: All pass.

**Step 4: Clean build from scratch**

```bash
make clean && make build
```

Expected: Success.

**Step 5: Commit any remaining cleanup**

```bash
git add -A
git commit -m "chore: final cleanup after frontend migration"
```
