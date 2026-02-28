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
		case "init":
			if err := cli.Init(os.Args[2:]); err != nil {
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
	defer recorder.Close() //nolint:errcheck // best-effort cleanup on shutdown

	notifier, err := webhook.NewNotifier(recorder.DB())
	if err != nil {
		log.Fatalf("creating webhook notifier: %v", err) //nolint:gocritic // exitAfterDefer is intentional — process is dying
	}

	admin.SetHideFooter(cfg.Server.HideFooter)

	// Control plane tsnet server — start it and listen before creating
	// handlers so we can resolve the DNS suffix first.
	srv := &tsnet.Server{
		Hostname: cfg.Tailscale.Hostname,
		Dir:      cfg.Tailscale.StateDir,
		AuthKey:  cfg.Tailscale.AuthKey,
	}
	defer srv.Close() //nolint:errcheck // best-effort cleanup on shutdown

	lc, err := srv.LocalClient()
	if err != nil {
		log.Fatalf("getting local client: %v", err)
	}

	ln, err := srv.ListenTLS("tcp", ":443")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Resolve DNS suffix now that the server is connected.
	var dnsSuffix string
	status, err := lc.StatusWithoutPeers(context.Background())
	if err != nil {
		log.Fatalf("getting tailnet status: %v", err)
	}
	if status.CurrentTailnet != nil {
		dnsSuffix = status.CurrentTailnet.MagicDNSSuffix
	}

	mgr := multihost.New(multihost.ManagerConfig{
		Store:      store,
		StateDir:   cfg.Tailscale.StateDir,
		AuthKey:    cfg.Tailscale.AuthKey,
		Capability: cfg.Tailscale.Capability,
		MaxSites:   cfg.Server.MaxSites,
		Recorder:   recorder,
		DNSSuffix:  dnsSuffix,
		Defaults:   cfg.Defaults,
	})
	defer mgr.Close()

	whoIsClient := tsadapter.New(lc)
	withAuth := auth.Middleware(whoIsClient, cfg.Tailscale.Capability)

	deployHandler := deploy.NewHandler(deploy.HandlerConfig{
		Store:          store,
		Manager:        mgr,
		MaxUploadMB:    cfg.Server.MaxUploadMB,
		MaxDeployments: cfg.Server.MaxDeployments,
		DNSSuffix:      dnsSuffix,
		Notifier:       notifier,
		Defaults:       cfg.Defaults,
	})
	deleteHandler := deploy.NewDeleteHandler(store, mgr, notifier, cfg.Defaults)
	listHandler := deploy.NewListDeploymentsHandler(store)
	deleteDeploymentHandler := deploy.NewDeleteDeploymentHandler(store)
	cleanupDeploymentsHandler := deploy.NewCleanupDeploymentsHandler(store)
	activateHandler := deploy.NewActivateHandler(store, mgr)
	h := admin.NewHandlers(store, recorder, dnsSuffix, mgr, mgr, cfg.Defaults, notifier)
	healthHandler := admin.NewHealthHandler(store, recorder)

	mux := http.NewServeMux()
	registerRoutes(mux, withAuth, h, healthHandler,
		deployHandler, listHandler, deleteHandler,
		deleteDeploymentHandler, cleanupDeploymentsHandler, activateHandler)

	listenErr := make(chan error, 3)

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
			slog.Info("dev server started", "addr", "http://localhost:8080", "hint", "run 'npx vite' for HMR")
			if err := http.ListenAndServe(":8080", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := auth.ContextWithCaps(r.Context(), []auth.Cap{{Access: "admin"}})
				ctx = auth.ContextWithIdentity(ctx, auth.Identity{LoginName: "dev@localhost", DisplayName: "Developer"})
				mux.ServeHTTP(w, r.WithContext(ctx))
			})); err != nil {
				listenErr <- fmt.Errorf("dev server: %w", err)
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
			slog.Info("health check listening", "addr", addr)
			if err := http.ListenAndServe(addr, healthMux); err != nil {
				listenErr <- fmt.Errorf("health listener: %w", err)
			}
		}()
	}

	// Start servers for all sites with active deployments
	if err := mgr.StartExistingSites(); err != nil {
		slog.Warn("starting existing sites", "err", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	httpSrv := &http.Server{Handler: httplog.Wrap(mux)}
	go func() {
		if err := httpSrv.Serve(ln); err != http.ErrServerClosed {
			listenErr <- fmt.Errorf("serve: %w", err)
		}
	}()

	slog.Info("tspages control plane listening", "hostname", cfg.Tailscale.Hostname)
	select {
	case <-ctx.Done():
	case err := <-listenErr:
		slog.Error("listener failed", "err", err)
	}
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
}

func registerRoutes(
	mux *http.ServeMux,
	withAuth func(http.Handler) http.Handler,
	h *admin.Handlers,
	healthHandler http.Handler,
	deployHandler http.Handler,
	listHandler http.Handler,
	deleteHandler http.Handler,
	deleteDeploymentHandler http.Handler,
	cleanupDeploymentsHandler http.Handler,
	activateHandler http.Handler,
) {
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
	mux.Handle("GET /webhooks/{id}", withAuth(h.WebhookDetail))
	mux.Handle("POST /webhooks/{id}/retry", withAuth(h.WebhookRetry))
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
}
