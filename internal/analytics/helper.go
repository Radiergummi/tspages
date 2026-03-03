package analytics

import "tspages/internal/web"

// AnalyticsData is the template data for both per-site and all-sites analytics.
// SiteName is empty for the all-sites view.
type AnalyticsData struct {
	User      web.UserInfo
	Admin     bool
	SiteName  string // empty = all-sites view
	Range     string
	Total     int64
	Visitors  int64
	Pages     int64 // per-site only
	SiteCount int   // all-sites only

	TimeSeries       []TimeBucket
	StatusTimeSeries []StatusTimeBucket
	CountOK          int64
	Count4xx         int64
	Count5xx         int64
	TopPages         []PathCount // per-site only
	TopVisitors      []VisitorCount
	StatusCodes      []StatusCount
	OS               []OSCount
	Nodes            []NodeCount
	Sites            []SiteCount // all-sites only
}

func statusTotals(codes []StatusCount) (ok, clientErr, serverErr int64) {
	for _, c := range codes {
		switch c.Status {
		case "4xx":
			clientErr = c.Count
		case "5xx":
			serverErr = c.Count
		default:
			ok += c.Count
		}
	}
	return
}
