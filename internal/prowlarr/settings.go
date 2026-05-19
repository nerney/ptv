package prowlarr

import (
	"encoding/json"
	"fmt"
	"html"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const ExistingSecretValue = "__ptv_existing_secret__"

const DefaultIndexerPriority = 25

// SettingField is the sanitized view of a Prowlarr schema field that the UI
// may render. Secret values are represented by ExistingSecretValue, never by
// the stored secret itself.
type SettingField struct {
	Name          string
	Label         string
	HelpText      string
	HelpLink      string
	Placeholder   string
	Type          string
	Value         string
	HasValue      bool
	Secret        bool
	Info          bool
	Required      bool
	Advanced      bool
	SelectOptions []SettingOption
}

type SettingOption struct {
	Name  string
	Value string
	Hint  string
}

// SettingsFromFields extracts a schema-backed settings map from a Prowlarr
// indexer's current fields. Only fields present in schema are kept.
func SettingsFromFields(schema IndexerSchema, fields []SchemaField) map[string]string {
	allowed := schemaFieldNames(schema)
	out := make(map[string]string, len(fields))
	for _, f := range fields {
		if !allowed[f.Name] {
			continue
		}
		out[f.Name] = valueString(f.Value)
	}
	return out
}

// MergeSettings returns the schema-backed settings after applying submitted
// form values to existing settings. Unknown existing/submitted keys are
// dropped. Blank secret submissions preserve the existing saved value.
func MergeSettings(schema IndexerSchema, existing, submitted map[string]string) map[string]string {
	out := make(map[string]string, len(schema.Fields))
	for _, f := range schema.Fields {
		current, hasCurrent := existing[f.Name]
		if !hasCurrent && hasRealDefault(f.Value) {
			current = valueString(f.Value)
			hasCurrent = true
		}

		next, submittedField := submitted[f.Name]
		if submittedField {
			if IsSecretField(f) && (next == "" || next == ExistingSecretValue) && hasCurrent {
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

// FieldsForPayload overlays settings onto schema fields for create/update
// calls. Unknown setting keys are ignored.
func FieldsForPayload(schema IndexerSchema, settings map[string]string) []SchemaField {
	out := make([]SchemaField, len(schema.Fields))
	copy(out, schema.Fields)
	for i := range out {
		if v, ok := settings[out[i].Name]; ok {
			out[i].Value = typedValue(out[i], v)
		}
	}
	return out
}

func IndexerSchemaForPayload(schema IndexerSchema, appProfileID int) IndexerSchema {
	if schema.AppProfileID <= 0 {
		schema.AppProfileID = appProfileID
	}
	if schema.Priority < 1 || schema.Priority > 50 {
		schema.Priority = DefaultIndexerPriority
	}
	return schema
}

// WithCoreCredentials returns schema-backed settings with PTV's core tracker
// URL/API key overlaid onto Prowlarr's matching URL/key fields.
func WithCoreCredentials(schema IndexerSchema, settings map[string]string, trackerURL, apiKey string) map[string]string {
	out := MergeSettings(schema, settings, nil)
	for _, f := range schema.Fields {
		low := strings.ToLower(f.Name)
		switch {
		case low == "baseurl" || low == "sitelink" || strings.Contains(low, "url"):
			out[f.Name] = strings.TrimRight(trackerURL, "/")
		case low == "apikey" || low == "api_key" || low == "passkey" || low == "apitoken" || strings.Contains(low, "key") || strings.Contains(low, "token"):
			out[f.Name] = apiKey
		}
	}
	return out
}

// RenderFields returns all schema fields with values safe for frontend use.
func RenderFields(schema IndexerSchema, settings map[string]string) []SettingField {
	out := make([]SettingField, 0, len(schema.Fields))
	for _, f := range schema.Fields {
		if IsURLField(f) || IsDefinitionFileField(f) {
			continue
		}
		v, ok := settings[f.Name]
		if !ok && hasRealDefault(f.Value) {
			v = valueString(f.Value)
			ok = true
		}
		secret := IsSecretField(f)
		info := IsInfoField(f)
		r := SettingField{
			Name:          f.Name,
			Label:         f.Label,
			HelpText:      displayHelpText(f),
			HelpLink:      f.HelpLink,
			Placeholder:   f.Placeholder,
			Type:          f.Type,
			HasValue:      ok && v != "",
			Secret:        secret,
			Info:          info,
			Required:      f.Required,
			Advanced:      f.Advanced,
			SelectOptions: renderOptions(f.SelectOptions),
		}
		if info {
			if r.HelpText == "" {
				r.HelpText = plainHelpText(v)
			}
		} else if !secret {
			r.Value = v
		} else if r.HasValue {
			r.Value = ExistingSecretValue
		}
		out = append(out, r)
	}
	return out
}

func displayHelpText(f SchemaField) string {
	text := plainHelpText(f.HelpText)
	label := plainHelpText(f.Label)
	if label != "" {
		text = strings.TrimSpace(strings.TrimPrefix(text, label))
	}
	for _, heading := range []string{"About Your API Key", "Account Inactivity"} {
		text = strings.TrimSpace(strings.TrimPrefix(text, heading))
	}
	return text
}

func plainHelpText(s string) string {
	if s == "" {
		return ""
	}

	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
		case '>':
			inTag = false
			b.WriteByte(' ')
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
	return strings.Join(strings.Fields(html.UnescapeString(b.String())), " ")
}

func renderOptions(in []SelectOption) []SettingOption {
	out := make([]SettingOption, 0, len(in))
	for _, opt := range in {
		out = append(out, SettingOption{
			Name:  opt.Name,
			Value: valueString(opt.Value),
			Hint:  opt.Hint,
		})
	}
	return out
}

// SettingsEqual compares schema-backed settings maps. Unknown keys are ignored
// and default schema values are applied before comparison.
func SettingsEqual(schema IndexerSchema, left, right map[string]string) bool {
	return len(DiffSettings(schema, left, right)) == 0
}

// DiffSettings returns schema field names whose normalized values differ.
func DiffSettings(schema IndexerSchema, left, right map[string]string) []string {
	l := MergeSettings(schema, left, nil)
	r := MergeSettings(schema, right, nil)
	var diff []string
	for _, f := range schema.Fields {
		if ignoreForDrift(f) {
			continue
		}
		lv, lok := l[f.Name]
		rv, rok := r[f.Name]
		if comparableSettingValue(f, lv, lok) != comparableSettingValue(f, rv, rok) {
			diff = append(diff, f.Name)
		}
	}
	sort.Strings(diff)
	return diff
}

func ignoreForDrift(f SchemaField) bool {
	return IsDefinitionFileField(f) || IsSecretField(f)
}

func comparableSettingValue(f SchemaField, value string, ok bool) string {
	if !ok {
		value = ""
		if hasRealDefault(f.Value) {
			value = valueString(f.Value)
		}
	}
	if IsURLField(f) {
		return NormalizeURL(value)
	}
	if isBoolField(f) {
		if value == "" {
			return "false"
		}
		return strconv.FormatBool(value == "true" || value == "on" || value == "1")
	}
	if isNumberField(f) {
		if n, err := strconv.ParseFloat(value, 64); err == nil {
			return strconv.FormatFloat(n, 'f', -1, 64)
		}
	}
	return value
}

func isBoolField(f SchemaField) bool {
	if f.Type == "checkbox" {
		return true
	}
	_, ok := f.Value.(bool)
	return ok
}

func isNumberField(f SchemaField) bool {
	if f.Type == "number" {
		return true
	}
	switch f.Value.(type) {
	case float64, float32, int, int64, json.Number:
		return true
	}
	return false
}

// IsSecretField follows PTV's Prowlarr rule: required schema fields without a
// real default are secret-like and must not be rendered back to the frontend.
func IsSecretField(f SchemaField) bool {
	low := strings.ToLower(f.Name)
	return (f.Required && !hasRealDefault(f.Value)) ||
		low == "apikey" ||
		low == "api_key" ||
		low == "passkey" ||
		low == "apitoken" ||
		strings.Contains(low, "key") ||
		strings.Contains(low, "token")
}

func IsURLField(f SchemaField) bool {
	low := strings.ToLower(f.Name)
	return low == "baseurl" || low == "sitelink" || strings.Contains(low, "url")
}

func IsDefinitionFileField(f SchemaField) bool {
	low := strings.ToLower(f.Name)
	return low == "definitionfile" ||
		low == "definition_file" ||
		strings.Contains(low, "definitionfile") ||
		strings.Contains(low, "definition_file")
}

func IsInfoField(f SchemaField) bool {
	name := strings.ToLower(f.Name)
	label := strings.ToLower(f.Label)
	help := strings.ToLower(plainHelpText(f.HelpText))
	return f.Type == "info" ||
		strings.HasPrefix(name, "info_") ||
		name == "accountinactivity" ||
		strings.Contains(label, "about your api key") ||
		strings.Contains(label, "account inactivity") ||
		strings.Contains(help, "about your api key") ||
		strings.Contains(help, "account inactivity")
}

func schemaFieldNames(schema IndexerSchema) map[string]bool {
	out := make(map[string]bool, len(schema.Fields))
	for _, f := range schema.Fields {
		out[f.Name] = true
	}
	return out
}

func hasRealDefault(v interface{}) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case string:
		return x != ""
	case json.Number:
		return string(x) != ""
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array:
		return rv.Len() > 0
	}
	return true
}

func valueString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case json.Number:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

func typedValue(f SchemaField, value string) interface{} {
	switch f.Type {
	case "checkbox":
		return value == "true" || value == "on" || value == "1"
	case "number":
		if n, err := strconv.ParseFloat(value, 64); err == nil {
			return n
		}
	}
	switch f.Value.(type) {
	case bool:
		return value == "true" || value == "on" || value == "1"
	case float64:
		if n, err := strconv.ParseFloat(value, 64); err == nil {
			return n
		}
	}
	return value
}
