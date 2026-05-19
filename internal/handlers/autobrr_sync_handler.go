package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/nerney/ptv/internal/autobrr"
	"github.com/nerney/ptv/internal/config"
)

const pathAutobrrSync = "/sync/autobrr"

type autobrrSyncRow struct {
	TrackerIdx int
	Name       string
	TrackerURL string
	AutobrrID  int
	State      string
	Error      string
}

type autobrrSyncData struct {
	AutobrrEnabled bool
	LoadError      string
	FlashError     string
	FlashSuccess   string
	New            []autobrrSyncRow
	Linked         []autobrrSyncRow
}

func (h *Handler) autobrrSyncPage(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.Get()
	data := autobrrSyncData{
		AutobrrEnabled: cfg.AutobrrEnabled && cfg.AutobrrURL != "" && cfg.AutobrrAPIKey != "",
		FlashError:     r.URL.Query().Get("err"),
		FlashSuccess:   r.URL.Query().Get("ok"),
	}
	if !data.AutobrrEnabled {
		h.render(w, r, "autobrr_sync", data)
		return
	}
	for i, t := range cfg.Trackers {
		row := autobrrSyncRow{TrackerIdx: i, Name: t.Name, TrackerURL: t.TrackerURL, AutobrrID: t.AutobrrID()}
		if t.AutobrrID() > 0 {
			row.State = "linked"
			data.Linked = append(data.Linked, row)
		} else {
			row.State = "new"
			data.New = append(data.New, row)
		}
	}
	h.render(w, r, "autobrr_sync", data)
}

func (h *Handler) autobrrSyncSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		flash(w, r, pathAutobrrSync, "", "invalid form")
		return
	}
	cfg := h.store.Get()
	if !cfg.AutobrrEnabled || cfg.AutobrrURL == "" || cfg.AutobrrAPIKey == "" {
		flash(w, r, pathAutobrrSync, "", "Autobrr not enabled")
		return
	}
	client := autobrr.New(cfg.AutobrrURL, cfg.AutobrrAPIKey, h.log)
	var synced, failures []string
	for _, idxStr := range r.Form["tracker_idx"] {
		i, err := strconv.Atoi(idxStr)
		if err != nil || i < 0 || i >= len(cfg.Trackers) {
			continue
		}
		if err := h.syncTrackerToAutobrr(&cfg, client, i); err != nil {
			failures = append(failures, fmt.Sprintf("%s (%s)", cfg.Trackers[i].Name, err.Error()))
			continue
		}
		synced = append(synced, cfg.Trackers[i].Name)
	}
	if len(synced) > 0 {
		if err := h.store.Save(&cfg); err != nil {
			flash(w, r, pathAutobrrSync, "", "Save failed: "+err.Error())
			return
		}
	}
	if len(failures) == 0 {
		flash(w, r, "/", fmt.Sprintf("Synced %d tracker(s) to Autobrr.", len(synced)), "")
		return
	}
	q := url.Values{}
	q.Set("ok", fmt.Sprintf("Synced %d tracker(s) to Autobrr.", len(synced)))
	q.Set("err", strings.Join(failures, "; "))
	http.Redirect(w, r, "/?"+q.Encode(), http.StatusSeeOther)
}

func (h *Handler) syncTrackerToAutobrr(cfg *config.Config, client *autobrr.Client, i int) error {
	entry := cfg.Trackers[i]
	if entry.TrackerURL == "" || entry.APIKey == "" {
		return fmt.Errorf("missing URL or API key")
	}
	existing, err := client.IndexerByURL(entry.TrackerURL)
	if err != nil {
		return err
	}
	if existing == nil {
		schema, err := client.SchemaForURL(entry.TrackerURL)
		if err != nil {
			return err
		}
		settings := h.autobrrSettingsForNew(entry, *schema)
		if settings != nil {
			existing, err = client.AddIndexerWithSettings(*schema, entry.TrackerURL, settings)
		} else {
			existing, err = client.AddIndexer(*schema, entry.TrackerURL, entry.APIKey)
		}
		if err != nil {
			return err
		}
	} else if entry.AutobrrID() > 0 {
		settings := h.autobrrSettingsForLinked(entry, *existing)
		if def := h.autobrrDefFor(entry, existing.Identifier); def != nil {
			settings = autobrr.WithCoreCredentials(*def, settings, entry.APIKey)
		}
		existing, err = client.UpdateIndexerWithSettings(*existing, entry.TrackerURL, settings)
		if err != nil {
			return err
		}
	}
	autobrrCfg := cfg.Trackers[i].EnsureAutobrr()
	autobrrCfg.ID = int(existing.ID)
	autobrrCfg.Identifier = existing.Identifier
	autobrrCfg.Enabled = existing.Enabled
	autobrrCfg.Settings = h.autobrrSettingsForLinked(entry, *existing)
	return nil
}
