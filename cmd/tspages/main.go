package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"tspages/config"
	"tspages/internal/admin"
	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/cli"
	"tspages/internal/deploy"
	"tspages/internal/httplog"
	"tspages/internal/metrics"
	"tspages/internal/multihost"
	"tspages/internal/storage"
	"tspages/internal/tsadapter"
	"tspages/internal/webhook"

	"tailscale.com/tsnet"
)

var version = "dev"

func main() {
	// Subcommand dispatch — must happen before flag.Parse().
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "deploy":
			if err := cli.Deploy(os.Args[2:]); err != nil {
				log.Fatal(err)
			}
			return
		case "version":
			fmt.Println(version)
			return
		}
	}

	configPath := flag.String("config", "tspages.toml", "path to config file")
	dev := flag.Bool("dev", false, "enable Vite dev mode with HMR on localhost:8080")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	var logLevel slog.Level
	if err := logLevel.UnmarshalText([]byte(cfg.Server.LogLevel)); err != nil {
		log.Fatalf("invalid log level %q: %v", cfg.Server.LogLevel, err)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	store := storage.New(cfg.Server.DataDir)
	store.CleanupOrphans()

	recorder, err := analytics.NewRecorder(filepath.Join(cfg.Server.DataDir, "analytics.db"))
	if err != nil {
		log.Fatalf("opening analytics db: %v", err)
	}
	defer recorder.Close()

	notifier, err := webhook.NewNotifier(recorder.DB())
	if err != nil {
		log.Fatalf("creating webhook notifier: %v", err)
	}

	var dnsSuffix string
	mgr := multihost.New(store, cfg.Tailscale.StateDir, cfg.Tailscale.AuthKey, cfg.Tailscale.Capability, cfg.Server.MaxSites, recorder, &dnsSuffix, cfg.Defaults)
	defer mgr.Close()

	// Control plane tsnet server
	srv := &tsnet.Server{
		Hostname: cfg.Tailscale.Hostname,
		Dir:      cfg.Tailscale.StateDir,
		AuthKey:  cfg.Tailscale.AuthKey,
	}
	defer srv.Close()

	lc, err := srv.LocalClient()
	if err != nil {
		log.Fatalf("getting local client: %v", err)
	}

	whoIsClient := tsadapter.New(lc)
	withAuth := auth.Middleware(whoIsClient, cfg.Tailscale.Capability)

	deployHandler := deploy.NewHandler(store, mgr, cfg.Server.MaxUploadMB, cfg.Server.MaxDeployments, &dnsSuffix, notifier, cfg.Defaults)
	deleteHandler := deploy.NewDeleteHandler(store, mgr, notifier, cfg.Defaults)
	listHandler := deploy.NewListDeploymentsHandler(store)
	deleteDeploymentHandler := deploy.NewDeleteDeploymentHandler(store)
	cleanupDeploymentsHandler := deploy.NewCleanupDeploymentsHandler(store)
	activateHandler := deploy.NewActivateHandler(store, mgr)
	h := admin.NewHandlers(store, recorder, &dnsSuffix, mgr, mgr, cfg.Defaults, notifier)
	healthHandler := admin.NewHealthHandler(store, recorder)

	mux := http.NewServeMux()
	// Health checks
	mux.Handle("GET /healthz", healthHandler)
	mux.Handle("GET /sites/{site}/healthz", withAuth(h.SiteHealth))
	// Deploy API (JSON only)
	mux.Handle("POST /deploy/{site}", withAuth(deployHandler))
	mux.Handle("POST /deploy/{site}/{filename}", withAuth(deployHandler))
	mux.Handle("PUT /deploy/{site}", withAuth(deployHandler))
	mux.Handle("PUT /deploy/{site}/{filename}", withAuth(deployHandler))
	mux.Handle("GET /deploy/{site}", withAuth(listHandler))
	mux.Handle("DELETE /deploy/{site}", withAuth(deleteHandler))
	mux.Handle("DELETE /deploy/{site}/deployments", withAuth(cleanupDeploymentsHandler))
	mux.Handle("DELETE /deploy/{site}/{id}", withAuth(deleteDeploymentHandler))
	mux.Handle("POST /deploy/{site}/{id}/activate", withAuth(activateHandler))
	// Browse routes (HTML + JSON via Accept header or .json suffix)
	mux.Handle("POST /sites", withAuth(h.CreateSite))
	mux.Handle("GET /sites", withAuth(h.Sites))
	mux.Handle("GET /sites.json", withAuth(h.Sites))
	mux.Handle("GET /sites/{site}", withAuth(h.Site))
	mux.Handle("GET /sites/{site}/deployments", withAuth(h.SiteDeployments))
	mux.Handle("GET /sites/{site}/deployments.json", withAuth(h.SiteDeployments))
	mux.Handle("GET /sites/{site}/deployments/{id}", withAuth(h.Deployment))
	mux.Handle("GET /sites/{site}/analytics", withAuth(h.Analytics))
	mux.Handle("GET /sites/{site}/analytics.json", withAuth(h.Analytics))
	mux.Handle("POST /sites/{site}/analytics/purge", withAuth(h.PurgeAnalytics))
	mux.Handle("GET /sites/{site}/webhooks", withAuth(h.SiteWebhooks))
	mux.Handle("GET /sites/{site}/webhooks.json", withAuth(h.SiteWebhooks))
	mux.Handle("GET /deployments", withAuth(h.Deployments))
	mux.Handle("GET /deployments.json", withAuth(h.Deployments))
	mux.Handle("GET /webhooks", withAuth(h.Webhooks))
	mux.Handle("GET /webhooks.json", withAuth(h.Webhooks))
	mux.Handle("GET /analytics", withAuth(h.AllAnalytics))
	mux.Handle("GET /analytics.json", withAuth(h.AllAnalytics))
	mux.Handle("GET /feed.atom", withAuth(h.Feed))
	mux.Handle("GET /sites/{site}/feed.atom", withAuth(h.SiteFeed))
	mux.Handle("GET /help", withAuth(h.Help))
	mux.Handle("GET /help/{page...}", withAuth(h.Help))
	mux.Handle("GET /assets/dist/{file...}", admin.AssetHandler())
	mux.Handle("GET /api", withAuth(h.API))
	mux.Handle("GET /openapi.yaml", admin.OpenAPIHandler())
	mux.Handle("GET /openapi", admin.SwaggerUIHandler())
	mux.Handle("GET /metrics", withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.CanScrapeMetrics(auth.CapsFromContext(r.Context())) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		metrics.Handler().ServeHTTP(w, r)
	})))

	var devWSProxy http.Handler
	if *dev {
		tmplDir, err := filepath.Abs("internal/admin/templates")
		if err != nil {
			log.Fatalf("resolving template dir: %v", err)
		}
		admin.EnableDevMode(tmplDir)

		// Vite asset proxy on the main mux so it works on both listeners.
		proxy := admin.DevAssetProxy()
		mux.Handle("GET /web/", proxy)
		mux.Handle("GET /@vite/", proxy)
		mux.Handle("GET /node_modules/", proxy)
		devWSProxy = admin.DevWebSocketProxy()

		// Localhost listener with mock admin auth (no tailscale needed).
		go func() {
			log.Printf("dev server: http://localhost:8080 (run 'npx vite' for HMR)")
			if err := http.ListenAndServe(":8080", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := auth.ContextWithCaps(r.Context(), []auth.Cap{{Access: "admin"}})
				ctx = auth.ContextWithIdentity(ctx, auth.Identity{LoginName: "dev@localhost", DisplayName: "Developer"})
				mux.ServeHTTP(w, r.WithContext(ctx))
			})); err != nil {
				log.Fatalf("dev server: %v", err)
			}
		}()
	}

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if devWSProxy != nil && r.Header.Get("Upgrade") == "websocket" {
			devWSProxy.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/sites", http.StatusFound)
			return
		}
		admin.RenderError(w, r, http.StatusNotFound, "")
	})

	// Local health check listener (plain HTTP, localhost only).
	if addr := cfg.Server.HealthAddr; addr != "" {
		healthMux := http.NewServeMux()
		healthMux.Handle("GET /healthz", healthHandler)
		go func() {
			log.Printf("health check listening on http://%s/healthz", addr)
			if err := http.ListenAndServe(addr, healthMux); err != nil {
				log.Fatalf("health listener: %v", err)
			}
		}()
	}

	ln, err := srv.ListenTLS("tcp", ":443")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	status, err := lc.StatusWithoutPeers(context.Background())
	if err != nil {
		log.Fatalf("getting tailnet status: %v", err)
	}
	if status.CurrentTailnet != nil {
		dnsSuffix = status.CurrentTailnet.MagicDNSSuffix
	}

	// Start servers for all sites with active deployments
	if err := mgr.StartExistingSites(); err != nil {
		log.Printf("warning: starting existing sites: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	httpSrv := &http.Server{Handler: httplog.Wrap(mux)}
	go func() {
		if err := httpSrv.Serve(ln); err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	log.Printf("tspages control plane listening on https://%s", cfg.Tailscale.Hostname)
	<-ctx.Done()
	log.Printf("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
