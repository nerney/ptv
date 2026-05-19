package handlers

import (
	"strconv"

	"github.com/nerney/ptv/internal/autobrr"
	"github.com/nerney/ptv/internal/config"
)

// IRC status constants surfaced to templates. Strings (not enums) so
// templates can compare with {{ if eq .AutobrrIRCStatus "connected" }}.
const (
	ircStatusNone         = ""             // Autobrr not enabled OR tracker not in Autobrr
	ircStatusUnknown      = "unknown"      // in Autobrr but no IRC network found
	ircStatusConnected    = "connected"    // IRC network is connected
	ircStatusDisconnected = "disconnected" // IRC network exists but not connected
)

// trackerCardView wraps a *TrackerEntry with the dynamic per-render fields
// the card template needs but the persisted entry doesn't carry. Currently
// just the Autobrr IRC connection status.
//
// Embedding *config.TrackerEntry promotes all its fields, so the existing
// template references ({{ .Name }}, {{ .ProwlarrID }}, etc.) keep working
// unchanged.
type trackerCardView struct {
	*config.TrackerEntry
	AutobrrIRCStatus  string
	TrackerConfigURL  string
	ProwlarrConfigURL string
	AutobrrConfigURL  string
}

// buildTrackerViews wraps every configured tracker in a render view and,
// when Autobrr is enabled, fills in the IRC connection status for each.
// A failed Autobrr fetch is non-fatal: the views still render, with the
// IRC indicator showing the "no autobrr" empty state.
func (h *Handler) buildTrackerViews(cfg config.Config) []*trackerCardView {
	views := make([]*trackerCardView, len(cfg.Trackers))
	for i, t := range cfg.Trackers {
		views[i] = &trackerCardView{
			TrackerEntry:     t,
			TrackerConfigURL: "/tracker/" + strconv.Itoa(i) + "/config",
		}
		if cfg.ProwlarrEnabled && cfg.ProwlarrURL != "" && cfg.ProwlarrAPIKey != "" && t.DefinitionName != "" {
			views[i].ProwlarrConfigURL = "/tracker/" + strconv.Itoa(i) + "/config/prowlarr"
		}
		if cfg.AutobrrEnabled && cfg.AutobrrURL != "" && cfg.AutobrrAPIKey != "" && t.DefinitionName != "" {
			views[i].AutobrrConfigURL = "/tracker/" + strconv.Itoa(i) + "/config/autobrr"
		}
	}
	h.fillAutobrrIRCStatus(cfg, views)
	return views
}

// fillAutobrrIRCStatus resolves each tracker's per-card IRC connection
// status by querying Autobrr once and matching networks to indexers by
// identifier/name (see autobrr.MatchNetwork). On any Autobrr API failure
// the function returns silently — empty status renders as a neutral "—".
func (h *Handler) fillAutobrrIRCStatus(cfg config.Config, views []*trackerCardView) {
	if !cfg.AutobrrEnabled || cfg.AutobrrURL == "" || cfg.AutobrrAPIKey == "" {
		return
	}
	// Skip the API call entirely if no tracker is linked to Autobrr.
	anyLinked := false
	for _, v := range views {
		if v.AutobrrID > 0 {
			anyLinked = true
			break
		}
	}
	if !anyLinked {
		return
	}

	client := autobrr.New(cfg.AutobrrURL, cfg.AutobrrAPIKey, h.log)
	networks, err := client.GetIRCNetworks()
	if err != nil {
		h.log.Err("AUTOBRR", "Fetch IRC networks: "+err.Error())
		return
	}
	for _, v := range views {
		if v.AutobrrID == 0 {
			continue
		}
		network := autobrr.MatchNetwork(networks, v.AutobrrIdentifier, v.Name)
		if network == nil {
			v.AutobrrIRCStatus = ircStatusUnknown
			continue
		}
		if network.Connected {
			v.AutobrrIRCStatus = ircStatusConnected
		} else {
			v.AutobrrIRCStatus = ircStatusDisconnected
		}
	}
}
