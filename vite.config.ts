import tailwindcss from "@tailwindcss/vite";
import {resolve} from "node:path";
import {defineConfig} from "vite";

export default defineConfig({
    plugins: [tailwindcss()],
    server: {
        allowedHosts: true,
        strictPort: true,
    },
    build: {
        outDir: resolve(import.meta.dirname, "internal/admin/assets/dist"),
        emptyOutDir: true,
        manifest: true,
        rollupOptions: {
            input: {
                main: resolve(import.meta.dirname, "web/admin/src/main.css"),
                sites: resolve(import.meta.dirname, "web/admin/src/pages/sites.ts"),
                site: resolve(import.meta.dirname, "web/admin/src/pages/site.ts"),
                deployment: resolve(import.meta.dirname, "web/admin/src/pages/deployment.ts"),
                deployments: resolve(import.meta.dirname, "web/admin/src/pages/deployments.ts"),
                analytics: resolve(import.meta.dirname, "web/admin/src/pages/analytics.ts"),
                webhooks: resolve(import.meta.dirname, "web/admin/src/pages/webhooks.ts"),
            },
        },
    },
});
