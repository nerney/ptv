package handlers

import (
	"context"
	"time"

	"github.com/nerney/ptv/internal/branding"
)

// discoverBrandingAsync kicks off a best-effort scrape of the tracker's
// landing page in a background goroutine. It NEVER blocks the caller
// and NEVER reports errors back — the worst case is the favicon/color
// just stays missing until the next attempt.
//
// definitionName is used (rather than a slice index) as the stable
// identifier because trackers can be reordered/deleted between the
// time we launch the goroutine and the time it completes.
//
// Semantics expected by callers:
//   - Save handlers call this unconditionally (the user resaving is
//     the explicit "go grab fresh branding" signal).
//   - Refresh-stats handlers call this only when the tracker has
//     neither a favicon nor a theme color (the "try again next refresh"
//     retry loop).
func (h *Handler) discoverBrandingAsync(definitionName, baseURL string) {
	if baseURL == "" || definitionName == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		res, err := branding.Discover(ctx, baseURL)
		if err != nil {
			h.log.Info("BRAND", definitionName+": discovery failed — "+err.Error())
			return
		}
		if res.FaviconDataURI == "" && res.ThemeColor == "" {
			return // nothing found this attempt — no save needed
		}
		h.applyBrandingByName(definitionName, res)
	}()
}

// applyBrandingByName writes the discovered branding fields for definitionName.
// ApplyBranding holds the store's write lock for the full read-modify-write,
// so this goroutine cannot race with a concurrent user-triggered Save.
func (h *Handler) applyBrandingByName(definitionName string, res branding.Result) {
	changed, err := h.store.ApplyBranding(definitionName, res.FaviconDataURI, res.ThemeColor)
	if err != nil {
		h.log.Info("BRAND", definitionName+": save skipped — "+err.Error())
		return
	}
	if changed {
		h.log.Info("BRAND", definitionName+": branding saved")
	}
}
