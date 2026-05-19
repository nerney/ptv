package config

import (
	"encoding/json"
	"testing"
)

func TestTrackerEntryUnmarshalLegacyIntegrationConfig(t *testing.T) {
	data := []byte(`{
		"definition_name": "Yu-Scene",
		"tracker_type": "unit3d",
		"name": "yu-scene [ptv]",
		"tracker_url": "https://yu-scene.test",
		"api_key": "unit3d-key",
		"username": "nern",
		"enabled": true,
		"prowlarr_id": 3,
		"prowlarr_settings": {"minimumSeeders": "1"},
		"prowlarr_name": "yu-scene",
		"prowlarr_app_profile_id": 7,
		"prowlarr_tags": [11, 12],
		"prowlarr_sync_error": "old prowlarr error",
		"autobrr_id": 44,
		"autobrr_identifier": "yu-scene",
		"autobrr_enabled": true,
		"autobrr_settings": {"rsskey": "secret"},
		"autobrr_sync_error": "old autobrr error",
		"favicon_data_uri": "data:image/png;base64,aaa",
		"theme_color": "#112233"
	}`)

	var got TrackerEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.Prowlarr == nil {
		t.Fatal("Prowlarr = nil, want migrated config")
	}
	if got.Prowlarr.ID != 3 ||
		got.Prowlarr.Settings["minimumSeeders"] != "1" ||
		got.Prowlarr.Name != "yu-scene" ||
		got.Prowlarr.AppProfileID != 7 ||
		len(got.Prowlarr.Tags) != 2 ||
		got.Prowlarr.SyncError != "old prowlarr error" {
		t.Fatalf("Prowlarr migrated incorrectly: %#v", got.Prowlarr)
	}
	if got.Autobrr == nil {
		t.Fatal("Autobrr = nil, want migrated config")
	}
	if got.Autobrr.ID != 44 ||
		got.Autobrr.Identifier != "yu-scene" ||
		!got.Autobrr.Enabled ||
		got.Autobrr.Settings["rsskey"] != "secret" ||
		got.Autobrr.SyncError != "old autobrr error" {
		t.Fatalf("Autobrr migrated incorrectly: %#v", got.Autobrr)
	}
}

func TestTrackerEntryUnmarshalNestedIntegrationConfig(t *testing.T) {
	data := []byte(`{
		"definition_name": "Yu-Scene",
		"name": "yu-scene [ptv]",
		"tracker_url": "https://yu-scene.test",
		"api_key": "unit3d-key",
		"prowlarr": {
			"id": 3,
			"settings": {"minimumSeeders": "1"},
			"name": "yu-scene",
			"app_profile_id": 7,
			"tags": [11]
		},
		"autobrr": {
			"id": 44,
			"identifier": "yu-scene",
			"enabled": true,
			"settings": {"rsskey": "secret"}
		}
	}`)

	var got TrackerEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.Prowlarr == nil || got.Prowlarr.ID != 3 || got.Prowlarr.AppProfileID != 7 {
		t.Fatalf("Prowlarr nested decode = %#v", got.Prowlarr)
	}
	if got.Autobrr == nil || got.Autobrr.ID != 44 || !got.Autobrr.Enabled {
		t.Fatalf("Autobrr nested decode = %#v", got.Autobrr)
	}
}
