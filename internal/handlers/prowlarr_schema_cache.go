package handlers

import (
	"fmt"
	"strings"

	"github.com/nerney/ptv/internal/prowlarr"
)

// warmProwlarrSchemas fetches the full indexer schema list from Prowlarr
// and stores it in the Handler's in-memory cache, keyed by lowercase name.
// It is a no-op when Prowlarr is not fully configured. Intended to run as
// a goroutine — failures are logged but never propagated.
func (h *Handler) warmProwlarrSchemas() {
	cfg := h.store.Get()
	if !cfg.ProwlarrEnabled || cfg.ProwlarrURL == "" || cfg.ProwlarrAPIKey == "" {
		return
	}
	client := prowlarr.New(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, h.log)
	schemas, err := client.GetAllSchemas()
	if err != nil {
		h.log.Err("PROWLARR", "schema cache: "+err.Error())
		return
	}
	byName := make(map[string]prowlarr.IndexerSchema, len(schemas))
	for _, s := range schemas {
		byName[strings.ToLower(s.Name)] = s
	}
	h.pSchemasMu.Lock()
	h.pSchemas = byName
	h.pSchemasMu.Unlock()
	h.log.Info("PROWLARR", fmt.Sprintf("Cached %d indexer schemas", len(schemas)))
}

// prowlarrSchemaByName looks up a schema by name from the in-memory cache.
// On a cache miss it falls back to a live Prowlarr fetch — this covers the
// window before warmProwlarrSchemas has completed on a fresh login.
func (h *Handler) prowlarrSchemaByName(name string) (*prowlarr.IndexerSchema, error) {
	h.pSchemasMu.RLock()
	s, ok := h.pSchemas[strings.ToLower(name)]
	h.pSchemasMu.RUnlock()
	if ok {
		return &s, nil
	}
	cfg := h.store.Get()
	client := prowlarr.New(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, h.log)
	return client.SchemaByName(name)
}
