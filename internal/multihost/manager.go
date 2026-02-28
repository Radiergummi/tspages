package multihost

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/httplog"
	"tspages/internal/metrics"
	"tspages/internal/serve"
	"tspages/internal/storage"
	"tspages/internal/tsadapter"

	"tailscale.com/tsnet"
)

type siteServer struct {
	ts       *tsnet.Server
	httpSrv  *http.Server
	closer   func() error // if set, used instead of default close logic
	isPublic bool
}

func (ss *siteServer) Close() error {
	if ss.closer != nil {
		return ss.closer()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ss.httpSrv.Shutdown(ctx); err != nil {
		log.Printf("warning: graceful shutdown: %v", err)
	}
	return ss.ts.Close()
}

// siteStarter creates and starts a site server. The default implementation
// creates a real tsnet.Server; tests can replace this to avoid network calls.
type siteStarter func(site string) (*siteServer, error)

// ManagerConfig holds configuration for creating a new Manager.
type ManagerConfig struct {
	Store      *storage.Store
	StateDir   string
	AuthKey    string
	Capability string
	MaxSites   int
	Recorder   *analytics.Recorder
	DNSSuffix  string
	Defaults   storage.SiteConfig
}

// Manager tracks per-site tsnet servers.
type Manager struct {
	store      *storage.Store
	stateDir   string
	authKey    string
	capability string
	maxSites   int
	recorder   *analytics.Recorder
	dnsSuffix  string
	defaults   storage.SiteConfig
	startSite  siteStarter

	mu      sync.Mutex
	servers map[string]*siteServer
}

func New(cfg ManagerConfig) *Manager {
	m := &Manager{
		store:      cfg.Store,
		stateDir:   cfg.StateDir,
		authKey:    cfg.AuthKey,
		capability: cfg.Capability,
		maxSites:   cfg.MaxSites,
		recorder:   cfg.Recorder,
		dnsSuffix:  cfg.DNSSuffix,
		defaults:   cfg.Defaults,
		servers:    make(map[string]*siteServer),
	}
	m.startSite = m.defaultStartSite
	return m
}

// EnsureServer starts a tsnet server for the given site if one isn't already running.
// If the site's public status has changed since it was started, the old server
// is stopped and a new one is started with the correct listener type.
func (m *Manager) EnsureServer(site string) error {
	m.mu.Lock()
	if existing, ok := m.servers[site]; ok {
		cfg, _ := m.store.ReadCurrentSiteConfig(site)
		merged := cfg.Merge(m.defaults)
		wantPublic := merged.Public != nil && *merged.Public
		if existing.isPublic == wantPublic {
			m.mu.Unlock()
			return nil
		}
		// Public status changed — close old server, fall through to start new one.
		delete(m.servers, site)
		m.mu.Unlock()
		log.Printf("restarting site %q: public changed %v → %v", site, existing.isPublic, wantPublic)
		existing.Close()
	} else {
		if len(m.servers) >= m.maxSites {
			m.mu.Unlock()
			return fmt.Errorf("maximum site limit (%d) reached", m.maxSites)
		}
		m.mu.Unlock()
	}

	ss, err := m.startSite(site)
	if err != nil {
		return err
	}

	// Re-acquire lock and re-check — another goroutine may have started
	// this site, or the limit may have been reached while we were starting.
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.servers[site]; ok {
		if err := ss.Close(); err != nil {
			log.Printf("warning: closing duplicate site %q: %v", site, err)
		}
		return nil
	}
	if len(m.servers) >= m.maxSites {
		if err := ss.Close(); err != nil {
			log.Printf("warning: closing site %q (limit reached): %v", site, err)
		}
		return fmt.Errorf("maximum site limit (%d) reached", m.maxSites)
	}
	m.servers[site] = ss
	metrics.SetActiveSites(len(m.servers))
	return nil
}

func (m *Manager) defaultStartSite(site string) (*siteServer, error) {
	cfg, _ := m.store.ReadCurrentSiteConfig(site)
	merged := cfg.Merge(m.defaults)
	public := merged.Public != nil && *merged.Public

	srv := &tsnet.Server{
		Hostname: site,
		Dir:      filepath.Join(m.stateDir, "sites", site),
		AuthKey:  m.authKey,
	}

	lc, err := srv.LocalClient()
	if err != nil {
		srv.Close()
		return nil, fmt.Errorf("local client for site %q: %w", site, err)
	}

	whoIsClient := tsadapter.New(lc)
	var withAuth func(http.Handler) http.Handler
	if public {
		withAuth = auth.MiddlewareAllowAnonymous(whoIsClient, m.capability)
	} else {
		withAuth = auth.Middleware(whoIsClient, m.capability)
	}

	handler := serve.NewHandler(m.store, site, m.dnsSuffix, m.defaults)
	handler.SetPublic(public)
	logged := httplog.Wrap(handler, slog.String("site", site))
	recorded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: 200}
		start := time.Now()
		logged.ServeHTTP(sw, r)
		metrics.ObserveRequest(site, sw.status, time.Since(start))
		if m.recorder != nil && handler.AnalyticsEnabled() {
			ri := auth.RequestInfoFromContext(r.Context())
			m.recorder.Record(analytics.Event{
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
	mux := http.NewServeMux()
	mux.Handle("GET /{path...}", withAuth(recorded))

	var ln net.Listener
	if public {
		ln, err = srv.ListenFunnel("tcp", ":443")
	} else {
		ln, err = srv.ListenTLS("tcp", ":443")
	}
	if err != nil {
		srv.Close()
		return nil, fmt.Errorf("listen for site %q: %w", site, err)
	}

	httpSrv := &http.Server{Handler: mux}
	go func() {
		if public {
			log.Printf("site %q listening on https://%s (public via Funnel)", site, site)
		} else {
			log.Printf("site %q listening on https://%s", site, site)
		}
		if err := httpSrv.Serve(ln); err != http.ErrServerClosed {
			log.Printf("site %q serve error: %v", site, err)
		}
	}()

	return &siteServer{ts: srv, httpSrv: httpSrv, isPublic: public}, nil
}

// StopServer shuts down and removes the tsnet server for the given site.
func (m *Manager) StopServer(site string) error {
	m.mu.Lock()
	ss, ok := m.servers[site]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.servers, site)
	metrics.SetActiveSites(len(m.servers))
	m.mu.Unlock()

	log.Printf("stopping site %q", site)
	return ss.Close()
}

// StartExistingSites starts servers for all created sites.
// Sites without an active deployment will serve a placeholder page.
func (m *Manager) StartExistingSites() error {
	sites, err := m.store.ListSites()
	if err != nil {
		return fmt.Errorf("listing sites: %w", err)
	}
	for _, s := range sites {
		if err := m.EnsureServer(s.Name); err != nil {
			log.Printf("warning: failed to start site %q: %v", s.Name, err)
		}
	}
	return nil
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

// IsRunning reports whether a tsnet server is running for the given site.
func (m *Manager) IsRunning(site string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.servers[site]
	return ok
}

// RunningCount returns the number of currently running site servers.
func (m *Manager) RunningCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.servers)
}

// Close shuts down all site servers.
func (m *Manager) Close() {
	m.mu.Lock()
	snapshot := make(map[string]*siteServer, len(m.servers))
	for name, ss := range m.servers {
		snapshot[name] = ss
	}
	m.servers = make(map[string]*siteServer)
	m.mu.Unlock()

	for name, ss := range snapshot {
		log.Printf("stopping site %q", name)
		if err := ss.Close(); err != nil {
			log.Printf("warning: closing site %q: %v", name, err)
		}
	}
}
