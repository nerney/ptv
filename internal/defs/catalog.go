package defs

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/nerney/ptv/internal/trackertype"
)

// TrackerDef is a tracker parsed from a Prowlarr indexer definition file
// and matched to a registered TrackerType.
type TrackerDef struct {
	Name   string
	TypeID string // stable trackertype.Type.ID(), e.g. "unit3d"
	URLs   []string
}

// defFile is the subset of a Prowlarr YAML definition that we inspect
// during catalog scanning.
type defFile struct {
	Name   string   `yaml:"name"`
	Links  []string `yaml:"links"`
	Search struct {
		Paths []struct {
			Path string `yaml:"path"`
		} `yaml:"paths"`
	} `yaml:"search"`
}

// parseCatalog scans all definition YAML files under <dir>/definitions/*/*.yml
// and returns one TrackerDef per file whose search paths match a registered
// TrackerType. Files that don't match any known type are silently skipped.
func parseCatalog(dir string) ([]TrackerDef, error) {
	pattern := filepath.Join(dir, "definitions", "*", "*.yml")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	var out []TrackerDef
	for _, f := range files {
		if def := parseAndDetect(f); def != nil {
			out = append(out, *def)
		}
	}
	return out, nil
}

// parseAndDetect parses a YAML definition file and returns a TrackerDef if
// it matches any registered TrackerType, or nil if it doesn't match or fails
// to parse.
func parseAndDetect(path string) *TrackerDef {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var d defFile
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil
	}
	if d.Name == "" || len(d.Links) == 0 {
		return nil
	}

	paths := make([]string, len(d.Search.Paths))
	for i, p := range d.Search.Paths {
		paths[i] = p.Path
	}

	for _, tt := range trackertype.All() {
		if tt.DetectDef(paths) {
			return &TrackerDef{
				Name:   d.Name,
				TypeID: tt.ID(),
				URLs:   d.Links,
			}
		}
	}
	return nil
}
