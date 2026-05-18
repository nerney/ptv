package unit3d

import (
	"strings"

	"github.com/nerney/ptv/internal/logger"
	"github.com/nerney/ptv/internal/trackertype"
)

func init() {
	trackertype.Register(unitType{})
}

// unitType implements trackertype.Type for the UNIT3D tracker software.
type unitType struct{}

func (unitType) ID() string          { return "unit3d" }
func (unitType) DisplayName() string { return "UNIT3D" }

// DetectDef identifies a UNIT3D instance by the first search path in its
// Prowlarr definition: the UNIT3D API filter endpoint is unique to this software.
func (unitType) DetectDef(searchPaths []string) bool {
	if len(searchPaths) == 0 {
		return false
	}
	return strings.TrimLeft(searchPaths[0], "/") == "api/torrents/filter"
}

// FetchStats retrieves account stats via the UNIT3D /api/user endpoint.
func (unitType) FetchStats(baseURL, apiKey string, log *logger.Logger) (*trackertype.Stats, error) {
	raw, err := New(baseURL, apiKey, log).FetchStats()
	if err != nil {
		return nil, err
	}
	return &trackertype.Stats{
		Username:   raw.Username,
		Group:      raw.Group,
		Uploaded:   raw.Uploaded,
		Downloaded: raw.Downloaded,
		Ratio:      raw.Ratio,
		Buffer:     raw.Buffer,
		SeedBonus:  raw.SeedBonus,
		Seeding:    raw.Seeding,
		Leeching:   raw.Leeching,
		HitAndRuns: raw.HitAndRuns,
	}, nil
}
