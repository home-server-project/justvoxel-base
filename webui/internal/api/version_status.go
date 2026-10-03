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
	StackState             string `json:"stack_state"`
	MinecraftState         string `json:"minecraft_state"`
	GeyserState            string `json:"geyser_state"`
	FloodgateState         string `json:"floodgate_state"`
	ViaVersionState        string `json:"viaversion_state"`
	ImageState             string `json:"image_state"`
	PlanFingerprint        string `json:"plan_fingerprint"`
	ServerSoftware         string `json:"server_software"`
	Installed              string `json:"installed"`
	Available              string `json:"available"`
	AvailableChannel       string `json:"available_channel"`
	Recommended            string `json:"recommended"`
	SelectedCandidate      string `json:"selected_candidate"`
	CandidateChannel       string `json:"candidate_channel"`
	ImageTag               string `json:"image_tag"`
	Policy                 string `json:"policy"`
	ConfiguredVersion      string `json:"configured_version"`
	UpdateAvailable        bool   `json:"update_available"`
	PaperSupported         bool   `json:"paper_supported"`
	CrossplayEnabled       bool   `json:"crossplay_enabled"`
	GeyserSupportedVersion string `json:"geyser_supported_version"`
	CrossplayCompatible    bool   `json:"crossplay_compatible"`
	Reason                 string `json:"reason"`
}

func (c *Client) AdminSetupVersionPreview(ctx context.Context, session, policy, version string, bedrock bool) (AdminVersionStatus, error) {
	var out AdminVersionStatus
	choice := "no"
	if bedrock {
		choice = "yes"
	}
	path := "/v1/admin/setup/version-preview?policy=" + url.QueryEscape(policy) + "&version=" + url.QueryEscape(version) + "&bedrock=" + choice
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

func (c *Client) AdminVersionStatus(ctx context.Context, session, policy, version string) (AdminVersionStatus, error) {
	var out AdminVersionStatus
	path := "/v1/admin/version/status?policy=" + url.QueryEscape(policy) + "&version=" + url.QueryEscape(version)
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

type MinecraftStackUpdateRequest struct {
	PlanFingerprint string `json:"plan_fingerprint"`
	ConfirmPlayers  bool   `json:"confirm_players"`
}

func (c *Client) AdminMinecraftStackUpdate(ctx context.Context, session string, change MinecraftStackUpdateRequest) (PersistentOperationResponse, error) {
	var out PersistentOperationResponse
	body, err := json.Marshal(change)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/v1/admin/version/update", bytes.NewReader(body))
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

func (c *Client) AdminCurrentMinecraftStackUpdate(ctx context.Context, session string) (PersistentOperationResponse, error) {
	return c.getPersistentOperation(ctx, session, "/v1/admin/version/update/current")
}

func (c *Client) AdminAcknowledgeMinecraftStackUpdate(ctx context.Context, session, id string) (PersistentOperationResponse, error) {
	var out PersistentOperationResponse
	body, err := json.Marshal(map[string]string{"operation_id": id})
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/v1/admin/version/update/acknowledge", bytes.NewReader(body))
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
