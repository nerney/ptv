package autobrr

import (
	"strings"

	"github.com/nerney/ptv/internal/autobrrdefs"
)

const ExistingSecretValue = "__ptv_existing_secret__"

// SettingsFromPairs converts Autobrr's GET response setting slice into the
// persisted map shape used by PTV.
func SettingsFromPairs(in []Setting) map[string]string {
	out := make(map[string]string, len(in))
	for _, s := range in {
		out[s.Name] = s.Value
	}
	return out
}

// MergeSettings applies submitted values to existing Autobrr settings using
// the checked-in Autobrr definition as the field contract. Unknown keys are
// dropped only when a valid definition is available to define that contract.
func MergeSettings(def autobrrdefs.Def, existing, submitted map[string]string) map[string]string {
	fields := defFields(def)
	out := make(map[string]string, len(fields))
	for _, f := range fields {
		current, hasCurrent := existing[f.Name]
		if !hasCurrent && f.Default != "" {
			current = f.Default
			hasCurrent = true
		}

		next, submittedField := submitted[f.Name]
		if submittedField {
			if isSecretDefField(f) && (next == "" || next == ExistingSecretValue) && hasCurrent {
				out[f.Name] = current
				continue
			}
			out[f.Name] = next
			continue
		}
		if hasCurrent {
			out[f.Name] = current
		}
	}
	return out
}

// WithCoreCredentials overlays PTV's core tracker credential onto Autobrr
// fields that are known credential slots in the Autobrr definition. The
// tracker URL remains the indexer root base_url payload field.
func WithCoreCredentials(def autobrrdefs.Def, settings map[string]string, apiKey string) map[string]string {
	out := MergeSettings(def, settings, nil)
	for _, f := range defFields(def) {
		if isCredentialField(f.Name) {
			out[f.Name] = apiKey
		}
	}
	return out
}

func defFields(def autobrrdefs.Def) []autobrrdefs.Setting {
	out := make([]autobrrdefs.Setting, 0, len(def.Settings)+len(def.IRCSettings))
	seen := make(map[string]bool, len(def.Settings)+len(def.IRCSettings))
	for _, group := range [][]autobrrdefs.Setting{def.Settings, def.IRCSettings} {
		for _, f := range group {
			key := strings.ToLower(strings.TrimSpace(f.Name))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, f)
		}
	}
	return out
}

func isSecretDefField(f autobrrdefs.Setting) bool {
	return strings.EqualFold(f.Type, "secret") || isCredentialField(f.Name)
}

// SettingField is the sanitized view of an Autobrr schema field that the UI
// may render. Secret values are represented by ExistingSecretValue, never by
// the stored secret itself.
type SettingField struct {
	Name      string
	Label     string
	Help      string
	Type      string
	Value     string
	HasValue  bool
	Secret    bool
	Required  bool
	Layer     string // "root" or "irc"
}

// RenderFields returns all definition fields with values safe for frontend use.
// Settings are grouped by layer (root vs IRC) for template rendering.
func RenderFields(def autobrrdefs.Def, settings map[string]string) []SettingField {
	out := make([]SettingField, 0, len(def.Settings)+len(def.IRCSettings))

	// Render root settings first
	for _, f := range def.Settings {
		v, ok := settings[f.Name]
		if !ok && f.Default != "" {
			v = f.Default
			ok = true
		}
		secret := isSecretDefField(f)
		r := SettingField{
			Name:     f.Name,
			Label:    f.Label,
			Help:     f.Help,
			Type:     f.Type,
			HasValue: ok && v != "",
			Secret:   secret,
			Required: f.Required,
			Layer:    "root",
		}
		if !secret {
			r.Value = v
		} else if r.HasValue {
			r.Value = ExistingSecretValue
		}
		out = append(out, r)
	}

	// Render IRC settings second
	for _, f := range def.IRCSettings {
		v, ok := settings[f.Name]
		if !ok && f.Default != "" {
			v = f.Default
			ok = true
		}
		secret := isSecretDefField(f)
		r := SettingField{
			Name:     f.Name,
			Label:    f.Label,
			Help:     f.Help,
			Type:     f.Type,
			HasValue: ok && v != "",
			Secret:   secret,
			Required: f.Required,
			Layer:    "irc",
		}
		if !secret {
			r.Value = v
		} else if r.HasValue {
			r.Value = ExistingSecretValue
		}
		out = append(out, r)
	}

	return out
}

// DiffSettings returns definition field names whose normalized values differ.
func DiffSettings(def autobrrdefs.Def, desired, actual map[string]string) []string {
	d := MergeSettings(def, desired, nil)
	a := MergeSettings(def, actual, nil)
	var diff []string
	for _, f := range defFields(def) {
		if isSecretDefField(f) || isCredentialField(f.Name) {
			continue
		}
		dv, dok := d[f.Name]
		av, aok := a[f.Name]
		if comparableValue(f.Type, dv, dok) != comparableValue(f.Type, av, aok) {
			diff = append(diff, f.Name)
		}
	}
	return diff
}

// comparableValue normalizes a setting value for comparison, accounting for
// field type and the presence of the value.
func comparableValue(fieldType, value string, hasValue bool) string {
	if !hasValue {
		return ""
	}
	// Normalize URLs: lowercase and strip trailing slashes
	if fieldType == "text" && strings.Contains(strings.ToLower(value), "http") {
		return strings.ToLower(strings.TrimRight(value, "/"))
	}
	// Normalize booleans: treat empty/false as false, everything else as-is
	if fieldType == "checkbox" || fieldType == "bool" {
		if value == "" || strings.EqualFold(value, "false") || value == "0" {
			return "false"
		}
		return "true"
	}
	return value
}
