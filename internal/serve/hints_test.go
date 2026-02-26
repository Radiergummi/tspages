package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractHints(t *testing.T) {
	tests := []struct {
		name string
		html string
		want []string
	}{
		{
			name: "stylesheet",
			html: `<html><head><link rel="stylesheet" href="/style.css"></head><body></body></html>`,
			want: []string{`</style.css>; rel=preload; as=style`},
		},
		{
			name: "script",
			html: `<html><head><script src="/app.js"></script></head><body></body></html>`,
			want: []string{`</app.js>; rel=preload; as=script`},
		},
		{
			name: "href before rel",
			html: `<html><head><link href="/style.css" rel="stylesheet"></head></html>`,
			want: []string{`</style.css>; rel=preload; as=style`},
		},
		{
			name: "single quotes",
			html: `<html><head><link rel='stylesheet' href='/style.css'></head></html>`,
			want: []string{`</style.css>; rel=preload; as=style`},
		},
		{
			name: "multiple resources",
			html: `<html><head>
				<link rel="stylesheet" href="/a.css">
				<link rel="stylesheet" href="/b.css">
				<script src="/app.js"></script>
			</head><body></body></html>`,
			want: []string{
				`</a.css>; rel=preload; as=style`,
				`</b.css>; rel=preload; as=style`,
				`</app.js>; rel=preload; as=script`,
			},
		},
		{
			name: "ignores external URLs",
			html: `<html><head>
				<link rel="stylesheet" href="https://cdn.example.com/style.css">
				<script src="//cdn.example.com/app.js"></script>
				<link rel="stylesheet" href="/local.css">
			</head><body></body></html>`,
			want: []string{`</local.css>; rel=preload; as=style`},
		},
		{
			name: "ignores body resources",
			html: `<html><head>
				<link rel="stylesheet" href="/head.css">
			</head><body>
				<script src="/body.js"></script>
			</body></html>`,
			want: []string{`</head.css>; rel=preload; as=style`},
		},
		{
			name: "deduplicates",
			html: `<html><head>
				<link rel="stylesheet" href="/style.css">
				<link rel="stylesheet" href="/style.css">
			</head></html>`,
			want: []string{`</style.css>; rel=preload; as=style`},
		},
		{
			name: "no resources",
			html: `<html><head><title>Hello</title></head><body></body></html>`,
			want: nil,
		},
		{
			name: "relative paths included",
			html: `<html><head><link rel="stylesheet" href="./style.css"></head></html>`,
			want: []string{`<./style.css>; rel=preload; as=style`},
		},
		{
			name: "ignores data URIs",
			html: `<html><head><script src="data:text/javascript,void(0)"></script></head></html>`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "index.html")
			os.WriteFile(path, []byte(tt.html), 0644)

			got := extractHints(path)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d hints %v, want %d %v", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("hint[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractHints_MaxLimit(t *testing.T) {
	var links strings.Builder
	links.WriteString("<html><head>")
	for i := 0; i < 20; i++ {
		links.WriteString(`<link rel="stylesheet" href="/style` + strings.Repeat("x", i) + `.css">`)
	}
	links.WriteString("</head></html>")

	dir := t.TempDir()
	path := filepath.Join(dir, "index.html")
	os.WriteFile(path, []byte(links.String()), 0644)

	got := extractHints(path)
	if len(got) > maxHints {
		t.Errorf("got %d hints, want at most %d", len(got), maxHints)
	}
}

func TestIsSameOrigin(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"/style.css", true},
		{"style.css", true},
		{"./style.css", true},
		{"../style.css", true},
		{"/assets/app.js", true},
		{"https://example.com/style.css", false},
		{"http://example.com/style.css", false},
		{"//cdn.example.com/style.css", false},
		{"data:text/css,body{}", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := isSameOrigin(tt.url); got != tt.want {
			t.Errorf("isSameOrigin(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}
