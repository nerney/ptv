// Package integration defines the Integration interface — the contract
// every external service connector must satisfy. PTV currently ships with
// Prowlarr; Autobrr is the second planned integration.
//
// Integrations are distinct from TrackerTypes: a TrackerType defines how
// to talk to a tracker's own API (fetching stats, etc.), while an
// Integration is an external management service that PTV can push tracker
// configuration into or sync state with.
package integration

import "github.com/nerney/ptv/internal/config"

// Integration is the minimal contract for an external service connector.
// Each integration's richer operations (CRUD on its own objects, import
// flows, status checks) live in the integration's own package; this
// interface captures only what generic PTV code needs to know about any
// integration.
type Integration interface {
	// Name returns the stable lowercase identifier for this integration.
	// Example: "prowlarr", "autobrr"
	Name() string

	// DisplayName is the human-readable service name shown in the UI.
	// Example: "Prowlarr", "Autobrr"
	DisplayName() string

	// Enabled reports whether this integration is configured and active
	// for the given config snapshot.
	Enabled(cfg config.Config) bool
}
