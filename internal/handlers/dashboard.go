package handlers

import (
	"net/http"
	"time"
)

// dashboard renders the home page — the tracker card grid plus the
// header (REFRESH, CONFIG, LOGOUT). When the user has zero trackers
// configured we show an empty state with a CTA, rather than redirecting
// to /config: redirects-on-empty mask the underlying state and surprise
// the user.
type dashboardData struct {
	Trackers        []*trackerCardView
	Date            string
	Sort            string // current sort key — "", "ratio", "uploaded", "active"
	ProwlarrEnabled bool
	AutobrrEnabled  bool
	FlashError      string
	FlashSuccess    string
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	cfg := h.refreshAllEntries()
	sortKey := r.URL.Query().Get("sort")
	views := h.buildTrackerViews(cfg)
	sortTrackerViews(views, sortKey)
	h.render(w, r, "dashboard", dashboardData{
		Trackers:        views,
		Date:            time.Now().Format("02 Jan 2006"),
		Sort:            sortKey,
		ProwlarrEnabled: cfg.ProwlarrEnabled && cfg.ProwlarrURL != "" && cfg.ProwlarrAPIKey != "",
		AutobrrEnabled:  cfg.AutobrrEnabled && cfg.AutobrrURL != "" && cfg.AutobrrAPIKey != "",
		FlashError:      r.URL.Query().Get("err"),
		FlashSuccess:    r.URL.Query().Get("ok"),
	})
}
