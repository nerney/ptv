package handlers

import (
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"
	"time"
)

// templateFuncs returns the functions available inside every template.
// Only add here what templates actually call — dead registrations cost
// nothing at runtime but make the map misleading.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"staleAge":      staleAge,
		"isStale":       isStale,
		"ratioClassStr": ratioClassStr,
		"hasInt":        hasInt,
	}
}

func hasInt(values []int, target int) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func formCheckboxChecked(values []string) bool {
	for _, raw := range values {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true", "1", "on", "yes":
			return true
		}
	}
	return false
}

func submittedFormValue(values []string, fieldType string) string {
	if strings.EqualFold(fieldType, "checkbox") {
		if formCheckboxChecked(values) {
			return "true"
		}
		return "false"
	}
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// ratioClass maps a numeric ratio to a CSS class name.
func ratioClass(f float64) string {
	switch {
	case f == 0:
		return "ratio-none"
	case f < 0.5:
		return "ratio-danger"
	case f < 1.0:
		return "ratio-warn"
	case f < 2.0:
		return "ratio-ok"
	default:
		return "ratio-high"
	}
}

// ratioClassStr is the template-facing variant that parses a pre-formatted
// ratio string (e.g. "2.71") before delegating to ratioClass.
func ratioClassStr(s string) string {
	if s == "" {
		return "ratio-none"
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return "ratio-none"
	}
	return ratioClass(f)
}

// staleAge formats a time.Time as a human-readable "N ago" string.
// Returns "never synced" for a nil pointer.
func staleAge(t *time.Time) string {
	if t == nil {
		return "never synced"
	}
	d := time.Since(*t).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	m := int(math.Floor(d.Minutes()))
	s := int(d.Seconds()) % 60
	if m < 60 {
		return fmt.Sprintf("%dm %02ds ago", m, s)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("%dh %02dm ago", h, m)
}

// isStale returns true when the last sync was more than 10 minutes ago.
func isStale(t *time.Time) bool {
	if t == nil {
		return false
	}
	return time.Since(*t) > 10*time.Minute
}
