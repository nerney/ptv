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
