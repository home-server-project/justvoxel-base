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

type AdminVersionStatus struct {
	BedrockTCPPort            int    `json:"bedrock_tcp_port,omitempty"`
	BedrockUDPStart           int    `json:"bedrock_udp_start,omitempty"`
	BedrockUDPEnd             int    `json:"bedrock_udp_end,omitempty"`
	LoaderVersion             string `json:"loader_version,omitempty"`
	InstanceID                string `json:"instance_id,omitempty"`
	ServerType                string `json:"server_type"`
	ServerSupported           bool   `json:"server_supported"`
	StackState                string `json:"stack_state"`
	MinecraftState            string `json:"minecraft_state"`
	GeyserState               string `json:"geyser_state"`
	FloodgateState            string `json:"floodgate_state"`
	ViaVersionState           string `json:"viaversion_state"`
	ImageState                string `json:"image_state"`
	PlanFingerprint           string `json:"plan_fingerprint"`
	ServerSoftware            string `json:"server_software"`
	Installed                 string `json:"installed"`
	Available                 string `json:"available"`
	AvailableChannel          string `json:"available_channel"`
	Recommended               string `json:"recommended"`
	SelectedCandidate         string `json:"selected_candidate"`
	CandidateChannel          string `json:"candidate_channel"`
	ImageTag                  string `json:"image_tag"`
	ConfiguredImageTag        string `json:"configured_image_tag"`
	ConfiguredServerType      string `json:"configured_server_type"`
	RuntimeUpdateRequired     bool   `json:"runtime_update_required"`
	Policy                    string `json:"policy"`
	ConfiguredVersion         string `json:"configured_version"`
	UpdateAvailable           bool   `json:"update_available"`
	PaperSupported            bool   `json:"paper_supported"`
	ManagedCrossplayAvailable bool   `json:"managed_crossplay_available"`
	CrossplayEnabled          bool   `json:"crossplay_enabled"`
	GeyserSupportedVersion    string `json:"geyser_supported_version"`
	CrossplayCompatible       bool   `json:"crossplay_compatible"`
	Reason                    string `json:"reason"`
}

func (c *Client) AdminSetupVersionPreview(ctx context.Context, session, policy, version string, bedrock bool, serverType string) (AdminVersionStatus, error) {
	return c.setupVersionPreview(ctx, session, policy, version, bedrock, serverType, 0)
}
func (c *Client) AdminSetupNativeBedrockPreview(ctx context.Context, session string, players int) (AdminVersionStatus, error) {
	return c.setupVersionPreview(ctx, session, "recommended", "", false, "bedrock", players)
}
func (c *Client) setupVersionPreview(ctx context.Context, session, policy, version string, bedrock bool, serverType string, players int) (AdminVersionStatus, error) {
	var out AdminVersionStatus
	choice := "no"
	if bedrock {
		choice = "yes"
	}
	path := "/v1/admin/setup/version-preview?policy=" + url.QueryEscape(policy) + "&version=" + url.QueryEscape(version) + "&bedrock=" + choice + "&server_type=" + url.QueryEscape(serverType)
	if players > 0 {
		path += fmt.Sprintf("&max_players=%d", players)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix"+path, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+session)
	req.Header.Set("Accept", "application/json")
	client := *c.http
	client.Timeout = 95 * time.Second
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
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}

func (c *Client) MinecraftInstanceVersionStatus(ctx context.Context, session, id, policy, version, serverType string, imageTag ...string) (AdminVersionStatus, error) {
	var out AdminVersionStatus
	path := "/v1/minecraft/instances/" + url.PathEscape(id) + "/version/status?policy=" + url.QueryEscape(policy) + "&version=" + url.QueryEscape(version) + "&server_type=" + url.QueryEscape(serverType)
	if len(imageTag) > 0 {
		path += "&image_tag=" + url.QueryEscape(imageTag[0])
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix"+path, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+session)
	req.Header.Set("Accept", "application/json")
	client := *c.http
	client.Timeout = 95 * time.Second
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
	err = json.NewDecoder(resp.Body).Decode(&out)
	if err == nil && out.InstanceID != id {
		return AdminVersionStatus{}, fmt.Errorf("selected version identity mismatch")
	}
	return out, err
}

type MinecraftStackUpdateRequest struct {
	Policy          string `json:"policy,omitempty"`
	Version         string `json:"version,omitempty"`
	ServerType      string `json:"server_type,omitempty"`
	ImageTag        string `json:"image_tag,omitempty"`
	PlanFingerprint string `json:"plan_fingerprint"`
	ConfirmPlayers  bool   `json:"confirm_players"`
}

func (c *Client) MinecraftInstanceStackUpdate(ctx context.Context, session, id string, change MinecraftStackUpdateRequest) (PersistentOperationResponse, error) {
	var out PersistentOperationResponse
	body, err := json.Marshal(change)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/v1/minecraft/instances/"+url.PathEscape(id)+"/version/update", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+session)
	req.Header.Set("Content-Type", "application/json")
	client := *c.http
	client.Timeout = 95 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, readResponseError(resp)
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}

func (c *Client) MinecraftInstanceStackUpdateOperation(ctx context.Context, session, id, operationID string) (PersistentOperationResponse, error) {
	return c.getPersistentOperation(ctx, session, "/v1/minecraft/instances/"+url.PathEscape(id)+"/version/update/current?operation_id="+url.QueryEscape(operationID))
}

func (c *Client) MinecraftInstanceAcknowledgeStackUpdate(ctx context.Context, session, instanceID, id string) (PersistentOperationResponse, error) {
	var out PersistentOperationResponse
	body, err := json.Marshal(map[string]string{"operation_id": id})
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/v1/minecraft/instances/"+url.PathEscape(instanceID)+"/version/update/acknowledge", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+session)
	req.Header.Set("Content-Type", "application/json")
	client := *c.http
	client.Timeout = 95 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, readResponseError(resp)
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}
