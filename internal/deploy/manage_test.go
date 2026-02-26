package deploy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

func TestDeleteHandler_Success(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateDeployment("docs", "aaa11111")
	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")

	mgr := newMockManager()
	h := NewDeleteHandler(store, mgr, nil, storage.SiteConfig{})

	req := httptest.NewRequest("DELETE", "/deploy/docs", nil)
	req = withCaps(req, []auth.Cap{{Access: "admin"}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
	}
	if mgr.stopped["docs"] != 1 {
		t.Errorf("StopServer called %d times, want 1", mgr.stopped["docs"])
	}
	// Site should be gone from storage
	sites, _ := store.ListSites()
	for _, s := range sites {
		if s.Name == "docs" {
			t.Error("site still exists after deletion")
		}
	}
}

func TestDeleteHandler_Forbidden(t *testing.T) {
	store := storage.New(t.TempDir())
	mgr := newMockManager()
	h := NewDeleteHandler(store, mgr, nil, storage.SiteConfig{})

	// Deploy permission is not enough — site deletion requires admin
	req := httptest.NewRequest("DELETE", "/deploy/docs", nil)
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestDeleteHandler_InvalidSite(t *testing.T) {
	h := NewDeleteHandler(storage.New(t.TempDir()), newMockManager(), nil, storage.SiteConfig{})

	req := httptest.NewRequest("DELETE", "/deploy/..", nil)
	req = withCaps(req, []auth.Cap{{Access: "admin"}})
	req.SetPathValue("site", "..")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestListDeploymentsHandler_Success(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateDeployment("docs", "aaa11111")
	store.MarkComplete("docs", "aaa11111")
	store.CreateDeployment("docs", "bbb22222")
	store.MarkComplete("docs", "bbb22222")
	store.ActivateDeployment("docs", "bbb22222")

	h := NewListDeploymentsHandler(store)

	req := httptest.NewRequest("GET", "/deploy/docs", nil)
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var deps []storage.DeploymentInfo
	json.NewDecoder(rec.Body).Decode(&deps)
	if len(deps) != 2 {
		t.Fatalf("got %d deployments, want 2", len(deps))
	}
}

func TestListDeploymentsHandler_Empty(t *testing.T) {
	store := storage.New(t.TempDir())
	h := NewListDeploymentsHandler(store)

	req := httptest.NewRequest("GET", "/deploy/docs", nil)
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var deps []storage.DeploymentInfo
	json.NewDecoder(rec.Body).Decode(&deps)
	if len(deps) != 0 {
		t.Errorf("got %d deployments, want 0", len(deps))
	}
}

func TestListDeploymentsHandler_Forbidden(t *testing.T) {
	h := NewListDeploymentsHandler(storage.New(t.TempDir()))

	req := httptest.NewRequest("GET", "/deploy/docs", nil)
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"other"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestDeleteDeploymentHandler_Success(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateDeployment("docs", "aaa11111")
	store.MarkComplete("docs", "aaa11111")
	store.CreateDeployment("docs", "bbb22222")
	store.MarkComplete("docs", "bbb22222")
	store.ActivateDeployment("docs", "bbb22222")

	h := NewDeleteDeploymentHandler(store)

	req := httptest.NewRequest("DELETE", "/deploy/docs/aaa11111", nil)
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")
	req.SetPathValue("id", "aaa11111")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
	}
	deps, _ := store.ListDeployments("docs")
	if len(deps) != 1 {
		t.Errorf("got %d deployments, want 1", len(deps))
	}
}

func TestDeleteDeploymentHandler_Active(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateDeployment("docs", "aaa11111")
	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")

	h := NewDeleteDeploymentHandler(store)

	req := httptest.NewRequest("DELETE", "/deploy/docs/aaa11111", nil)
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")
	req.SetPathValue("id", "aaa11111")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteDeploymentHandler_NotFound(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateDeployment("docs", "aaa11111")
	store.MarkComplete("docs", "aaa11111")

	h := NewDeleteDeploymentHandler(store)

	req := httptest.NewRequest("DELETE", "/deploy/docs/nonexistent", nil)
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")
	req.SetPathValue("id", "nonexistent")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestDeleteDeploymentHandler_Forbidden(t *testing.T) {
	h := NewDeleteDeploymentHandler(storage.New(t.TempDir()))

	req := httptest.NewRequest("DELETE", "/deploy/docs/abc", nil)
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"other"}}})
	req.SetPathValue("site", "docs")
	req.SetPathValue("id", "abc")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestActivateHandler_Success(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateDeployment("docs", "aaa11111")
	store.MarkComplete("docs", "aaa11111")
	store.ActivateDeployment("docs", "aaa11111")
	store.CreateDeployment("docs", "bbb22222")
	store.MarkComplete("docs", "bbb22222")

	mgr := newMockManager()
	h := NewActivateHandler(store, mgr)

	req := httptest.NewRequest("POST", "/deploy/docs/bbb22222/activate", nil)
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")
	req.SetPathValue("id", "bbb22222")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var info storage.DeploymentInfo
	json.NewDecoder(rec.Body).Decode(&info)
	if info.ID != "bbb22222" || !info.Active {
		t.Errorf("got %+v, want bbb22222 active", info)
	}

	cur, _ := store.CurrentDeployment("docs")
	if cur != "bbb22222" {
		t.Errorf("current = %q, want %q", cur, "bbb22222")
	}
	if mgr.ensured["docs"] != 1 {
		t.Errorf("EnsureServer called %d times, want 1", mgr.ensured["docs"])
	}
}

func TestActivateHandler_NotFound(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateDeployment("docs", "aaa11111")
	store.MarkComplete("docs", "aaa11111")

	h := NewActivateHandler(store, newMockManager())

	req := httptest.NewRequest("POST", "/deploy/docs/nonexistent/activate", nil)
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")
	req.SetPathValue("id", "nonexistent")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestCleanupDeploymentsHandler_Success(t *testing.T) {
	store := storage.New(t.TempDir())
	store.CreateDeployment("docs", "aaa11111")
	store.MarkComplete("docs", "aaa11111")
	store.CreateDeployment("docs", "bbb22222")
	store.MarkComplete("docs", "bbb22222")
	store.CreateDeployment("docs", "ccc33333")
	store.MarkComplete("docs", "ccc33333")
	store.ActivateDeployment("docs", "bbb22222")

	h := NewCleanupDeploymentsHandler(store)

	req := httptest.NewRequest("DELETE", "/deploy/docs/deployments", nil)
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"docs"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Deleted int `json:"deleted"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Deleted != 2 {
		t.Errorf("deleted = %d, want 2", resp.Deleted)
	}

	deps, _ := store.ListDeployments("docs")
	if len(deps) != 1 {
		t.Errorf("remaining = %d, want 1", len(deps))
	}
}

func TestCleanupDeploymentsHandler_Forbidden(t *testing.T) {
	h := NewCleanupDeploymentsHandler(storage.New(t.TempDir()))

	req := httptest.NewRequest("DELETE", "/deploy/docs/deployments", nil)
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"other"}}})
	req.SetPathValue("site", "docs")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestActivateHandler_Forbidden(t *testing.T) {
	h := NewActivateHandler(storage.New(t.TempDir()), newMockManager())

	req := httptest.NewRequest("POST", "/deploy/docs/abc/activate", nil)
	req = withCaps(req, []auth.Cap{{Access: "deploy", Sites: []string{"other"}}})
	req.SetPathValue("site", "docs")
	req.SetPathValue("id", "abc")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}
