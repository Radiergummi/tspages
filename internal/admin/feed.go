package admin

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"sort"
	"time"

	"tspages/internal/auth"
	"tspages/internal/storage"
)

const feedMaxEntries = 50

// Atom XML types (RFC 4287)

type atomXMLFeed struct {
	XMLName xml.Name       `xml:"feed"`
	XMLNS   string         `xml:"xmlns,attr"`
	Title   string         `xml:"title"`
	ID      string         `xml:"id"`
	Updated string         `xml:"updated"`
	Links   []atomXMLLink  `xml:"link"`
	Entries []atomXMLEntry `xml:"entry"`
}

type atomXMLEntry struct {
	Title   string         `xml:"title"`
	ID      string         `xml:"id"`
	Updated string         `xml:"updated"`
	Author  atomXMLAuthor  `xml:"author"`
	Links   []atomXMLLink  `xml:"link"`
	Content atomXMLContent `xml:"content"`
}

type atomXMLAuthor struct {
	Name string `xml:"name"`
}

type atomXMLLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr,omitempty"`
}

type atomXMLContent struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

// --- GET /feed.atom ---

type FeedHandler struct{ handlerDeps }

func (h *FeedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())

	sites, err := h.store.ListSites()
	if err != nil {
		http.Error(w, "listing sites", http.StatusInternalServerError)
		return
	}

	var entries []atomXMLEntry
	for _, s := range sites {
		if !auth.CanView(caps, s.Name) {
			continue
		}
		deps, err := h.store.ListDeployments(s.Name)
		if err != nil {
			continue
		}
		for _, d := range deps {
			entries = append(entries, deploymentToEntry(s.Name, d, *h.dnsSuffix, r.Host))
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Updated > entries[j].Updated
	})
	if len(entries) > feedMaxEntries {
		entries = entries[:feedMaxEntries]
	}

	var updated string
	if len(entries) > 0 {
		updated = entries[0].Updated
	} else {
		updated = time.Now().UTC().Format(time.RFC3339)
	}

	feed := atomXMLFeed{
		XMLNS:   "http://www.w3.org/2005/Atom",
		Title:   "tspages deployments",
		ID:      fmt.Sprintf("https://%s/feed.atom", r.Host),
		Updated: updated,
		Links: []atomXMLLink{
			{Href: fmt.Sprintf("https://%s/feed.atom", r.Host), Rel: "self", Type: "application/atom+xml"},
			{Href: fmt.Sprintf("https://%s/deployments", r.Host), Rel: "alternate", Type: "text/html"},
		},
		Entries: entries,
	}

	writeFeed(w, feed)
}

// --- GET /sites/{site}/feed.atom ---

type SiteFeedHandler struct{ handlerDeps }

func (h *SiteFeedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	siteName := r.PathValue("site")
	if !storage.ValidSiteName(siteName) {
		http.Error(w, "invalid site name", http.StatusBadRequest)
		return
	}

	caps := auth.CapsFromContext(r.Context())
	if !auth.IsAdmin(caps) && !auth.CanView(caps, siteName) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	deps, err := h.store.ListDeployments(siteName)
	if err != nil {
		http.Error(w, "listing deployments", http.StatusInternalServerError)
		return
	}

	entries := make([]atomXMLEntry, 0, len(deps))
	for _, d := range deps {
		entries = append(entries, deploymentToEntry(siteName, d, *h.dnsSuffix, r.Host))
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Updated > entries[j].Updated
	})
	if len(entries) > feedMaxEntries {
		entries = entries[:feedMaxEntries]
	}

	var updated string
	if len(entries) > 0 {
		updated = entries[0].Updated
	} else {
		updated = time.Now().UTC().Format(time.RFC3339)
	}

	feed := atomXMLFeed{
		XMLNS:   "http://www.w3.org/2005/Atom",
		Title:   fmt.Sprintf("tspages: %s", siteName),
		ID:      fmt.Sprintf("https://%s/sites/%s/feed.atom", r.Host, siteName),
		Updated: updated,
		Links: []atomXMLLink{
			{Href: fmt.Sprintf("https://%s/sites/%s/feed.atom", r.Host, siteName), Rel: "self", Type: "application/atom+xml"},
			{Href: fmt.Sprintf("https://%s/sites/%s", r.Host, siteName), Rel: "alternate", Type: "text/html"},
		},
		Entries: entries,
	}

	writeFeed(w, feed)
}

func deploymentToEntry(site string, d storage.DeploymentInfo, dnsSuffix, host string) atomXMLEntry {
	updated := d.CreatedAt.UTC().Format(time.RFC3339)
	author := d.CreatedBy
	if author == "" {
		author = "unknown"
	}

	return atomXMLEntry{
		Title:   fmt.Sprintf("Deployed %s (%s)", site, d.ID),
		ID:      fmt.Sprintf("https://%s/sites/%s/deployments/%s", host, site, d.ID),
		Updated: updated,
		Author:  atomXMLAuthor{Name: author},
		Links: []atomXMLLink{
			{Href: fmt.Sprintf("https://%s/sites/%s/deployments/%s", host, site, d.ID), Rel: "alternate", Type: "text/html"},
		},
		Content: atomXMLContent{
			Type: "text",
			Body: fmt.Sprintf("Deployed to %s.%s by %s (%s)", site, dnsSuffix, author, formatBytes(d.SizeBytes)),
		},
	}
}

func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}

func writeFeed(w http.ResponseWriter, feed atomXMLFeed) {
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	enc.Encode(feed)
}
