package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/nerney/ptv/internal/autobrr"
	"github.com/nerney/ptv/internal/autobrrdefs"
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

type autobrrTrackerData struct {
	TrackerIdx      int
	Tracker         *config.TrackerEntry
	AutobrrEnabled  bool
	ProwlarrEnabled bool
	Definition      *autobrrdefs.Def
	DefError        string
	Fields          []autobrr.SettingField
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
	data := trackerPTVConfigData{
		trackerConfigData: h.trackerConfigData(idx, cfg.Trackers[idx], cfg, r, "ptv"),
		URLs:              h.trackerDefinitionURLs(cfg.Trackers[idx].DefinitionName),
	}
	h.render(w, "tracker_ptv_config", data)
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
	data := autobrrTrackerData{
		TrackerIdx:      idx,
		Tracker:         cfg.Trackers[idx],
		AutobrrEnabled:  cfg.AutobrrEnabled && cfg.AutobrrURL != "" && cfg.AutobrrAPIKey != "",
		ProwlarrEnabled: cfg.ProwlarrEnabled && cfg.ProwlarrURL != "" && cfg.ProwlarrAPIKey != "",
		FlashError:      r.URL.Query().Get("err"),
		FlashSuccess:    r.URL.Query().Get("ok"),
		ActiveTab:       "autobrr",
		Section:         "tracker",
	}

	if !data.AutobrrEnabled {
		h.render(w, "tracker_autobrr_config", data)
		return
	}

	def := h.autobrrDefFor(cfg.Trackers[idx], cfg.Trackers[idx].AutobrrIdentifier)
	if def == nil {
		data.DefError = "Definition not available"
		h.render(w, "tracker_autobrr_config", data)
		return
	}

	data.Definition = def
	data.Fields = autobrr.RenderFields(*def, cfg.Trackers[idx].AutobrrSettings)
	h.render(w, "tracker_autobrr_config", data)
}

func (h *Handler) trackerAutobrrConfigPost(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, pathConfigTrackers, "", "invalid tracker index")
		return
	}
	if err := r.ParseForm(); err != nil {
		flash(w, r, trackerAutobrrPath(idx), "", "invalid form")
		return
	}
	if !cfg.AutobrrEnabled || cfg.AutobrrURL == "" || cfg.AutobrrAPIKey == "" {
		flash(w, r, pathConfigAutobrr, "", "Autobrr not enabled")
		return
	}

	def := h.autobrrDefFor(cfg.Trackers[idx], cfg.Trackers[idx].AutobrrIdentifier)
	if def == nil {
		flash(w, r, trackerAutobrrPath(idx), "", "Definition not available")
		return
	}

	submitted := submittedAutobrrSettings(r, *def)
	cfg.Trackers[idx].AutobrrSettings = autobrr.MergeSettings(*def, cfg.Trackers[idx].AutobrrSettings, submitted)

	if err := h.store.Save(cfg); err != nil {
		flash(w, r, trackerAutobrrPath(idx), "", "Save failed: "+err.Error())
		return
	}

	if r.FormValue("action") != "save_push" {
		h.log.Info("CONFIG", fmt.Sprintf("Saved Autobrr settings for %q", cfg.Trackers[idx].Name))
		flash(w, r, trackerAutobrrPath(idx), "Autobrr settings saved.", "")
		return
	}

	if cfg.Trackers[idx].AutobrrID == 0 {
		flash(w, r, trackerAutobrrPath(idx), "", "Tracker not linked to Autobrr")
		return
	}

	if err := h.pushTrackerAutobrrConfig(cfg, idx, *def); err != nil {
		cfg.Trackers[idx].AutobrrSyncError = err.Error()
		if saveErr := h.store.Save(cfg); saveErr != nil {
			flash(w, r, trackerAutobrrPath(idx), "", "Push failed: "+err.Error()+"; save failed: "+saveErr.Error())
			return
		}
		h.log.Err("CONFIG", fmt.Sprintf("Autobrr push failed for %q: %s", cfg.Trackers[idx].Name, err.Error()))
		flash(w, r, trackerAutobrrPath(idx), "", "Autobrr push failed: "+err.Error())
		return
	}

	if err := h.store.Save(cfg); err != nil {
		flash(w, r, trackerAutobrrPath(idx), "", "Push succeeded but save failed: "+err.Error())
		return
	}

	h.log.Info("CONFIG", fmt.Sprintf("Pushed Autobrr settings for %q", cfg.Trackers[idx].Name))
	flash(w, r, trackerAutobrrPath(idx), "Autobrr settings pushed.", "")
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
	return trackerConfigPath(idx)
}

func trackerProwlarrDiffPath(idx int) string {
	return trackerProwlarrPath(idx) + "/diff"
}

func trackerAutobrrPath(idx int) string {
	return "/tracker/" + strconv.Itoa(idx) + "/config/autobrr"
}

func submittedAutobrrSettings(r *http.Request, def autobrrdefs.Def) map[string]string {
	out := make(map[string]string)
	for _, f := range def.Settings {
		name := "setting_" + f.Name
		if _, ok := r.Form[name]; !ok {
			continue
		}
		out[f.Name] = r.FormValue(name)
	}
	for _, f := range def.IRCSettings {
		name := "setting_" + f.Name
		if _, ok := r.Form[name]; !ok {
			continue
		}
		out[f.Name] = r.FormValue(name)
	}
	return out
}

func (h *Handler) pushTrackerAutobrrConfig(cfg *config.Config, i int, def autobrrdefs.Def) error {
	t := cfg.Trackers[i]
	if t.APIKey == "" {
		return fmt.Errorf("missing core tracker API key")
	}
	if t.AutobrrID == 0 {
		return fmt.Errorf("tracker not linked to Autobrr")
	}

	settings := autobrr.WithCoreCredentials(def, t.AutobrrSettings, t.APIKey)
	client := autobrr.New(cfg.AutobrrURL, cfg.AutobrrAPIKey, h.log)

	// Fetch the existing indexer to get all its metadata
	existing, err := client.GetIndexer(int64(t.AutobrrID))
	if err != nil {
		return err
	}

	// Update indexer in Autobrr with merged settings
	updated, err := client.UpdateIndexerWithSettings(*existing, t.TrackerURL, settings)
	if err != nil {
		return err
	}

	// Capture and merge readback settings
	readback := autobrr.SettingsFromPairs(updated.Settings)
	t.AutobrrSettings = autobrr.MergeSettings(def, settings, readback)
	now := time.Now()
	t.AutobrrLastSync = &now
	t.AutobrrSyncError = ""

	return nil
}
