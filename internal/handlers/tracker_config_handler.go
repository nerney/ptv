package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/nerney/ptv/internal/config"
	"github.com/nerney/ptv/internal/defs"
	"github.com/nerney/ptv/internal/prowlarr"
)

type trackerConfigData struct {
	TrackerIdx      int
	Tracker         *config.TrackerEntry
	ProwlarrEnabled bool
	AutobrrEnabled  bool
	ActiveTab       string
	Section         string
	FlashError      string
	FlashSuccess    string
}

type trackerPTVConfigData struct {
	trackerConfigData
	URLs []string
}

type trackerAddData struct {
	Rows         []configRow
	DefsState    defs.State
	DefsMsg      string
	LoadError    string
	FlashError   string
	FlashSuccess string
}

type prowlarrDiffData struct {
	TrackerIdx      int
	Tracker         *config.TrackerEntry
	ProwlarrEnabled bool
	Row             prowlarrSyncRow
	FlashError      string
	FlashSuccess    string
	ActiveTab       string
	Section         string
}

func (h *Handler) trackerConfigPage(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, "/", "", "invalid tracker index")
		return
	}
	data := h.trackerConfigData(idx, cfg.Trackers[idx], cfg, r, "overview")
	h.render(w, "tracker_config", data)
}

func (h *Handler) trackerPTVConfigPage(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, "/", "", "invalid tracker index")
		return
	}
	data := trackerPTVConfigData{
		trackerConfigData: h.trackerConfigData(idx, cfg.Trackers[idx], cfg, r, "ptv"),
		URLs:              h.trackerDefinitionURLs(cfg.Trackers[idx].DefinitionName),
	}
	h.render(w, "tracker_ptv_config", data)
}

func (h *Handler) trackerAutobrrConfigPage(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, "/", "", "invalid tracker index")
		return
	}
	data := h.trackerConfigData(idx, cfg.Trackers[idx], cfg, r, "autobrr")
	h.render(w, "tracker_autobrr_config", data)
}

func (h *Handler) trackerAddPage(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.Get()
	data := trackerAddData{
		FlashError:   r.URL.Query().Get("err"),
		FlashSuccess: r.URL.Query().Get("ok"),
	}
	data.DefsState, data.DefsMsg = h.syncer.Status()
	allDefs := h.catalogIfReady(data.DefsState, &data.LoadError)
	for _, row := range buildConfigRows(cfg.Trackers, allDefs) {
		if !row.Configured {
			data.Rows = append(data.Rows, row)
		}
	}
	h.render(w, "tracker_add", data)
}

func (h *Handler) trackerProwlarrDiffPage(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, "/", "", "invalid tracker index")
		return
	}
	data := prowlarrDiffData{
		TrackerIdx:      idx,
		Tracker:         cfg.Trackers[idx],
		ProwlarrEnabled: cfg.ProwlarrEnabled && cfg.ProwlarrURL != "" && cfg.ProwlarrAPIKey != "",
		FlashError:      r.URL.Query().Get("err"),
		FlashSuccess:    r.URL.Query().Get("ok"),
		ActiveTab:       "prowlarr",
		Section:         "tracker",
	}
	if !data.ProwlarrEnabled {
		h.render(w, "tracker_prowlarr_diff", data)
		return
	}
	client := prowlarr.New(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, h.log)
	indexers, err := client.GetIndexers()
	if err != nil {
		data.Row = prowlarrSyncRow{TrackerIdx: idx, Name: cfg.Trackers[idx].Name, State: syncDrift, SchemaError: err.Error()}
		h.render(w, "tracker_prowlarr_diff", data)
		return
	}
	data.Row = h.classifyTracker(idx, cfg.Trackers[idx], indexersByID(indexers))
	h.render(w, "tracker_prowlarr_diff", data)
}

func (h *Handler) trackerProwlarrDiffPush(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, "/", "", "invalid tracker index")
		return
	}
	if !cfg.ProwlarrEnabled || cfg.ProwlarrURL == "" || cfg.ProwlarrAPIKey == "" {
		flash(w, r, trackerProwlarrDiffPath(idx), "", "Prowlarr not enabled")
		return
	}
	client := prowlarr.New(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, h.log)
	indexers, err := client.GetIndexers()
	if err != nil {
		flash(w, r, trackerProwlarrDiffPath(idx), "", "Failed to fetch Prowlarr indexers: "+err.Error())
		return
	}
	action, err := h.pushTrackerToProwlarr(cfg, idx, client, indexersByID(indexers))
	if err != nil {
		flash(w, r, trackerProwlarrDiffPath(idx), "", "Prowlarr push failed: "+err.Error())
		return
	}
	if err := h.store.Save(cfg); err != nil {
		flash(w, r, trackerProwlarrDiffPath(idx), "", "Save failed: "+err.Error())
		return
	}
	flash(w, r, trackerProwlarrPath(idx), fmt.Sprintf("Prowlarr %s.", action), "")
}

func (h *Handler) trackerConfigData(idx int, t *config.TrackerEntry, cfg *config.Config, r *http.Request, tab string) trackerConfigData {
	return trackerConfigData{
		TrackerIdx:      idx,
		Tracker:         t,
		ProwlarrEnabled: cfg.ProwlarrEnabled && cfg.ProwlarrURL != "" && cfg.ProwlarrAPIKey != "",
		AutobrrEnabled:  cfg.AutobrrEnabled && cfg.AutobrrURL != "" && cfg.AutobrrAPIKey != "",
		ActiveTab:       tab,
		Section:         "tracker",
		FlashError:      r.URL.Query().Get("err"),
		FlashSuccess:    r.URL.Query().Get("ok"),
	}
}

func trackerConfigPath(idx int) string {
	return "/tracker/" + strconv.Itoa(idx) + "/config"
}

func trackerPTVConfigPath(idx int) string {
	return trackerConfigPath(idx) + "/ptv"
}

func trackerProwlarrDiffPath(idx int) string {
	return trackerProwlarrPath(idx) + "/diff"
}
