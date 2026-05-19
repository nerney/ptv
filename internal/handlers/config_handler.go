package handlers

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/nerney/ptv/internal/config"
	"github.com/nerney/ptv/internal/defs"
	"github.com/nerney/ptv/internal/prowlarr"
)

// config_handler covers the /config card landing, the Trackers tab, and
// the Prowlarr settings tab. Larger sub-features (import, sync, network)
// live in dedicated files. Per-tracker actions also live here because
// they share the trackerIndex() helper.

// ── page data types ────────────────────────────────────────────────────────

type configLandingData struct {
	HasTrackers     bool
	TrackerCount    int
	ProwlarrEnabled bool
	ProwlarrSet     bool
	AutobrrEnabled  bool
	AutobrrSet      bool
	DefsState       defs.State
	DefsMsg         string
	FlashError      string
	FlashSuccess    string
}

// configRow is a unified row type so the trackers config page can render
// configured-and-available trackers in one alphabetical list. Configured
// rows have Tracker != nil and a valid TrackerIdx.
type configRow struct {
	Name       string
	TypeID     string // trackertype.Type.ID() — e.g. "unit3d"
	URLs       []string
	Configured bool
	Tracker    *config.TrackerEntry
	TrackerIdx int
}

type configTrackersData struct {
	Rows            []configRow
	ProwlarrEnabled bool
	ProwlarrSet     bool
	AutobrrEnabled  bool
	AutobrrSet      bool
	DefsState       defs.State
	DefsMsg         string
	LoadError       string
	FlashError      string
	FlashSuccess    string
	Section         string // for the shared config_nav partial
}

type configProwlarrData struct {
	Config       config.Config
	FlashError   string
	FlashSuccess string
	ActiveTab    string // "settings" | "import" | "sync"
	Section      string
}

// Redirect paths used by the flash() helper. Centralized so callers
// don't sprinkle string literals.
const (
	pathConfig         = "/config"
	pathConfigTrackers = "/trackers/add"
	pathConfigProwlarr = "/config/integrations/prowlarr"
)

// ── /config landing ────────────────────────────────────────────────────────

// configLanding shows the three-card menu (Trackers / Prowlarr / Network).
// It's intentionally low-content — high-density work happens on the
// sub-pages.
func (h *Handler) configLanding(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.Get()
	data := configLandingData{
		HasTrackers:     len(cfg.Trackers) > 0,
		TrackerCount:    len(cfg.Trackers),
		ProwlarrEnabled: cfg.ProwlarrEnabled,
		ProwlarrSet:     cfg.ProwlarrURL != "" && cfg.ProwlarrAPIKey != "",
		AutobrrEnabled:  cfg.AutobrrEnabled,
		AutobrrSet:      cfg.AutobrrURL != "" && cfg.AutobrrAPIKey != "",
		FlashError:      r.URL.Query().Get("err"),
		FlashSuccess:    r.URL.Query().Get("ok"),
	}
	data.DefsState, data.DefsMsg = h.syncer.Status()
	h.render(w, r, "config_landing", data)
}

// ── /config/trackers ──────────────────────────────────────────────────────────

// configTrackersPage renders the unified Trackers list: every UNIT3D
// tracker from the YAML catalog, with the user's configured ones at the
// top. Each row is a <details> element — configured rows show update
// forms; available rows show an add-with-validation form.
func (h *Handler) configTrackersPage(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.Get()

	data := configTrackersData{
		ProwlarrEnabled: cfg.ProwlarrEnabled,
		ProwlarrSet:     cfg.ProwlarrURL != "" && cfg.ProwlarrAPIKey != "",
		AutobrrEnabled:  cfg.AutobrrEnabled,
		AutobrrSet:      cfg.AutobrrURL != "" && cfg.AutobrrAPIKey != "",
		FlashError:      r.URL.Query().Get("err"),
		FlashSuccess:    r.URL.Query().Get("ok"),
		Section:         "trackers",
	}
	data.DefsState, data.DefsMsg = h.syncer.Status()

	// Catalog is unavailable in two states: initial-loading (transient)
	// and permanent-failure. Both are surfaced via DefsState — the
	// template renders a banner accordingly.
	allDefs := h.catalogIfReady(data.DefsState, &data.LoadError)
	data.Rows = buildConfigRows(cfg.Trackers, allDefs)

	h.render(w, r, "config_trackers", data)
}

// catalogIfReady returns the parsed catalog if the syncer has it, or
// empty + sets loadErr otherwise. Splits the syncer's "loading" /
// "stale" / "failed" states down to a simple boolean for the template.
func (h *Handler) catalogIfReady(state defs.State, loadErr *string) []defs.TrackerDef {
	if state != defs.StateOK && state != defs.StateStalePullFailed {
		return nil
	}
	all, err := h.syncer.Catalog()
	if err != nil {
		*loadErr = "catalog parse error: " + err.Error()
		return nil
	}
	return all
}

// buildConfigRows produces the sorted, deduplicated row list for the
// Trackers page. Configured rows always come first (alphabetical), then
// the remaining catalog entries (also alphabetical).
func buildConfigRows(trackers []*config.TrackerEntry, allDefs []defs.TrackerDef) []configRow {
	catalogByName := map[string]defs.TrackerDef{}
	for _, d := range allDefs {
		catalogByName[strings.ToLower(d.Name)] = d
	}

	var configured []configRow
	managedKeys := map[string]bool{}
	for i, t := range trackers {
		key := strings.ToLower(t.DefinitionName)
		managedKeys[key] = true
		row := configRow{
			Name:       t.Name,
			TypeID:     t.TrackerType,
			Configured: true,
			Tracker:    t,
			TrackerIdx: i,
		}
		if d, ok := catalogByName[key]; ok {
			row.URLs = d.URLs
			if row.TypeID == "" {
				row.TypeID = d.TypeID
			}
		}
		configured = append(configured, row)
	}
	sort.Slice(configured, func(i, j int) bool {
		return strings.ToLower(configured[i].Name) < strings.ToLower(configured[j].Name)
	})

	var available []configRow
	for _, d := range allDefs {
		if managedKeys[strings.ToLower(d.Name)] {
			continue
		}
		available = append(available, configRow{Name: d.Name, TypeID: d.TypeID, URLs: d.URLs})
	}
	sort.Slice(available, func(i, j int) bool {
		return strings.ToLower(available[i].Name) < strings.ToLower(available[j].Name)
	})

	return append(configured, available...)
}

// ── /config/prowlarr (settings tab) ───────────────────────────────────────────────────

// configProwlarrPage renders the Prowlarr connection settings. If the
// URL field is empty, we pre-fill a sensible Docker-network default —
// most users have prowlarr on the same docker-compose stack.
func (h *Handler) configProwlarrPage(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.Get()
	if cfg.ProwlarrURL == "" {
		cfg.ProwlarrURL = "http://prowlarr:9696"
	}
	h.render(w, r, "config_prowlarr", configProwlarrData{
		Config:       cfg,
		FlashError:   r.URL.Query().Get("err"),
		FlashSuccess: r.URL.Query().Get("ok"),
		ActiveTab:    "settings",
		Section:      "integrations",
	})
}

// configProwlarrPost saves Prowlarr URL + API key. We Ping() the
// instance before persisting so the user gets immediate feedback on
// bad credentials, rather than discovering the failure on the next
// import attempt.
//
// Empty API key in the form means "keep existing" — lets the user save
// just a URL change without retyping the key (which the form never
// echoes back, by design).
func (h *Handler) configProwlarrPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		flash(w, r, pathConfigProwlarr, "", "invalid form")
		return
	}
	prowlarrURL := strings.TrimSpace(r.FormValue("url"))
	apiKey := strings.TrimSpace(r.FormValue("api_key"))
	if prowlarrURL == "" {
		flash(w, r, pathConfigProwlarr, "", "URL is required.")
		return
	}

	cfg := h.store.Get()
	if apiKey == "" {
		if cfg.ProwlarrAPIKey == "" {
			flash(w, r, pathConfigProwlarr, "", "API key is required.")
			return
		}
		apiKey = cfg.ProwlarrAPIKey
	}

	h.log.Info("CONFIG", "Saving Prowlarr settings — testing connection")
	client := prowlarr.New(prowlarrURL, apiKey, h.log)
	if err := client.Ping(); err != nil {
		h.log.Err("CONFIG", "Prowlarr ping failed: "+err.Error())
		flash(w, r, pathConfigProwlarr, "", "Cannot reach Prowlarr: "+err.Error())
		return
	}

	cfg.ProwlarrURL = prowlarrURL
	cfg.ProwlarrAPIKey = apiKey
	cfg.ProwlarrEnabled = true
	if err := h.store.Save(&cfg); err != nil {
		flash(w, r, pathConfigProwlarr, "", "Save failed: "+err.Error())
		return
	}
	h.invalidateProwlarrMetadataCache()
	h.log.Info("CONFIG", "Prowlarr settings saved")
	go h.warmProwlarrSchemas()
	flash(w, r, pathConfigProwlarr, "Prowlarr settings saved.", "")
}

// configProwlarrEnable / configProwlarrDisable flip the boolean without
// touching credentials. Disabling preserves URL+key so re-enabling
// doesn't require retyping.
func (h *Handler) configProwlarrEnable(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.Get()
	cfg.ProwlarrEnabled = true
	if err := h.store.Save(&cfg); err != nil {
		flash(w, r, pathConfigProwlarr, "", "Save failed: "+err.Error())
		return
	}
	h.log.Info("CONFIG", "Prowlarr integration enabled")
	go h.warmProwlarrSchemas()
	flash(w, r, pathConfigProwlarr, "Prowlarr integration enabled.", "")
}

func (h *Handler) configProwlarrDisable(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.Get()
	cfg.ProwlarrEnabled = false
	if err := h.store.Save(&cfg); err != nil {
		flash(w, r, pathConfigProwlarr, "", "Save failed: "+err.Error())
		return
	}
	h.log.Info("CONFIG", "Prowlarr integration disabled (credentials preserved)")
	flash(w, r, pathConfigProwlarr, "Prowlarr integration disabled.", "")
}

// ── add from catalog ──────────────────────────────────────────────────────────

// configAdd is the "add a new tracker from the YAML catalog" path.
// Default behavior: validate URL+key against UNIT3D /api/user before
// saving, so the saved row is known-good. With override=true (FORCE ADD
// button), we skip validation and store the row with empty stats —
// used when the user knows their tracker is temporarily unreachable
// but they want it in the config.
func (h *Handler) configAdd(w http.ResponseWriter, r *http.Request) {
	if !h.defsReady(w, r, pathConfigTrackers) {
		return
	}
	if err := r.ParseForm(); err != nil {
		flash(w, r, pathConfigTrackers, "", "invalid form")
		return
	}
	schemaName := strings.TrimSpace(r.FormValue("schema_name"))
	trackerURL := strings.TrimSpace(r.FormValue("url"))
	apiKey := strings.TrimSpace(r.FormValue("api_key"))
	trackerTypeID := strings.TrimSpace(r.FormValue("tracker_type"))
	override := r.FormValue("override") == "true"

	if schemaName == "" || trackerURL == "" || apiKey == "" {
		flash(w, r, pathConfigTrackers, "", "Tracker, URL, and API key are all required.")
		return
	}

	cfg := h.store.Get()
	// Reject duplicates by definition name — case-insensitive match
	// because catalog and prowlarr-import variants of the same tracker
	// can have casing differences.
	for _, t := range cfg.Trackers {
		if strings.EqualFold(t.DefinitionName, schemaName) {
			flash(w, r, pathConfigTrackers, "", schemaName+" is already managed.")
			return
		}
	}

	entry := &config.TrackerEntry{
		DefinitionName: schemaName,
		TrackerType:    trackerTypeID,
		Name:           schemaName,
		TrackerURL:     trackerURL,
		APIKey:         apiKey,
	}

	if !override {
		stats, vErr := h.validateTracker(trackerTypeID, trackerURL, apiKey)
		if vErr != nil {
			h.log.Err("CONFIG", fmt.Sprintf("Validation failed for %q: %s", schemaName, vErr.Error()))
			flash(w, r, pathConfigTrackers, "", fmt.Sprintf(
				"%s validation failed: %s — use FORCE ADD to save without validation, or fix credentials and retry.",
				schemaName, vErr.Error()))
			return
		}
		now := time.Now()
		entry.UserStats = stats
		entry.LastSync = &now
	}

	cfg.Trackers = append(cfg.Trackers, entry)
	if err := h.store.Save(&cfg); err != nil {
		flash(w, r, pathConfigTrackers, "", "Save failed: "+err.Error())
		return
	}
	h.log.Info("CONFIG", fmt.Sprintf("Added %q from catalog (override=%v, validated=%v)",
		schemaName, override, entry.UserStats != nil))
	h.discoverBrandingAsync(entry.DefinitionName, entry.TrackerURL)
	flash(w, r, pathConfigTrackers, "Added "+schemaName+".", "")
}

// ── per-tracker actions ──────────────────────────────────────────────────────────

// trackerIndex parses the {idx} URL param and returns the entry alongside
// a snapshot of the current Config. Returns ok=false to signal "send a
// flash error and bail" — the caller doesn't need to log details, every
// invalid index here would be a tampered URL.
func (h *Handler) trackerIndex(r *http.Request) (int, *config.Config, bool) {
	idx, err := strconv.Atoi(chi.URLParam(r, "idx"))
	if err != nil {
		return 0, nil, false
	}
	cfg := h.store.Get()
	if idx < 0 || idx >= len(cfg.Trackers) {
		return 0, nil, false
	}
	return idx, &cfg, true
}

// configTrackerUpdate accepts URL and API-key edits. Either field may
// be blank to mean "keep existing". Always validates; if validation fails,
// renders the config page with error and "Save Anyway" option.
func (h *Handler) configTrackerUpdate(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, "/", "", "invalid tracker index")
		return
	}
	if err := r.ParseForm(); err != nil {
		flash(w, r, trackerConfigPath(idx), "", "invalid form")
		return
	}

	forceSave := r.FormValue("force_save") == "true"
	newURL := strings.TrimSpace(r.FormValue("url"))
	newKey := strings.TrimSpace(r.FormValue("api_key"))

	// "Keep existing on blank" — applied after trim
	effURL := cfg.Trackers[idx].TrackerURL
	effKey := cfg.Trackers[idx].APIKey
	if newURL != "" {
		effURL = newURL
	}
	if newKey != "" {
		effKey = newKey
	}
	name := cfg.Trackers[idx].Name

	// Always validate unless force saving
	if !forceSave {
		stats, vErr := h.validateTracker(cfg.Trackers[idx].TrackerType, effURL, effKey)
		if vErr != nil {
			h.log.Err("CONFIG", fmt.Sprintf("Validation failed for %q: %s", name, vErr.Error()))
			// Render the config page with validation error, preserving user input
			h.renderTrackerConfigWithError(w, r, idx, cfg, newURL, newKey, vErr.Error())
			return
		}
		now := time.Now()
		cfg.Trackers[idx].UserStats = stats
		cfg.Trackers[idx].LastSync = &now
		cfg.Trackers[idx].SyncError = ""
	} else {
		// Save anyway: clear stats since we're not validating
		cfg.Trackers[idx].UserStats = nil
		cfg.Trackers[idx].LastSync = nil
		cfg.Trackers[idx].SyncError = ""
	}

	cfg.Trackers[idx].TrackerURL = effURL
	cfg.Trackers[idx].APIKey = effKey

	if err := h.store.Save(cfg); err != nil {
		flash(w, r, trackerConfigPath(idx), "", "Save failed: "+err.Error())
		return
	}
	h.log.Info("CONFIG", fmt.Sprintf("Updated tracker %q credentials (forceSave=%v)", name, forceSave))
	h.discoverBrandingAsync(cfg.Trackers[idx].DefinitionName, cfg.Trackers[idx].TrackerURL)
	flash(w, r, trackerConfigPath(idx), name+" updated.", "")
}

// configTrackerProwlarrAdd creates a new indexer in Prowlarr from the
// tracker's stored credentials. The Prowlarr ID Prowlarr returns is
// then persisted so subsequent toggle/remove operations can target it.
func (h *Handler) configTrackerProwlarrAdd(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, "/", "", "invalid tracker index")
		return
	}
	basePath := trackerConfigPath(idx)
	entry := cfg.Trackers[idx]
	if entry.TrackerURL == "" || entry.APIKey == "" {
		flash(w, r, basePath, "", entry.Name+": set URL and API key first.")
		return
	}

	client := prowlarr.New(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, h.log)
	schema, err := client.SchemaByName(entry.DefinitionName)
	if err != nil {
		flash(w, r, basePath, "", "Schema not found in Prowlarr: "+err.Error())
		return
	}
	if entry.ProwlarrSettings() == nil {
		cfg.Trackers[idx].EnsureProwlarr().Settings = prowlarr.MergeSettings(*schema, nil, nil)
	}
	if err := h.pushTrackerProwlarrConfig(cfg, idx, *schema); err != nil {
		flash(w, r, basePath, "", "Prowlarr add failed: "+err.Error())
		return
	}

	if err := h.store.Save(cfg); err != nil {
		flash(w, r, basePath, "", "Save failed: "+err.Error())
		return
	}
	h.log.Info("CONFIG", fmt.Sprintf("Added %q to Prowlarr (id=%d)", entry.Name, cfg.Trackers[idx].ProwlarrID()))
	flash(w, r, basePath, entry.Name+" added to Prowlarr.", "")
}

// configTrackerProwlarrToggle flips Prowlarr's enable flag for this
// indexer. The dashboard mirrors the resulting state.
func (h *Handler) configTrackerProwlarrToggle(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, "/", "", "invalid tracker index")
		return
	}
	basePath := trackerConfigPath(idx)
	entry := cfg.Trackers[idx]
	if entry.ProwlarrID() == 0 {
		flash(w, r, basePath, "", entry.Name+" is not in Prowlarr.")
		return
	}

	client := prowlarr.New(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, h.log)
	indexer, err := client.GetIndexer(entry.ProwlarrID())
	if err != nil {
		flash(w, r, basePath, "", "Prowlarr fetch failed: "+err.Error())
		return
	}
	if err := client.SetEnabled(*indexer, !entry.Enabled); err != nil {
		flash(w, r, basePath, "", "Prowlarr update failed: "+err.Error())
		return
	}

	cfg.Trackers[idx].Enabled = !entry.Enabled
	if err := h.store.Save(cfg); err != nil {
		flash(w, r, basePath, "", "Save failed: "+err.Error())
		return
	}
	status := "disabled"
	if cfg.Trackers[idx].Enabled {
		status = "enabled"
	}
	h.log.Info("CONFIG", fmt.Sprintf("%s %s in Prowlarr", entry.Name, status))
	flash(w, r, basePath, entry.Name+" "+status+" in Prowlarr.", "")
}

// configTrackerProwlarrRemove deletes the indexer in Prowlarr and
// clears the local ProwlarrID. We do NOT delete the tracker from the
// dashboard — only the Prowlarr linkage is broken.
func (h *Handler) configTrackerProwlarrRemove(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, "/", "", "invalid tracker index")
		return
	}
	basePath := trackerConfigPath(idx)
	entry := cfg.Trackers[idx]
	if entry.ProwlarrID() == 0 {
		flash(w, r, basePath, "", entry.Name+" is not in Prowlarr.")
		return
	}
	client := prowlarr.New(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, h.log)
	if err := client.DeleteIndexer(entry.ProwlarrID()); err != nil {
		flash(w, r, basePath, "", "Prowlarr remove failed: "+err.Error())
		return
	}

	cfg.Trackers[idx].Enabled = false
	prowlarrCfg := cfg.Trackers[idx].EnsureProwlarr()
	prowlarrCfg.ID = 0
	prowlarrCfg.Name = ""
	prowlarrCfg.AppProfileID = 0
	prowlarrCfg.Tags = nil
	if err := h.store.Save(cfg); err != nil {
		flash(w, r, basePath, "", "Save failed: "+err.Error())
		return
	}
	h.log.Info("CONFIG", fmt.Sprintf("Removed %q from Prowlarr", entry.Name))
	flash(w, r, basePath, entry.Name+" removed from Prowlarr.", "")
}

// configTrackerDelete removes a tracker from the dashboard. Prowlarr
// state is left untouched — the operator can clean that up separately
// via REMOVE FROM PROWLARR.
func (h *Handler) configTrackerDelete(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, "/", "", "invalid tracker index")
		return
	}
	basePath := trackerConfigPath(idx)
	name := cfg.Trackers[idx].Name
	cfg.Trackers = append(cfg.Trackers[:idx], cfg.Trackers[idx+1:]...)
	if err := h.store.Save(cfg); err != nil {
		flash(w, r, basePath, "", "Save failed: "+err.Error())
		return
	}
	h.log.Info("CONFIG", fmt.Sprintf("Deleted %q from dashboard", name))
	flash(w, r, "/", name+" removed from dashboard.", "")
}

// ── shared helpers (used by sibling files too) ───────────────────────────────────────────────

// managedSet returns a lowercase-keyed set of DefinitionNames already
// in the dashboard config. Used to mark catalog entries as "already
// managed" in the trackers UI and in import-flow dedup.
func managedSet(trackers []*config.TrackerEntry) map[string]bool {
	m := map[string]bool{}
	for _, t := range trackers {
		m[strings.ToLower(t.DefinitionName)] = true
	}
	return m
}

// buildURLMap maps every catalog URL → tracker name. Used to detect
// "this Prowlarr indexer is one of our UNIT3D trackers, but renamed".
// First-wins on collisions (which matter for multi-URL trackers).
func buildURLMap(catalog []defs.TrackerDef) map[string]string {
	out := map[string]string{}
	for _, d := range catalog {
		for _, u := range d.URLs {
			n := prowlarr.NormalizeURL(u)
			if _, exists := out[n]; !exists {
				out[n] = d.Name
			}
		}
	}
	return out
}

// defsReady blocks briefly waiting for the defs sync. On timeout it
// flashes an error to redirectBase and returns false — caller bails.
// This exists because the syncer is async on first boot: the user can
// hit /config before the GitHub clone finishes.
func (h *Handler) defsReady(w http.ResponseWriter, r *http.Request, redirectBase string) bool {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := h.syncer.WaitReady(ctx); err != nil {
		flash(w, r, redirectBase, "", "Indexer definitions unavailable — try again shortly.")
		return false
	}
	return true
}

// ── importable list (shared with import + sync handlers) ──────────────────────────────────────────

// configuredImport is the row type for the Prowlarr Import page: a
// Prowlarr indexer that matches a catalog entry but isn't yet managed
// by this dashboard.
type configuredImport struct {
	Name       string
	TrackerURL string
	HasKey     bool
	ProwlarrID int
	Enabled    bool
}

// loadImportable queries Prowlarr, cross-references each indexer
// against the YAML catalog, filters out ones we already manage, and
// returns the importable remainder.
func (h *Handler) loadImportable(client *prowlarr.Client, cfg *config.Config) ([]configuredImport, error) {
	allDefs, err := h.syncer.Catalog()
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	urlToName := buildURLMap(allDefs)

	configured, err := client.GetIndexers()
	if err != nil {
		return nil, fmt.Errorf("fetch Prowlarr indexers: %w", err)
	}
	h.log.Info("CONFIG", fmt.Sprintf("Prowlarr has %d configured indexer(s)", len(configured)))

	already := managedSet(cfg.Trackers)

	var out []configuredImport
	for _, idx := range configured {
		idxURL, key := prowlarr.ExtractCreds(idx.Fields)
		if idxURL == "" && len(idx.IndexerUrls) > 0 {
			idxURL = idx.IndexerUrls[0]
		}
		schemaName, inCatalog := urlToName[prowlarr.NormalizeURL(idxURL)]
		if !inCatalog || already[strings.ToLower(schemaName)] {
			continue
		}
		out = append(out, configuredImport{
			Name:       idx.Name,
			TrackerURL: idxURL,
			HasKey:     key != "",
			ProwlarrID: idx.ID,
			Enabled:    idx.Enable,
		})
	}
	h.log.Info("CONFIG", fmt.Sprintf("Importable from Prowlarr: %d", len(out)))
	return out, nil
}
