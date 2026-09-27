package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type AdminVersionStatus struct {
	ServerSoftware         string `json:"server_software"`
	Installed              string `json:"installed"`
	Available              string `json:"available"`
	Recommended            string `json:"recommended"`
	SelectedCandidate      string `json:"selected_candidate"`
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
	client.Timeout = 35 * time.Second
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
