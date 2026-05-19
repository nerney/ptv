package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nerney/ptv/internal/autobrr"
	"github.com/nerney/ptv/internal/autobrrdefs"
	"github.com/nerney/ptv/internal/config"
	"github.com/nerney/ptv/internal/prowlarr"
)

func TestBuildUnifiedTrackerConfigData(t *testing.T) {
	t.Run("success with schema cache and metadata endpoints", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/appprofile":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[{"id":7,"name":"Default"}]`))
			case "/api/v1/tag":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[{"id":11,"label":"ptv"}]`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		cfg := config.Config{
			ProwlarrEnabled: true,
			ProwlarrURL:     srv.URL,
			ProwlarrAPIKey:  "prowlarr-key",
			AutobrrEnabled:  true,
			AutobrrURL:      "http://autobrr.test",
			AutobrrAPIKey:   "autobrr-key",
			Trackers: []*config.TrackerEntry{
				{
					DefinitionName:    "Yu-Scene",
					Name:              "yu-scene [ptv]",
					ProwlarrSettings:  map[string]string{"minimumSeeders": "1"},
					AutobrrIdentifier: "missing-def",
				},
			},
		}
		h := newTestHandler(t, cfg)
		h.pSchemas = map[string]prowlarr.IndexerSchema{
			"yu-scene": {
				Name: "Yu-Scene",
				Fields: []prowlarr.SchemaField{
					{Name: "minimumSeeders", Label: "Minimum Seeders", Type: "number", Value: float64(0)},
				},
			},
		}

		got := h.buildUnifiedTrackerConfigData(0, &cfg)
		if got.ProwlarrEnabled != true {
			t.Fatalf("ProwlarrEnabled = %v, want true", got.ProwlarrEnabled)
		}
		if got.AutobrrEnabled != true {
			t.Fatalf("AutobrrEnabled = %v, want true", got.AutobrrEnabled)
		}
		if got.ProwlarrBaseName != "yu-scene" {
			t.Fatalf("ProwlarrBaseName = %q, want yu-scene", got.ProwlarrBaseName)
		}
		if got.ProwlarrSchema == nil {
			t.Fatal("ProwlarrSchema = nil, want schema")
		}
		if got.ProwlarrError != "" {
			t.Fatalf("ProwlarrError = %q, want empty", got.ProwlarrError)
		}
		if len(got.ProwlarrAppProfiles) != 1 || got.ProwlarrAppProfiles[0].ID != 7 {
			t.Fatalf("ProwlarrAppProfiles = %#v", got.ProwlarrAppProfiles)
		}
		if len(got.ProwlarrTags) != 1 || got.ProwlarrTags[0].ID != 11 {
			t.Fatalf("ProwlarrTags = %#v", got.ProwlarrTags)
		}
		if got.AutobrrError != "Definition not available" {
			t.Fatalf("AutobrrError = %q, want definition error", got.AutobrrError)
		}
	})

	t.Run("schema fetch failure populates prowlarr error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/appprofile":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[]`))
			case "/api/v1/tag":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[]`))
			case "/api/v1/indexer/schema":
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"boom"}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		cfg := config.Config{
			ProwlarrEnabled: true,
			ProwlarrURL:     srv.URL,
			ProwlarrAPIKey:  "prowlarr-key",
			Trackers: []*config.TrackerEntry{
				{DefinitionName: "NoSchema", Name: "noschema [ptv]"},
			},
		}
		h := newTestHandler(t, cfg)
		h.pSchemas = map[string]prowlarr.IndexerSchema{}

		got := h.buildUnifiedTrackerConfigData(0, &cfg)
		if got.ProwlarrSchema != nil {
			t.Fatalf("ProwlarrSchema = %#v, want nil", got.ProwlarrSchema)
		}
		if !strings.Contains(got.ProwlarrError, "Schema unavailable:") {
			t.Fatalf("ProwlarrError = %q, want schema unavailable error", got.ProwlarrError)
		}
	})
}

func TestSubmittedProwlarrSettings(t *testing.T) {
	form := "setting_alpha=one&setting_beta=false&setting_beta=true&setting_unknown=drop"
	req := newFormRequest(http.MethodPost, "/tracker/0/config/prowlarr", form)
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm() error = %v", err)
	}
	schema := prowlarr.IndexerSchema{
		Fields: []prowlarr.SchemaField{
			{Name: "alpha", Type: "text"},
			{Name: "beta", Type: "checkbox"},
		},
	}
	got := submittedProwlarrSettings(req, schema)
	if got["alpha"] != "one" {
		t.Fatalf("alpha = %q, want one", got["alpha"])
	}
	if got["beta"] != "true" {
		t.Fatalf("beta = %q, want true", got["beta"])
	}
	if _, ok := got["unknown"]; ok {
		t.Fatal("unknown key should be ignored")
	}
}

func TestSubmittedAutobrrSettings(t *testing.T) {
	form := "setting_rsskey=abcd&setting_announce=false&setting_announce=true&setting_irc_nick=ptv&setting_unknown=drop"
	req := newFormRequest(http.MethodPost, "/tracker/0/config/autobrr", form)
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm() error = %v", err)
	}
	def := autobrrdefs.Def{
		Settings: []autobrrdefs.Setting{
			{Name: "rsskey", Type: "secret"},
			{Name: "announce", Type: "checkbox"},
		},
		IRCSettings: []autobrrdefs.Setting{
			{Name: "irc_nick", Type: "text"},
		},
	}
	got := submittedAutobrrSettings(req, def)
	if got["rsskey"] != "abcd" {
		t.Fatalf("rsskey = %q, want abcd", got["rsskey"])
	}
	if got["announce"] != "true" {
		t.Fatalf("announce = %q, want true", got["announce"])
	}
	if got["irc_nick"] != "ptv" {
		t.Fatalf("irc_nick = %q, want ptv", got["irc_nick"])
	}
	if _, ok := got["unknown"]; ok {
		t.Fatal("unknown key should be ignored")
	}
}

func TestSubmittedSettingsSecretPreservationViaMerge(t *testing.T) {
	prowlarrReq := newFormRequest(http.MethodPost, "/tracker/0/config/prowlarr",
		"setting_apiKey=&setting_token="+prowlarr.ExistingSecretValue)
	if err := prowlarrReq.ParseForm(); err != nil {
		t.Fatalf("ParseForm() error = %v", err)
	}
	prowlarrSchema := prowlarr.IndexerSchema{
		Fields: []prowlarr.SchemaField{
			{Name: "apiKey", Type: "text", Required: true},
			{Name: "token", Type: "text", Required: true},
		},
	}
	prowlarrSubmitted := submittedProwlarrSettings(prowlarrReq, prowlarrSchema)
	prowlarrMerged := prowlarr.MergeSettings(prowlarrSchema,
		map[string]string{"apiKey": "saved-a", "token": "saved-b"},
		prowlarrSubmitted,
	)
	if prowlarrMerged["apiKey"] != "saved-a" || prowlarrMerged["token"] != "saved-b" {
		t.Fatalf("prowlarr merged secrets not preserved: %#v", prowlarrMerged)
	}

	autobrrReq := newFormRequest(http.MethodPost, "/tracker/0/config/autobrr",
		"setting_rsskey=&setting_passkey="+autobrr.ExistingSecretValue)
	if err := autobrrReq.ParseForm(); err != nil {
		t.Fatalf("ParseForm() error = %v", err)
	}
	autobrrDef := autobrrdefs.Def{
		Settings: []autobrrdefs.Setting{
			{Name: "rsskey", Type: "secret", Required: true},
			{Name: "passkey", Type: "secret", Required: true},
		},
	}
	autobrrSubmitted := submittedAutobrrSettings(autobrrReq, autobrrDef)
	autobrrMerged := autobrr.MergeSettings(autobrrDef,
		map[string]string{"rsskey": "saved-rss", "passkey": "saved-pass"},
		autobrrSubmitted,
	)
	if autobrrMerged["rsskey"] != "saved-rss" || autobrrMerged["passkey"] != "saved-pass" {
		t.Fatalf("autobrr merged secrets not preserved: %#v", autobrrMerged)
	}
}

func TestConfigTrackerUpdate(t *testing.T) {
	baseTracker := &config.TrackerEntry{
		DefinitionName: "Yu-Scene",
		TrackerType:    "unit3d",
		Name:           "yu-scene [ptv]",
		TrackerURL:     "https://old.test",
		APIKey:         "old-key",
	}

	t.Run("validation success saves credentials and stats", func(t *testing.T) {
		cfg := config.Config{Trackers: []*config.TrackerEntry{baseTracker}}
		h := newTestHandler(t, cfg)
		h.validateFn = func(_, _, _ string) (*config.UserStats, error) {
			return &config.UserStats{Username: "nern", Ratio: "2.0"}, nil
		}

		req := newFormRequest(http.MethodPost, "/tracker/0/config", "url=https%3A%2F%2Fnew.test&api_key=new-key")
		req = withTrackerIdx(req, "0")
		rr := httptest.NewRecorder()

		h.configTrackerUpdate(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
		}

		got := h.store.Get().Trackers[0]
		if got.TrackerURL != "https://new.test" || got.APIKey != "new-key" {
			t.Fatalf("updated credentials = (%q,%q)", got.TrackerURL, got.APIKey)
		}
		if got.UserStats == nil || got.UserStats.Username != "nern" {
			t.Fatalf("UserStats = %#v, want populated", got.UserStats)
		}
		if got.LastSync == nil {
			t.Fatal("LastSync = nil, want populated")
		}
		if got.SyncError != "" {
			t.Fatalf("SyncError = %q, want empty", got.SyncError)
		}
	})

	t.Run("validation failure renders unified template with submitted values", func(t *testing.T) {
		cfg := config.Config{Trackers: []*config.TrackerEntry{{
			DefinitionName: "Yu-Scene",
			TrackerType:    "unit3d",
			Name:           "yu-scene [ptv]",
			TrackerURL:     "https://old.test",
			APIKey:         "old-key",
		}}}
		h := newTestHandler(t, cfg)
		h.validateFn = func(_, _, _ string) (*config.UserStats, error) {
			return nil, errors.New("bad credentials")
		}

		req := newFormRequest(http.MethodPost, "/tracker/0/config", "url=https%3A%2F%2Fbad.test&api_key=bad-key")
		req = withTrackerIdx(req, "0")
		rr := httptest.NewRecorder()

		h.configTrackerUpdate(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "bad credentials") {
			t.Fatalf("body missing validation error: %q", body)
		}
		if !strings.Contains(body, "https://bad.test") || !strings.Contains(body, "bad-key") {
			t.Fatalf("body missing submitted values: %q", body)
		}

		got := h.store.Get().Trackers[0]
		if got.TrackerURL != "https://old.test" || got.APIKey != "old-key" {
			t.Fatalf("credentials changed on validation failure: (%q,%q)", got.TrackerURL, got.APIKey)
		}
	})

	t.Run("force save skips validation and clears stats", func(t *testing.T) {
		now := time.Now()
		cfg := config.Config{Trackers: []*config.TrackerEntry{{
			DefinitionName: "Yu-Scene",
			TrackerType:    "unit3d",
			Name:           "yu-scene [ptv]",
			TrackerURL:     "https://old.test",
			APIKey:         "old-key",
			UserStats:      &config.UserStats{Username: "old"},
			LastSync:       &now,
			SyncError:      "old error",
		}}}
		h := newTestHandler(t, cfg)
		called := false
		h.validateFn = func(_, _, _ string) (*config.UserStats, error) {
			called = true
			return nil, errors.New("should not be called")
		}

		req := newFormRequest(http.MethodPost, "/tracker/0/config", "force_save=true&url=&api_key=")
		req = withTrackerIdx(req, "0")
		rr := httptest.NewRecorder()

		h.configTrackerUpdate(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
		}
		if called {
			t.Fatal("validateFn was called on force_save path")
		}

		got := h.store.Get().Trackers[0]
		if got.TrackerURL != "https://old.test" || got.APIKey != "old-key" {
			t.Fatalf("credentials changed unexpectedly: (%q,%q)", got.TrackerURL, got.APIKey)
		}
		if got.UserStats != nil || got.LastSync != nil {
			t.Fatalf("stats not cleared: UserStats=%#v LastSync=%v", got.UserStats, got.LastSync)
		}
		if got.SyncError != "" {
			t.Fatalf("SyncError = %q, want empty", got.SyncError)
		}
	})
}
