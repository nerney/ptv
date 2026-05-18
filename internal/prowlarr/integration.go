package prowlarr

import (
	"github.com/nerney/ptv/internal/config"
	"github.com/nerney/ptv/internal/integration"
)

// Compile-time assertion: *Client satisfies the Integration contract.
var _ integration.Integration = (*Client)(nil)

// Name implements integration.Integration.
func (c *Client) Name() string { return "prowlarr" }

// DisplayName implements integration.Integration.
func (c *Client) DisplayName() string { return "Prowlarr" }

// Enabled implements integration.Integration: the integration is active
// when Prowlarr is enabled in config and both URL and API key are set.
func (c *Client) Enabled(cfg config.Config) bool {
	return cfg.ProwlarrEnabled && cfg.ProwlarrURL != "" && cfg.ProwlarrAPIKey != ""
}
