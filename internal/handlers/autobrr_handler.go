package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/nerney/ptv/internal/autobrr"
	"github.com/nerney/ptv/internal/config"
)

// autobrr_handler covers the Autobrr settings page, the per-tracker
// add/toggle/remove actions, and the import page.
//
// Autobrr's role in PTV is intentionally narrow: it is a destination for
// tracker credentials and a read source for IRC connection status. We do
// not manage filters, releases, IRC networks, or any other Autobrr feature.

const (
	pathConfigAutobrr = "/config/autobrr"
	pathAutobrrImport = "/config/autobrr/import"
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
		Section:      "autobrr",
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
		flash(w, r, pathConfigTrackers, "", "invalid tracker index")
		return
	}
	entry := cfg.Trackers[idx]
	if entry.TrackerURL == "" || entry.APIKey == "" {
		flash(w, r, pathConfigTrackers, "", entry.Name+": set URL and API key first.")
		return
	}
	if !cfg.AutobrrEnabled || cfg.AutobrrURL == "" || cfg.AutobrrAPIKey == "" {
		flash(w, r, pathConfigTrackers, "", "Autobrr integration is not configured.")
		return
	}

	client := autobrr.New(cfg.AutobrrURL, cfg.AutobrrAPIKey, h.log)
	schema, err := client.SchemaForURL(entry.TrackerURL)
	if err != nil {
		flash(w, r, pathConfigTrackers, "", "Autobrr schema lookup failed: "+err.Error())
		return
	}
	added, err := client.AddIndexer(*schema, entry.TrackerURL, entry.APIKey)
	if err != nil {
		flash(w, r, pathConfigTrackers, "", "Autobrr add failed: "+err.Error())
		return
	}

	cfg.Trackers[idx].AutobrrID = int(added.ID)
	cfg.Trackers[idx].AutobrrIdentifier = added.Identifier
	cfg.Trackers[idx].AutobrrEnabled = added.Enabled
	if err := h.store.Save(cfg); err != nil {
		flash(w, r, pathConfigTrackers, "", "Save failed: "+err.Error())
		return
	}
	h.log.Info("CONFIG", fmt.Sprintf("Added %q to Autobrr (id=%d)", entry.Name, added.ID))
	flash(w, r, pathConfigTrackers, entry.Name+" added to Autobrr.", "")
}

// configTrackerAutobrrToggle flips Autobrr's enabled flag for this indexer
// and mirrors the resulting state locally.
func (h *Handler) configTrackerAutobrrToggle(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, pathConfigTrackers, "", "invalid tracker index")
		return
	}
	entry := cfg.Trackers[idx]
	if entry.AutobrrID == 0 {
		flash(w, r, pathConfigTrackers, "", entry.Name+" is not in Autobrr.")
		return
	}

	client := autobrr.New(cfg.AutobrrURL, cfg.AutobrrAPIKey, h.log)
	if err := client.SetEnabled(int64(entry.AutobrrID), !entry.AutobrrEnabled); err != nil {
		flash(w, r, pathConfigTrackers, "", "Autobrr update failed: "+err.Error())
		return
	}

	cfg.Trackers[idx].AutobrrEnabled = !entry.AutobrrEnabled
	if err := h.store.Save(cfg); err != nil {
		flash(w, r, pathConfigTrackers, "", "Save failed: "+err.Error())
		return
	}
	status := "disabled"
	if cfg.Trackers[idx].AutobrrEnabled {
		status = "enabled"
	}
	h.log.Info("CONFIG", fmt.Sprintf("%s %s in Autobrr", entry.Name, status))
	flash(w, r, pathConfigTrackers, entry.Name+" "+status+" in Autobrr.", "")
}

// configTrackerAutobrrRemove deletes the indexer in Autobrr and clears the
// local AutobrrID. The dashboard tracker itself stays put.
func (h *Handler) configTrackerAutobrrRemove(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, pathConfigTrackers, "", "invalid tracker index")
		return
	}
	entry := cfg.Trackers[idx]
	if entry.AutobrrID == 0 {
		flash(w, r, pathConfigTrackers, "", entry.Name+" is not in Autobrr.")
		return
	}
	client := autobrr.New(cfg.AutobrrURL, cfg.AutobrrAPIKey, h.log)
	if err := client.DeleteIndexer(int64(entry.AutobrrID)); err != nil {
		flash(w, r, pathConfigTrackers, "", "Autobrr remove failed: "+err.Error())
		return
	}

	cfg.Trackers[idx].AutobrrID = 0
	cfg.Trackers[idx].AutobrrIdentifier = ""
	cfg.Trackers[idx].AutobrrEnabled = false
	if err := h.store.Save(cfg); err != nil {
		flash(w, r, pathConfigTrackers, "", "Save failed: "+err.Error())
		return
	}
	h.log.Info("CONFIG", fmt.Sprintf("Removed %q from Autobrr", entry.Name))
	flash(w, r, pathConfigTrackers, entry.Name+" removed from Autobrr.", "")
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

// autobrrImportRow is one importable indexer fetched from Autobrr that PTV
// doesn't yet manage. Matched to a PTV-known tracker by URL.
type autobrrImportRow struct {
	AutobrrID  int
	Name       string
	Identifier string
	BaseURL    string
	Enabled    bool
	SchemaName string // matching definition name from the PTV catalog
}

// importAutobrrPage lists Autobrr indexers that map to a tracker in PTV's
// catalog but are not yet managed locally. Selecting them creates PTV
// TrackerEntry rows with the AutobrrID already linked.
func (h *Handler) importAutobrrPage(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.Get()
	data := autobrrImportData{
		Config:       cfg,
		FlashError:   r.URL.Query().Get("err"),
		FlashSuccess: r.URL.Query().Get("ok"),
		ActiveTab:    "import",
		Section:      "autobrr",
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

// importAutobrrSubmit creates one PTV TrackerEntry per selected Autobrr
// indexer. Same skip-on-conflict semantics as the Prowlarr import.
func (h *Handler) importAutobrrSubmit(w http.ResponseWriter, r *http.Request) {
	if !h.defsReady(w, r, pathAutobrrImport) {
		return
	}
	if err := r.ParseForm(); err != nil {
		flash(w, r, pathAutobrrImport, "", "invalid form")
		return
	}
	cfg := h.store.Get()
	if !cfg.AutobrrEnabled || cfg.AutobrrURL == "" || cfg.AutobrrAPIKey == "" {
		flash(w, r, pathAutobrrImport, "", "Autobrr not enabled")
		return
	}
	idStrs := r.Form["autobrr_id"]
	if len(idStrs) == 0 {
		flash(w, r, pathAutobrrImport, "", "No trackers selected")
		return
	}

	_, typeMap, err := h.catalogMaps()
	if err != nil {
		flash(w, r, pathAutobrrImport, "", "Catalog unavailable: "+err.Error())
		return
	}

	rows, err := h.loadAutobrrImportable(&cfg)
	if err != nil {
		flash(w, r, pathAutobrrImport, "", "Autobrr fetch failed: "+err.Error())
		return
	}
	byID := map[int]autobrrImportRow{}
	for _, row := range rows {
		byID[row.AutobrrID] = row
	}

	already := managedSet(cfg.Trackers)
	var imported []string
	for _, idStr := range idStrs {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		row, ok := byID[id]
		if !ok {
			continue
		}
		if already[strings.ToLower(row.SchemaName)] {
			continue
		}
		entry := &config.TrackerEntry{
			DefinitionName:    row.SchemaName,
			TrackerType:       typeMap[strings.ToLower(row.SchemaName)],
			Name:              row.Name,
			TrackerURL:        row.BaseURL,
			AutobrrID:         row.AutobrrID,
			AutobrrIdentifier: row.Identifier,
			AutobrrEnabled:    row.Enabled,
		}
		cfg.Trackers = append(cfg.Trackers, entry)
		already[strings.ToLower(entry.DefinitionName)] = true
		imported = append(imported, entry.Name)
		h.log.Info("CONFIG", fmt.Sprintf("Imported %q from Autobrr", entry.Name))
		h.discoverBrandingAsync(entry.DefinitionName, entry.TrackerURL)
	}
	if len(imported) == 0 {
		flash(w, r, pathAutobrrImport, "", "Nothing imported.")
		return
	}
	if err := h.store.Save(&cfg); err != nil {
		flash(w, r, pathAutobrrImport, "", "Save failed: "+err.Error())
		return
	}
	flash(w, r, "/", fmt.Sprintf("Imported %d tracker(s) from Autobrr: %s.",
		len(imported), strings.Join(imported, ", ")), "")
}

// loadAutobrrImportable fetches Autobrr indexers, cross-references them
// against the PTV catalog by URL, and filters out anything PTV already
// manages.
func (h *Handler) loadAutobrrImportable(cfg *config.Config) ([]autobrrImportRow, error) {
	urlToName, _, err := h.catalogMaps()
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	client := autobrr.New(cfg.AutobrrURL, cfg.AutobrrAPIKey, h.log)
	configured, err := client.GetIndexers()
	if err != nil {
		return nil, fmt.Errorf("fetch Autobrr indexers: %w", err)
	}
	already := managedSet(cfg.Trackers)

	var out []autobrrImportRow
	for _, idx := range configured {
		schemaName, inCatalog := urlToName[autobrr.NormalizeURL(idx.BaseURL)]
		if idx.BaseURL == "" || !inCatalog || already[strings.ToLower(schemaName)] {
			continue
		}
		out = append(out, autobrrImportRow{
			AutobrrID:  int(idx.ID),
			Name:       idx.Name,
			Identifier: idx.Identifier,
			BaseURL:    idx.BaseURL,
			Enabled:    idx.Enabled,
			SchemaName: schemaName,
		})
	}
	return out, nil
}
