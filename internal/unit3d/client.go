package unit3d

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nerney/ptv/internal/logger"
)

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

// Stats matches the flat JSON returned by GET /api/user in UNIT3D.
// uploaded, downloaded, buffer are pre-formatted strings ("1.23 TiB").
// ratio and seedbonus are also formatted strings ("2.71", "12345.67").
type Stats struct {
	Username   string `json:"username"`
	Group      string `json:"group"`
	Uploaded   string `json:"uploaded"`
	Downloaded string `json:"downloaded"`
	Ratio      string `json:"ratio"`
	Buffer     string `json:"buffer"`
	SeedBonus  string `json:"seedbonus"`
	Seeding    int    `json:"seeding"`
	Leeching   int    `json:"leeching"`
	HitAndRuns int    `json:"hit_and_runs"`
}

func (c *Client) FetchStats() (*Stats, error) {
	endpoint := c.baseURL + "/api/user"

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	// UNIT3D accepts both Bearer token header and ?api_token= query param.
	// Sending both covers all versions and server configurations.
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	q := req.URL.Query()
	q.Set("api_token", c.apiKey)
	req.URL.RawQuery = q.Encode()

	resp, err := c.http.Do(req)
	if err != nil {
		if c.log != nil {
			c.log.HTTP(logger.ERROR, "UNIT3D", "GET", endpoint, 0, 0)
		}
		return nil, err
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	lvl := logger.INFO
	if resp.StatusCode >= 400 {
		lvl = logger.ERROR
	}
	if c.log != nil {
		c.log.HTTP(lvl, "UNIT3D", "GET", endpoint, resp.StatusCode, len(body))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, c.baseURL)
	}

	var stats Stats
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &stats, nil
}
