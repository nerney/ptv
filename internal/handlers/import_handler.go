package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nerney/ptv/internal/config"
	"github.com/nerney/ptv/internal/defs"
	"github.com/nerney/ptv/internal/prowlarr"
)

// import_handler covers /config/prowlarr/import — bulk-import indexers
// that the user already configured in Prowlarr into the dashboard. The
// import is "best effort": each selected indexer is validated against
// UNIT3D, but a validation failure does NOT block the import — it just
// flags that row in the redirect-to-home flash. This matches the spec:
// "imported with empty stats" is a valid state.

const pathImport = "/config/integrations/prowlarr/import"

type importPageData struct {
	ProwlarrEnabled bool
	ProwlarrOK      bool
	LoadError       string
	FlashError      string
	FlashSuccess    string
	Importable      []configuredImport
	ActiveTab       string
	Section         string
}

// importPage renders the list of Prowlarr-configured indexers that
// match the UNIT3D catalog and aren't already in the dashboard.
func (h *Handler) importPage(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.Get()
	data := importPageData{
		FlashError:   r.URL.Query().Get("err"),
		FlashSuccess: r.URL.Query().Get("ok"),
		ActiveTab:    "import",
		Section:      "integrations",
	}
	data.ProwlarrEnabled = cfg.ProwlarrEnabled && cfg.ProwlarrURL != "" && cfg.ProwlarrAPIKey != ""

	if !data.ProwlarrEnabled {
		h.render(w, "import", data)
		return
	}

	if st, _ := h.syncer.Status(); st == defs.StateUnavailable {
		data.LoadError = "Indexer definitions unavailable — try again shortly."
		h.render(w, "import", data)
		return
	}

	client := prowlarr.New(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, h.log)
	importable, err := h.loadImportable(client, &cfg)
	if err != nil {
		data.LoadError = err.Error()
		h.render(w, "import", data)
		return
	}
	data.ProwlarrOK = true
	data.Importable = importable
	h.render(w, "import", data)
}

// importSubmit imports every selected Prowlarr indexer. Validation
// successes get populated UserStats; validation failures get an empty-
// stats entry and a note in the flash. Redirect target is always /
// so the operator sees the result on the dashboard they care about.
func (h *Handler) importSubmit(w http.ResponseWriter, r *http.Request) {
	if !h.defsReady(w, r, pathImport) {
		return
	}
	if err := r.ParseForm(); err != nil {
		flash(w, r, pathImport, "", "invalid form")
		return
	}
	cfg := h.store.Get()
	if !cfg.ProwlarrEnabled || cfg.ProwlarrURL == "" || cfg.ProwlarrAPIKey == "" {
		flash(w, r, pathImport, "", "Prowlarr not enabled")
		return
	}
	idStrs := r.Form["prowlarr_id"]
	if len(idStrs) == 0 {
		flash(w, r, pathImport, "", "No trackers selected")
		return
	}

	client := prowlarr.New(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, h.log)
	urlMap, typeMap, err := h.catalogMaps()
	if err != nil {
		flash(w, r, pathImport, "", "Catalog unavailable: "+err.Error())
		return
	}

	result := h.runImport(client, &cfg, idStrs, urlMap, typeMap)

	if len(result.imported) > 0 {
		if err := h.store.Save(&cfg); err != nil {
			flash(w, r, pathImport, "", "Save failed: "+err.Error())
			return
		}
	}
	flashImportResult(w, r, result)
}

// ---------- import internals --------------------------------------------

// importOutcome aggregates what happened during a multi-tracker import.
// validated = imported with stats populated.
// unvalidated = imported but stats empty + a per-row reason string.
type importOutcome struct {
	imported    []string
	validated   []string
	unvalidated []string
}

// catalogMaps loads the catalog and builds two lookup tables used by the
// import flow: URL → definition name, and definition name → tracker type ID.
func (h *Handler) catalogMaps() (urlMap map[string]string, typeMap map[string]string, err error) {
	allDefs, err := h.syncer.Catalog()
	if err != nil {
		return nil, nil, err
	}
	urlMap = buildURLMap(allDefs)
	typeMap = make(map[string]string, len(allDefs))
	for _, d := range allDefs {
		typeMap[strings.ToLower(d.Name)] = d.TypeID
	}
	return urlMap, typeMap, nil
}

// runImport iterates the selected Prowlarr IDs and appends the
// resulting TrackerEntry rows to cfg.Trackers in place. The outcome
// drives the final flash message.
func (h *Handler) runImport(
	client *prowlarr.Client,
	cfg *config.Config,
	idStrs []string,
	urlMap map[string]string,
	typeMap map[string]string,
) importOutcome {
	out := importOutcome{}
	already := managedSet(cfg.Trackers)

	for _, idStr := range idStrs {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue // tampered form value — skip silently
		}
		idx, err := client.GetIndexer(id)
		if err != nil {
			h.log.Err("CONFIG", fmt.Sprintf("Prowlarr fetch failed for id=%d: %s", id, err.Error()))
			out.unvalidated = append(out.unvalidated, fmt.Sprintf("indexer #%d (Prowlarr fetch failed)", id))
			continue
		}

		entry, skipReason := h.buildImportEntry(idx, urlMap, typeMap, already)
		if skipReason != "" {
			if skipReason != skipAlreadyManaged {
				// "already managed" is a no-op, not a failure to surface.
				out.unvalidated = append(out.unvalidated, idx.Name+" ("+skipReason+")")
			}
			continue
		}
		schema, sErr := h.prowlarrSchemaByName(entry.DefinitionName)
		if sErr != nil {
			out.unvalidated = append(out.unvalidated, idx.Name+" (schema unavailable)")
			h.log.Err("CONFIG", fmt.Sprintf("Prowlarr import schema failed for %q: %s", idx.Name, sErr.Error()))
			continue
		}
		entry.EnsureProwlarr().Settings = prowlarr.SettingsFromFields(*schema, idx.Fields)

		// Validate against the tracker API. Failure doesn't block the import —
		// the row lands with empty stats per spec.
		if entry.TrackerURL != "" && entry.APIKey != "" {
			stats, vErr := h.validateTracker(entry.TrackerType, entry.TrackerURL, entry.APIKey)
			if vErr == nil {
				now := time.Now()
				entry.UserStats = stats
				entry.LastSync = &now
				out.validated = append(out.validated, entry.Name)
			} else {
				entry.SyncError = vErr.Error()
				out.unvalidated = append(out.unvalidated, fmt.Sprintf("%s (%s)", entry.Name, vErr.Error()))
			}
		} else {
			out.unvalidated = append(out.unvalidated, entry.Name+" (Prowlarr has no URL or API key)")
		}

		cfg.Trackers = append(cfg.Trackers, entry)
		already[strings.ToLower(entry.DefinitionName)] = true
		out.imported = append(out.imported, entry.Name)
		h.log.Info("CONFIG", fmt.Sprintf("Imported %q from Prowlarr (validated=%v)",
			entry.Name, entry.UserStats != nil))
		h.discoverBrandingAsync(entry.DefinitionName, entry.TrackerURL)
	}
	return out
}

// skip-reason sentinels — string values used both internally and in
// the flash error list.
const (
	skipURLNotCatalog  = "URL not in catalog"
	skipAlreadyManaged = "already managed"
)

// buildImportEntry inspects one Prowlarr indexer, decides whether it
// should be imported, and returns either the new TrackerEntry or a
// skip reason. The function is pure — no I/O — so the import loop
// stays easy to read.
func (h *Handler) buildImportEntry(
	idx *prowlarr.Indexer,
	urlMap map[string]string,
	typeMap map[string]string,
	already map[string]bool,
) (*config.TrackerEntry, string) {
	idxURL, key := prowlarr.ExtractCreds(idx.Fields)
	if idxURL == "" && len(idx.IndexerUrls) > 0 {
		idxURL = idx.IndexerUrls[0]
	}
	schemaName := urlMap[prowlarr.NormalizeURL(idxURL)]
	if schemaName == "" {
		return nil, skipURLNotCatalog
	}
	if already[strings.ToLower(schemaName)] {
		return nil, skipAlreadyManaged
	}
	return &config.TrackerEntry{
		DefinitionName: schemaName,
		TrackerType:    typeMap[strings.ToLower(schemaName)],
		Name:           idx.Name,
		TrackerURL:     idxURL,
		APIKey:         key,
		Enabled:        idx.Enable,
		Prowlarr: &config.ProwlarrTrackerConfig{
			ID:           idx.ID,
			Name:         prowlarr.BaseIndexerName(idx.Name),
			AppProfileID: idx.AppProfileID,
			Tags:         append([]int(nil), idx.Tags...),
		},
	}, ""
}

// flashImportResult turns the outcome into the right redirect:
//   - nothing imported → "Nothing imported." flash error on the import page;
//   - all imports validated → "Imported N." flash success on /;
//   - mixed → both ok= and err= on / so the dashboard shows both banners.
func flashImportResult(w http.ResponseWriter, r *http.Request, out importOutcome) {
	if len(out.imported) == 0 {
		flash(w, r, "/", "", "Nothing imported.")
		return
	}
	okMsg := fmt.Sprintf("Imported %d tracker(s).", len(out.imported))
	if len(out.validated) > 0 {
		okMsg += fmt.Sprintf(" Validated: %s.", strings.Join(out.validated, ", "))
	}
	if len(out.unvalidated) == 0 {
		flash(w, r, "/", okMsg, "")
		return
	}
	// Mixed: keep both messages, build query manually so we can set
	// ok and err in one redirect.
	errMsg := fmt.Sprintf("Imported %d but validation failed for %d (added with empty stats): %s",
		len(out.imported), len(out.unvalidated), strings.Join(out.unvalidated, "; "))
	q := url.Values{}
	q.Set("ok", okMsg)
	q.Set("err", errMsg)
	http.Redirect(w, r, "/?"+q.Encode(), http.StatusSeeOther)
}
