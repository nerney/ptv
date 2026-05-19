package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/nerney/ptv/internal/autobrr"
	"github.com/nerney/ptv/internal/autobrrdefs"
	"github.com/nerney/ptv/internal/config"
)

// autobrr_handler covers the Autobrr settings page, the per-tracker
// add/toggle/remove actions, and the import page.
//
// Autobrr's role in PTV is intentionally narrow: it is a destination for
// tracker credentials and a read source for IRC connection status. We do
// not manage filters, releases, IRC networks, or any other Autobrr feature.

const (
	pathConfigAutobrr = "/config/integrations/autobrr"
	pathAutobrrImport = "/config/integrations/autobrr/import"
)

// ── settings page ──────────────────────────────────────────────────

type configAutobrrData struct {
	Config       config.Config
	FlashError   string
	FlashSuccess string
	ActiveTab    string // "settings" | "import"
	Section      string
}

// configAutobrrPage renders the Autobrr connection settings. Pre-fills a
// docker-compose-friendly default URL when nothing is configured yet.
func (h *Handler) configAutobrrPage(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.Get()
	if cfg.AutobrrURL == "" {
		cfg.AutobrrURL = "http://autobrr:7474"
	}
	h.render(w, "config_autobrr", configAutobrrData{
		Config:       cfg,
		FlashError:   r.URL.Query().Get("err"),
		FlashSuccess: r.URL.Query().Get("ok"),
		ActiveTab:    "settings",
		Section:      "integrations",
	})
}

// configAutobrrPost saves Autobrr URL + API key. We Ping() before persisting,
// so a typo or wrong key is caught immediately instead of failing silently
// on the next import.
func (h *Handler) configAutobrrPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		flash(w, r, pathConfigAutobrr, "", "invalid form")
		return
	}
	autobrrURL := strings.TrimSpace(r.FormValue("url"))
	apiKey := strings.TrimSpace(r.FormValue("api_key"))
	if autobrrURL == "" {
		flash(w, r, pathConfigAutobrr, "", "URL is required.")
		return
	}

	cfg := h.store.Get()
	if apiKey == "" {
		if cfg.AutobrrAPIKey == "" {
			flash(w, r, pathConfigAutobrr, "", "API key is required.")
			return
		}
		apiKey = cfg.AutobrrAPIKey
	}

	h.log.Info("CONFIG", "Saving Autobrr settings — testing connection")
	client := autobrr.New(autobrrURL, apiKey, h.log)
	if err := client.Ping(); err != nil {
		h.log.Err("CONFIG", "Autobrr ping failed: "+err.Error())
		flash(w, r, pathConfigAutobrr, "", "Cannot reach Autobrr: "+err.Error())
		return
	}

	cfg.AutobrrURL = autobrrURL
	cfg.AutobrrAPIKey = apiKey
	cfg.AutobrrEnabled = true
	if err := h.store.Save(&cfg); err != nil {
		flash(w, r, pathConfigAutobrr, "", "Save failed: "+err.Error())
		return
	}
	h.log.Info("CONFIG", "Autobrr settings saved")
	flash(w, r, pathConfigAutobrr, "Autobrr settings saved.", "")
}

// configAutobrrEnable / configAutobrrDisable flip the boolean without
// touching credentials so re-enabling doesn't require retyping.
func (h *Handler) configAutobrrEnable(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.Get()
	cfg.AutobrrEnabled = true
	if err := h.store.Save(&cfg); err != nil {
		flash(w, r, pathConfigAutobrr, "", "Save failed: "+err.Error())
		return
	}
	h.log.Info("CONFIG", "Autobrr integration enabled")
	flash(w, r, pathConfigAutobrr, "Autobrr integration enabled.", "")
}

func (h *Handler) configAutobrrDisable(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.Get()
	cfg.AutobrrEnabled = false
	if err := h.store.Save(&cfg); err != nil {
		flash(w, r, pathConfigAutobrr, "", "Save failed: "+err.Error())
		return
	}
	h.log.Info("CONFIG", "Autobrr integration disabled (credentials preserved)")
	flash(w, r, pathConfigAutobrr, "Autobrr integration disabled.", "")
}

// ── per-tracker actions ────────────────────────────────────────────

// configTrackerAutobrrAdd pushes a managed tracker into Autobrr. The
// matching Autobrr schema is found by URL; credentials are populated
// from the stored TrackerEntry. The returned AutobrrID + identifier are
// persisted so toggle/remove can find the right indexer later.
func (h *Handler) configTrackerAutobrrAdd(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, "/", "", "invalid tracker index")
		return
	}
	basePath := trackerConfigPath(idx) + "/autobrr"
	entry := cfg.Trackers[idx]
	if entry.TrackerURL == "" || entry.APIKey == "" {
		flash(w, r, basePath, "", entry.Name+": set URL and API key first.")
		return
	}
	if !cfg.AutobrrEnabled || cfg.AutobrrURL == "" || cfg.AutobrrAPIKey == "" {
		flash(w, r, basePath, "", "Autobrr integration is not configured.")
		return
	}

	client := autobrr.New(cfg.AutobrrURL, cfg.AutobrrAPIKey, h.log)

	// Check if Autobrr already has an indexer at this URL. If so, link to it
	// rather than creating a duplicate.
	existing, err := client.IndexerByURL(entry.TrackerURL)
	if err != nil {
		flash(w, r, basePath, "", "Autobrr indexer lookup failed: "+err.Error())
		return
	}

	var linked *autobrr.Indexer
	var linkedSettings map[string]string
	var action string
	if existing != nil {
		linked = existing
		linkedSettings = h.autobrrSettingsForLinked(entry, *linked)
		action = "linked to existing"
	} else {
		schema, err := client.SchemaForURL(entry.TrackerURL)
		if err != nil {
			flash(w, r, basePath, "", "Autobrr schema lookup failed: "+err.Error())
			return
		}
		settings := h.autobrrSettingsForNew(entry, *schema)
		var added *autobrr.Indexer
		if settings != nil {
			added, err = client.AddIndexerWithSettings(*schema, entry.TrackerURL, settings)
		} else {
			added, err = client.AddIndexer(*schema, entry.TrackerURL, entry.APIKey)
		}
		if err != nil {
			flash(w, r, basePath, "", "Autobrr add failed: "+err.Error())
			return
		}
		linked = added
		linkedSettings = settings
		if def := h.autobrrDefFor(entry, linked.Identifier); def != nil {
			linkedSettings = autobrr.MergeSettings(*def, linkedSettings, autobrr.SettingsFromPairs(linked.Settings))
		} else if linkedSettings == nil {
			linkedSettings = autobrr.SettingsFromPairs(linked.Settings)
		}
		action = "added to"
	}

	autobrrCfg := cfg.Trackers[idx].EnsureAutobrr()
	autobrrCfg.ID = int(linked.ID)
	autobrrCfg.Identifier = linked.Identifier
	autobrrCfg.Enabled = linked.Enabled
	autobrrCfg.Settings = linkedSettings
	if err := h.store.Save(cfg); err != nil {
		flash(w, r, basePath, "", "Save failed: "+err.Error())
		return
	}
	h.log.Info("CONFIG", fmt.Sprintf("%s %q in Autobrr (id=%d)", action, entry.Name, linked.ID))
	flash(w, r, basePath, entry.Name+" "+action+" Autobrr.", "")
}

// configTrackerAutobrrToggle flips Autobrr's enabled flag for this indexer
// and mirrors the resulting state locally.
func (h *Handler) configTrackerAutobrrToggle(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, "/", "", "invalid tracker index")
		return
	}
	basePath := trackerConfigPath(idx) + "/autobrr"
	entry := cfg.Trackers[idx]
	if entry.AutobrrID() == 0 {
		flash(w, r, basePath, "", entry.Name+" is not in Autobrr.")
		return
	}

	client := autobrr.New(cfg.AutobrrURL, cfg.AutobrrAPIKey, h.log)
	if err := client.SetEnabled(int64(entry.AutobrrID()), !entry.AutobrrEnabled()); err != nil {
		flash(w, r, basePath, "", "Autobrr update failed: "+err.Error())
		return
	}

	cfg.Trackers[idx].EnsureAutobrr().Enabled = !entry.AutobrrEnabled()
	if err := h.store.Save(cfg); err != nil {
		flash(w, r, basePath, "", "Save failed: "+err.Error())
		return
	}
	status := "disabled"
	if cfg.Trackers[idx].AutobrrEnabled() {
		status = "enabled"
	}
	h.log.Info("CONFIG", fmt.Sprintf("%s %s in Autobrr", entry.Name, status))
	flash(w, r, basePath, entry.Name+" "+status+" in Autobrr.", "")
}

// configTrackerAutobrrRemove deletes the indexer in Autobrr and clears the
// local AutobrrID. The dashboard tracker itself stays put.
func (h *Handler) configTrackerAutobrrRemove(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, "/", "", "invalid tracker index")
		return
	}
	basePath := trackerConfigPath(idx) + "/autobrr"
	entry := cfg.Trackers[idx]
	if entry.AutobrrID() == 0 {
		flash(w, r, basePath, "", entry.Name+" is not in Autobrr.")
		return
	}
	client := autobrr.New(cfg.AutobrrURL, cfg.AutobrrAPIKey, h.log)
	if err := client.DeleteIndexer(int64(entry.AutobrrID())); err != nil {
		flash(w, r, basePath, "", "Autobrr remove failed: "+err.Error())
		return
	}

	autobrrCfg := cfg.Trackers[idx].EnsureAutobrr()
	autobrrCfg.ID = 0
	autobrrCfg.Identifier = ""
	autobrrCfg.Enabled = false
	if err := h.store.Save(cfg); err != nil {
		flash(w, r, basePath, "", "Save failed: "+err.Error())
		return
	}
	h.log.Info("CONFIG", fmt.Sprintf("Removed %q from Autobrr", entry.Name))
	flash(w, r, basePath, entry.Name+" removed from Autobrr.", "")
}

// ── import flow ────────────────────────────────────────────────────

type autobrrImportData struct {
	Config       config.Config
	OK           bool
	LoadError    string
	Importable   []autobrrImportRow
	FlashError   string
	FlashSuccess string
	ActiveTab    string
	Section      string
}

// autobrrImportRow is a managed PTV tracker that has a matching Autobrr
// indexer (by URL) but is not yet linked (AutobrrID == 0).
type autobrrImportRow struct {
	DefinitionName string // PTV tracker definition name — form submission key
	TrackerName    string
	TrackerURL     string
	AutobrrID      int
	Identifier     string
	Enabled        bool
	Settings       map[string]string
}

// importAutobrrPage lists managed PTV trackers that have a matching Autobrr
// indexer but are not yet linked. Selecting them populates the AutobrrID and
// identifier on the existing tracker entry.
func (h *Handler) importAutobrrPage(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.Get()
	data := autobrrImportData{
		Config:       cfg,
		FlashError:   r.URL.Query().Get("err"),
		FlashSuccess: r.URL.Query().Get("ok"),
		ActiveTab:    "import",
		Section:      "integrations",
	}
	if !cfg.AutobrrEnabled || cfg.AutobrrURL == "" || cfg.AutobrrAPIKey == "" {
		data.LoadError = "Autobrr integration is not configured."
		h.render(w, "autobrr_import", data)
		return
	}
	rows, err := h.loadAutobrrImportable(&cfg)
	if err != nil {
		data.LoadError = err.Error()
		h.render(w, "autobrr_import", data)
		return
	}
	data.OK = true
	data.Importable = rows
	h.render(w, "autobrr_import", data)
}

// importAutobrrSubmit links each selected PTV tracker to its matching Autobrr
// indexer by populating AutobrrID, AutobrrIdentifier, and AutobrrEnabled on
// the existing tracker entry. No new PTV tracker entries are created.
func (h *Handler) importAutobrrSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		flash(w, r, pathAutobrrImport, "", "invalid form")
		return
	}
	cfg := h.store.Get()
	if !cfg.AutobrrEnabled || cfg.AutobrrURL == "" || cfg.AutobrrAPIKey == "" {
		flash(w, r, pathAutobrrImport, "", "Autobrr not enabled")
		return
	}
	defNames := r.Form["definition_name"]
	if len(defNames) == 0 {
		flash(w, r, pathAutobrrImport, "", "No trackers selected")
		return
	}

	rows, err := h.loadAutobrrImportable(&cfg)
	if err != nil {
		flash(w, r, pathAutobrrImport, "", "Autobrr fetch failed: "+err.Error())
		return
	}
	byDef := make(map[string]autobrrImportRow, len(rows))
	for _, row := range rows {
		byDef[strings.ToLower(row.DefinitionName)] = row
	}

	var linked []string
	for _, defName := range defNames {
		row, ok := byDef[strings.ToLower(defName)]
		if !ok {
			continue
		}
		for i, t := range cfg.Trackers {
			if strings.ToLower(t.DefinitionName) != strings.ToLower(defName) {
				continue
			}
			autobrrCfg := cfg.Trackers[i].EnsureAutobrr()
			autobrrCfg.ID = row.AutobrrID
			autobrrCfg.Identifier = row.Identifier
			autobrrCfg.Enabled = row.Enabled
			autobrrCfg.Settings = row.Settings
			linked = append(linked, t.Name)
			h.log.Info("CONFIG", fmt.Sprintf("Linked %q to Autobrr indexer id=%d", t.Name, row.AutobrrID))
			break
		}
	}
	if len(linked) == 0 {
		flash(w, r, pathAutobrrImport, "", "Nothing linked.")
		return
	}
	if err := h.store.Save(&cfg); err != nil {
		flash(w, r, pathAutobrrImport, "", "Save failed: "+err.Error())
		return
	}
	flash(w, r, "/", fmt.Sprintf("Linked %d tracker(s) to Autobrr: %s.",
		len(linked), strings.Join(linked, ", ")), "")
}

// loadAutobrrImportable finds managed PTV trackers that have no AutobrrID yet
// but have a matching indexer in Autobrr (matched by normalized base URL).
func (h *Handler) loadAutobrrImportable(cfg *config.Config) ([]autobrrImportRow, error) {
	client := autobrr.New(cfg.AutobrrURL, cfg.AutobrrAPIKey, h.log)
	idxs, err := client.GetIndexers()
	if err != nil {
		return nil, fmt.Errorf("fetch Autobrr indexers: %w", err)
	}

	byURL := make(map[string]autobrr.Indexer, len(idxs))
	for _, idx := range idxs {
		if idx.BaseURL != "" {
			byURL[autobrr.NormalizeURL(idx.BaseURL)] = idx
		}
	}

	var out []autobrrImportRow
	for _, t := range cfg.Trackers {
		if t.AutobrrID() > 0 || t.TrackerURL == "" {
			continue
		}
		idx, ok := byURL[autobrr.NormalizeURL(t.TrackerURL)]
		if !ok {
			continue
		}
		out = append(out, autobrrImportRow{
			DefinitionName: t.DefinitionName,
			TrackerName:    t.Name,
			TrackerURL:     t.TrackerURL,
			AutobrrID:      int(idx.ID),
			Identifier:     idx.Identifier,
			Enabled:        idx.Enabled,
			Settings:       h.autobrrSettingsForLinked(t, idx),
		})
	}
	return out, nil
}

func (h *Handler) autobrrSettingsForNew(t *config.TrackerEntry, schema autobrr.IndexerSchema) map[string]string {
	def := h.autobrrDefFor(t, schema.Identifier)
	if def == nil {
		return nil
	}
	settings := autobrr.MergeSettings(*def, autobrr.SettingsFromPairs(schema.Settings), nil)
	return autobrr.WithCoreCredentials(*def, settings, t.APIKey)
}

func (h *Handler) autobrrSettingsForLinked(t *config.TrackerEntry, idx autobrr.Indexer) map[string]string {
	settings := autobrr.SettingsFromPairs(idx.Settings)
	def := h.autobrrDefFor(t, idx.Identifier)
	if def == nil {
		return settings
	}
	return autobrr.MergeSettings(*def, settings, nil)
}

func (h *Handler) autobrrDefFor(t *config.TrackerEntry, identifier string) *autobrrdefs.Def {
	if h.autobrrSyncer == nil {
		return nil
	}
	if identifier != "" {
		if def := h.autobrrSyncer.ByIdentifier(identifier); def != nil {
			return def
		}
	}
	if t.AutobrrIdentifier() != "" {
		if def := h.autobrrSyncer.ByIdentifier(t.AutobrrIdentifier()); def != nil {
			return def
		}
	}
	if t.TrackerURL != "" {
		return h.autobrrSyncer.ByURL(t.TrackerURL)
	}
	return nil
}
