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
