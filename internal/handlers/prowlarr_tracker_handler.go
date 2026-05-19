package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nerney/ptv/internal/config"
	"github.com/nerney/ptv/internal/prowlarr"
)

func (h *Handler) configTrackerProwlarrPost(w http.ResponseWriter, r *http.Request) {
	idx, cfg, ok := h.trackerIndex(r)
	if !ok {
		flash(w, r, pathConfigTrackers, "", "invalid tracker index")
		return
	}
	if err := r.ParseForm(); err != nil {
		flash(w, r, trackerConfigPath(idx), "", "invalid form")
		return
	}
	if !cfg.ProwlarrEnabled || cfg.ProwlarrURL == "" || cfg.ProwlarrAPIKey == "" {
		flash(w, r, pathConfigProwlarr, "", "Prowlarr not enabled")
		return
	}
	if newURL := strings.TrimSpace(r.FormValue("url")); newURL != "" {
		cfg.Trackers[idx].TrackerURL = newURL
	}
	cfg.Trackers[idx].ProwlarrName = strings.TrimSpace(r.FormValue("prowlarr_name"))
	cfg.Trackers[idx].Enabled = formCheckboxChecked(r.Form["enabled"])
	cfg.Trackers[idx].ProwlarrAppProfileID = formInt(r.FormValue("app_profile_id"))
	cfg.Trackers[idx].ProwlarrTags = submittedIntSlice(r.Form["tag"])
	schema, err := h.prowlarrSchemaByName(cfg.Trackers[idx].DefinitionName)
	if err != nil {
		flash(w, r, trackerConfigPath(idx), "", "Schema unavailable — re-import from Prowlarr when available: "+err.Error())
		return
	}

	submitted := submittedProwlarrSettings(r, *schema)
	cfg.Trackers[idx].ProwlarrSettings = prowlarr.MergeSettings(*schema, cfg.Trackers[idx].ProwlarrSettings, submitted)
	if err := h.store.Save(cfg); err != nil {
		flash(w, r, trackerConfigPath(idx), "", "Save failed: "+err.Error())
		return
	}

	if r.FormValue("action") != "save_push" {
		h.log.Info("CONFIG", fmt.Sprintf("Saved Prowlarr settings for %q", cfg.Trackers[idx].Name))
		flash(w, r, trackerProwlarrDiffPath(idx), "Prowlarr settings saved. Review drift before pushing.", "")
		return
	}
	h.log.Info("CONFIG", fmt.Sprintf("Saved Prowlarr settings for %q (awaiting diff confirmation)", cfg.Trackers[idx].Name))
	flash(w, r, trackerProwlarrDiffPath(idx), "Prowlarr settings saved. Confirm drift, then push to Prowlarr.", "")
}

func (h *Handler) trackerDefinitionURLs(definitionName string) []string {
	if h.syncer == nil {
		return nil
	}
	allDefs, err := h.syncer.Catalog()
	if err != nil {
		return nil
	}
	for _, d := range allDefs {
		if strings.EqualFold(d.Name, definitionName) {
			return d.URLs
		}
	}
	return nil
}

func submittedProwlarrSettings(r *http.Request, schema prowlarr.IndexerSchema) map[string]string {
	out := make(map[string]string, len(schema.Fields))
	for _, f := range schema.Fields {
		name := "setting_" + f.Name
		values, ok := r.Form[name]
		if !ok {
			continue
		}
		out[f.Name] = submittedFormValue(values, f.Type)
	}
	return out
}

func (h *Handler) pushTrackerProwlarrConfig(cfg *config.Config, i int, schema prowlarr.IndexerSchema) error {
	t := cfg.Trackers[i]
	if t.TrackerURL == "" || t.APIKey == "" {
		return fmt.Errorf("missing core tracker URL or API key")
	}
	settings := prowlarr.WithCoreCredentials(schema, t.ProwlarrSettings, t.TrackerURL, t.APIKey)
	fields := prowlarr.FieldsForPayload(schema, settings)
	client := prowlarr.New(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, h.log)
	root, err := h.prowlarrRootConfig(cfg, i, schema, client)
	if err != nil {
		return err
	}

	if t.ProwlarrID != 0 {
		existing, err := client.GetIndexer(t.ProwlarrID)
		if err != nil {
			return err
		}
		updated, err := client.UpdateIndexerWithRoot(*existing, fields, root)
		if err != nil {
			return err
		}
		updated, err = h.ensureProwlarrEnabled(client, updated, root.Enable)
		if err != nil {
			return err
		}
		now := time.Now()
		cfg.Trackers[i].Enabled = updated.Enable
		cfg.Trackers[i].ProwlarrName = prowlarr.BaseIndexerName(updated.Name)
		cfg.Trackers[i].ProwlarrAppProfileID = updated.AppProfileID
		cfg.Trackers[i].ProwlarrTags = append([]int(nil), updated.Tags...)
		returned := prowlarr.SettingsFromFields(schema, updated.Fields)
		cfg.Trackers[i].ProwlarrSettings = prowlarr.MergeSettings(schema, settings, returned)
		cfg.Trackers[i].ProwlarrLastSync = &now
		cfg.Trackers[i].ProwlarrSyncError = ""
		return nil
	}

	appProfileID := schema.AppProfileID
	if appProfileID <= 0 {
		var err error
		appProfileID, err = client.FirstAppProfileID()
		if err != nil {
			return err
		}
	}
	schema = prowlarr.IndexerSchemaForPayload(schema, appProfileID)
	root.AppProfileID = appProfileID
	updated, err := client.AddIndexerWithRoot(schema, fields, root)
	if err != nil {
		return err
	}
	updated, err = h.ensureProwlarrEnabled(client, updated, root.Enable)
	if err != nil {
		return err
	}
	now := time.Now()
	cfg.Trackers[i].ProwlarrID = updated.ID
	cfg.Trackers[i].Enabled = updated.Enable
	cfg.Trackers[i].ProwlarrName = prowlarr.BaseIndexerName(updated.Name)
	cfg.Trackers[i].ProwlarrAppProfileID = updated.AppProfileID
	cfg.Trackers[i].ProwlarrTags = append([]int(nil), updated.Tags...)
	returned := prowlarr.SettingsFromFields(schema, updated.Fields)
	cfg.Trackers[i].ProwlarrSettings = prowlarr.MergeSettings(schema, settings, returned)
	cfg.Trackers[i].ProwlarrLastSync = &now
	cfg.Trackers[i].ProwlarrSyncError = ""
	return nil
}

func (h *Handler) prowlarrRootConfig(cfg *config.Config, i int, schema prowlarr.IndexerSchema, client *prowlarr.Client) (prowlarr.IndexerRootConfig, error) {
	t := cfg.Trackers[i]
	name := prowlarrBaseName(t)
	enabled := t.Enabled
	if t.ProwlarrID == 0 {
		enabled = true
	}
	appProfileID := t.ProwlarrAppProfileID
	if appProfileID <= 0 {
		appProfileID = schema.AppProfileID
	}
	if appProfileID <= 0 {
		var err error
		appProfileID, err = client.FirstAppProfileID()
		if err != nil {
			return prowlarr.IndexerRootConfig{}, err
		}
	}
	cfg.Trackers[i].ProwlarrName = prowlarr.BaseIndexerName(name)
	cfg.Trackers[i].ProwlarrAppProfileID = appProfileID
	cfg.Trackers[i].Enabled = enabled
	return prowlarr.RootConfig(name, enabled, appProfileID, t.ProwlarrTags), nil
}

func (h *Handler) ensureProwlarrEnabled(client *prowlarr.Client, idx *prowlarr.Indexer, want bool) (*prowlarr.Indexer, error) {
	if idx == nil {
		return nil, fmt.Errorf("missing prowlarr indexer")
	}

	// Prowlarr update responses can echo the requested value even when it did
	// not persist. Always verify persisted state via readback.
	current, err := client.GetIndexer(idx.ID)
	if err != nil {
		return nil, fmt.Errorf("readback enabled=%t: %w", want, err)
	}
	if current.Enable == want {
		return current, nil
	}
	if err := client.SetEnabled(*current, want); err != nil {
		return nil, fmt.Errorf("set enabled=%t: %w", want, err)
	}
	refreshed, err := client.GetIndexer(idx.ID)
	if err != nil {
		return nil, fmt.Errorf("verify enabled=%t: %w", want, err)
	}
	if refreshed.Enable != want {
		return nil, fmt.Errorf("prowlarr enable state remained %t (wanted %t)", refreshed.Enable, want)
	}
	return refreshed, nil
}

func prowlarrBaseName(t *config.TrackerEntry) string {
	if strings.TrimSpace(t.ProwlarrName) != "" {
		return t.ProwlarrName
	}
	return t.Name
}

func formInt(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func submittedIntSlice(values []string) []int {
	out := make([]int, 0, len(values))
	for _, value := range values {
		n, err := strconv.Atoi(value)
		if err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}
