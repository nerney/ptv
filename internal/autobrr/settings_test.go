package autobrr

import (
	"reflect"
	"testing"

	"github.com/nerney/ptv/internal/autobrrdefs"
)

func TestMergeSettingsUsesDefinitionAndPreservesSecrets(t *testing.T) {
	def := autobrrdefs.Def{
		Settings: []autobrrdefs.Setting{
			{Name: "rsskey", Type: "secret", Required: true},
			{Name: "maxDelay", Type: "number", Default: "10"},
		},
		IRCSettings: []autobrrdefs.Setting{
			{Name: "nick", Type: "text"},
			{Name: "auth.password", Type: "secret"},
		},
	}

	got := MergeSettings(def,
		map[string]string{
			"rsskey":        "saved-rss",
			"auth.password": "saved-pass",
			"unknown":       "drop-me",
		},
		map[string]string{
			"rsskey":        ExistingSecretValue,
			"nick":          "ptv_bot",
			"auth.password": "",
		},
	)
	want := map[string]string{
		"rsskey":        "saved-rss",
		"maxDelay":      "10",
		"nick":          "ptv_bot",
		"auth.password": "saved-pass",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MergeSettings() = %#v, want %#v", got, want)
	}
}

func TestWithCoreCredentialsOnlyOverlaysCredentialFields(t *testing.T) {
	def := autobrrdefs.Def{
		Settings: []autobrrdefs.Setting{
			{Name: "rsskey", Type: "secret"},
			{Name: "nick", Type: "text"},
		},
		IRCSettings: []autobrrdefs.Setting{
			{Name: "auth.password", Type: "secret"},
		},
	}

	got := WithCoreCredentials(def, map[string]string{
		"rsskey":        "old-rss",
		"nick":          "ptv_bot",
		"auth.password": "irc-secret",
	}, "unit3d-key")

	want := map[string]string{
		"rsskey":        "unit3d-key",
		"nick":          "ptv_bot",
		"auth.password": "irc-secret",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WithCoreCredentials() = %#v, want %#v", got, want)
	}
}

func TestSettingsFromPairsLastValueWins(t *testing.T) {
	got := SettingsFromPairs([]Setting{
		{Name: "rsskey", Value: "first"},
		{Name: "rsskey", Value: "second"},
		{Name: "nick", Value: "ptv"},
	})
	want := map[string]string{"rsskey": "second", "nick": "ptv"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SettingsFromPairs() = %#v, want %#v", got, want)
	}
}

func TestRenderFieldsHandlesBothLayers(t *testing.T) {
	def := autobrrdefs.Def{
		Settings: []autobrrdefs.Setting{
			{Name: "rsskey", Type: "secret", Required: true},
			{Name: "maxDelay", Type: "number", Default: "10"},
		},
		IRCSettings: []autobrrdefs.Setting{
			{Name: "nick", Type: "text"},
			{Name: "auth.password", Type: "secret"},
		},
	}
	settings := map[string]string{
		"rsskey":        "saved-rss",
		"nick":          "ptv_bot",
		"auth.password": "irc-pass",
	}

	got := RenderFields(def, settings)

	// Should have 4 fields total: 2 root, 2 IRC
	if len(got) != 4 {
		t.Fatalf("RenderFields() returned %d fields, want 4", len(got))
	}

	// Verify layers are set correctly
	rootCount := 0
	ircCount := 0
	for _, f := range got {
		if f.Layer == "root" {
			rootCount++
		} else if f.Layer == "irc" {
			ircCount++
		}
	}
	if rootCount != 2 {
		t.Fatalf("Expected 2 root fields, got %d", rootCount)
	}
	if ircCount != 2 {
		t.Fatalf("Expected 2 IRC fields, got %d", ircCount)
	}

	// Verify secrets are not exposed
	for _, f := range got {
		if f.Secret && f.HasValue {
			if f.Value != ExistingSecretValue {
				t.Fatalf("Secret field %s has exposed value %q", f.Name, f.Value)
			}
		}
	}

	// Verify defaults are applied
	found := false
	for _, f := range got {
		if f.Name == "maxDelay" && f.Value == "10" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Default value for maxDelay not applied")
	}
}

func TestRenderFieldsSecretsNotExposed(t *testing.T) {
	def := autobrrdefs.Def{
		Settings: []autobrrdefs.Setting{
			{Name: "rsskey", Type: "secret"},
		},
	}
	settings := map[string]string{
		"rsskey": "my-secret-value",
	}

	got := RenderFields(def, settings)

	if len(got) != 1 {
		t.Fatalf("Expected 1 field, got %d", len(got))
	}
	if got[0].Value != ExistingSecretValue {
		t.Fatalf("Secret exposed as %q, want %q", got[0].Value, ExistingSecretValue)
	}
}

func TestDiffSettingsIgnoresSecrets(t *testing.T) {
	def := autobrrdefs.Def{
		Settings: []autobrrdefs.Setting{
			{Name: "rsskey", Type: "secret"},
			{Name: "baseurl", Type: "text"},
		},
	}

	// Only rsskey differs (secret), baseurl is same
	desired := map[string]string{
		"rsskey":  "secret-a",
		"baseurl": "https://example.com",
	}
	actual := map[string]string{
		"rsskey":  "secret-b",
		"baseurl": "https://example.com",
	}

	got := DiffSettings(def, desired, actual)

	// Should be no diff because secrets are ignored
	if len(got) != 0 {
		t.Fatalf("DiffSettings() = %v, want no diff when only secrets differ", got)
	}
}

func TestDiffSettingsNormalizesURLs(t *testing.T) {
	def := autobrrdefs.Def{
		Settings: []autobrrdefs.Setting{
			{Name: "baseurl", Type: "text"},
		},
	}

	// URLs differ only in trailing slash and case
	desired := map[string]string{
		"baseurl": "https://Example.com/",
	}
	actual := map[string]string{
		"baseurl": "https://example.com",
	}

	got := DiffSettings(def, desired, actual)

	// Should be no diff because URLs are normalized
	if len(got) != 0 {
		t.Fatalf("DiffSettings() = %v, want no diff for normalized URLs", got)
	}
}

func TestDiffSettingsDetectsRealChanges(t *testing.T) {
	def := autobrrdefs.Def{
		Settings: []autobrrdefs.Setting{
			{Name: "nick", Type: "text"},
		},
	}

	desired := map[string]string{
		"nick": "ptv_bot_a",
	}
	actual := map[string]string{
		"nick": "ptv_bot_b",
	}

	got := DiffSettings(def, desired, actual)

	if len(got) != 1 || got[0] != "nick" {
		t.Fatalf("DiffSettings() = %v, want [nick]", got)
	}
}
