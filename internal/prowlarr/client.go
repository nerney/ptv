// Package prowlarr is a minimal HTTP client for the Prowlarr REST API.
//
// PTV uses Prowlarr to import already-configured indexers and to push/update
// managed trackers (credentials, enable/disable state) into Prowlarr. Only
// the indexer resource is touched — no tags, download clients, or other
// Prowlarr features.
package prowlarr

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nerney/ptv/internal/logger"
)

// Client speaks the Prowlarr v1 REST API. Auth is via the X-Api-Key header.
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

type IndexerSchema struct {
	ID                 int           `json:"id"`
	Name               string        `json:"name"`
	Description        string        `json:"description,omitempty"`
	Implementation     string        `json:"implementation"`
	ImplementationName string        `json:"implementationName"`
	ConfigContract     string        `json:"configContract"`
	AppProfileID       int           `json:"appProfileId"`
	Priority           int           `json:"priority"`
	InfoLink           string        `json:"infoLink,omitempty"`
	Tags               []int         `json:"tags"`
	Fields             []SchemaField `json:"fields"`
}

type SchemaField struct {
	Name          string         `json:"name"`
	Label         string         `json:"label"`
	HelpText      string         `json:"helpText,omitempty"`
	HelpLink      string         `json:"helpLink,omitempty"`
	Placeholder   string         `json:"placeholder,omitempty"`
	Type          string         `json:"type"`
	Value         interface{}    `json:"value,omitempty"`
	SelectOptions []SelectOption `json:"selectOptions,omitempty"`
	Advanced      bool           `json:"advanced,omitempty"`
	Required      bool           `json:"required,omitempty"`
}

type SelectOption struct {
	Name  string      `json:"name"`
	Value interface{} `json:"value"`
	Hint  string      `json:"hint,omitempty"`
}

type Indexer struct {
	ID                 int           `json:"id"`
	Name               string        `json:"name"`
	Enable             bool          `json:"enable"`
	Implementation     string        `json:"implementation"`
	ImplementationName string        `json:"implementationName"`
	ConfigContract     string        `json:"configContract"`
	AppProfileID       int           `json:"appProfileId"`
	Priority           int           `json:"priority"`
	Fields             []SchemaField `json:"fields"`
	Tags               []int         `json:"tags"`
	IndexerUrls        []string      `json:"indexerUrls"`
	DefinitionName     string        `json:"definitionName"`
}

type AppProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func (c *Client) do(method, path string, body io.Reader) (*http.Response, []byte, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if c.log != nil {
			c.log.HTTP(logger.ERROR, "PROWLARR", method, c.baseURL+path, 0, 0)
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
		c.log.HTTP(lvl, "PROWLARR", method, c.baseURL+path, resp.StatusCode, len(respBody))
	}

	if readErr != nil {
		return resp, respBody, readErr
	}
	return resp, respBody, nil
}

func (c *Client) Ping() error {
	resp, _, err := c.do("GET", "/api/v1/system/status", nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("prowlarr returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// NormalizeURL lowercases and strips trailing slashes for set membership checks.
func NormalizeURL(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	u = strings.TrimRight(u, "/")
	return u
}

// ExtractCreds pulls baseUrl/sitelink and apikey-like values out of an
// indexer's stored field values.
func ExtractCreds(fields []SchemaField) (url, apiKey string) {
	for _, f := range fields {
		if f.Value == nil {
			continue
		}
		s, ok := f.Value.(string)
		if !ok || s == "" {
			continue
		}
		switch strings.ToLower(f.Name) {
		case "baseurl", "sitelink":
			url = s
		case "apikey", "api_key", "apitoken":
			apiKey = s
		}
	}
	return
}

func (c *Client) GetAllSchemas() ([]IndexerSchema, error) {
	resp, body, err := c.do("GET", "/api/v1/indexer/schema", nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prowlarr returned HTTP %d", resp.StatusCode)
	}
	var schemas []IndexerSchema
	if err := json.Unmarshal(body, &schemas); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return schemas, nil
}

func (c *Client) GetIndexers() ([]Indexer, error) {
	resp, body, err := c.do("GET", "/api/v1/indexer", nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prowlarr returned HTTP %d", resp.StatusCode)
	}
	var out []Indexer
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return out, nil
}

func (c *Client) GetAppProfiles() ([]AppProfile, error) {
	resp, body, err := c.do("GET", "/api/v1/appprofile", nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prowlarr returned HTTP %d", resp.StatusCode)
	}
	var out []AppProfile
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return out, nil
}

func (c *Client) FirstAppProfileID() (int, error) {
	profiles, err := c.GetAppProfiles()
	if err != nil {
		return 0, err
	}
	for _, p := range profiles {
		if p.ID > 0 {
			return p.ID, nil
		}
	}
	return 0, fmt.Errorf("no Prowlarr app profiles found")
}

func (c *Client) SchemaByName(name string) (*IndexerSchema, error) {
	schemas, err := c.GetAllSchemas()
	if err != nil {
		return nil, err
	}
	for _, s := range schemas {
		if strings.EqualFold(s.Name, name) {
			return &s, nil
		}
	}
	return nil, fmt.Errorf("schema %q not found in Prowlarr", name)
}

func (c *Client) AddIndexer(schema IndexerSchema, trackerURL, apiKey string) (*Indexer, error) {
	fields := populateFields(schema.Fields, trackerURL, apiKey)
	return c.AddIndexerWithFields(schema, fields)
}

func (c *Client) AddIndexerWithFields(schema IndexerSchema, fields []SchemaField) (*Indexer, error) {
	payload := map[string]interface{}{
		"name":               schema.Name,
		"enable":             true,
		"implementation":     schema.Implementation,
		"implementationName": schema.ImplementationName,
		"configContract":     schema.ConfigContract,
		"appProfileId":       schema.AppProfileID,
		"priority":           schema.Priority,
		"fields":             fields,
		"tags":               []int{},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	resp, body, err := c.do("POST", "/api/v1/indexer?forceSave=true", strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("prowlarr HTTP %d: %s", resp.StatusCode, string(body))
	}
	var idx Indexer
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func (c *Client) SetEnabled(indexer Indexer, enabled bool) error {
	indexer.Enable = enabled
	data, err := json.Marshal(indexer)
	if err != nil {
		return err
	}
	resp, body, err := c.do("PUT", fmt.Sprintf("/api/v1/indexer/%d", indexer.ID), strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("prowlarr HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// UpdateIndexer pushes our tracker URL + API key into an existing Prowlarr
// indexer, using forceSave=true so Prowlarr accepts the changes without
// re-testing the connection (the dashboard already validated against the tracker).
func (c *Client) UpdateIndexer(indexer Indexer, trackerURL, apiKey string) (*Indexer, error) {
	indexer.Fields = populateFields(indexer.Fields, trackerURL, apiKey)
	return c.UpdateIndexerWithFields(indexer, indexer.Fields)
}

func (c *Client) UpdateIndexerWithFields(indexer Indexer, fields []SchemaField) (*Indexer, error) {
	indexer.Fields = fields
	data, err := json.Marshal(indexer)
	if err != nil {
		return nil, err
	}
	resp, body, err := c.do("PUT",
		fmt.Sprintf("/api/v1/indexer/%d?forceSave=true", indexer.ID),
		strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prowlarr HTTP %d: %s", resp.StatusCode, string(body))
	}
	var updated Indexer
	if err := json.Unmarshal(body, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (c *Client) DeleteIndexer(id int) error {
	resp, _, err := c.do("DELETE", fmt.Sprintf("/api/v1/indexer/%d", id), nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("prowlarr HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) GetIndexer(id int) (*Indexer, error) {
	resp, body, err := c.do("GET", fmt.Sprintf("/api/v1/indexer/%d", id), nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prowlarr HTTP %d", resp.StatusCode)
	}
	var idx Indexer
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func populateFields(fields []SchemaField, trackerURL, apiKey string) []SchemaField {
	out := make([]SchemaField, len(fields))
	copy(out, fields)
	for i, f := range out {
		low := strings.ToLower(f.Name)
		switch {
		case low == "baseurl" || low == "sitelink" || strings.Contains(low, "url"):
			out[i].Value = strings.TrimRight(trackerURL, "/")
		case low == "apikey" || low == "api_key" || low == "passkey" || low == "apitoken" || strings.Contains(low, "key") || strings.Contains(low, "token"):
			out[i].Value = apiKey
		}
	}
	return out
}
