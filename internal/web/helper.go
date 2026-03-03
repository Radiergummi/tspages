package web

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

// UserInfo holds user display data for templates.
type UserInfo struct {
	Name          string `json:"name"`
	ProfilePicURL string `json:"profile_pic_url,omitempty"`
	Admin         bool   `json:"admin,omitempty"`
	CanDeploy     bool   `json:"can_deploy,omitempty"`
}

// NewUserInfo builds a UserInfo from an auth identity and capabilities.
func NewUserInfo(identity auth.Identity, caps []auth.Cap) UserInfo {
	name := identity.DisplayName
	if name == "" {
		name = identity.LoginName
	}
	return UserInfo{Name: name, ProfilePicURL: identity.ProfilePicURL, Admin: auth.HasAdminCap(caps), CanDeploy: auth.HasDeployCap(caps)}
}

// FormatBytes formats a byte count for human display.
func FormatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}

// ParseRange parses the "range" query parameter as an ISO 8601 duration and
// returns the range string, start, and end times. Defaults to PT24H.
func ParseRange(r *http.Request) (rangeParam string, from, to time.Time) {
	rangeParam = r.URL.Query().Get("range")
	to = time.Now()
	if rangeParam == "all" {
		from = time.Time{}
		return
	}
	var ok bool
	from, ok = subtractISO8601(to, rangeParam)
	if !ok {
		rangeParam = "PT24H"
		from = to.Add(-24 * time.Hour)
	}
	return
}

// subtractISO8601 parses an ISO 8601 duration string (e.g. "P7D", "PT24H",
// "P1M") and returns now minus that duration. Returns false for invalid or
// zero-length durations.
func subtractISO8601(now time.Time, s string) (time.Time, bool) {
	if len(s) < 3 || s[0] != 'P' {
		return time.Time{}, false
	}
	rest := s[1:]
	var years, months, days, hours, minutes, seconds int
	inTime := false
	for len(rest) > 0 {
		if rest[0] == 'T' {
			inTime = true
			rest = rest[1:]
			continue
		}
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i == 0 || i >= len(rest) {
			return time.Time{}, false
		}
		n, _ := strconv.Atoi(rest[:i])
		unit := rest[i]
		rest = rest[i+1:]
		switch {
		case !inTime && unit == 'Y':
			years = n
		case !inTime && unit == 'M':
			months = n
		case !inTime && unit == 'W':
			days += n * 7
		case !inTime && unit == 'D':
			days += n
		case inTime && unit == 'H':
			hours = n
		case inTime && unit == 'M':
			minutes = n
		case inTime && unit == 'S':
			seconds = n
		default:
			return time.Time{}, false
		}
	}
	if years == 0 && months == 0 && days == 0 && hours == 0 && minutes == 0 && seconds == 0 {
		return time.Time{}, false
	}
	from := now.AddDate(-years, -months, -days).Add(
		-time.Duration(hours)*time.Hour - time.Duration(minutes)*time.Minute - time.Duration(seconds)*time.Second,
	)
	return from, true
}

// AnalyticsEnabled reports whether analytics are enabled for the given site
// by reading the current deployment's config and merging with server defaults.
func AnalyticsEnabled(store *storage.Store, defaults storage.SiteConfig, site string) bool {
	deployCfg, _ := store.ReadCurrentDeploymentConfig(site)
	siteCfg, _ := store.ReadSiteConfig(site)
	merged := deployCfg.Merge(siteCfg).Merge(defaults)
	if merged.Analytics == nil {
		return true
	}
	return *merged.Analytics
}
