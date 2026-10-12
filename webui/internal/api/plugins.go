package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type PluginServerContext struct {
	Configured       bool   `json:"configured"`
	ServerType       string `json:"server_type"`
	ServerSoftware   string `json:"server_software"`
	MinecraftVersion string `json:"minecraft_version"`
	Supported        bool   `json:"supported"`
	Message          string `json:"message,omitempty"`
}
type InstalledPlugin struct {
	Name           string `json:"name,omitempty"`
	Version        string `json:"version,omitempty"`
	Filename       string `json:"filename"`
	Management     string `json:"management"`
	UpdateStatus   string `json:"update_status"`
	MetadataStatus string `json:"metadata_status"`
}
type PluginInventory struct {
	Server  PluginServerContext `json:"server"`
	Plugins []InstalledPlugin   `json:"plugins"`
}
type PluginDependency struct {
	ProjectID string `json:"project_id,omitempty"`
	VersionID string `json:"version_id,omitempty"`
	Filename  string `json:"file_name,omitempty"`
	Type      string `json:"dependency_type"`
}
type PluginCatalogVersion struct {
	ID           string             `json:"id"`
	Number       string             `json:"version_number"`
	Type         string             `json:"version_type"`
	Published    string             `json:"date_published"`
	GameVersions []string           `json:"game_versions"`
	Loaders      []string           `json:"loaders"`
	Dependencies []PluginDependency `json:"dependencies"`
}
type PluginCatalogProject struct {
	ID            string                `json:"project_id"`
	Name          string                `json:"title"`
	Description   string                `json:"description"`
	Source        string                `json:"source"`
	Versions      []string              `json:"versions"`
	Version       *PluginCatalogVersion `json:"version,omitempty"`
	Compatible    bool                  `json:"compatible"`
	Compatibility string                `json:"compatibility"`
	MetadataError string                `json:"metadata_error,omitempty"`
}
type PluginCatalog struct {
	Server     PluginServerContext    `json:"server"`
	Source     string                 `json:"source"`
	Projects   []PluginCatalogProject `json:"projects"`
	Offset     int                    `json:"offset"`
	NextOffset int                    `json:"next_offset"`
	Total      int                    `json:"total"`
}

// Plugin reads can include bounded runtime discovery and catalog metadata reads.
// Keep this timeout specific to these routes rather than changing the shared client.
func (c *Client) pluginRead(ctx context.Context, session, path string, out any) error {
	client := *c.http
	client.Timeout = 50 * time.Second
	scoped := &Client{http: &client}
	return scoped.do(ctx, http.MethodGet, path, session, nil, out)
}
func (c *Client) AdminPluginInventory(ctx context.Context, session string, updates bool) (PluginInventory, error) {
	var out PluginInventory
	path := "/v1/admin/plugins/installed"
	if updates {
		path = "/v1/admin/plugins/updates"
	}
	err := c.pluginRead(ctx, session, path, &out)
	return out, err
}
func (c *Client) AdminPluginDiscover(ctx context.Context, session string, query url.Values) (PluginCatalog, error) {
	var out PluginCatalog
	err := c.pluginRead(ctx, session, "/v1/admin/plugins/discover?"+query.Encode(), &out)
	return out, err
}
