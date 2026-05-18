// Package autobrr is a minimal HTTP client for the Autobrr REST API.
//
// PTV uses Autobrr strictly as a destination for tracker credentials: we
// import existing Autobrr indexers and push/update our managed trackers
// into Autobrr. Nothing else (no filter management, no release queries,
// no irc network CRUD). The IRC connection status reported by Autobrr is
// surfaced read-only as a per-card indicator.
package autobrr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nerney/ptv/internal/logger"
)

// Client speaks the Autobrr HTTP API. Auth is via the X-API-Token header,
// using an API key minted in the Autobrr UI under Settings → API.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	log     *logger.Logger
}

func New(baseURL, apiKey string, log *logger.Logger) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 20 * time.Second},
		log:     log,
	}
}

// ---------- shared HTTP -------------------------------------------------

func (c *Client) do(method, path string, body io.Reader) (*http.Response, []byte, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("X-API-Token", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if c.log != nil {
			c.log.HTTP(logger.ERROR, "AUTOBRR", method, c.baseURL+path, 0, 0)
		}
		return nil, nil, err
	}

	respBody, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()

	lvl := logger.INFO
	if resp.StatusCode >= 400 {
		lvl = logger.ERROR
	}
	if c.log != nil {
		c.log.HTTP(lvl, "AUTOBRR", method, c.baseURL+path, resp.StatusCode, len(respBody))
	}

	if readErr != nil {
		return resp, respBody, readErr
	}
	return resp, respBody, nil
}

// Ping verifies the URL + API key combination by hitting the auth-required
// /api/config endpoint. 200 = both URL reachable AND key valid.
func (c *Client) Ping() error {
	resp, _, err := c.do("GET", "/api/config", nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("autobrr rejected the API key (401)")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("autobrr returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// ---------- indexers ----------------------------------------------------

// Setting is the {name, value} pair used in Autobrr's indexer settings array.
type Setting struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

// Indexer represents a configured indexer in Autobrr (one tracker entry).
type Indexer struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Identifier     string    `json:"identifier"`            // "alpharatio", "blutopia", …
	IdentifierExt  string    `json:"identifier_external"`   // human-readable, may be empty
	Enabled        bool      `json:"enabled"`
	Implementation string    `json:"implementation"`        // "irc", "torznab", "rss", …
	BaseURL        string    `json:"base_url"`
	Settings       []Setting `json:"settings"`
}

// IndexerSchema is one entry from Autobrr's built-in indexer catalog.
type IndexerSchema struct {
	Identifier     string    `json:"identifier"`
	Name           string    `json:"name"`
	Implementation string    `json:"implementation"`
	URLs           []string  `json:"urls"`
	Settings       []Setting `json:"settings"`
}

func (c *Client) GetIndexers() ([]Indexer, error) {
	resp, body, err := c.do("GET", "/api/indexer", nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("autobrr HTTP %d", resp.StatusCode)
	}
	var out []Indexer
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return out, nil
}

func (c *Client) GetIndexer(id int64) (*Indexer, error) {
	resp, body, err := c.do("GET", fmt.Sprintf("/api/indexer/%d", id), nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("autobrr HTTP %d", resp.StatusCode)
	}
	var idx Indexer
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return &idx, nil
}

// GetSchemas returns the catalog of indexer definitions known to Autobrr.
// Used by the import + add flows to look up the right identifier + setting
// shape for a given tracker URL.
func (c *Client) GetSchemas() ([]IndexerSchema, error) {
	resp, body, err := c.do("GET", "/api/indexer/schema", nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("autobrr HTTP %d", resp.StatusCode)
	}
	var out []IndexerSchema
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return out, nil
}

// SchemaForURL returns the first schema whose URLs include the supplied
// tracker URL (case-insensitive, trailing slash agnostic).
func (c *Client) SchemaForURL(trackerURL string) (*IndexerSchema, error) {
	schemas, err := c.GetSchemas()
	if err != nil {
		return nil, err
	}
	want := NormalizeURL(trackerURL)
	for _, s := range schemas {
		for _, u := range s.URLs {
			if NormalizeURL(u) == want {
				return &s, nil
			}
		}
	}
	return nil, fmt.Errorf("no Autobrr schema matches URL %q", trackerURL)
}

// AddIndexer creates a new indexer in Autobrr from the supplied schema,
// populating its settings with the user's tracker credentials. Returns the
// created indexer (with its assigned ID) on success.
func (c *Client) AddIndexer(schema IndexerSchema, trackerURL, apiKey string) (*Indexer, error) {
	payload := Indexer{
		Name:           schema.Name,
		Identifier:     schema.Identifier,
		Enabled:        true,
		Implementation: schema.Implementation,
		BaseURL:        strings.TrimRight(trackerURL, "/"),
		Settings:       populateSettings(schema.Settings, apiKey),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	resp, body, err := c.do("POST", "/api/indexer", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("autobrr HTTP %d: %s", resp.StatusCode, string(body))
	}
	var idx Indexer
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return &idx, nil
}

// UpdateIndexer pushes refreshed credentials into an existing Autobrr indexer.
func (c *Client) UpdateIndexer(existing Indexer, trackerURL, apiKey string) (*Indexer, error) {
	existing.BaseURL = strings.TrimRight(trackerURL, "/")
	existing.Settings = populateSettings(existing.Settings, apiKey)
	data, err := json.Marshal(existing)
	if err != nil {
		return nil, err
	}
	resp, body, err := c.do("PUT",
		fmt.Sprintf("/api/indexer/%d", existing.ID),
		bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("autobrr HTTP %d: %s", resp.StatusCode, string(body))
	}
	if len(body) == 0 {
		return &existing, nil
	}
	var updated Indexer
	if err := json.Unmarshal(body, &updated); err != nil {
		// Some Autobrr versions return empty body on PUT — treat as success.
		return &existing, nil
	}
	return &updated, nil
}

// SetEnabled flips the enable flag on an Autobrr indexer.
func (c *Client) SetEnabled(id int64, enabled bool) error {
	payload := map[string]bool{"enabled": enabled}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, body, err := c.do("PATCH",
		fmt.Sprintf("/api/indexer/%d/enabled", id),
		bytes.NewReader(data))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("autobrr HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (c *Client) DeleteIndexer(id int64) error {
	resp, body, err := c.do("DELETE", fmt.Sprintf("/api/indexer/%d", id), nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("autobrr HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// ---------- IRC networks ------------------------------------------------

// IRCNetwork is the read-only view of one IRC network in Autobrr. The fields
// we care about for the per-card indicator are Name (for matching to an
// indexer) and Connected.
type IRCNetwork struct {
	ID             int64        `json:"id"`
	Name           string       `json:"name"`
	Enabled        bool         `json:"enabled"`
	Server         string       `json:"server"`
	Port           int          `json:"port"`
	TLS            bool         `json:"tls"`
	Connected      bool         `json:"connected"`
	ConnectedSince *time.Time   `json:"connected_since,omitempty"`
	Channels       []IRCChannel `json:"channels"`
}

type IRCChannel struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	Monitoring bool   `json:"monitoring"`
}

func (c *Client) GetIRCNetworks() ([]IRCNetwork, error) {
	resp, body, err := c.do("GET", "/api/irc", nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("autobrr HTTP %d", resp.StatusCode)
	}
	var out []IRCNetwork
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return out, nil
}

// ---------- helpers -----------------------------------------------------

// NormalizeURL lowercases and strips trailing slashes for set-membership
// checks. Matches the convention used by internal/prowlarr.
func NormalizeURL(u string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(u)), "/")
}

// populateSettings fills credential-shaped setting fields with the supplied
// API key. Autobrr definitions name these fields differently per tracker
// ("api_key", "authkey", "torrent_pass", "passkey", "rsskey", …) so we
// substitute everything that looks like a key/token slot.
//
// PTV stores ONE credential per tracker (the UNIT3D API key). Trackers that
// require multiple separate Autobrr secrets (auth + torrent_pass + irc key
// for non-UNIT3D software) won't be fully populated by this and would need
// to be finished in the Autobrr UI.
func populateSettings(in []Setting, apiKey string) []Setting {
	out := make([]Setting, len(in))
	copy(out, in)
	for i, s := range out {
		low := strings.ToLower(s.Name)
		switch {
		case strings.Contains(low, "api_key"),
			strings.Contains(low, "apikey"),
			strings.Contains(low, "api-key"),
			strings.Contains(low, "rsskey"),
			low == "key",
			low == "token":
			out[i].Value = apiKey
		}
	}
	return out
}

// MatchNetwork picks the IRC network whose name best matches the supplied
// indexer identifier or name. Autobrr names networks deterministically when
// auto-created from an indexer (usually the indexer identifier or its
// uppercased form), but operators can rename them — so the comparison is
// case-insensitive substring in both directions. Returns nil if nothing
// looks like a match.
func MatchNetwork(networks []IRCNetwork, indexerIdentifier, indexerName string) *IRCNetwork {
	ident := strings.ToLower(strings.TrimSpace(indexerIdentifier))
	name := strings.ToLower(strings.TrimSpace(indexerName))
	for i := range networks {
		nl := strings.ToLower(networks[i].Name)
		if ident != "" && (strings.Contains(nl, ident) || strings.Contains(ident, nl)) {
			return &networks[i]
		}
		if name != "" && (strings.Contains(nl, name) || strings.Contains(name, nl)) {
			return &networks[i]
		}
	}
	return nil
}
