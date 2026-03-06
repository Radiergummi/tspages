package web

import (
	"html/template"
	"strings"
	"testing"
	"time"
)

func TestBadge(t *testing.T) {
	fn := funcMap["badge"].(func(string, ...string) template.HTML)

	tests := []struct {
		status string
		title  string
		want   string
	}{
		{"active", "", `<span class="inline-block text-xs font-semibold uppercase tracking-wide px-2 py-0.5 rounded-full bg-blue-500/10 text-blue-500">active</span>`},
		{"failed", "timeout", `<span class="inline-block text-xs font-semibold uppercase tracking-wide px-2 py-0.5 rounded-full bg-red-500/10 text-red-600 dark:text-red-400" title="timeout">failed</span>`},
		{"ok", "", `<span class="inline-block text-xs font-semibold uppercase tracking-wide px-2 py-0.5 rounded-full bg-green-500/10 text-green-600 dark:text-green-400">ok</span>`},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := fn(tt.status, tt.title)
			if string(got) != tt.want {
				t.Errorf("badge(%q, %q)\n got: %s\nwant: %s", tt.status, tt.title, got, tt.want)
			}
		})
	}
}

func TestEmptyState(t *testing.T) {
	fn := funcMap["emptystate"].(func(string) template.HTML)
	got := fn("No deployments yet.")
	want := `<p class="text-center py-12 px-8 text-muted text-sm border border-default rounded-md bg-surface">No deployments yet.</p>`
	if string(got) != want {
		t.Errorf("emptystate\n got: %s\nwant: %s", got, want)
	}
}

func TestIcon(t *testing.T) {
	fn := funcMap["icon"].(func(string, ...int) template.HTML)

	got := string(fn("arrow-left"))
	if !strings.Contains(got, `width="18"`) {
		t.Errorf("icon: expected default size 18, got: %s", got)
	}
	if !strings.Contains(got, `<path`) {
		t.Errorf("icon: expected path elements, got: %s", got)
	}

	got = string(fn("arrow-left", 16))
	if !strings.Contains(got, `width="16"`) {
		t.Errorf("icon with size: expected size 16, got: %s", got)
	}

	got = string(fn("nonexistent"))
	if !strings.Contains(got, "unknown icon") {
		t.Errorf("icon unknown: expected comment, got: %s", got)
	}
}

func TestTimeago(t *testing.T) {
	fn := funcMap["timeago"].(func(any) template.HTML)
	ts := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	got := string(fn(ts))

	if !strings.HasPrefix(got, `<time datetime="`) {
		t.Errorf("timeago: expected <time> element, got: %s", got)
	}
	if !strings.Contains(got, `2026-01-15 12:00 UTC`) {
		t.Errorf("timeago: expected abs time in title/datetime, got: %s", got)
	}
	if !strings.HasSuffix(got, `</time>`) {
		t.Errorf("timeago: expected closing </time>, got: %s", got)
	}
}
