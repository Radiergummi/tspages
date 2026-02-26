package admin

import "testing"

func TestDocPages_AllExist(t *testing.T) {
	pages := DocPages()
	if len(pages) == 0 {
		t.Fatal("no doc pages defined")
	}
	for _, p := range pages {
		html, err := RenderDoc(p.Slug)
		if err != nil {
			t.Errorf("rendering %q: %v", p.Slug, err)
			continue
		}
		if len(html) == 0 {
			t.Errorf("rendering %q: empty output", p.Slug)
		}
	}
}

func TestRenderDoc_NotFound(t *testing.T) {
	_, err := RenderDoc("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent doc")
	}
}
