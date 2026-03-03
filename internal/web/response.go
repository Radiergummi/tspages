package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// WantsJSON returns true if the request prefers JSON output,
// based on a .json suffix on the last path value or the Accept header.
func WantsJSON(r *http.Request) bool {
	if strings.HasSuffix(r.URL.Path, ".json") {
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// TrimSuffix strips a .json or .html extension from a path parameter.
func TrimSuffix(s string) string {
	s = strings.TrimSuffix(s, ".json")
	s = strings.TrimSuffix(s, ".html")
	return s
}

// WriteJSON encodes v as JSON and writes it to the response.
func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("encoding JSON response failed", "component", "web", "err", err)
	}
}

// SetAlternateLinks sets RFC 8288 Link headers for alternate representations.
// Each pair is [href, media-type].
func SetAlternateLinks(w http.ResponseWriter, alternates [][2]string) {
	var parts []string
	for _, a := range alternates {
		parts = append(parts, fmt.Sprintf(`<%s>; rel="alternate"; type="%s"`, a[0], a[1]))
	}
	w.Header().Set("Link", strings.Join(parts, ", "))
}
