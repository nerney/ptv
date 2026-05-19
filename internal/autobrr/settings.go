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
