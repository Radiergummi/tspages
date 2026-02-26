package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
)

// Cap represents a single capability object from the tailnet policy.
// Access is one of "admin", "deploy", or "view". Each level implies the ones
// below it (admin > deploy > view). Sites scopes which sites the cap applies
// to; omitting it means all sites.
type Cap struct {
	Access string   `json:"access"`
	Sites  []string `json:"sites,omitempty"`
}

// WhoIsResult is the subset of WhoIs response data we need.
// This decouples us from the tailscale types for testability.
type WhoIsResult struct {
	CapMap         map[string][]json.RawMessage
	LoginName      string
	DisplayName    string
	ProfilePicURL  string
	// Node metadata for analytics.
	NodeName  string
	NodeIP    string
	OS        string
	OSVersion string
	Device    string
	Tags      []string
}

// Identity holds the authenticated user's profile information.
type Identity struct {
	LoginName     string
	DisplayName   string
	ProfilePicURL string
}

// WhoIsClient abstracts the tailscale LocalClient.WhoIs call.
type WhoIsClient interface {
	WhoIs(ctx context.Context, remoteAddr string) (*WhoIsResult, error)
}

// RequestInfo holds per-request metadata extracted from WhoIs for analytics.
type RequestInfo struct {
	UserLogin     string
	UserName      string
	ProfilePicURL string
	NodeName      string
	NodeIP        string
	OS            string
	OSVersion     string
	Device        string
	Tags          []string
}

type capsKey struct{}
type identityKey struct{}
type requestInfoKey struct{}

// RequestInfoFromContext retrieves analytics metadata from the request context.
func RequestInfoFromContext(ctx context.Context) RequestInfo {
	ri, _ := ctx.Value(requestInfoKey{}).(RequestInfo)
	return ri
}

// ContextWithRequestInfo adds RequestInfo to a context. Used by tests.
func ContextWithRequestInfo(ctx context.Context, ri RequestInfo) context.Context {
	return context.WithValue(ctx, requestInfoKey{}, ri)
}

// ParseCaps unmarshals raw JSON capability objects into Cap structs.
func ParseCaps(raw []json.RawMessage) ([]Cap, error) {
	caps := make([]Cap, 0, len(raw))
	for _, r := range raw {
		var c Cap
		if err := json.Unmarshal(r, &c); err != nil {
			return nil, fmt.Errorf("parsing capability: %w", err)
		}
		caps = append(caps, c)
	}
	return caps, nil
}

// matchesSite reports whether a sites list includes the given site.
// An empty list means all sites (equivalent to ["*"]).
// Patterns may contain wildcards: * matches any sequence of characters,
// ? matches a single character (using path.Match semantics).
func matchesSite(sites []string, site string) bool {
	if len(sites) == 0 {
		return true
	}
	for _, s := range sites {
		if s == site {
			return true
		}
		if matched, _ := path.Match(s, site); matched {
			return true
		}
	}
	return false
}

// CanView reports whether caps grant view access to the named site.
func CanView(caps []Cap, site string) bool {
	for _, c := range caps {
		switch c.Access {
		case "admin", "deploy", "view":
			if matchesSite(c.Sites, site) {
				return true
			}
		}
	}
	return false
}

// CanDeploy reports whether caps grant deploy access to the named site.
func CanDeploy(caps []Cap, site string) bool {
	for _, c := range caps {
		switch c.Access {
		case "admin", "deploy":
			if matchesSite(c.Sites, site) {
				return true
			}
		}
	}
	return false
}

// CanDeleteSite reports whether caps grant permission to delete a site.
// Requires an admin cap that covers the site.
func CanDeleteSite(caps []Cap, site string) bool {
	for _, c := range caps {
		if c.Access == "admin" && matchesSite(c.Sites, site) {
			return true
		}
	}
	return false
}

// CanCreateSite reports whether caps grant permission to create a site
// with the given name. Requires an admin cap covering that name.
func CanCreateSite(caps []Cap, name string) bool {
	for _, c := range caps {
		if c.Access == "admin" && matchesSite(c.Sites, name) {
			return true
		}
	}
	return false
}

// CanScrapeMetrics reports whether caps grant access to the metrics endpoint.
// This is a global (non-site-scoped) capability; the Sites field is ignored.
func CanScrapeMetrics(caps []Cap) bool {
	for _, c := range caps {
		if c.Access == "admin" || c.Access == "metrics" {
			return true
		}
	}
	return false
}

// IsAdmin reports whether any cap grants admin access.
func IsAdmin(caps []Cap) bool {
	for _, c := range caps {
		if c.Access == "admin" {
			return true
		}
	}
	return false
}

// CapsFromContext retrieves parsed caps from the request context.
func CapsFromContext(ctx context.Context) []Cap {
	caps, _ := ctx.Value(capsKey{}).([]Cap)
	return caps
}

// ContextWithCaps adds caps to a context. Used by tests.
func ContextWithCaps(ctx context.Context, caps []Cap) context.Context {
	return context.WithValue(ctx, capsKey{}, caps)
}

// IdentityFromContext retrieves the caller's identity from the request context.
func IdentityFromContext(ctx context.Context) Identity {
	id, _ := ctx.Value(identityKey{}).(Identity)
	return id
}

// ContextWithIdentity adds identity to a context. Used by tests.
func ContextWithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// Middleware returns HTTP middleware that calls WhoIs, parses capabilities,
// and attaches them to the request context. It does NOT enforce permissions --
// individual handlers decide what access level is required.
func Middleware(client WhoIsClient, capName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If caps are already in context (e.g. dev mode), skip WhoIs.
			if CapsFromContext(r.Context()) != nil {
				next.ServeHTTP(w, r)
				return
			}

			result, err := client.WhoIs(r.Context(), r.RemoteAddr)
			if err != nil {
				http.Error(w, "identity check failed", http.StatusForbidden)
				return
			}

			var caps []Cap
			if raw, ok := result.CapMap[capName]; ok && len(raw) > 0 {
				parsed, err := ParseCaps(raw)
				if err != nil {
					http.Error(w, "invalid capabilities", http.StatusInternalServerError)
					return
				}
				caps = parsed
			}

			ctx := context.WithValue(r.Context(), capsKey{}, caps)
			ctx = context.WithValue(ctx, identityKey{}, Identity{
				LoginName:     result.LoginName,
				DisplayName:   result.DisplayName,
				ProfilePicURL: result.ProfilePicURL,
			})
			ctx = context.WithValue(ctx, requestInfoKey{}, RequestInfo{
				UserLogin:     result.LoginName,
				UserName:      result.DisplayName,
				ProfilePicURL: result.ProfilePicURL,
				NodeName:      result.NodeName,
				NodeIP:    result.NodeIP,
				OS:        result.OS,
				OSVersion: result.OSVersion,
				Device:    result.Device,
				Tags:      result.Tags,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
