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

// applyBrandingByName locates the tracker by DefinitionName in the
// current (unlocked) config and writes the branding fields. Saves
// only if something actually changed. A locked store (logout / timeout
// between the fetch and now) is silently ignored — the next refresh
// or save will trigger another attempt.
func (h *Handler) applyBrandingByName(definitionName string, res branding.Result) {
	cfg := h.store.Get()
	if cfg.Trackers == nil {
		return // store locked, or no trackers
	}
	changed := false
	for i, t := range cfg.Trackers {
		if t.DefinitionName != definitionName {
			continue
		}
		if res.FaviconDataURI != "" && cfg.Trackers[i].FaviconDataURI != res.FaviconDataURI {
			cfg.Trackers[i].FaviconDataURI = res.FaviconDataURI
			changed = true
		}
		if res.ThemeColor != "" && cfg.Trackers[i].ThemeColor != res.ThemeColor {
			cfg.Trackers[i].ThemeColor = res.ThemeColor
			changed = true
		}
		break
	}
	if !changed {
		return
	}
	if err := h.store.Save(&cfg); err != nil {
		// Most likely cause: store locked (session ended between
		// discovery start and now). Quiet info-level log.
		h.log.Info("BRAND", definitionName+": save skipped — "+err.Error())
		return
	}
	h.log.Info("BRAND", definitionName+": branding saved")
}
