package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type AdminConfigurationChangeRequest struct {
	JavaMemory         string `json:"java_memory"`
	ContainerMemory    string `json:"container_memory"`
	JavaPort           int    `json:"java_port"`
	BedrockEnabled     bool   `json:"bedrock_enabled"`
	BedrockPort        int    `json:"bedrock_port"`
	Timezone           string `json:"timezone"`
	MaxPlayers         int    `json:"max_players"`
	MOTD               string `json:"motd"`
	ImageTag           string `json:"image_tag"`
	VersionPolicy      string `json:"version_policy"`
	Version            string `json:"version"`
	BackupKeep         int    `json:"backup_keep"`
	BackupSchedule     string `json:"backup_schedule"`
	BackupTimerEnabled bool   `json:"backup_timer_enabled"`
}

type AdminConfigurationChange struct {
	Field           string `json:"field"`
	Label           string `json:"label"`
	Before          string `json:"before"`
	After           string `json:"after"`
	RestartRequired bool   `json:"restart_required"`
}

type AdminConfigurationChangeResponse struct {
	OK                 bool                        `json:"ok"`
	Error              string                      `json:"error,omitempty"`
	Changes            []AdminConfigurationChange  `json:"changes"`
	Warnings           []string                    `json:"warnings"`
	RestartRequired    bool                        `json:"restart_required"`
	MemoryRemainingMiB int                         `json:"memory_remaining_mib"`
	Proposed           AdminConfigurationDiscovery `json:"proposed"`
	Applied            bool                        `json:"applied"`
}

func (c *Client) AdminConfigurationPlan(ctx context.Context, session string, request AdminConfigurationChangeRequest) (AdminConfigurationChangeResponse, error) {
	return c.adminConfigurationChange(ctx, "/v1/admin/configuration/plan", session, request)
}

func (c *Client) AdminConfigurationApply(ctx context.Context, session string, request AdminConfigurationChangeRequest) (AdminConfigurationChangeResponse, error) {
	return c.adminConfigurationChange(ctx, "/v1/admin/configuration/apply", session, request)
}

func (c *Client) adminConfigurationChange(ctx context.Context, path, session string, request AdminConfigurationChangeRequest) (AdminConfigurationChangeResponse, error) {
	var out AdminConfigurationChangeResponse
	data, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+path, bytes.NewReader(data))
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)

	client := *c.http
	client.Timeout = 30 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		return out, fmt.Errorf("management API unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return out, ErrUnauthorized
	}
	if resp.StatusCode == http.StatusForbidden {
		return out, ErrPasswordChangeRequired
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, readResponseError(resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("management API returned invalid configuration response: %w", err)
	}
	return out, nil
}
