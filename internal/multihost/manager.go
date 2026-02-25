package multihost

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/httplog"
	"tspages/internal/serve"
	"tspages/internal/storage"
	"tspages/internal/tsadapter"

	"tailscale.com/tsnet"
)

type siteServer struct {
	ts      *tsnet.Server
	httpSrv *http.Server
	closer  func() error // if set, used instead of default close logic
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

// Manager tracks per-site tsnet servers.
type Manager struct {
	store      *storage.Store
	stateDir   string
	authKey    string
	capability string
	maxSites   int
	recorder   *analytics.Recorder
	dnsSuffix  *string
	defaults   storage.SiteConfig
	startSite  siteStarter

	mu      sync.Mutex
	servers map[string]*siteServer
}

func New(store *storage.Store, stateDir, authKey, capability string, maxSites int, recorder *analytics.Recorder, dnsSuffix *string, defaults storage.SiteConfig) *Manager {
	m := &Manager{
		store:      store,
		stateDir:   stateDir,
		authKey:    authKey,
		capability: capability,
		maxSites:   maxSites,
		recorder:   recorder,
		dnsSuffix:  dnsSuffix,
		defaults:   defaults,
		servers:    make(map[string]*siteServer),
	}
	m.startSite = m.defaultStartSite
	return m
}

// EnsureServer starts a tsnet server for the given site if one isn't already running.
func (m *Manager) EnsureServer(site string) error {
	m.mu.Lock()
	if _, ok := m.servers[site]; ok {
		m.mu.Unlock()
		return nil
	}
	if len(m.servers) >= m.maxSites {
		m.mu.Unlock()
		return fmt.Errorf("maximum site limit (%d) reached", m.maxSites)
	}
	m.mu.Unlock()

	ss, err := m.startSite(site)
	if err != nil {
		return err
	}

	// Re-acquire lock and re-check — another goroutine may have started
	// this site, or the limit may have been reached while we were starting.
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.servers[site]; ok {
		ss.Close()
		return nil
	}
	if len(m.servers) >= m.maxSites {
		ss.Close()
		return fmt.Errorf("maximum site limit (%d) reached", m.maxSites)
	}
	m.servers[site] = ss
	return nil
}

func (m *Manager) defaultStartSite(site string) (*siteServer, error) {
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
	withAuth := auth.Middleware(whoIsClient, m.capability)

	handler := serve.NewHandler(m.store, site, m.dnsSuffix, m.defaults)
	logged := httplog.Wrap(handler, slog.String("site", site))
	recorded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: 200}
		logged.ServeHTTP(sw, r)
		if m.recorder != nil && handler.AnalyticsEnabled() {
			ri := auth.RequestInfoFromContext(r.Context())
			m.recorder.Record(analytics.Event{
				Timestamp:     time.Now(),
				Site:          site,
				Path:          r.URL.Path,
				Status:        sw.status,
				UserLogin:     ri.UserLogin,
				UserName:      ri.UserName,
				ProfilePicURL: ri.ProfilePicURL,
				NodeName:      ri.NodeName,
				NodeIP:    ri.NodeIP,
				OS:        ri.OS,
				OSVersion: ri.OSVersion,
				Device:    ri.Device,
				Tags:      ri.Tags,
			})
		}
	})
	mux := http.NewServeMux()
	mux.Handle("GET /{path...}", withAuth(recorded))

	ln, err := srv.ListenTLS("tcp", ":443")
	if err != nil {
		srv.Close()
		return nil, fmt.Errorf("listen for site %q: %w", site, err)
	}

	httpSrv := &http.Server{Handler: mux}
	go func() {
		log.Printf("site %q listening on https://%s", site, site)
		if err := httpSrv.Serve(ln); err != http.ErrServerClosed {
			log.Printf("site %q serve error: %v", site, err)
		}
	}()

	return &siteServer{ts: srv, httpSrv: httpSrv}, nil
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

// Close shuts down all site servers.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, ss := range m.servers {
		log.Printf("stopping site %q", name)
		ss.Close()
	}
	m.servers = nil
}
