package notify

import (
	"testing"

	"tspages/internal/storage"
)

type recorder struct {
	calls []string
}

func (r *recorder) Fire(event string, site string, _ storage.SiteConfig, _ map[string]any) {
	r.calls = append(r.calls, event+":"+site)
}

func TestMulti_FansOutToAll(t *testing.T) {
	a := &recorder{}
	b := &recorder{}
	m := Multi{a, b}

	m.Fire("form.submission", "docs", storage.SiteConfig{}, nil)

	if len(a.calls) != 1 || a.calls[0] != "form.submission:docs" {
		t.Errorf("notifier a: got %v", a.calls)
	}
	if len(b.calls) != 1 || b.calls[0] != "form.submission:docs" {
		t.Errorf("notifier b: got %v", b.calls)
	}
}

func TestMulti_Empty(t *testing.T) {
	m := Multi{}
	m.Fire("deploy.success", "docs", storage.SiteConfig{}, nil) // should not panic
}

func TestMulti_Single(t *testing.T) {
	a := &recorder{}
	m := Multi{a}

	m.Fire("site.created", "demo", storage.SiteConfig{}, nil)

	if len(a.calls) != 1 {
		t.Errorf("got %d calls, want 1", len(a.calls))
	}
}
