package admin

import "testing"

func TestResolveAsset(t *testing.T) {
	m := &manifest{
		entries: map[string]string{
			"web/admin/src/main.css":             "assets/main-abc123.css",
			"web/admin/src/pages/deployments.ts": "assets/deployments-def456.js",
		},
	}

	tests := []struct {
		key  string
		want string
	}{
		{"main.css", "/assets/dist/assets/main-abc123.css"},
		{"pages/deployments.ts", "/assets/dist/assets/deployments-def456.js"},
		{"nonexistent.ts", ""},
	}

	for _, tt := range tests {
		got := m.resolve(tt.key)
		if got != tt.want {
			t.Errorf("resolve(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}
