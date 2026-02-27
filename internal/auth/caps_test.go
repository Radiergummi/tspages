package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCanView(t *testing.T) {
	tests := []struct {
		name string
		caps []Cap
		site string
		want bool
	}{
		{"view grant", []Cap{{Access: "view", Sites: []string{"docs"}}}, "docs", true},
		{"view wildcard", []Cap{{Access: "view", Sites: []string{"*"}}}, "docs", true},
		{"view omitted sites", []Cap{{Access: "view"}}, "docs", true},
		{"deploy implies view", []Cap{{Access: "deploy", Sites: []string{"docs"}}}, "docs", true},
		{"admin implies view", []Cap{{Access: "admin"}}, "docs", true},
		{"scoped admin implies view", []Cap{{Access: "admin", Sites: []string{"docs"}}}, "docs", true},
		{"wildcard prefix", []Cap{{Access: "view", Sites: []string{"docs-*"}}}, "docs-foo", true},
		{"wildcard prefix no match", []Cap{{Access: "view", Sites: []string{"docs-*"}}}, "other", false},
		{"wildcard suffix", []Cap{{Access: "view", Sites: []string{"*-staging"}}}, "docs-staging", true},
		{"wildcard middle", []Cap{{Access: "view", Sites: []string{"docs-*-v2"}}}, "docs-foo-v2", true},
		{"wildcard middle no match", []Cap{{Access: "view", Sites: []string{"docs-*-v2"}}}, "docs-foo-v3", false},
		{"question mark", []Cap{{Access: "view", Sites: []string{"doc?"}}}, "docs", true},
		{"question mark no match", []Cap{{Access: "view", Sites: []string{"doc?"}}}, "documents", false},
		{"invalid pattern fails closed", []Cap{{Access: "view", Sites: []string{"["}}}, "docs", false},
		{"no grant", []Cap{{Access: "view", Sites: []string{"other"}}}, "docs", false},
		{"empty caps", []Cap{}, "docs", false},
		{"nil caps", nil, "docs", false},
		{"multi cap merge", []Cap{{Access: "view", Sites: []string{"a"}}, {Access: "view", Sites: []string{"docs"}}}, "docs", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanView(tt.caps, tt.site); got != tt.want {
				t.Errorf("CanView() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCanDeploy(t *testing.T) {
	tests := []struct {
		name string
		caps []Cap
		site string
		want bool
	}{
		{"deploy grant", []Cap{{Access: "deploy", Sites: []string{"docs"}}}, "docs", true},
		{"deploy wildcard", []Cap{{Access: "deploy", Sites: []string{"*"}}}, "docs", true},
		{"deploy omitted sites", []Cap{{Access: "deploy"}}, "docs", true},
		{"admin implies deploy", []Cap{{Access: "admin"}}, "docs", true},
		{"view does not imply deploy", []Cap{{Access: "view", Sites: []string{"docs"}}}, "docs", false},
		{"no grant", []Cap{{Access: "deploy", Sites: []string{"other"}}}, "docs", false},
		{"empty caps", []Cap{}, "docs", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanDeploy(tt.caps, tt.site); got != tt.want {
				t.Errorf("CanDeploy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCanDeleteSite(t *testing.T) {
	tests := []struct {
		name string
		caps []Cap
		site string
		want bool
	}{
		{"unscoped admin", []Cap{{Access: "admin"}}, "docs", true},
		{"scoped admin match", []Cap{{Access: "admin", Sites: []string{"docs"}}}, "docs", true},
		{"scoped admin no match", []Cap{{Access: "admin", Sites: []string{"other"}}}, "docs", false},
		{"deploy cannot delete", []Cap{{Access: "deploy"}}, "docs", false},
		{"view cannot delete", []Cap{{Access: "view"}}, "docs", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanDeleteSite(tt.caps, tt.site); got != tt.want {
				t.Errorf("CanDeleteSite() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCanCreateSite(t *testing.T) {
	tests := []struct {
		name string
		caps []Cap
		site string
		want bool
	}{
		{"unscoped admin", []Cap{{Access: "admin"}}, "docs", true},
		{"scoped admin match", []Cap{{Access: "admin", Sites: []string{"docs"}}}, "docs", true},
		{"scoped admin no match", []Cap{{Access: "admin", Sites: []string{"other"}}}, "docs", false},
		{"deploy cannot create", []Cap{{Access: "deploy"}}, "docs", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanCreateSite(tt.caps, tt.site); got != tt.want {
				t.Errorf("CanCreateSite() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCanScrapeMetrics(t *testing.T) {
	tests := []struct {
		name string
		caps []Cap
		want bool
	}{
		{"metrics grant", []Cap{{Access: "metrics"}}, true},
		{"admin implies metrics", []Cap{{Access: "admin"}}, true},
		{"scoped admin implies metrics", []Cap{{Access: "admin", Sites: []string{"docs"}}}, true},
		{"metrics ignores sites", []Cap{{Access: "metrics", Sites: []string{"docs"}}}, true},
		{"deploy does not imply metrics", []Cap{{Access: "deploy"}}, false},
		{"view does not imply metrics", []Cap{{Access: "view"}}, false},
		{"empty caps", []Cap{}, false},
		{"nil caps", nil, false},
		{"multi cap merge", []Cap{{Access: "view"}, {Access: "metrics"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanScrapeMetrics(tt.caps); got != tt.want {
				t.Errorf("CanScrapeMetrics() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAdmin(t *testing.T) {
	tests := []struct {
		name string
		caps []Cap
		site string
		want bool
	}{
		{"unscoped admin", []Cap{{Access: "admin"}}, "docs", true},
		{"scoped admin match", []Cap{{Access: "admin", Sites: []string{"docs"}}}, "docs", true},
		{"scoped admin no match", []Cap{{Access: "admin", Sites: []string{"other"}}}, "docs", false},
		{"deploy", []Cap{{Access: "deploy"}}, "docs", false},
		{"multi merge", []Cap{{Access: "view"}, {Access: "admin"}}, "docs", true},
		{"empty", []Cap{}, "docs", false},
		{"wildcard admin", []Cap{{Access: "admin", Sites: []string{"*"}}}, "docs", true},
		{"glob admin", []Cap{{Access: "admin", Sites: []string{"docs-*"}}}, "docs-staging", true},
		{"glob admin no match", []Cap{{Access: "admin", Sites: []string{"docs-*"}}}, "other", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAdmin(tt.caps, tt.site); got != tt.want {
				t.Errorf("IsAdmin() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasAdminCap(t *testing.T) {
	tests := []struct {
		name string
		caps []Cap
		want bool
	}{
		{"unscoped admin", []Cap{{Access: "admin"}}, true},
		{"scoped admin", []Cap{{Access: "admin", Sites: []string{"docs"}}}, true},
		{"deploy", []Cap{{Access: "deploy"}}, false},
		{"multi merge", []Cap{{Access: "view"}, {Access: "admin"}}, true},
		{"empty", []Cap{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasAdminCap(tt.caps); got != tt.want {
				t.Errorf("HasAdminCap() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseCaps(t *testing.T) {
	raw := []json.RawMessage{
		json.RawMessage(`{"access":"deploy","sites":["docs"]}`),
		json.RawMessage(`{"access":"admin"}`),
	}
	caps, err := ParseCaps(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(caps) != 2 {
		t.Fatalf("got %d caps, want 2", len(caps))
	}
	if !CanView(caps, "docs") {
		t.Error("expected view access to docs")
	}
	if !CanDeploy(caps, "docs") {
		t.Error("expected deploy access to docs")
	}
	if !HasAdminCap(caps) {
		t.Error("expected admin cap")
	}
	if !IsAdmin(caps, "docs") {
		t.Error("expected admin for docs")
	}
}

func TestMiddleware_NoCaps(t *testing.T) {
	client := &mockWhoIs{caps: nil}
	handler := Middleware(client, "example.com/cap/pages")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		}),
	)
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "100.64.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	caps := CapsFromContext(req.Context())
	if len(caps) != 0 {
		t.Errorf("expected no caps, got %d", len(caps))
	}
}

func TestMiddleware_WithCaps(t *testing.T) {
	raw := []json.RawMessage{json.RawMessage(`{"access":"view"}`)}
	client := &mockWhoIs{caps: raw}

	var gotCaps []Cap
	handler := Middleware(client, "example.com/cap/pages")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotCaps = CapsFromContext(r.Context())
			w.WriteHeader(200)
		}),
	)
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "100.64.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if len(gotCaps) != 1 {
		t.Fatalf("expected 1 cap, got %d", len(gotCaps))
	}
	if !CanView(gotCaps, "anything") {
		t.Error("expected wildcard view access")
	}
}

func TestMiddleware_WhoIsError(t *testing.T) {
	client := &mockWhoIs{err: fmt.Errorf("connection refused")}
	handler := Middleware(client, "example.com/cap/pages")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called on WhoIs error")
		}),
	)
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "100.64.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestIdentityContext(t *testing.T) {
	ctx := context.Background()

	// Empty context returns zero Identity
	id := IdentityFromContext(ctx)
	if id.LoginName != "" || id.DisplayName != "" {
		t.Errorf("expected zero identity, got %+v", id)
	}

	// Round-trip
	ctx = ContextWithIdentity(ctx, Identity{
		LoginName:   "alice@example.com",
		DisplayName: "Alice Smith",
	})
	id = IdentityFromContext(ctx)
	if id.LoginName != "alice@example.com" {
		t.Errorf("LoginName = %q, want %q", id.LoginName, "alice@example.com")
	}
	if id.DisplayName != "Alice Smith" {
		t.Errorf("DisplayName = %q, want %q", id.DisplayName, "Alice Smith")
	}
}

func TestMiddleware_StoresIdentity(t *testing.T) {
	client := &mockWhoIs{
		caps:        []json.RawMessage{json.RawMessage(`{"access":"view"}`)},
		loginName:   "bob@example.com",
		displayName: "Bob Jones",
	}

	var gotIdentity Identity
	handler := Middleware(client, "example.com/cap/pages")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotIdentity = IdentityFromContext(r.Context())
			w.WriteHeader(200)
		}),
	)
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "100.64.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotIdentity.LoginName != "bob@example.com" {
		t.Errorf("LoginName = %q, want %q", gotIdentity.LoginName, "bob@example.com")
	}
	if gotIdentity.DisplayName != "Bob Jones" {
		t.Errorf("DisplayName = %q, want %q", gotIdentity.DisplayName, "Bob Jones")
	}
}

// mockWhoIs implements WhoIsClient for testing
type mockWhoIs struct {
	caps        []json.RawMessage
	loginName   string
	displayName string
	err         error
}

func (m *mockWhoIs) WhoIs(ctx context.Context, remoteAddr string) (*WhoIsResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	capMap := make(map[string][]json.RawMessage)
	if m.caps != nil {
		capMap["example.com/cap/pages"] = m.caps
	}
	return &WhoIsResult{
		CapMap:      capMap,
		LoginName:   m.loginName,
		DisplayName: m.displayName,
	}, nil
}
