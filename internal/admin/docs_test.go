package admin

import (
	"fmt"
	"sync"
	"testing"
)

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
			html, err := RenderDoc(slug)
			if err != nil {
				errs <- err
				return
			}
			if len(html) == 0 {
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
