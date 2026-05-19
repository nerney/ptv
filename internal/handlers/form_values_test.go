package handlers

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nerney/ptv/internal/autobrrdefs"
	"github.com/nerney/ptv/internal/prowlarr"
)

func TestFormCheckboxChecked(t *testing.T) {
	if !formCheckboxChecked([]string{"false", "true"}) {
		t.Fatal("expected checked when any value is true")
	}
	if formCheckboxChecked([]string{"false"}) {
		t.Fatal("expected unchecked when only false values are present")
	}
}

func TestSubmittedProwlarrSettingsCheckboxUsesAllValues(t *testing.T) {
	form := url.Values{
		"setting_enabled": {"false", "true"},
		"setting_name":    {"tracker"},
	}
	r := httptest.NewRequest("POST", "/tracker/0/config/prowlarr", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm() error = %v", err)
	}

	schema := prowlarr.IndexerSchema{
		Fields: []prowlarr.SchemaField{
			{Name: "enabled", Type: "checkbox"},
			{Name: "name", Type: "text"},
		},
	}
	got := submittedProwlarrSettings(r, schema)
	if got["enabled"] != "true" {
		t.Fatalf("enabled = %q, want true", got["enabled"])
	}
	if got["name"] != "tracker" {
		t.Fatalf("name = %q, want tracker", got["name"])
	}
}

func TestSubmittedAutobrrSettingsCheckboxUsesAllValues(t *testing.T) {
	form := url.Values{
		"setting_announce": {"false", "true"},
		"setting_nick":     {"ptv"},
	}
	r := httptest.NewRequest("POST", "/tracker/0/config/autobrr", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm() error = %v", err)
	}

	def := autobrrdefs.Def{
		Settings: []autobrrdefs.Setting{
			{Name: "announce", Type: "checkbox"},
		},
		IRCSettings: []autobrrdefs.Setting{
			{Name: "nick", Type: "text"},
		},
	}
	got := submittedAutobrrSettings(r, def)
	if got["announce"] != "true" {
		t.Fatalf("announce = %q, want true", got["announce"])
	}
	if got["nick"] != "ptv" {
		t.Fatalf("nick = %q, want ptv", got["nick"])
	}
}
