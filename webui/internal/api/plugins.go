package api

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type PluginServerContext struct {
	InstanceID       string `json:"instance_id"`
	Configured       bool   `json:"configured"`
	ServerType       string `json:"server_type"`
	ServerSoftware   string `json:"server_software"`
	MinecraftVersion string `json:"minecraft_version"`
	Supported        bool   `json:"supported"`
	Message          string `json:"message,omitempty"`
}
type InstalledPlugin struct {
	Source         string `json:"source,omitempty"`
	State          string `json:"state,omitempty"`
	Name           string `json:"name,omitempty"`
	Version        string `json:"version,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	IconProjectID  string `json:"icon_project_id,omitempty"`
	VersionID      string `json:"version_id,omitempty"`
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
	Source    string `json:"source,omitempty"`
	Name      string `json:"name,omitempty"`
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
	Installed        bool                  `json:"installed,omitempty"`
	InstalledState   string                `json:"installed_state,omitempty"`
	IconURL          string                `json:"icon_url,omitempty"`
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
type PluginCatalogCursor struct {
	ModrinthOffset int  `json:"modrinth_offset"`
	HangarOffset   int  `json:"hangar_offset"`
	HangarFirst    bool `json:"hangar_first"`
}
type PluginCatalog struct {
	Server     PluginServerContext    `json:"server"`
	Modrinth   bool                   `json:"modrinth"`
	Hangar     bool                   `json:"hangar"`
	Projects   []PluginCatalogProject `json:"projects"`
	Cursor     PluginCatalogCursor    `json:"cursor"`
	NextCursor *PluginCatalogCursor   `json:"next_cursor"`
	Total      int                    `json:"total"`
}

// Plugin reads can include bounded runtime discovery and catalog metadata reads.
// Keep this timeout specific to these routes rather than changing the shared client.
func (c *Client) pluginRead(ctx context.Context, session, path string, out any) error {
	var err error
	path, err = selectedMaintenancePath(ctx, path)
	if err != nil {
		return err
	}
	client := *c.http
	client.Timeout = 50 * time.Second
	scoped := &Client{http: &client}
	if err := scoped.do(ctx, http.MethodGet, path, session, nil, out); err != nil {
		return err
	}
	return validateMaintenanceResponse(ctx, out)
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

// ValidatePluginDiscoverQuery permits only the bounded catalog contract.
func ValidatePluginDiscoverQuery(query url.Values) error {
	for key, values := range query {
		if (key != "q" && key != "compatible_only" && key != "release_only" && key != "modrinth_offset" && key != "hangar_offset" && key != "modrinth" && key != "hangar" && key != "hangar_first" && key != "limit") || len(values) != 1 {
			return errors.New("Invalid catalog query.")
		}
	}
	if len(query.Get("q")) > 200 {
		return errors.New("Search is too long.")
	}
	for _, key := range []string{"compatible_only", "release_only", "modrinth", "hangar", "hangar_first"} {
		if values, present := query[key]; present && values[0] != "true" && values[0] != "false" {
			return errors.New("Invalid catalog filter.")
		}
	}
	for _, key := range []string{"modrinth_offset", "hangar_offset"} {
		if values, present := query[key]; present {
			value := values[0]
			if value == "" || strings.Trim(value, "0123456789") != "" {
				return errors.New("Invalid catalog offset.")
			}
			offset, err := strconv.Atoi(value)
			if err != nil || offset < 0 || offset > 10000 {
				return errors.New("Invalid catalog offset.")
			}
		}
	}
	if values, present := query["limit"]; present {
		value := values[0]
		for _, digit := range value {
			if digit < '0' || digit > '9' {
				return errors.New("Invalid catalog limit.")
			}
		}
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 12 {
			return errors.New("Invalid catalog limit.")
		}
	}
	return nil
}

func (c *Client) AdminPluginDiscover(ctx context.Context, session string, query url.Values) (PluginCatalog, error) {
	var out PluginCatalog
	if err := ValidatePluginDiscoverQuery(query); err != nil {
		return out, err
	}
	err := c.pluginRead(ctx, session, "/v1/admin/plugins/discover?"+query.Encode(), &out)
	return out, err
}

type PluginInstallRequest struct {
	Source    string `json:"source,omitempty"`
	ProjectID string `json:"project_id"`
	VersionID string `json:"version_id"`
}
type PluginInstallApplyRequest struct {
	Source          string `json:"source,omitempty"`
	ProjectID       string `json:"project_id"`
	VersionID       string `json:"version_id"`
	PlanFingerprint string `json:"plan_fingerprint"`
	ConfirmPlayers  bool   `json:"confirm_players"`
}
type PluginInstallArtifact struct {
	Source        string   `json:"source,omitempty"`
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
	Source               string                  `json:"source,omitempty"`
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
	var err error
	path, err = selectedMaintenancePath(ctx, path)
	if err != nil {
		return err
	}
	client := *c.http
	client.Timeout = 100 * time.Second
	scoped := &Client{http: &client}
	if err := scoped.do(ctx, http.MethodPost, path, session, request, out); err != nil {
		return err
	}
	return validateMaintenanceResponse(ctx, out)
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
	err := c.pluginRead(ctx, session, "/v1/admin/plugins/install-operation", &out)
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

// PluginIcon carries only Agent-verified bytes, never an upstream URL.
type PluginIcon struct {
	Data        []byte
	ContentType string
}

func (c *Client) AdminPluginIcon(ctx context.Context, session, projectID string, iconURL ...string) (PluginIcon, error) {
	return c.adminPluginIcon(ctx, session, "Modrinth", projectID, iconURL...)
}
func (c *Client) AdminPluginSourceIcon(ctx context.Context, session, source, projectID string) (PluginIcon, error) {
	return c.adminPluginIcon(ctx, session, source, projectID)
}
func (c *Client) adminPluginIcon(ctx context.Context, session, source, projectID string, iconURL ...string) (PluginIcon, error) {
	var out PluginIcon
	if !ValidPluginIdentity(source, projectID) || (source == "Hangar" && len(iconURL) > 0) {
		return out, errors.New("Invalid plugin provider identity")
	}
	query := url.Values{"source": {source}, "project_id": {projectID}}
	if len(iconURL) > 1 {
		return out, errors.New("Invalid plugin icon request.")
	}
	if len(iconURL) == 1 {
		query.Set("icon_url", iconURL[0])
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/v1/admin/plugins/icon?"+query.Encode(), nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+session)
	req.Header.Set("Accept", "image/png, image/jpeg, image/bmp, image/gif, image/webp")
	client := *c.http
	client.Timeout = 15 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return out, errors.New("Plugin icon unavailable.")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return out, ErrUnauthorized
	}
	if response.StatusCode == http.StatusForbidden {
		return out, ErrPasswordChangeRequired
	}
	if response.StatusCode != http.StatusOK {
		return out, &ResponseError{StatusCode: response.StatusCode, Message: "Plugin icon unavailable."}
	}
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || (contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/webp" && contentType != "image/bmp" && contentType != "image/gif") {
		return out, errors.New("Plugin icon unavailable.")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (512<<10)+1))
	if err != nil || len(data) > 512<<10 || http.DetectContentType(data) != contentType {
		return out, errors.New("Plugin icon unavailable.")
	}
	return PluginIcon{Data: data, ContentType: contentType}, nil
}

// Explicit lifecycle requests share install's bounded transport and strict Agent
// decoding. No caller-selected path or JAR filename is accepted.
type PluginManageRequest struct {
	Source    string `json:"source,omitempty"`
	Action    string `json:"action"`
	ProjectID string `json:"project_id"`
}
type PluginManageApplyRequest struct {
	Source          string `json:"source,omitempty"`
	Action          string `json:"action"`
	ProjectID       string `json:"project_id"`
	PlanFingerprint string `json:"plan_fingerprint"`
	ConfirmPlayers  bool   `json:"confirm_players"`
}
type PluginManagePlan struct {
	Source               string              `json:"source,omitempty"`
	Action               string              `json:"action"`
	ProjectID            string              `json:"project_id"`
	Name                 string              `json:"project_name"`
	State                string              `json:"state"`
	Server               PluginServerContext `json:"server"`
	Online               int                 `json:"online"`
	Players              []string            `json:"players"`
	ConfirmationRequired bool                `json:"confirmation_required"`
	RestartRequired      bool                `json:"restart_required"`
	Fingerprint          string              `json:"plan_fingerprint"`
}

func validPluginManageRequest(action, project string) bool {
	return (action == "enable" || action == "disable" || action == "remove") && validPluginProjectID(project)
}
func (c *Client) AdminPluginManagePlan(ctx context.Context, session string, request PluginManageRequest) (PluginManagePlan, error) {
	var out PluginManagePlan
	if !validPluginManageRequest(request.Action, request.ProjectID) || !ValidPluginIdentity(request.Source, request.ProjectID) {
		return out, errors.New("Invalid plugin management request")
	}
	err := c.pluginPost(ctx, session, "/v1/admin/plugins/manage/plan", request, &out)
	return out, err
}
func (c *Client) AdminPluginManage(ctx context.Context, session string, request PluginManageApplyRequest) (PersistentOperationResponse, error) {
	var out PersistentOperationResponse
	if !validPluginManageRequest(request.Action, request.ProjectID) || !ValidPluginIdentity(request.Source, request.ProjectID) || !adminResetFingerprintPattern.MatchString(request.PlanFingerprint) {
		return out, errors.New("Invalid plugin management request")
	}
	err := c.pluginPost(ctx, session, "/v1/admin/plugins/manage", request, &out)
	return out, err
}
func (c *Client) AdminPluginManageAcknowledge(ctx context.Context, session string, request PluginInstallAcknowledgeRequest) (PersistentOperationResponse, error) {
	var out PersistentOperationResponse
	if !persistentOperationIDPattern.MatchString(request.OperationID) {
		return out, errors.New("Invalid plugin operation identity")
	}
	err := c.pluginPost(ctx, session, "/v1/admin/plugins/manage/acknowledge", request, &out)
	return out, err
}
func (c *Client) AdminCurrentPluginOperation(ctx context.Context, session string) (PersistentOperationResponse, error) {
	path, err := selectedMaintenancePath(ctx, "/v1/admin/plugins/operation")
	if err != nil {
		return PersistentOperationResponse{}, err
	}
	return c.getPersistentOperation(ctx, session, path)
}

func validPluginProjectID(project string) bool {
	if len(project) == 0 || len(project) > 64 {
		return false
	}
	for _, c := range project {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// Empty source retains the deployed Modrinth request/manifest contract.
func ValidPluginIdentity(source, id string) bool {
	if source == "" || source == "Modrinth" {
		return validPluginProjectID(id)
	}
	if source != "Hangar" || len(id) == 0 || len(id) > 19 || id[0] == '0' || strings.Trim(id, "0123456789") != "" {
		return false
	}
	n, err := strconv.ParseInt(id, 10, 64)
	return err == nil && n > 0
}
func ValidatePluginIconQuery(query url.Values) error {
	for key, values := range query {
		if (key != "source" && key != "project_id" && key != "icon_url") || len(values) != 1 {
			return errors.New("Invalid plugin icon request")
		}
	}
	if !ValidPluginIdentity(query.Get("source"), query.Get("project_id")) || (query.Get("source") == "Hangar" && len(query["icon_url"]) != 0) {
		return errors.New("Invalid plugin icon request")
	}
	return nil
}

type maintenanceSelectionKey struct{}
type maintenanceSelection struct{ ID, Kind string }

// WithMinecraftMaintenanceInstance carries a resolved immutable request selection.
// It never chooses a server or falls back to appliance-global configuration.
func WithMinecraftMaintenanceInstance(ctx context.Context, id, kind string) context.Context {
	return context.WithValue(ctx, maintenanceSelectionKey{}, maintenanceSelection{id, kind})
}
func selectedMaintenancePath(ctx context.Context, path string) (string, error) {
	selected, _ := ctx.Value(maintenanceSelectionKey{}).(maintenanceSelection)
	if !minecraftMaintenanceIDPattern.MatchString(selected.ID) {
		return "", errors.New("Minecraft server selection is required")
	}
	prefix := "/v1/minecraft/instances/" + url.PathEscape(selected.ID)
	if strings.HasPrefix(path, "/v1/admin/plugins/") {
		return prefix + "/plugins/" + strings.TrimPrefix(path, "/v1/admin/plugins/"), nil
	}
	if strings.HasPrefix(path, "/v1/admin/reset/minecraft/") {
		return prefix + "/reset/" + strings.TrimPrefix(path, "/v1/admin/reset/minecraft/"), nil
	}
	return "", errors.New("Unsupported selected Minecraft operation")
}

var minecraftMaintenanceIDPattern = regexp.MustCompile(`^jv-[a-z0-9]{6,12}$`)

func validateMaintenanceResponse(ctx context.Context, out any) error {
	selected, _ := ctx.Value(maintenanceSelectionKey{}).(maintenanceSelection)
	id := ""
	switch v := out.(type) {
	case *PluginInventory:
		id = v.Server.InstanceID
	case *PluginCatalog:
		id = v.Server.InstanceID
	case *PluginInstallPlan:
		id = v.Server.InstanceID
	case *PluginManagePlan:
		id = v.Server.InstanceID
	case *AddonInventory:
		id = v.Server.InstanceID
	case *AddonPlan:
		id = v.Server.InstanceID
	case *PersistentOperationResponse:
		if v.Operation == nil {
			return nil
		}
		id = v.Operation.InstanceID
	default:
		return errors.New("Unsupported Minecraft maintenance response")
	}
	if selected.ID == "" || selected.ID != id {
		return errors.New("Minecraft maintenance response instance mismatch")
	}
	return nil
}

// MinecraftMaintenanceInstance returns only the immutable selection carried by this request.
func MinecraftMaintenanceInstance(ctx context.Context) string {
	selected, _ := ctx.Value(maintenanceSelectionKey{}).(maintenanceSelection)
	return selected.ID
}
