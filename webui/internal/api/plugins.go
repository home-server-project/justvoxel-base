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
	ID               string                `json:"project_id"`
	Slug             string                `json:"slug"`
	InstallAvailable bool                  `json:"install_available"`
	Name             string                `json:"title"`
	Description      string                `json:"description"`
	Source           string                `json:"source"`
	Versions         []string              `json:"versions"`
	Version          *PluginCatalogVersion `json:"version,omitempty"`
	Compatible       bool                  `json:"compatible"`
	Compatibility    string                `json:"compatibility"`
	MetadataError    string                `json:"metadata_error,omitempty"`
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

type PluginInstallRequest struct {
	ProjectID string `json:"project_id"`
	VersionID string `json:"version_id"`
}
type PluginInstallApplyRequest struct {
	ProjectID       string `json:"project_id"`
	VersionID       string `json:"version_id"`
	PlanFingerprint string `json:"plan_fingerprint"`
	ConfirmPlayers  bool   `json:"confirm_players"`
}
type PluginInstallArtifact struct {
	ProjectID     string   `json:"project_id"`
	VersionID     string   `json:"version_id"`
	VersionNumber string   `json:"version_number"`
	Name          string   `json:"project_name"`
	Filename      string   `json:"filename"`
	Size          int64    `json:"size"`
	Requested     bool     `json:"requested"`
	RequiredBy    []string `json:"required_by"`
	Satisfied     string   `json:"satisfied,omitempty"`
}
type PluginInstallPlan struct {
	ProjectID            string                  `json:"project_id"`
	VersionID            string                  `json:"version_id"`
	Name                 string                  `json:"project_name"`
	Description          string                  `json:"description"`
	VersionNumber        string                  `json:"version_number"`
	Compatibility        string                  `json:"compatibility"`
	Server               PluginServerContext     `json:"server"`
	Location             string                  `json:"installation_location"`
	RestartRequired      bool                    `json:"restart_required"`
	Artifacts            []PluginInstallArtifact `json:"artifacts"`
	DisplayDependencies  []PluginDependency      `json:"display_dependencies"`
	Online               int                     `json:"online"`
	Players              []string                `json:"players"`
	ConfirmationRequired bool                    `json:"confirmation_required"`
	Fingerprint          string                  `json:"plan_fingerprint"`
}

func (c *Client) pluginPost(ctx context.Context, session, path string, request, out any) error {
	client := *c.http
	client.Timeout = 100 * time.Second
	scoped := &Client{http: &client}
	return scoped.do(ctx, http.MethodPost, path, session, request, out)
}
func (c *Client) AdminPluginInstallPlan(ctx context.Context, session string, request PluginInstallRequest) (PluginInstallPlan, error) {
	var out PluginInstallPlan
	err := c.pluginPost(ctx, session, "/v1/admin/plugins/install/plan", request, &out)
	return out, err
}
func (c *Client) AdminPluginInstall(ctx context.Context, session string, request PluginInstallApplyRequest) (PersistentOperationResponse, error) {
	var out PersistentOperationResponse
	err := c.pluginPost(ctx, session, "/v1/admin/plugins/install", request, &out)
	return out, err
}
func (c *Client) AdminCurrentPluginInstall(ctx context.Context, session string) (PersistentOperationResponse, error) {
	var out PersistentOperationResponse
	err := c.do(ctx, http.MethodGet, "/v1/admin/plugins/install-operation", session, nil, &out)
	return out, err
}

type PluginInstallAcknowledgeRequest struct {
	OperationID string `json:"operation_id"`
}

func (c *Client) AdminPluginInstallAcknowledge(ctx context.Context, session string, request PluginInstallAcknowledgeRequest) (PersistentOperationResponse, error) {
	var out PersistentOperationResponse
	err := c.pluginPost(ctx, session, "/v1/admin/plugins/install/acknowledge", request, &out)
	return out, err
}
