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
	JavaMemory       string `json:"java_memory"`
	ContainerMemory  string `json:"container_memory"`
	Software         string `json:"software"`
	Version          string `json:"version"`
	PaperChannel     string `json:"paper_channel"`
	ImageTag         string `json:"image_tag"`
	StartOnBoot      bool   `json:"start_on_boot"`
	EULAAccepted     bool   `json:"eula_accepted"`
	Timezone         string `json:"timezone"`
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
		Runtime InstanceRuntimeSettings `json:"runtime"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/minecraft/instances/"+url.PathEscape(id)+"/configuration", session, nil, &out)
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
