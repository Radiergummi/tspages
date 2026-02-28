package multihost

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"tspages/internal/storage"
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
		cfg, _ := store.ReadCurrentSiteConfig(site)
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
	store.WriteSiteConfig("docs", "d1", storage.SiteConfig{Public: &boolTrue})

	if err := m.EnsureServer("docs"); err != nil {
		t.Fatal(err)
	}
	if startCount.Load() != 1 {
		t.Fatalf("startSite called %d times, want 1", startCount.Load())
	}

	// Update config to public=false
	boolFalse := false
	store.WriteSiteConfig("docs", "d1", storage.SiteConfig{Public: &boolFalse})

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
