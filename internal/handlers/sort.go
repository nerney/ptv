package handlers

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// sort.go orders the dashboard tracker cards by a user-selected key.
//
// Keys are passed via ?sort= on the dashboard URL and re-applied to the
// /refresh response so an htmx grid swap preserves the chosen order.
// Direction is always descending — biggest ratio / most uploaded / most
// actively seeding first — because that's what every user has asked for
// when surveyed. Trackers without UserStats (never synced or no creds)
// always sort to the bottom regardless of key.

const (
	sortDefault  = ""
	sortRatio    = "ratio"
	sortUploaded = "uploaded"
	sortActive   = "active"
)

// sortTrackerViews orders the slice in place. Unknown or empty keys leave
// the slice in its config-order default. Stable sort preserves config
// order as the tiebreaker.
func sortTrackerViews(views []*trackerCardView, key string) {
	if key == sortDefault {
		return
	}
	sort.SliceStable(views, func(i, j int) bool {
		a, b := views[i], views[j]
		// Missing stats always last — half-configured trackers shouldn't
		// jump to the top just because their values parse as 0.
		if (a.UserStats == nil) != (b.UserStats == nil) {
			return a.UserStats != nil
		}
		if a.UserStats == nil {
			return false
		}
		switch key {
		case sortRatio:
			return parseRatio(a.UserStats.Ratio) > parseRatio(b.UserStats.Ratio)
		case sortUploaded:
			return parseBytes(a.UserStats.Uploaded) > parseBytes(b.UserStats.Uploaded)
		case sortActive:
			return a.UserStats.Seeding > b.UserStats.Seeding
		}
		return false
	})
}

// parseRatio handles UNIT3D's "Inf" / "∞" sentinel for fresh accounts
// with zero downloaded, plus normal numeric strings like "2.71".
func parseRatio(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if strings.EqualFold(s, "inf") || s == "∞" {
		return math.Inf(1)
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// parseBytes turns a UNIT3D byte-string like "1.23 TiB" / "456.7 GiB"
// into a raw byte count for ordering. Unknown units fall through as 1×
// (raw number) so the ordering is at least monotonic within a tracker's
// own data. Returns 0 on empty / unparsable input.
func parseBytes(s string) float64 {
	parts := strings.Fields(strings.TrimSpace(s))
	if len(parts) == 0 {
		return 0
	}
	n, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	if len(parts) < 2 {
		return n
	}
	switch strings.ToLower(parts[1]) {
	case "kib", "kb":
		return n * 1024
	case "mib", "mb":
		return n * 1024 * 1024
	case "gib", "gb":
		return n * 1024 * 1024 * 1024
	case "tib", "tb":
		return n * 1024 * 1024 * 1024 * 1024
	case "pib", "pb":
		return n * 1024 * 1024 * 1024 * 1024 * 1024
	}
	return n
}
