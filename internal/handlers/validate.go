package handlers

import (
	"fmt"

	"github.com/nerney/ptv/internal/config"
	"github.com/nerney/ptv/internal/trackertype"
)

// validateTracker fetches stats from the tracker API to confirm credentials
// are valid. typeID selects the tracker software (e.g. "unit3d"); an
// unrecognised or empty ID falls back to "unit3d" for backward compatibility
// with entries created before the TrackerType field existed.
//
// This is the canonical credential-check path: any time the user enters
// or changes URL+API key we call this to confirm the pair actually works.
func (h *Handler) validateTracker(typeID, url, apiKey string) (*config.UserStats, error) {
	if h.validateFn != nil {
		return h.validateFn(typeID, url, apiKey)
	}
	tt := resolveTrackerType(typeID)
	if tt == nil {
		return nil, fmt.Errorf("no tracker type registered for %q", typeID)
	}
	stats, err := tt.FetchStats(url, apiKey, h.log)
	if err != nil {
		return nil, err
	}
	return statsToConfig(stats), nil
}

// statsToConfig converts the interface-level Stats into the storage struct.
// They have identical shapes; keeping them separate allows the storage layer
// to evolve independently of any one tracker client's wire format.
func statsToConfig(s *trackertype.Stats) *config.UserStats {
	return &config.UserStats{
		Username:   s.Username,
		Group:      s.Group,
		Uploaded:   s.Uploaded,
		Downloaded: s.Downloaded,
		Ratio:      s.Ratio,
		Buffer:     s.Buffer,
		SeedBonus:  s.SeedBonus,
		Seeding:    s.Seeding,
		Leeching:   s.Leeching,
		HitAndRuns: s.HitAndRuns,
	}
}
