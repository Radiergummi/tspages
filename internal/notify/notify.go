// Package notify defines the event notification interface used across tspages
// for form submissions, deploys, and site lifecycle events.
package notify

import "tspages/internal/storage"

// Notifier dispatches site events (e.g. form submissions, deploys).
// Implementations decide which events to act on and how to deliver them.
type Notifier interface {
	Fire(event string, site string, cfg storage.SiteConfig, data map[string]any)
}

// Multi fans out events to multiple notifiers.
type Multi []Notifier

func (m Multi) Fire(event string, site string, cfg storage.SiteConfig, data map[string]any) {
	for _, n := range m {
		n.Fire(event, site, cfg, data)
	}
}
