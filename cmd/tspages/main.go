package main

import (
	"context"
	"flag"
	"fmt"
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
	"tspages/internal/db"
	"tspages/internal/deploy"
	"tspages/internal/help"
	"tspages/internal/httplog"
	"tspages/internal/multihost"
	"tspages/internal/notify"
	"tspages/internal/pushnotify"
	"tspages/internal/ratelimit"
	"tspages/internal/serve"
	"tspages/internal/storage"
	"tspages/internal/submissions"
	"tspages/internal/telemetry"
	"tspages/internal/tsadapter"
	"tspages/internal/web"
	"tspages/internal/webhooks"

	"tailscale.com/tsnet"
)

var version = "dev"

func main() {
	// Subcommand dispatch — must happen before flag.Parse().
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "deploy":
			if err := cli.Deploy(os.Args[2:]); err != nil {
				slog.Error("deploy failed", "component", "cli", "err", err)
				os.Exit(1)
			}
			return
		case "init":
			if err := cli.Init(os.Args[2:]); err != nil {
				slog.Error("init failed", "component", "cli", "err", err)
				os.Exit(1)
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
		slog.Error("loading config", "component", "server", "err", err)
		os.Exit(1)
	}

	var logLevel slog.Level
	if err := logLevel.UnmarshalText([]byte(cfg.Server.LogLevel)); err != nil {
		slog.Error("invalid log level", "component", "server", "level", cfg.Server.LogLevel, "err", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	store := storage.New(cfg.Server.DataDir)
	store.CleanupOrphans()

	if err := db.MigrateFilename(cfg.Server.DataDir, "analytics.db", "tspages.db"); err != nil {
		slog.Error("migrating database filename", "component", "server", "err", err)
		os.Exit(1)
	}

	database, err := db.Open(
		filepath.Join(cfg.Server.DataDir, "tspages.db"),
		analytics.Migrations,
		submissions.Migrations,
		webhooks.Migrations,
		pushnotify.Migrations,
	)
	if err != nil {
		slog.Error("opening database", "component", "server", "err", err)
		os.Exit(1)
	}
	defer database.Close() //nolint:errcheck // best-effort cleanup on shutdown

	recorder := analytics.NewRecorder(database)
	defer recorder.Close()

	webhookNotifier := webhooks.NewNotifier(database)

	vapidPub, vapidPriv, err := pushnotify.LoadOrCreateVAPIDKeys(cfg.Server.DataDir)
	if err != nil {
		slog.Error("loading VAPID keys", "component", "server", "err", err)
		os.Exit(1)
	}
	submissionStore := submissions.NewStore(database)

	web.SetHideFooter(cfg.Server.HideFooter)
	admin.SetupWeb()

	// Control plane tsnet server — start it and listen before creating
	// handlers so we can resolve the DNS suffix first.
	srv := &tsnet.Server{
		Hostname: cfg.Tailscale.Hostname,
		Dir:      cfg.Tailscale.StateDir,
		AuthKey:  cfg.Tailscale.AuthKey,
		UserLogf: func(format string, args ...any) { slog.Debug(fmt.Sprintf(format, args...), "component", "tsnet") },
	}
	defer srv.Close() //nolint:errcheck // best-effort cleanup on shutdown

	lc, err := srv.LocalClient()
	if err != nil {
		slog.Error("getting local client", "component", "server", "err", err)
		os.Exit(1)
	}

	ln, err := srv.ListenTLS("tcp", ":443")
	if err != nil {
		slog.Error("listen failed", "component", "server", "err", err)
		os.Exit(1)
	}
	defer ln.Close()

	// Resolve DNS suffix now that the server is connected.
	var dnsSuffix string
	status, err := lc.StatusWithoutPeers(context.Background())
	if err != nil {
		slog.Error("getting tailnet status", "component", "server", "err", err)
		os.Exit(1)
	}
	if status.CurrentTailnet != nil {
		dnsSuffix = status.CurrentTailnet.MagicDNSSuffix
	}

	// Create push notifier after DNS suffix is known so the VAPID subject
	// contains the full FQDN (Apple rejects bare hostnames without a TLD).
	vapidContact := "https://" + cfg.Tailscale.Hostname
	if dnsSuffix != "" {
		vapidContact = "https://" + cfg.Tailscale.Hostname + "." + dnsSuffix
	}
	pushNotifier := pushnotify.NewNotifier(database, vapidPub, vapidPriv, vapidContact)
	notifier := notify.Multi{webhookNotifier, pushNotifier}

	var limiter *ratelimit.Limiter
	rl := cfg.RateLimits
	if rl.MutateRate > 0 || rl.SubmitRate > 0 || rl.QueryRate > 0 {
		limiter = ratelimit.New(
			ratelimit.TierConfig{Rate: rl.MutateRate, Burst: rl.MutateBurst},
			ratelimit.TierConfig{Rate: rl.SubmitRate, Burst: rl.SubmitBurst},
			ratelimit.TierConfig{Rate: rl.QueryRate, Burst: rl.QueryBurst},
		)
	}

	buildStack := func(site string, handler *serve.Handler, whoIs auth.WhoIsClient, isPublic bool) http.Handler {
		var withSiteAuth func(http.Handler) http.Handler
		if isPublic {
			withSiteAuth = auth.MiddlewareAllowAnonymous(whoIs, cfg.Tailscale.Capability)
		} else {
			withSiteAuth = auth.Middleware(whoIs, cfg.Tailscale.Capability)
		}

		logged := httplog.Wrap(handler, slog.String("site", site))
		recorded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &statusWriter{ResponseWriter: w, status: 200}
			start := time.Now()
			logged.ServeHTTP(sw, r)
			telemetry.ObserveRequest(site, sw.status, time.Since(start))
			if recorder != nil && handler.AnalyticsEnabled() {
				ri := auth.RequestInfoFromContext(r.Context())
				recorder.Record(analytics.Event{
					Timestamp:     start,
					Site:          site,
					Path:          r.URL.Path,
					Status:        sw.status,
					UserLogin:     ri.UserLogin,
					UserName:      ri.UserName,
					ProfilePicURL: ri.ProfilePicURL,
					NodeName:      ri.NodeName,
					NodeIP:        ri.NodeIP,
					OS:            ri.OS,
					OSVersion:     ri.OSVersion,
					Device:        ri.Device,
					Tags:          ri.Tags,
				})
			}
		})
		formHandler := submissions.NewHandler(submissionStore, notifier, site, handler.Config)
		formLogged := httplog.Wrap(formHandler, slog.String("site", site))
		submitLimited := ratelimit.Middleware(limiter, ratelimit.Submit, nil)(formLogged)

		cop := http.NewCrossOriginProtection()
		mux := http.NewServeMux()
		mux.Handle("GET /{path...}", withSiteAuth(recorded))
		mux.Handle("POST /{path...}", withSiteAuth(submitLimited))
		return cop.Handler(mux)
	}

	mgr := multihost.New(multihost.ManagerConfig{
		Store:      store,
		StateDir:   cfg.Tailscale.StateDir,
		AuthKey:    cfg.Tailscale.AuthKey,
		DNSSuffix:  dnsSuffix,
		Defaults:   cfg.Defaults,
		MaxSites:   cfg.Server.MaxSites,
		BuildStack: buildStack,
	})
	defer mgr.Close()

	whoIsClient := tsadapter.New(lc)
	withAuth := auth.Middleware(whoIsClient, cfg.Tailscale.Capability)
	denyRateLimit := func(w http.ResponseWriter, r *http.Request) {
		web.RenderError(w, r, http.StatusTooManyRequests, "Rate limit exceeded. Please wait a moment and try again.")
	}
	withMutate := compose(withAuth, ratelimit.Middleware(limiter, ratelimit.Mutate, denyRateLimit))
	withQuery := compose(withAuth, ratelimit.Middleware(limiter, ratelimit.Query, denyRateLimit))

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
	mux := http.NewServeMux()

	// Deploy API — mutate tier
	mux.Handle("POST /deploy/{site}", withMutate(deployHandler))
	mux.Handle("POST /deploy/{site}/{filename}", withMutate(deployHandler))
	mux.Handle("PUT /deploy/{site}", withMutate(deployHandler))
	mux.Handle("PUT /deploy/{site}/{filename}", withMutate(deployHandler))
	mux.Handle("GET /deploy/{site}", withAuth(listHandler))
	mux.Handle("DELETE /deploy/{site}", withMutate(deleteHandler))
	mux.Handle("DELETE /deploy/{site}/deployments", withMutate(cleanupDeploymentsHandler))
	mux.Handle("DELETE /deploy/{site}/{id}", withMutate(deleteDeploymentHandler))
	mux.Handle("POST /deploy/{site}/{id}/activate", withMutate(activateHandler))

	// POST aliases for HTML form submissions (forms can't send DELETE) — mutate tier
	mux.Handle("POST /sites/{site}/delete", withMutate(deleteHandler))
	mux.Handle("POST /sites/{site}/cleanup", withMutate(cleanupDeploymentsHandler))

	// Per-module route registration
	telemetry.RegisterRoutes(mux, withAuth, telemetry.Config{
		Store:    store,
		Recorder: recorder,
		Checker:  mgr,
	})
	admin.RegisterRoutes(mux, withAuth, withMutate, withQuery, admin.Config{
		Store:     store,
		Recorder:  recorder,
		DNSSuffix: dnsSuffix,
		Ensurer:   mgr,
		Defaults:  cfg.Defaults,
		Notifier:  webhookNotifier,
	})
	help.RegisterRoutes(mux, withAuth)
	webhooks.RegisterRoutes(mux, withAuth, withMutate, withQuery, store, cfg.Defaults, webhookNotifier)
	analytics.RegisterRoutes(mux, withAuth, withMutate, withQuery, store, recorder, cfg.Defaults)
	submissions.RegisterRoutes(mux, withAuth, withMutate, withQuery, store, submissionStore, cfg.Defaults, vapidPub)
	pushnotify.RegisterRoutes(mux, withAuth, withMutate, withQuery, pushNotifier, vapidPub)

	listenErr := make(chan error, 3)

	var devWSProxy http.Handler
	if *dev {
		tmplDir, err := filepath.Abs("internal/admin/templates")
		if err != nil {
			slog.Error("resolving template dir", "component", "server", "err", err)
			os.Exit(1)
		}
		webhooksTmplDir, err := filepath.Abs("internal/webhooks/templates")
		if err != nil {
			slog.Error("resolving webhooks template dir", "component", "server", "err", err)
			os.Exit(1)
		}
		analyticsTmplDir, err := filepath.Abs("internal/analytics/templates")
		if err != nil {
			slog.Error("resolving analytics template dir", "component", "server", "err", err)
			os.Exit(1)
		}
		submissionsTmplDir, err := filepath.Abs("internal/submissions/templates")
		if err != nil {
			slog.Error("resolving submissions template dir", "component", "server", "err", err)
			os.Exit(1)
		}
		helpTmplDir, err := filepath.Abs("internal/help/templates")
		if err != nil {
			slog.Error("resolving help template dir", "component", "server", "err", err)
			os.Exit(1)
		}
		web.EnableDevMode(map[string]string{"admin": tmplDir, "webhooks": webhooksTmplDir, "analytics": analyticsTmplDir, "submissions": submissionsTmplDir, "help": helpTmplDir})

		openapiPath, err := filepath.Abs("internal/help/openapi.yaml")
		if err != nil {
			slog.Error("resolving openapi path", "component", "server", "err", err)
			os.Exit(1)
		}
		help.SetDevOpenAPIPath(openapiPath)

		serveTmplDir, err := filepath.Abs("internal/serve/templates")
		if err != nil {
			slog.Error("resolving serve template dir", "component", "server", "err", err)
			os.Exit(1)
		}
		serve.EnableDevMode(serveTmplDir)

		// Vite asset proxy on the main mux so it works on both listeners.
		proxy := admin.DevAssetProxy()
		mux.Handle("GET /web/", proxy)
		mux.Handle("GET /@vite/", proxy)
		mux.Handle("GET /node_modules/", proxy)
		devWSProxy = admin.DevWebSocketProxy()

		// Localhost listener with mock admin auth (no tailscale needed).
		go func() {
			slog.Info("dev server started", "component", "server", "addr", "http://localhost:8080", "hint", "run 'npx vite' for HMR")
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
		web.RenderError(w, r, http.StatusNotFound, "")
	})

	// Local health check listener (plain HTTP, localhost only).
	if addr := cfg.Server.HealthAddr; addr != "" {
		healthMux := http.NewServeMux()
		healthMux.Handle("GET /healthz", telemetry.NewHealthHandler(store, recorder))
		go func() {
			slog.Info("health check listening", "component", "server", "addr", addr)
			if err := http.ListenAndServe(addr, healthMux); err != nil {
				listenErr <- fmt.Errorf("health listener: %w", err)
			}
		}()
	}

	// Start servers for all sites with active deployments
	if err := mgr.StartExistingSites(); err != nil {
		slog.Warn("starting existing sites", "component", "server", "err", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cop := http.NewCrossOriginProtection()
	httpSrv := &http.Server{Handler: httplog.Wrap(cop.Handler(mux))}
	go func() {
		if err := httpSrv.Serve(ln); err != http.ErrServerClosed {
			listenErr <- fmt.Errorf("serve: %w", err)
		}
	}()

	slog.Info("tspages control plane listening", "component", "server", "hostname", cfg.Tailscale.Hostname)
	select {
	case <-ctx.Done():
	case err := <-listenErr:
		slog.Error("listener failed", "component", "server", "err", err)
	}
	slog.Info("shutting down", "component", "server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "component", "server", "err", err)
	}
}

// compose chains two middleware functions: outer runs first, then inner.
func compose(outer, inner func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return outer(inner(h))
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
