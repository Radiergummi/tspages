package admin

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestDocPages_AllExist(t *testing.T) {
	pages := DocPages()
	if len(pages) == 0 {
		t.Fatal("no doc pages defined")
	}
	for _, p := range pages {
		result, err := RenderDoc(p.Slug)
		if err != nil {
			t.Errorf("rendering %q: %v", p.Slug, err)
			continue
		}
		if len(result.Content) == 0 {
			t.Errorf("rendering %q: empty output", p.Slug)
		}
	}
}

func TestRenderDoc_HeadingIDs(t *testing.T) {
	result, err := RenderDoc("authorization")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Content), `id="how-it-works"`) {
		t.Error("expected heading ID 'how-it-works' in rendered HTML")
	}
	if len(result.TOC) == 0 {
		t.Fatal("expected non-empty TOC")
	}
	found := false
	for _, e := range result.TOC {
		if e.ID == "how-it-works" && e.Level == 2 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected TOC entry for 'how-it-works' at level 2")
	}
}

func TestRenderDoc_TableWrap(t *testing.T) {
	result, err := RenderDoc("per-site-config")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Content), `class="table-wrap"`) {
		t.Error("expected tables wrapped in div.table-wrap")
	}
}

func TestRenderDoc_SyntaxHighlighting(t *testing.T) {
	result, err := RenderDoc("getting-started")
	if err != nil {
		t.Fatal(err)
	}
	// Code blocks should have Chroma CSS classes for syntax highlighting.
	if !strings.Contains(string(result.Content), `class="chroma"`) {
		t.Error("expected Chroma syntax highlighting classes in rendered HTML")
	}
}

func TestRenderDoc_AnchorLinks(t *testing.T) {
	result, err := RenderDoc("authorization")
	if err != nil {
		t.Fatal(err)
	}
	// Headings should contain anchor links.
	if !strings.Contains(string(result.Content), `class="anchor"`) {
		t.Error("expected anchor links in headings")
	}
}

func TestRenderDoc_Frontmatter(t *testing.T) {
	result, err := RenderDoc("getting-started")
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta.Category != "guide" {
		t.Errorf("expected category 'guide', got %q", result.Meta.Category)
	}
	if result.Meta.Description == "" {
		t.Error("expected non-empty description from frontmatter")
	}
}

func TestRenderDoc_NotFound(t *testing.T) {
	_, err := RenderDoc("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent doc")
	}
}

func TestRenderDoc_Concurrent(t *testing.T) {
	slugs := DocPages()
	if len(slugs) == 0 {
		t.Skip("no doc pages")
	}
	slug := slugs[0].Slug

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := RenderDoc(slug)
			if err != nil {
				errs <- err
				return
			}
			if len(result.Content) == 0 {
				errs <- fmt.Errorf("empty HTML for %q", slug)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
