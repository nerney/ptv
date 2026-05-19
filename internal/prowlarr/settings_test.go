package prowlarr

import "testing"

func TestMergeSettingsPreservesBlankSecret(t *testing.T) {
	schema := IndexerSchema{Fields: []SchemaField{
		{Name: "apiKey", Label: "API Key", Required: true},
		{Name: "baseUrl", Label: "URL", Value: "https://example.test"},
	}}
	existing := map[string]string{
		"apiKey": "saved-secret",
		"old":    "dropped",
	}
	submitted := map[string]string{
		"apiKey":  "",
		"baseUrl": "https://tracker.test",
		"other":   "ignored",
	}

	got := MergeSettings(schema, existing, submitted)
	if got["apiKey"] != "saved-secret" {
		t.Fatalf("apiKey = %q, want preserved secret", got["apiKey"])
	}
	if got["baseUrl"] != "https://tracker.test" {
		t.Fatalf("baseUrl = %q", got["baseUrl"])
	}
	if _, ok := got["old"]; ok {
		t.Fatal("stale setting was not dropped")
	}
	if _, ok := got["other"]; ok {
		t.Fatal("unknown submitted setting was not ignored")
	}
}

func TestMergeSettingsPreservesDummySecret(t *testing.T) {
	schema := IndexerSchema{Fields: []SchemaField{
		{Name: "apiKey", Label: "API Key", Value: "not-real"},
	}}
	existing := map[string]string{"apiKey": "saved-secret"}
	submitted := map[string]string{"apiKey": ExistingSecretValue}

	got := MergeSettings(schema, existing, submitted)
	if got["apiKey"] != "saved-secret" {
		t.Fatalf("apiKey = %q, want preserved secret", got["apiKey"])
	}
}

func TestRenderFieldsDoesNotExposeSecrets(t *testing.T) {
	schema := IndexerSchema{Fields: []SchemaField{
		{Name: "apiKey", Label: "API Key", Required: true, Value: "not-real"},
		{Name: "baseUrl", Label: "URL"},
		{Name: "definitionFile", Label: "Definition File", Value: "unit3d.yml"},
		{Name: "minimumSeeders", Label: "Minimum Seeders"},
	}}
	settings := map[string]string{
		"apiKey":         "saved-secret",
		"baseUrl":        "https://tracker.test",
		"definitionFile": "unit3d.yml",
		"minimumSeeders": "2",
	}

	fields := RenderFields(schema, settings)
	if len(fields) != 2 {
		t.Fatalf("len(fields) = %d", len(fields))
	}
	if !fields[0].Secret || fields[0].Value != ExistingSecretValue || !fields[0].HasValue {
		t.Fatalf("secret field rendered incorrectly: %+v", fields[0])
	}
	if fields[1].Name != "minimumSeeders" || fields[1].Secret || fields[1].Value != "2" {
		t.Fatalf("non-secret field rendered incorrectly: %+v", fields[1])
	}
}

func TestRenderFieldsSanitizesHTMLHelpText(t *testing.T) {
	schema := IndexerSchema{Fields: []SchemaField{
		{
			Name:     "apiKey",
			Label:    "API Key",
			HelpText: `<div><strong>About Your API Key</strong><br>Use your tracker API key &amp; keep it private.</div>`,
		},
		{
			Name:     "accountInactivity",
			Label:    "Account Inactivity",
			HelpText: `<p><b>Account Inactivity</b></p><p>The time a torrent should be seeded before stopping...</p>`,
		},
		{
			Name:     "minimumSeeders",
			Label:    "Minimum Seeders",
			HelpText: "The time a torrent should be seeded before stopping...",
		},
	}}

	fields := RenderFields(schema, nil)
	if fields[0].HelpText != "Use your tracker API key & keep it private." {
		t.Fatalf("apiKey HelpText = %q", fields[0].HelpText)
	}
	if fields[1].HelpText != "The time a torrent should be seeded before stopping..." {
		t.Fatalf("accountInactivity HelpText = %q", fields[1].HelpText)
	}
	if fields[2].HelpText != "The time a torrent should be seeded before stopping..." {
		t.Fatalf("plain HelpText changed: %q", fields[2].HelpText)
	}
}

func TestRenderFieldsMarksInfoFieldsDisplayOnly(t *testing.T) {
	schema := IndexerSchema{Fields: []SchemaField{
		{
			Name:     "info_key",
			Label:    "About Your API Key",
			HelpText: "About Your API Key Use your tracker API key.",
			Value:    "do not render as input",
		},
		{
			Name:     "accountInactivity",
			Label:    "Account Inactivity",
			HelpText: "Account Inactivity Seeding may prevent account inactivity.",
		},
		{
			Name:  "minimumSeeders",
			Label: "Minimum Seeders",
			Value: "1",
		},
	}}

	fields := RenderFields(schema, nil)
	if !fields[0].Info || fields[0].Value != "" || fields[0].HelpText != "Use your tracker API key." {
		t.Fatalf("api key info field rendered incorrectly: %+v", fields[0])
	}
	if !fields[1].Info || fields[1].Value != "" || fields[1].HelpText != "Seeding may prevent account inactivity." {
		t.Fatalf("account inactivity info field rendered incorrectly: %+v", fields[1])
	}
	if fields[2].Info || fields[2].Value != "1" {
		t.Fatalf("ordinary field rendered incorrectly: %+v", fields[2])
	}
}

func TestWithCoreCredentialsOverlaysURLAndKey(t *testing.T) {
	schema := IndexerSchema{Fields: []SchemaField{
		{Name: "baseUrl"},
		{Name: "apiKey", Required: true},
		{Name: "minimumSeeders", Value: float64(1)},
	}}
	settings := map[string]string{
		"baseUrl":        "https://old.test",
		"apiKey":         "old-key",
		"minimumSeeders": "3",
	}

	got := WithCoreCredentials(schema, settings, "https://tracker.test/", "core-key")
	if got["baseUrl"] != "https://tracker.test" {
		t.Fatalf("baseUrl = %q", got["baseUrl"])
	}
	if got["apiKey"] != "core-key" {
		t.Fatalf("apiKey = %q", got["apiKey"])
	}
	if got["minimumSeeders"] != "3" {
		t.Fatalf("minimumSeeders = %q", got["minimumSeeders"])
	}
}

func TestDiffSettingsIgnoresProwlarrReadbackOnlyFields(t *testing.T) {
	schema := IndexerSchema{Fields: []SchemaField{
		{Name: "baseUrl"},
		{Name: "info_key", Value: "masked"},
		{Name: "definitionFile", Value: "unit3d.yml"},
	}}
	desired := map[string]string{
		"baseUrl":        "https://tracker.test/",
		"info_key":       "real-secret",
		"definitionFile": "unit3d.yml",
	}
	actual := map[string]string{
		"baseUrl":        "https://tracker.test",
		"info_key":       "",
		"definitionFile": "prowlarr-readback.yml",
	}

	if diff := DiffSettings(schema, desired, actual); len(diff) != 0 {
		t.Fatalf("DiffSettings() = %v, want no drift", diff)
	}
}

func TestDiffSettingsNormalizesBlankCheckboxFalse(t *testing.T) {
	schema := IndexerSchema{Fields: []SchemaField{
		{Name: "torrentBaseSettings.preferMagnetUrl", Type: "checkbox"},
	}}
	actual := map[string]string{"torrentBaseSettings.preferMagnetUrl": ""}

	if diff := DiffSettings(schema, nil, actual); len(diff) != 0 {
		t.Fatalf("DiffSettings() = %v, want blank checkbox to equal false", diff)
	}

	actual["torrentBaseSettings.preferMagnetUrl"] = "true"
	if diff := DiffSettings(schema, nil, actual); len(diff) != 1 || diff[0] != "torrentBaseSettings.preferMagnetUrl" {
		t.Fatalf("DiffSettings() = %v, want preferMagnetUrl drift", diff)
	}
}

func TestIndexerSchemaForPayloadFillsRequiredRootDefaults(t *testing.T) {
	schema := IndexerSchema{AppProfileID: 0, Priority: 0}

	got := IndexerSchemaForPayload(schema, 7)
	if got.AppProfileID != 7 {
		t.Fatalf("AppProfileID = %d, want fallback app profile", got.AppProfileID)
	}
	if got.Priority != DefaultIndexerPriority {
		t.Fatalf("Priority = %d, want default priority", got.Priority)
	}
}

func TestIndexerSchemaForPayloadPreservesValidRootValues(t *testing.T) {
	schema := IndexerSchema{AppProfileID: 3, Priority: 10}

	got := IndexerSchemaForPayload(schema, 7)
	if got.AppProfileID != 3 {
		t.Fatalf("AppProfileID = %d, want schema value", got.AppProfileID)
	}
	if got.Priority != 10 {
		t.Fatalf("Priority = %d, want schema value", got.Priority)
	}
}

func TestManagedIndexerNameAddsSuffix(t *testing.T) {
	got := ManagedIndexerName("Example Tracker")
	if got != "Example Tracker [ptv]" {
		t.Fatalf("ManagedIndexerName() = %q", got)
	}
}

func TestManagedIndexerNamePreservesExistingSuffix(t *testing.T) {
	got := ManagedIndexerName("Example Tracker [PTV]")
	if got != "Example Tracker [PTV]" {
		t.Fatalf("ManagedIndexerName() = %q", got)
	}
}
