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

type trackerAddData struct {
	Rows         []configRow
	DefsState    defs.State
	DefsMsg      string
	LoadError    string
	FlashError   string
	FlashSuccess string
}

type prowlarrDiffData struct {
	trackerConfigData
	Row prowlarrSyncRow
}

type unifiedTrackerConfigData struct {
	TrackerIdx            int
	Tracker               *config.TrackerEntry
	URLs                  []string
	SubmittedURL          string
	SubmittedAPIKey       string
	ProwlarrEnabled       bool
	ProwlarrSchema        *prowlarr.IndexerSchema
	ProwlarrBaseName      string
	ProwlarrSettingRows   []settingFieldRow
	ProwlarrAppProfiles   []prowlarr.AppProfile
	ProwlarrTags          []prowlarr.Tag
	ProwlarrError         string
	AutobrrEnabled        bool
	AutobrrDefinition     *autobrrdefs.Def
	AutobrrSettingRows    []settingFieldRow
	AutobrrError          string
	FlashError            string
	FlashSuccess          string
	ValidationError       string
	ValidationTrackerName string
}

type settingFieldRow struct {
	RowClass  string
	Field     settingFieldView
	ExtraHint string
}

type settingFieldView struct {
	Name          string
	Label         string
	Type          string
	Value         string
	HasValue      bool
	Secret        bool
	Required      bool
	HelpText      string
	Placeholder   string
	Info          bool
	SelectOptions []settingSelectOption
}

type settingSelectOption struct {
	Name  string
	Value string
}

func (h *Handler) trackerConfigPage(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, "/", "", "invalid tracker index")
		return
	}
	data := h.buildUnifiedTrackerConfigData(idx, cfg)
	data.FlashError = r.URL.Query().Get("err")
	data.FlashSuccess = r.URL.Query().Get("ok")
	h.render(w, r, "tracker_config_unified", data)
}

func (h *Handler) buildUnifiedTrackerConfigData(idx int, cfg *config.Config) unifiedTrackerConfigData {
	t := cfg.Trackers[idx]
	data := unifiedTrackerConfigData{
		TrackerIdx:      idx,
		Tracker:         t,
		URLs:            h.trackerDefinitionURLs(t.DefinitionName),
		ProwlarrEnabled: cfg.ProwlarrEnabled && cfg.ProwlarrURL != "" && cfg.ProwlarrAPIKey != "",
		AutobrrEnabled:  cfg.AutobrrEnabled && cfg.AutobrrURL != "" && cfg.AutobrrAPIKey != "",
	}

	if data.ProwlarrEnabled {
		if profiles, err := h.prowlarrAppProfiles(cfg); err == nil {
			data.ProwlarrAppProfiles = profiles
		} else {
			h.log.Err("PROWLARR", "app profiles: "+err.Error())
		}
		if tags, err := h.prowlarrTags(cfg); err == nil {
			data.ProwlarrTags = tags
		} else {
			h.log.Err("PROWLARR", "tags: "+err.Error())
		}
		if schema, err := h.prowlarrSchemaByName(t.DefinitionName); err == nil {
			data.ProwlarrSchema = schema
			data.ProwlarrSettingRows = prowlarrSettingRows(prowlarr.RenderFields(*schema, t.ProwlarrSettings()))
		} else {
			data.ProwlarrError = "Schema unavailable: " + err.Error()
		}
		baseName := t.ProwlarrName()
		if baseName == "" {
			baseName = t.Name
		}
		data.ProwlarrBaseName = prowlarr.BaseIndexerName(baseName)
	}

	if data.AutobrrEnabled {
		if def := h.autobrrDefFor(t, t.AutobrrIdentifier()); def != nil {
			data.AutobrrDefinition = def
			data.AutobrrSettingRows = autobrrSettingRows(autobrr.RenderFields(*def, t.AutobrrSettings()))
		} else {
			data.AutobrrError = "Definition not available"
		}
	}

	return data
}

func prowlarrSettingRows(fields []prowlarr.SettingField) []settingFieldRow {
	rows := make([]settingFieldRow, 0, len(fields))
	for _, f := range fields {
		options := make([]settingSelectOption, 0, len(f.SelectOptions))
		for _, opt := range f.SelectOptions {
			options = append(options, settingSelectOption{Name: opt.Name, Value: opt.Value})
		}
		label := f.Label
		if label == "" {
			label = f.Name
		}
		rows = append(rows, settingFieldRow{
			RowClass: "prowlarr-field-row",
			Field: settingFieldView{
				Name:          f.Name,
				Label:         label,
				Type:          f.Type,
				Value:         f.Value,
				HasValue:      f.HasValue,
				Secret:        f.Secret,
				Required:      f.Required,
				HelpText:      f.HelpText,
				Placeholder:   f.Placeholder,
				Info:          f.Info,
				SelectOptions: options,
			},
		})
	}
	return rows
}

func autobrrSettingRows(fields []autobrr.SettingField) []settingFieldRow {
	rows := make([]settingFieldRow, 0, len(fields))
	for _, f := range fields {
		label := f.Label
		if label == "" {
			label = f.Name
		}
		extraHint := ""
		if f.Layer == "irc" {
			extraHint = "IRC setting"
		}
		rows = append(rows, settingFieldRow{
			RowClass:  "autobrr-field-row",
			ExtraHint: extraHint,
			Field: settingFieldView{
				Name:        f.Name,
				Label:       label,
				Type:        f.Type,
				Value:       f.Value,
				HasValue:    f.HasValue,
				Secret:      f.Secret,
				Required:    f.Required,
				HelpText:    f.Help,
				Placeholder: "",
			},
		})
	}
	return rows
}

func (h *Handler) trackerAutobrrConfigPost(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, pathConfigTrackers, "", "invalid tracker index")
		return
	}
	if err := r.ParseForm(); err != nil {
		flash(w, r, trackerConfigPath(idx), "", "invalid form")
		return
	}
	if !cfg.AutobrrEnabled || cfg.AutobrrURL == "" || cfg.AutobrrAPIKey == "" {
		flash(w, r, pathConfigAutobrr, "", "Autobrr not enabled")
		return
	}

	def := h.autobrrDefFor(cfg.Trackers[idx], cfg.Trackers[idx].AutobrrIdentifier())
	if def == nil {
		flash(w, r, trackerConfigPath(idx), "", "Definition not available")
		return
	}

	submitted := submittedAutobrrSettings(r, *def)
	autobrrCfg := cfg.Trackers[idx].EnsureAutobrr()
	autobrrCfg.Settings = autobrr.MergeSettings(*def, autobrrCfg.Settings, submitted)

	if err := h.store.Save(cfg); err != nil {
		flash(w, r, trackerConfigPath(idx), "", "Save failed: "+err.Error())
		return
	}

	if r.FormValue("action") != "save_push" {
		h.log.Info("CONFIG", fmt.Sprintf("Saved Autobrr settings for %q", cfg.Trackers[idx].Name))
		flash(w, r, trackerConfigPath(idx), "Autobrr settings saved.", "")
		return
	}

	if cfg.Trackers[idx].AutobrrID() == 0 {
		flash(w, r, trackerConfigPath(idx), "", "Tracker not linked to Autobrr")
		return
	}

	if err := h.pushTrackerAutobrrConfig(cfg, idx, *def); err != nil {
		cfg.Trackers[idx].EnsureAutobrr().SyncError = err.Error()
		if saveErr := h.store.Save(cfg); saveErr != nil {
			flash(w, r, trackerConfigPath(idx), "", "Push failed: "+err.Error()+"; save failed: "+saveErr.Error())
			return
		}
		h.log.Err("CONFIG", fmt.Sprintf("Autobrr push failed for %q: %s", cfg.Trackers[idx].Name, err.Error()))
		flash(w, r, trackerConfigPath(idx), "", "Autobrr push failed: "+err.Error())
		return
	}

	if err := h.store.Save(cfg); err != nil {
		flash(w, r, trackerConfigPath(idx), "", "Push succeeded but save failed: "+err.Error())
		return
	}

	h.log.Info("CONFIG", fmt.Sprintf("Pushed Autobrr settings for %q", cfg.Trackers[idx].Name))
	flash(w, r, trackerConfigPath(idx), "Autobrr settings pushed.", "")
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
	h.render(w, r, "tracker_add", data)
}

func (h *Handler) trackerProwlarrDiffPage(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, "/", "", "invalid tracker index")
		return
	}
	data := prowlarrDiffData{
		trackerConfigData: h.trackerConfigData(idx, cfg.Trackers[idx], cfg, r, "prowlarr"),
	}
	if !data.ProwlarrEnabled {
		h.render(w, r, "tracker_prowlarr_diff", data)
		return
	}
	client := prowlarr.New(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, h.log)
	indexers, err := client.GetIndexers()
	if err != nil {
		data.Row = prowlarrSyncRow{TrackerIdx: idx, Name: cfg.Trackers[idx].Name, State: syncDrift, SchemaError: err.Error()}
		h.render(w, r, "tracker_prowlarr_diff", data)
		return
	}
	data.Row = h.classifyTracker(idx, cfg.Trackers[idx], indexersByID(indexers))
	h.render(w, r, "tracker_prowlarr_diff", data)
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
	flash(w, r, trackerConfigPath(idx), fmt.Sprintf("Prowlarr %s.", action), "")
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

func trackerProwlarrDiffPath(idx int) string {
	return "/tracker/" + strconv.Itoa(idx) + "/config/prowlarr/diff"
}

func (h *Handler) renderTrackerConfigWithError(w http.ResponseWriter, r *http.Request, idx int, cfg *config.Config, submittedURL, submittedKey, validationError string) {
	data := h.buildUnifiedTrackerConfigData(idx, cfg)
	data.SubmittedURL = submittedURL
	data.SubmittedAPIKey = submittedKey
	data.ValidationError = validationError
	data.ValidationTrackerName = cfg.Trackers[idx].Name
	h.render(w, r, "tracker_config_unified", data)
}

func submittedAutobrrSettings(r *http.Request, def autobrrdefs.Def) map[string]string {
	out := make(map[string]string)
	for _, f := range def.Settings {
		name := "setting_" + f.Name
		values, ok := r.Form[name]
		if !ok {
			continue
		}
		out[f.Name] = submittedFormValue(values, f.Type)
	}
	for _, f := range def.IRCSettings {
		name := "setting_" + f.Name
		values, ok := r.Form[name]
		if !ok {
			continue
		}
		out[f.Name] = submittedFormValue(values, f.Type)
	}
	return out
}

func (h *Handler) pushTrackerAutobrrConfig(cfg *config.Config, i int, def autobrrdefs.Def) error {
	t := cfg.Trackers[i]
	if t.APIKey == "" {
		return fmt.Errorf("missing core tracker API key")
	}
	if t.AutobrrID() == 0 {
		return fmt.Errorf("tracker not linked to Autobrr")
	}

	settings := autobrr.WithCoreCredentials(def, t.AutobrrSettings(), t.APIKey)
	client := autobrr.New(cfg.AutobrrURL, cfg.AutobrrAPIKey, h.log)

	// Fetch the existing indexer to get all its metadata
	existing, err := client.GetIndexer(int64(t.AutobrrID()))
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
	autobrrCfg := t.EnsureAutobrr()
	autobrrCfg.Settings = autobrr.MergeSettings(def, settings, readback)
	now := time.Now()
	autobrrCfg.LastSync = &now
	autobrrCfg.SyncError = ""

	return nil
}
