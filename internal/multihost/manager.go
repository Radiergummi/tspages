package multihost

import (
	"context"
	"fmt"
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
	handler  *serve.Handler
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
		slog.Warn("graceful shutdown failed", "err", err)
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

	mu       sync.Mutex
	servers  map[string]*siteServer
	starting map[string]chan struct{} // closed when startup completes
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
		starting:   make(map[string]chan struct{}),
	}
	m.startSite = m.defaultStartSite
	return m
}

// EnsureServer starts a tsnet server for the given site if one isn't already running.
// If the site's public status has changed since it was started, the old server
// is stopped and a new one is started with the correct listener type.
func (m *Manager) EnsureServer(site string) error {
	m.mu.Lock()

	// If another goroutine is already starting this site, wait for it.
	if ch, ok := m.starting[site]; ok {
		m.mu.Unlock()
		<-ch
		return nil
	}

	var old *siteServer
	if existing, ok := m.servers[site]; ok {
		cfg, _ := m.store.ReadCurrentSiteConfig(site)
		merged := cfg.Merge(m.defaults)
		wantPublic := merged.Public != nil && *merged.Public
		if existing.isPublic == wantPublic {
			if existing.handler != nil {
				existing.handler.InvalidateConfig()
			}
			m.mu.Unlock()
			return nil
		}
		// Public status changed — close old server, fall through to start new one.
		old = existing
		delete(m.servers, site)
	} else if len(m.servers) >= m.maxSites {
		m.mu.Unlock()
		return fmt.Errorf("maximum site limit (%d) reached", m.maxSites)
	}

	// Mark this site as starting so concurrent callers wait.
	ch := make(chan struct{})
	m.starting[site] = ch
	m.mu.Unlock()

	// Close the old server (if restarting) outside the lock.
	// The starting guard prevents concurrent starts for the same site.
	if old != nil {
		slog.Info("restarting site", "site", site)
		old.Close() //nolint:errcheck // best-effort shutdown of old server
	}

	ss, err := m.startSite(site)

	m.mu.Lock()
	delete(m.starting, site)
	close(ch)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if len(m.servers) >= m.maxSites {
		m.mu.Unlock()
		if cerr := ss.Close(); cerr != nil {
			slog.Warn("closing site after limit reached", "site", site, "err", cerr)
		}
		return fmt.Errorf("maximum site limit (%d) reached", m.maxSites)
	}
	m.servers[site] = ss
	metrics.SetActiveSites(len(m.servers))
	m.mu.Unlock()
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
		srv.Close() //nolint:errcheck // cleanup on error path
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
		srv.Close() //nolint:errcheck // cleanup on error path
		return nil, fmt.Errorf("listen for site %q: %w", site, err)
	}

	httpSrv := &http.Server{Handler: mux}
	go func() {
		if public {
			slog.Info("site listening", "site", site, "url", "https://"+site, "public", true)
		} else {
			slog.Info("site listening", "site", site, "url", "https://"+site)
		}
		if err := httpSrv.Serve(ln); err != http.ErrServerClosed {
			slog.Error("site serve error", "site", site, "err", err)
		}
	}()

	return &siteServer{ts: srv, httpSrv: httpSrv, handler: handler, isPublic: public}, nil
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

	slog.Info("stopping site", "site", site)
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
			slog.Warn("failed to start site", "site", s.Name, "err", err)
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
		slog.Info("stopping site", "site", name)
		if err := ss.Close(); err != nil {
			slog.Warn("closing site", "site", name, "err", err)
		}
	}
}
