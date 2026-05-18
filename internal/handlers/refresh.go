package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/nerney/ptv/internal/config"
	"github.com/nerney/ptv/internal/trackertype"
)

// refresh.go pulls fresh stats from a tracker's API for one or all
// configured trackers. Two endpoints:
//
//   POST /refresh         → refresh every tracker, return the whole grid
//   POST /refresh/{idx}   → refresh one tracker, return one card
//
// Both are htmx targets — they return HTML fragments, not full pages.
// Per-tracker LastSync + SyncError are recorded so a single failure
// leaves the others fresh and the error surfaces inline on the card.

// refreshResult is the data passed to the tracker_cards partial.
type refreshResult struct {
	Trackers []*trackerCardView
	LastSync *time.Time
}

// refresh syncs every configured tracker. Trackers without credentials
// are skipped (rather than errored) — they're presumably half-configured.
func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.Get()
	h.log.Info("SYSTEM", "Refresh-all triggered")

	changed := false
	for i, entry := range cfg.Trackers {
		if entry.APIKey == "" || entry.TrackerURL == "" {
			h.log.Info("TRACKER", "Skipping "+entry.Name+" — no credentials configured")
			continue
		}
		h.refreshOneEntry(&cfg, i)
		changed = true
	}

	if changed {
		// Persist even partial failures: SyncError + LastSync is what
		// the UI uses to render the stale-mark and per-card error banner.
		_ = h.store.Save(&cfg)
	}

	views := h.buildTrackerViews(cfg)
	sortTrackerViews(views, r.URL.Query().Get("sort"))
	h.renderPartial(w, "tracker_cards", refreshResult{
		Trackers: views,
		LastSync: latestSync(cfg.Trackers),
	})
}

// refreshOne refreshes a single tracker by URL-path index and returns
// just that card's HTML for htmx to swap in.
func (h *Handler) refreshOne(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(chi.URLParam(r, "idx"))
	if err != nil {
		http.Error(w, "invalid index", http.StatusBadRequest)
		return
	}
	cfg := h.store.Get()
	if idx < 0 || idx >= len(cfg.Trackers) {
		http.Error(w, "index out of range", http.StatusBadRequest)
		return
	}
	h.refreshOneEntry(&cfg, idx)
	_ = h.store.Save(&cfg)
	views := h.buildTrackerViews(cfg)
	h.renderPartial(w, "tracker_card", views[idx])
}

// refreshOneEntry fetches stats for one tracker entry and updates it in
// place. Semantics:
//
//   - LastSync is set on EVERY attempt (success or failure) — the UI shows
//     "tried 30s ago, failed" vs silence.
//   - SyncError is cleared on success, set on failure.
//   - UserStats is overwritten on success; preserved on failure (last-good
//     numbers stay visible with a stale warning).
func (h *Handler) refreshOneEntry(cfg *config.Config, i int) {
	entry := cfg.Trackers[i]
	if entry.APIKey == "" || entry.TrackerURL == "" {
		return
	}

	tt := resolveTrackerType(entry.TrackerType)
	if tt == nil {
		return // unsupported tracker type — skip silently
	}
	stats, err := tt.FetchStats(entry.TrackerURL, entry.APIKey, h.log)

	now := time.Now()
	cfg.Trackers[i].LastSync = &now
	if err != nil {
		cfg.Trackers[i].SyncError = err.Error()
		h.log.Err("TRACKER", entry.Name+": "+err.Error())
		return
	}
	cfg.Trackers[i].SyncError = ""
	cfg.Trackers[i].UserStats = statsToConfig(stats)
	h.log.Info("TRACKER", entry.Name+": refreshed OK ("+stats.Username+")")

	// Retry branding discovery only when nothing has ever been found.
	// Once we have either field, it sticks until the user resaves.
	if cfg.Trackers[i].FaviconDataURI == "" && cfg.Trackers[i].ThemeColor == "" {
		h.discoverBrandingAsync(cfg.Trackers[i].DefinitionName, cfg.Trackers[i].TrackerURL)
	}
}

// resolveTrackerType returns the registered TrackerType for typeID, or nil
// if the ID is empty or unknown. A nil result means "unsupported" — the
// caller skips that tracker rather than guessing a type.
func resolveTrackerType(typeID string) trackertype.Type {
	return trackertype.Lookup(typeID)
}

// latestSync returns the most recent LastSync across all trackers, or nil
// if none have ever synced. Used by the header to show overall freshness.
func latestSync(trackers []*config.TrackerEntry) *time.Time {
	var latest *time.Time
	for _, t := range trackers {
		if t.LastSync == nil {
			continue
		}
		if latest == nil || t.LastSync.After(*latest) {
			ts := *t.LastSync
			latest = &ts
		}
	}
	return latest
}
