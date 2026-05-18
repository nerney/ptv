package autobrr

import (
	"github.com/nerney/ptv/internal/config"
	"github.com/nerney/ptv/internal/integration"
)

// Compile-time assertion: *Client satisfies the Integration contract.
var _ integration.Integration = (*Client)(nil)

// Name implements integration.Integration.
func (c *Client) Name() string { return "autobrr" }

// DisplayName implements integration.Integration.
func (c *Client) DisplayName() string { return "Autobrr" }

// Enabled implements integration.Integration: the integration is active
// when Autobrr is enabled in config and both URL and API key are set.
func (c *Client) Enabled(cfg config.Config) bool {
	return cfg.AutobrrEnabled && cfg.AutobrrURL != "" && cfg.AutobrrAPIKey != ""
}
