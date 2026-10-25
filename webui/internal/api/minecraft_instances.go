package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// InstanceRuntimeSettings is the existing public runtime contract. Identity,
// storage paths and credentials are generated exclusively by the agent.
type InstanceRuntimeSettings struct {
	JavaPort         int    `json:"java_port"`
	BedrockEnabled   bool   `json:"bedrock_enabled"`
	BedrockPort      int    `json:"bedrock_port"`
	BedrockTCPPort   int    `json:"bedrock_tcp_port,omitempty"`
	BedrockUDPStart  int    `json:"bedrock_udp_start,omitempty"`
	BedrockUDPEnd    int    `json:"bedrock_udp_end,omitempty"`
	JavaMemory       string `json:"java_memory"`
	ContainerMemory  string `json:"container_memory"`
	Software         string `json:"software"`
	LoaderVersion    string `json:"loader_version,omitempty"`
	VersionPolicy    string `json:"version_policy,omitempty"`
	Version          string `json:"version"`
	PaperChannel     string `json:"paper_channel"`
	ImageTag         string `json:"image_tag"`
	StartOnBoot      bool   `json:"start_on_boot"`
	EULAAccepted     bool   `json:"eula_accepted"`
	Timezone         string `json:"timezone,omitempty"`
	GameMode         string `json:"game_mode"`
	Difficulty       string `json:"difficulty"`
	MaxPlayers       int    `json:"max_players"`
	Whitelist        bool   `json:"whitelist"`
	EnforceWhitelist bool   `json:"enforce_whitelist"`
	MOTD             string `json:"motd"`
}

type RegisteredMinecraftInstance struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type MinecraftInstanceStatus struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	StartOnBoot bool   `json:"start_on_boot"`
}

func (c *Client) MinecraftInstanceStatus(ctx context.Context, session, id string) (MinecraftInstanceStatus, error) {
	var out MinecraftInstanceStatus
	err := c.do(ctx, http.MethodGet, "/v1/minecraft/instances/"+url.PathEscape(id)+"/status", session, nil, &out)
	return out, err
}

func (c *Client) MinecraftInstanceAction(ctx context.Context, session, id, action string) error {
	switch action {
	case "start", "stop", "restart":
	default:
		return fmt.Errorf("unsupported Minecraft action")
	}
	client := *c.http
	client.Timeout = 310 * time.Second
	caller := &Client{http: &client}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := caller.do(ctx, http.MethodPost, "/v1/minecraft/instances/"+url.PathEscape(id)+"/"+action, session, struct{}{}, &out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("Minecraft action was not completed")
	}
	return nil
}

type InstanceBackupSettings struct {
	Enabled   bool   `json:"enabled"`
	Time      string `json:"time"`
	Retention int    `json:"retention"`
}

type CreateMinecraftInstanceRequest struct {
	DisplayName string                  `json:"display_name"`
	Runtime     InstanceRuntimeSettings `json:"runtime"`
}

type CreateMinecraftInstanceResponse struct {
	OK               bool   `json:"ok"`
	ID               string `json:"id"`
	State            string `json:"state"`
	Error            string `json:"error"`
	RecoveryRequired bool   `json:"recovery_required"`
}

func (c *Client) MinecraftInstances(ctx context.Context, session string) ([]RegisteredMinecraftInstance, error) {
	var out struct {
		Instances []RegisteredMinecraftInstance `json:"instances"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/minecraft/instances", session, nil, &out)
	return out.Instances, err
}

func (c *Client) MinecraftInstanceRuntimes(ctx context.Context, session string) ([]string, error) {
	var out struct {
		Instances []struct {
			ID string `json:"id"`
		} `json:"instances"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/minecraft/instances/runtime", session, nil, &out)
	ids := make([]string, 0, len(out.Instances))
	for _, instance := range out.Instances {
		ids = append(ids, instance.ID)
	}
	return ids, err
}

func (c *Client) MinecraftInstanceConfiguration(ctx context.Context, session, id string) (InstanceRuntimeSettings, error) {
	var out struct {
		ID      string                  `json:"id"`
		Runtime InstanceRuntimeSettings `json:"runtime"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/minecraft/instances/"+url.PathEscape(id)+"/configuration", session, nil, &out)
	if err == nil && out.ID != id {
		return InstanceRuntimeSettings{}, fmt.Errorf("selected instance configuration identity mismatch")
	}
	return out.Runtime, err
}

func (c *Client) MinecraftInstanceBackupSettings(ctx context.Context, session, id string) (InstanceBackupSettings, error) {
	var out InstanceBackupSettings
	err := c.do(ctx, http.MethodGet, "/v1/minecraft/instances/"+url.PathEscape(id)+"/backups/settings", session, nil, &out)
	return out, err
}

func (c *Client) SetMinecraftInstanceBackupSettings(ctx context.Context, session, id string, settings InstanceBackupSettings) error {
	client := *c.http
	client.Timeout = 70 * time.Second
	caller := &Client{http: &client}
	return caller.do(ctx, http.MethodPut, "/v1/minecraft/instances/"+url.PathEscape(id)+"/backups/settings", session, settings, nil)
}

// Decode errors as well as success: an error can carry the identity of a
// registered server whose runtime activation needs recovery. Never lose it.
func (c *Client) CreateMinecraftInstance(ctx context.Context, session string, settings CreateMinecraftInstanceRequest) (CreateMinecraftInstanceResponse, error) {
	var out CreateMinecraftInstanceResponse
	data, err := json.Marshal(settings)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/v1/minecraft/instances", bytes.NewReader(data))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+session)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := *c.http
	client.Timeout = 310 * time.Second
	response, err := client.Do(req)
	if err != nil {
		return out, fmt.Errorf("management API unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return out, ErrUnauthorized
	}
	if response.StatusCode == http.StatusForbidden {
		return out, ErrPasswordChangeRequired
	}
	if err := json.NewDecoder(response.Body).Decode(&out); err != nil {
		return out, err
	}
	if response.StatusCode != http.StatusCreated || !out.OK {
		return out, &ResponseError{StatusCode: response.StatusCode, Message: out.Error}
	}
	return out, nil
}

func (c *Client) SetMinecraftInstanceConfiguration(ctx context.Context, session, id string, settings InstanceRuntimeSettings) error {
	client := *c.http
	client.Timeout = 310 * time.Second
	caller := &Client{http: &client}
	return caller.do(ctx, http.MethodPut, "/v1/minecraft/instances/"+url.PathEscape(id)+"/configuration", session, struct {
		Runtime InstanceRuntimeSettings `json:"runtime"`
	}{settings}, nil)
}
func (c *Client) RenameMinecraftInstance(ctx context.Context, session, id, name string) error {
	return c.do(ctx, http.MethodPost, "/v1/minecraft/instances/"+url.PathEscape(id)+"/name", session, struct {
		DisplayName string `json:"display_name"`
	}{name}, nil)
}
func (c *Client) DeleteMinecraftInstance(ctx context.Context, session, id, name string) error {
	client := *c.http
	client.Timeout = 310 * time.Second
	caller := &Client{http: &client}
	var out struct {
		OK bool `json:"ok"`
	}
	err := caller.do(ctx, http.MethodPost, "/v1/minecraft/instances/"+url.PathEscape(id)+"/delete", session, struct {
		ConfirmName string `json:"confirm_name"`
	}{name}, &out)
	if err == nil && !out.OK {
		return fmt.Errorf("Server deletion did not complete")
	}
	return err
}

type InstancePlayers struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Online  int    `json:"online"`
	Maximum int    `json:"maximum"`
}

func (c *Client) MinecraftInstancePlayers(ctx context.Context, session, id string) (InstancePlayers, error) {
	var out InstancePlayers
	err := c.do(ctx, http.MethodGet, "/v1/minecraft/instances/"+url.PathEscape(id)+"/players", session, nil, &out)
	return out, err
}
func (c *Client) MinecraftInstanceLogs(ctx context.Context, session, id string) (string, error) {
	var out struct {
		Logs string `json:"logs"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/minecraft/instances/"+url.PathEscape(id)+"/logs", session, nil, &out)
	return out.Logs, err
}

// The caller selects from this finite Phase 4 surface; it cannot supply an API
// path, unit, container or filesystem path.
func (c *Client) MinecraftInstanceBackups(ctx context.Context, session, id, action string, request any) (json.RawMessage, error) {
	method, suffix := http.MethodGet, ""
	switch action {
	case "inventory":
	case "status", "settings":
		suffix = "/" + action
	case "now":
		method = http.MethodPost
	case "configure":
		method = http.MethodPut
		suffix = "/settings"
	case "restore/plan", "restore/apply", "restore/recover":
		method = http.MethodPost
		suffix = "/" + action
	case "restore/status":
		suffix = "/restore/status"
	default:
		return nil, fmt.Errorf("Unsupported backup operation")
	}
	client := *c.http
	client.Timeout = 2*time.Hour + time.Minute
	caller := &Client{http: &client}
	var out json.RawMessage
	err := caller.do(ctx, method, "/v1/minecraft/instances/"+url.PathEscape(id)+"/backups"+suffix, session, request, &out)
	return out, err
}

func (c *Client) MinecraftInstanceDeletions(ctx context.Context, session string) ([]RegisteredMinecraftInstance, error) {
	var out struct {
		Deletions []RegisteredMinecraftInstance `json:"deletions"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/minecraft/instances/deletions", session, nil, &out)
	return out.Deletions, err
}

func (c *Client) MinecraftInstanceWhitelist(ctx context.Context, session, id string, request any) (TextOutputResponse, error) {
	var out struct {
		ID string `json:"id"`
		TextOutputResponse
	}

	method := http.MethodGet
	if request != nil {
		method = http.MethodPost
	}
	err := c.do(ctx, method, "/v1/minecraft/instances/"+url.PathEscape(id)+"/whitelist", session, request, &out)
	if err == nil && out.ID != id {
		return TextOutputResponse{}, fmt.Errorf("selected whitelist identity mismatch")
	}
	return out.TextOutputResponse, err
}

// Console commands always target one registered immutable instance.
type InstanceConsoleRequest struct {
	Command  string `json:"command"`
	Discover bool   `json:"discover,omitempty"`
}
type InstanceConsoleCommand struct {
	Name        string `json:"name"`
	Usage       string `json:"usage,omitempty"`
	Description string `json:"description,omitempty"`
}
type InstanceConsoleResponse struct {
	OK       bool                     `json:"ok"`
	ID       string                   `json:"id"`
	Output   string                   `json:"output"`
	Commands []InstanceConsoleCommand `json:"commands,omitempty"`
	Partial  bool                     `json:"partial,omitempty"`
}

func (c *Client) MinecraftInstanceConsole(ctx context.Context, session, id string, request InstanceConsoleRequest) (InstanceConsoleResponse, error) {
	var out InstanceConsoleResponse
	client := *c.http
	client.Timeout = 25 * time.Second
	caller := &Client{http: &client}
	err := caller.do(ctx, http.MethodPost, "/v1/minecraft/instances/"+url.PathEscape(id)+"/console", session, request, &out)
	if err == nil && (out.ID != id || !out.OK) {
		return InstanceConsoleResponse{}, fmt.Errorf("selected console identity mismatch or incomplete command")
	}
	return out, err
}
