package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nerney/ptv/internal/config"
	"github.com/nerney/ptv/internal/prowlarr"
)

// prowlarr_sync_handler covers the /config/prowlarr/sync page — the
// reverse of the import flow. The dashboard is the source of truth;
// this page pushes its configured trackers into Prowlarr (creates
// missing ones, updates drifted ones, leaves synced ones alone).

const pathProwlarrSync = "/sync/prowlarr"

// syncState classifies each managed tracker for the comparison view.
type syncState string

const (
	syncSynced syncState = "synced" // dashboard and Prowlarr match
	syncNew    syncState = "new"    // dashboard has it, Prowlarr does not
	syncDrift  syncState = "drift"  // both have it but configs differ
)

// prowlarrSyncRow is one row in the sync comparison table.
type prowlarrSyncRow struct {
	TrackerIdx     int
	Name           string
	TrackerURL     string
	ProwlarrID     int
	State          syncState
	ProwlarrURL    string // Prowlarr's current value (relevant on drift)
	ProwlarrHasKey bool   // whether Prowlarr has an API key set
	DiffFields     []string
	SchemaError    string
}

type prowlarrSyncData struct {
	ProwlarrEnabled bool
	LoadError       string
	FlashError      string
	FlashSuccess    string
	Synced          []prowlarrSyncRow
	New             []prowlarrSyncRow
	Drift           []prowlarrSyncRow
	ActiveTab       string
	Section         string
}

// ── GET /config/prowlarr/sync ──────────────────────────────────────────────────────

// prowlarrSyncPage queries Prowlarr once, builds the synced/new/drift
// buckets, and renders the comparison view. The buckets drive the
// template's three-section layout.
func (h *Handler) prowlarrSyncPage(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.Get()
	data := prowlarrSyncData{
		FlashError:   r.URL.Query().Get("err"),
		FlashSuccess: r.URL.Query().Get("ok"),
		ActiveTab:    "sync",
		Section:      "",
	}
	data.ProwlarrEnabled = cfg.ProwlarrEnabled && cfg.ProwlarrURL != "" && cfg.ProwlarrAPIKey != ""

	if !data.ProwlarrEnabled {
		h.render(w, "prowlarr_sync", data)
		return
	}

	client := prowlarr.New(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, h.log)
	prowlarrIndexers, err := client.GetIndexers()
	if err != nil {
		data.LoadError = "Failed to fetch Prowlarr indexers: " + err.Error()
		h.render(w, "prowlarr_sync", data)
		return
	}

	byID := indexersByID(prowlarrIndexers)
	for i, t := range cfg.Trackers {
		row := h.classifyTracker(i, t, byID)
		switch row.State {
		case syncNew:
			data.New = append(data.New, row)
		case syncDrift:
			data.Drift = append(data.Drift, row)
		case syncSynced:
			data.Synced = append(data.Synced, row)
		}
	}

	h.log.Info("CONFIG", fmt.Sprintf("Prowlarr sync state: %d synced, %d new, %d drift",
		len(data.Synced), len(data.New), len(data.Drift)))
	h.render(w, "prowlarr_sync", data)
}

// indexersByID indexes the Prowlarr list by ID so classifyTracker can
// do O(1) lookups instead of nested scans.
func indexersByID(idxs []prowlarr.Indexer) map[int]prowlarr.Indexer {
	out := make(map[int]prowlarr.Indexer, len(idxs))
	for _, idx := range idxs {
		out[idx.ID] = idx
	}
	return out
}

// classifyTracker decides which sync bucket a tracker belongs to. A
// stored ProwlarrID that no longer exists in Prowlarr is treated as
// "new" — the next push will recreate it and overwrite the stale ID.
func (h *Handler) classifyTracker(i int, t *config.TrackerEntry, byID map[int]prowlarr.Indexer) prowlarrSyncRow {
	row := prowlarrSyncRow{
		TrackerIdx: i,
		Name:       t.Name,
		TrackerURL: t.TrackerURL,
		ProwlarrID: t.ProwlarrID(),
	}
	if t.ProwlarrID() == 0 {
		row.State = syncNew
		return row
	}
	idx, exists := byID[t.ProwlarrID()]
	if !exists {
		row.State = syncNew
		return row
	}
	pURL, pKey := prowlarr.ExtractCreds(idx.Fields)
	if pURL == "" && len(idx.IndexerUrls) > 0 {
		pURL = idx.IndexerUrls[0]
	}
	row.ProwlarrURL = pURL
	row.ProwlarrHasKey = pKey != ""
	schema, err := h.prowlarrSchemaByName(t.DefinitionName)
	if err != nil {
		row.State = syncDrift
		row.SchemaError = err.Error()
		return row
	}
	desired := prowlarr.WithCoreCredentials(*schema, t.ProwlarrSettings(), t.TrackerURL, t.APIKey)
	actual := prowlarr.SettingsFromFields(*schema, idx.Fields)
	root := h.desiredProwlarrRootForCompare(t, *schema)
	actualRoot := prowlarr.RootConfigFromIndexer(idx)
	if !prowlarr.SettingsEqual(*schema, desired, actual) {
		row.State = syncDrift
		row.DiffFields = prowlarr.DiffSettings(*schema, desired, actual)
	} else if !prowlarr.RootConfigsEqual(root, actualRoot) {
		row.State = syncDrift
		row.DiffFields = prowlarr.DiffRootConfig(root, actualRoot)
	} else {
		row.State = syncSynced
	}
	return row
}

// ── POST /config/prowlarr/sync ────────────────────────────────────────────────────────

// prowlarrSyncSubmit pushes the selected dashboard rows into Prowlarr.
// For each tracker:
//   - if it has no ProwlarrID (or has one Prowlarr no longer knows about),
//     create a new indexer via AddIndexer (forceSave=true);
//   - otherwise update via UpdateIndexer (also forceSave=true).
//
// forceSave bypasses Prowlarr's own connection test — the dashboard has
// already validated the credentials, so a second test would just double
// the round trips.
func (h *Handler) prowlarrSyncSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		flash(w, r, pathProwlarrSync, "", "invalid form")
		return
	}
	cfg := h.store.Get()
	if !cfg.ProwlarrEnabled || cfg.ProwlarrURL == "" || cfg.ProwlarrAPIKey == "" {
		flash(w, r, pathProwlarrSync, "", "Prowlarr not enabled")
		return
	}

	idxStrs := r.Form["tracker_idx"]
	if len(idxStrs) == 0 {
		flash(w, r, pathProwlarrSync, "", "No trackers selected")
		return
	}

	client := prowlarr.New(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, h.log)
	prowlarrIndexers, err := client.GetIndexers()
	if err != nil {
		flash(w, r, pathProwlarrSync, "", "Failed to fetch Prowlarr indexers: "+err.Error())
		return
	}
	byID := indexersByID(prowlarrIndexers)

	pushed, failures, dirty := h.runSyncPush(&cfg, client, byID, idxStrs)
	if dirty {
		if err := h.store.Save(&cfg); err != nil {
			flash(w, r, pathProwlarrSync, "", "Save failed: "+err.Error())
			return
		}
	}
	flashSyncResult(w, r, pushed, failures)
}

// runSyncPush iterates the selected indices and pushes each tracker
// into Prowlarr (create or update as appropriate). Returns the
// successes, failures, and whether the local cfg was mutated and
// needs persisting.
func (h *Handler) runSyncPush(
	cfg *config.Config,
	client *prowlarr.Client,
	byID map[int]prowlarr.Indexer,
	idxStrs []string,
) (pushed, failures []string, dirty bool) {
	for _, idxStr := range idxStrs {
		i, err := strconv.Atoi(idxStr)
		if err != nil || i < 0 || i >= len(cfg.Trackers) {
			continue
		}
		t := cfg.Trackers[i]
		if t.TrackerURL == "" || t.APIKey == "" {
			failures = append(failures, t.Name+" (missing URL or API key)")
			continue
		}

		action, pushErr := h.pushTrackerToProwlarr(cfg, i, client, byID)
		if pushErr != nil {
			h.log.Err("CONFIG", fmt.Sprintf("Prowlarr sync failed for %q: %s", t.Name, pushErr.Error()))
			failures = append(failures, fmt.Sprintf("%s (%s)", t.Name, pushErr.Error()))
			continue
		}
		dirty = true
		h.log.Info("CONFIG", fmt.Sprintf("Prowlarr %s: %q", action, t.Name))
		pushed = append(pushed, t.Name)
	}
	return pushed, failures, dirty
}

// pushTrackerToProwlarr is the create-or-update decision for one
// tracker. Returns the action taken ("created"/"updated") for logging,
// or a non-nil error if the push failed.
func (h *Handler) pushTrackerToProwlarr(
	cfg *config.Config,
	i int,
	client *prowlarr.Client,
	byID map[int]prowlarr.Indexer,
) (action string, err error) {
	t := cfg.Trackers[i]
	schema, sErr := h.prowlarrSchemaByName(t.DefinitionName)
	if sErr != nil {
		return "pushed", fmt.Errorf("schema lookup: %w", sErr)
	}
	settings := prowlarr.WithCoreCredentials(*schema, t.ProwlarrSettings(), t.TrackerURL, t.APIKey)
	fields := prowlarr.FieldsForPayload(*schema, settings)
	root, rootErr := h.prowlarrRootConfig(cfg, i, *schema, client)
	if rootErr != nil {
		return "pushed", rootErr
	}

	// Update path: dashboard has a Prowlarr ID AND Prowlarr still has it.
	if t.ProwlarrID() != 0 {
		if existing, ok := byID[t.ProwlarrID()]; ok {
			updated, uErr := client.UpdateIndexerWithRoot(existing, fields, root)
			if uErr != nil {
				return "updated", uErr
			}
			updated, uErr = h.ensureProwlarrEnabled(client, updated, root.Enable)
			if uErr != nil {
				return "updated", uErr
			}
			cfg.Trackers[i].Enabled = updated.Enable
			prowlarrCfg := cfg.Trackers[i].EnsureProwlarr()
			prowlarrCfg.Name = prowlarr.BaseIndexerName(updated.Name)
			prowlarrCfg.AppProfileID = updated.AppProfileID
			prowlarrCfg.Tags = append([]int(nil), updated.Tags...)
			returned := prowlarr.SettingsFromFields(*schema, updated.Fields)
			prowlarrCfg.Settings = prowlarr.MergeSettings(*schema, settings, returned)
			now := time.Now()
			prowlarrCfg.LastSync = &now
			prowlarrCfg.SyncError = ""
			return "updated", nil
		}
		// Stale ID → fall through to create.
	}

	appProfileID := schema.AppProfileID
	if appProfileID <= 0 {
		var pErr error
		appProfileID, pErr = client.FirstAppProfileID()
		if pErr != nil {
			return "created", pErr
		}
	}
	payloadSchema := prowlarr.IndexerSchemaForPayload(*schema, appProfileID)
	root.AppProfileID = appProfileID
	added, aErr := client.AddIndexerWithRoot(payloadSchema, fields, root)
	if aErr != nil {
		return "created", aErr
	}
	added, aErr = h.ensureProwlarrEnabled(client, added, root.Enable)
	if aErr != nil {
		return "created", aErr
	}
	cfg.Trackers[i].Enabled = added.Enable
	prowlarrCfg := cfg.Trackers[i].EnsureProwlarr()
	prowlarrCfg.ID = added.ID
	prowlarrCfg.Name = prowlarr.BaseIndexerName(added.Name)
	prowlarrCfg.AppProfileID = added.AppProfileID
	prowlarrCfg.Tags = append([]int(nil), added.Tags...)
	returned := prowlarr.SettingsFromFields(*schema, added.Fields)
	prowlarrCfg.Settings = prowlarr.MergeSettings(*schema, settings, returned)
	now := time.Now()
	prowlarrCfg.LastSync = &now
	prowlarrCfg.SyncError = ""
	return "created", nil
}

func (h *Handler) desiredProwlarrRootForCompare(t *config.TrackerEntry, schema prowlarr.IndexerSchema) prowlarr.IndexerRootConfig {
	appProfileID := t.ProwlarrAppProfileID()
	if appProfileID <= 0 {
		appProfileID = schema.AppProfileID
	}
	return prowlarr.RootConfig(prowlarrBaseName(t), t.Enabled, appProfileID, t.ProwlarrTags())
}

// flashSyncResult mirrors flashImportResult — same three-way redirect
// logic (nothing / success / mixed).
func flashSyncResult(w http.ResponseWriter, r *http.Request, pushed, failures []string) {
	if len(pushed) == 0 && len(failures) == 0 {
		flash(w, r, "/", "", "Nothing synced.")
		return
	}
	okMsg := fmt.Sprintf("Synced %d tracker(s) to Prowlarr.", len(pushed))
	if len(failures) == 0 {
		flash(w, r, "/", okMsg, "")
		return
	}
	errMsg := fmt.Sprintf("%d sync failure(s): %s", len(failures), strings.Join(failures, "; "))
	q := url.Values{}
	if len(pushed) > 0 {
		q.Set("ok", okMsg)
	}
	q.Set("err", errMsg)
	http.Redirect(w, r, "/?"+q.Encode(), http.StatusSeeOther)
}
