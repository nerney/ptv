// Package trackertype defines the TrackerType interface and the global
// registry of known tracker software implementations.
//
// Each tracker software (UNIT3D, Gazelle, etc.) registers a Type in its
// own package init(). Callers look up a type by its stable string ID
// (e.g. "unit3d") and use the interface to fetch stats, detect definition
// files, and produce human-readable display names without importing the
// concrete package directly.
package trackertype

import "github.com/nerney/ptv/internal/logger"

// Stats is a user account snapshot fetched from a live tracker API.
// String fields are pre-formatted by the tracker software ("1.23 TiB",
// "2.71") — PTV does not re-parse or reformat them.
type Stats struct {
	Username   string
	Group      string
	Uploaded   string
	Downloaded string
	Ratio      string
	Buffer     string
	SeedBonus  string
	Seeding    int
	Leeching   int
	HitAndRuns int
}

// Type is the interface every tracker software must implement and
// register via Register().
type Type interface {
	// ID returns the stable lowercase identifier stored in TrackerEntry.TrackerType.
	// Example: "unit3d"
	ID() string

	// DisplayName is the human-readable tracker software name shown in the UI.
	// Example: "UNIT3D"
	DisplayName() string

	// DetectDef reports whether the given Prowlarr definition search paths
	// identify this tracker software. Used to filter the definition catalog
	// to only the software types PTV knows how to talk to.
	DetectDef(searchPaths []string) bool

	// FetchStats retrieves the logged-in user's account statistics via the
	// tracker's API. Returns a non-nil error on any failure; the caller
	// records the error on the TrackerEntry but does not surface it to the
	// user as a hard failure.
	FetchStats(baseURL, apiKey string, log *logger.Logger) (*Stats, error)
}

// registry maps type IDs to their implementations.
// Populated by each tracker package's init() via Register().
var registry = map[string]Type{}

// Register adds a Type to the global registry.
// Panics on duplicate IDs to catch copy-paste mistakes at startup.
func Register(t Type) {
	if _, dup := registry[t.ID()]; dup {
		panic("trackertype: duplicate registration for ID " + t.ID())
	}
	registry[t.ID()] = t
}

// Lookup returns the Type for id, or nil if not registered.
func Lookup(id string) Type {
	return registry[id]
}

// All returns all registered Types in no guaranteed order.
func All() []Type {
	out := make([]Type, 0, len(registry))
	for _, t := range registry {
		out = append(out, t)
	}
	return out
}
