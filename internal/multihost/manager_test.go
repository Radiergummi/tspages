package multihost

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/db"
	"tspages/internal/httplog"
	"tspages/internal/notify"
	"tspages/internal/serve"
	"tspages/internal/storage"
	"tspages/internal/submissions"
)

// newTestManager creates a Manager with a fake startSite that records calls
// and creates minimal siteServer stubs (no real tsnet or network).
func newTestManager(t *testing.T, maxSites int) (*Manager, *startLog) {
	t.Helper()
	store := storage.New(t.TempDir())
	m := New(ManagerConfig{
		Store:      store,
		StateDir:   t.TempDir(),
		Capability: "test/cap",
		MaxSites:   maxSites,
	})
	sl := &startLog{}
	m.startSite = sl.starter()
	return m, sl
}

type startLog struct {
	mu    sync.Mutex
	sites []string
}

func (sl *startLog) starter() siteStarter {
	return func(site string) (*siteServer, error) {
		sl.mu.Lock()
		sl.sites = append(sl.sites, site)
		sl.mu.Unlock()
		return &siteServer{closer: func() error { return nil }}, nil
	}
}

func (sl *startLog) count() int {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return len(sl.sites)
}

func TestEnsureServer_Basic(t *testing.T) {
	m, sl := newTestManager(t, 10)

	if err := m.EnsureServer("docs"); err != nil {
		t.Fatalf("EnsureServer: %v", err)
	}
	if sl.count() != 1 {
		t.Fatalf("startSite called %d times, want 1", sl.count())
	}
}

func TestEnsureServer_Idempotent(t *testing.T) {
	m, sl := newTestManager(t, 10)

	if err := m.EnsureServer("docs"); err != nil {
		t.Fatal(err)
	}
	if err := m.EnsureServer("docs"); err != nil {
		t.Fatal(err)
	}
	if sl.count() != 1 {
		t.Errorf("startSite called %d times, want 1 (idempotent)", sl.count())
	}
}

func TestEnsureServer_MaxSites(t *testing.T) {
	m, _ := newTestManager(t, 2)

	m.EnsureServer("a")
	m.EnsureServer("b")
	err := m.EnsureServer("c")
	if err == nil {
		t.Fatal("expected error for exceeding max sites")
	}
}

func TestEnsureServer_DifferentSites(t *testing.T) {
	m, sl := newTestManager(t, 10)

	m.EnsureServer("docs")
	m.EnsureServer("demo")
	if sl.count() != 2 {
		t.Errorf("startSite called %d times, want 2", sl.count())
	}
}

func TestStopServer_Nonexistent(t *testing.T) {
	m, _ := newTestManager(t, 10)

	// Stopping a site that was never started should be a no-op.
	if err := m.StopServer("nonexistent"); err != nil {
		t.Fatalf("StopServer: %v", err)
	}
}

func TestStopServer_RemovesFromMap(t *testing.T) {
	m, sl := newTestManager(t, 10)

	m.EnsureServer("docs")
	m.StopServer("docs")

	// After stopping, EnsureServer should start it again.
	m.EnsureServer("docs")
	if sl.count() != 2 {
		t.Errorf("startSite called %d times, want 2 (re-created after stop)", sl.count())
	}
}

func TestStopServer_FreesSlot(t *testing.T) {
	m, _ := newTestManager(t, 2)

	m.EnsureServer("a")
	m.EnsureServer("b")

	// At max capacity. Stop one to free a slot.
	m.StopServer("a")

	if err := m.EnsureServer("c"); err != nil {
		t.Fatalf("expected slot freed after stop, got: %v", err)
	}
}

func TestStartExistingSites(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)
	m := New(ManagerConfig{
		Store:      store,
		StateDir:   dir,
		Capability: "test/cap",
		MaxSites:   10,
	})
	sl := &startLog{}
	m.startSite = sl.starter()

	// Create two sites, only activate one.
	store.CreateSite("active")
	depDir, _ := store.CreateDeployment("active", "d1")
	writeFile(t, depDir, "index.html", "hi")
	store.MarkComplete("active", "d1")
	store.ActivateDeployment("active", "d1")

	store.CreateSite("inactive")

	if err := m.StartExistingSites(); err != nil {
		t.Fatalf("StartExistingSites: %v", err)
	}

	// Both sites should be started (inactive serves placeholder).
	if sl.count() != 2 {
		t.Errorf("startSite called %d times, want 2", sl.count())
	}
}

func TestEnsureServer_ConcurrentSameSite(t *testing.T) {
	m, _ := newTestManager(t, 10)

	var started atomic.Int32
	m.startSite = func(site string) (*siteServer, error) {
		started.Add(1)
		return &siteServer{closer: func() error { return nil }}, nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.EnsureServer("docs")
		}()
	}
	wg.Wait()

	// Due to double-check locking, startSite may be called more than once
	// (by goroutines that passed the first check before any completed),
	// but the server map should only contain one entry.
	m.mu.Lock()
	count := len(m.servers)
	m.mu.Unlock()
	if count != 1 {
		t.Errorf("servers map has %d entries, want 1", count)
	}
}

func TestEnsureServer_StartError(t *testing.T) {
	m, _ := newTestManager(t, 10)
	m.startSite = func(site string) (*siteServer, error) {
		return nil, fmt.Errorf("boom")
	}

	err := m.EnsureServer("docs")
	if err == nil {
		t.Fatal("expected error")
	}

	// After a failed start, the site should not be in the map.
	m.mu.Lock()
	_, ok := m.servers["docs"]
	m.mu.Unlock()
	if ok {
		t.Error("failed site should not be in servers map")
	}
}

func TestClose(t *testing.T) {
	m, _ := newTestManager(t, 10)

	m.EnsureServer("a")
	m.EnsureServer("b")
	m.Close()

	// After close, the map is cleared.
	if m.RunningCount() != 0 {
		t.Errorf("expected empty map after Close, got %d", m.RunningCount())
	}
}

func TestClose_WithCloserError(t *testing.T) {
	store := storage.New(t.TempDir())
	m := New(ManagerConfig{
		Store:      store,
		StateDir:   t.TempDir(),
		Capability: "test/cap",
		MaxSites:   10,
	})
	m.startSite = func(site string) (*siteServer, error) {
		return &siteServer{closer: func() error {
			return fmt.Errorf("close error for %s", site)
		}}, nil
	}

	m.EnsureServer("a")
	m.EnsureServer("b")

	// Close should not panic and should clear the map even when closers fail.
	m.Close()

	if m.RunningCount() != 0 {
		t.Errorf("expected empty map after Close, got %d", m.RunningCount())
	}
}

func TestStopServer_WithCloserError(t *testing.T) {
	store := storage.New(t.TempDir())
	m := New(ManagerConfig{
		Store:      store,
		StateDir:   t.TempDir(),
		Capability: "test/cap",
		MaxSites:   10,
	})
	m.startSite = func(site string) (*siteServer, error) {
		return &siteServer{closer: func() error {
			return fmt.Errorf("close failed")
		}}, nil
	}

	m.EnsureServer("docs")
	err := m.StopServer("docs")
	if err == nil {
		t.Error("expected error from failing closer")
	}

	// Site should still be removed from the map despite the error.
	if m.IsRunning("docs") {
		t.Error("site should be removed from map after StopServer")
	}
}

func TestStartExistingSites_ListError(t *testing.T) {
	// Place a file where the "sites" directory would be, so ReadDir fails
	// with a non-IsNotExist error.
	dir := t.TempDir()
	sitesDir := filepath.Join(dir, "sites")
	os.WriteFile(sitesDir, []byte("not a dir"), 0644)
	store := storage.New(dir)
	m := New(ManagerConfig{
		Store:      store,
		StateDir:   t.TempDir(),
		Capability: "test/cap",
		MaxSites:   10,
	})
	m.startSite = func(site string) (*siteServer, error) {
		return &siteServer{closer: func() error { return nil }}, nil
	}

	err := m.StartExistingSites()
	if err == nil {
		t.Error("expected error from ListSites failure")
	}
}

func TestEnsureServer_PublicToggle_Restart(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)
	m := New(ManagerConfig{
		Store:      store,
		StateDir:   t.TempDir(),
		Capability: "test/cap",
		MaxSites:   10,
	})

	var startCount atomic.Int32
	var closeCalls atomic.Int32
	m.startSite = func(site string) (*siteServer, error) {
		startCount.Add(1)
		// Read config to determine public status, mirroring defaultStartSite.
		cfg, _ := store.ReadCurrentDeploymentConfig(site)
		merged := cfg.Merge(m.defaults)
		public := merged.Public != nil && *merged.Public
		return &siteServer{
			isPublic: public,
			closer:   func() error { closeCalls.Add(1); return nil },
		}, nil
	}

	// Create site with public=true
	store.CreateSite("docs")
	depDir, _ := store.CreateDeployment("docs", "d1")
	writeFile(t, depDir, "index.html", "hi")
	store.MarkComplete("docs", "d1")
	store.ActivateDeployment("docs", "d1")
	boolTrue := true
	store.WriteDeploymentConfig("docs", "d1", storage.SiteConfig{Public: &boolTrue})

	if err := m.EnsureServer("docs"); err != nil {
		t.Fatal(err)
	}
	if startCount.Load() != 1 {
		t.Fatalf("startSite called %d times, want 1", startCount.Load())
	}

	// Update config to public=false
	boolFalse := false
	store.WriteDeploymentConfig("docs", "d1", storage.SiteConfig{Public: &boolFalse})

	if err := m.EnsureServer("docs"); err != nil {
		t.Fatal(err)
	}
	if startCount.Load() != 2 {
		t.Errorf("startSite called %d times, want 2 (restart)", startCount.Load())
	}
	if closeCalls.Load() != 1 {
		t.Errorf("close called %d times, want 1", closeCalls.Load())
	}
}

func TestEnsureServer_PublicUnchanged_NoRestart(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)
	m := New(ManagerConfig{
		Store:      store,
		StateDir:   t.TempDir(),
		Capability: "test/cap",
		MaxSites:   10,
	})

	var startCount atomic.Int32
	m.startSite = func(site string) (*siteServer, error) {
		startCount.Add(1)
		return &siteServer{
			isPublic: false,
			closer:   func() error { return nil },
		}, nil
	}

	store.CreateSite("docs")

	if err := m.EnsureServer("docs"); err != nil {
		t.Fatal(err)
	}
	if err := m.EnsureServer("docs"); err != nil {
		t.Fatal(err)
	}
	if startCount.Load() != 1 {
		t.Errorf("startSite called %d times, want 1 (no restart)", startCount.Load())
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func newTestSubmissionStore(t *testing.T) *submissions.Store {
	t.Helper()
	return submissions.NewStore(db.OpenTestDB(t, analytics.Migrations, submissions.Migrations))
}

type mockNotifier struct {
	calls []struct{ event, site string }
}

func (n *mockNotifier) Fire(event string, site string, cfg storage.SiteConfig, data map[string]any) {
	n.calls = append(n.calls, struct{ event, site string }{event, site})
}

// buildSiteMux constructs the same mux that defaultStartSite creates,
// but without tsnet. This lets tests verify POST/GET routing without
// a real tailscale server.
func buildSiteMux(store *storage.Store, subStore *submissions.Store, notifier notify.Notifier, site string, defaults storage.SiteConfig) http.Handler {
	handler := serve.NewHandler(store, site, "", defaults)
	logged := httplog.Wrap(handler)
	formHandler := submissions.NewHandler(subStore, notifier, site, handler.Config)
	formLogged := httplog.Wrap(formHandler)
	mux := http.NewServeMux()
	mux.Handle("GET /{path...}", logged)
	mux.Handle("POST /{path...}", formLogged)
	return mux
}

func TestFormHandler_POSTRoute_FormsEnabled(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)
	store.CreateSite("mysite")
	depDir, _ := store.CreateDeployment("mysite", "d1")
	contentDir := filepath.Join(depDir, "content")
	os.MkdirAll(contentDir, 0755)
	writeFile(t, contentDir, "index.html", "<html>hello</html>")
	store.MarkComplete("mysite", "d1")
	store.ActivateDeployment("mysite", "d1")

	boolTrue := true
	store.WriteDeploymentConfig("mysite", "d1", storage.SiteConfig{
		Forms: &storage.FormsConfig{Enabled: &boolTrue},
	})

	subStore := newTestSubmissionStore(t)

	notif := &mockNotifier{}
	mux := buildSiteMux(store, subStore, notif, "mysite", storage.SiteConfig{})

	// POST with form data should NOT return 405.
	body := "name=Alice"
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.ContextWithCaps(req.Context(), []auth.Cap{{Access: "view"}}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Forms are enabled so we expect 303 (redirect after submission), not 405.
	if w.Code == http.StatusMethodNotAllowed {
		t.Fatal("POST returned 405, but forms are enabled — route should be registered")
	}
	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}
}

func TestFormHandler_POSTRoute_FormsDisabled(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)
	store.CreateSite("mysite")
	depDir, _ := store.CreateDeployment("mysite", "d1")
	contentDir := filepath.Join(depDir, "content")
	os.MkdirAll(contentDir, 0755)
	writeFile(t, contentDir, "index.html", "<html>hello</html>")
	store.MarkComplete("mysite", "d1")
	store.ActivateDeployment("mysite", "d1")

	// No forms config — forms disabled by default.
	subStore := newTestSubmissionStore(t)

	notif := &mockNotifier{}
	mux := buildSiteMux(store, subStore, notif, "mysite", storage.SiteConfig{})

	body := "name=Alice"
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.ContextWithCaps(req.Context(), []auth.Cap{{Access: "view"}}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Forms are disabled so the form handler should return 405.
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d (forms disabled)", w.Code, http.StatusMethodNotAllowed)
	}
}
