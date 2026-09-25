package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type AdminStorageActionRequest struct {
	Operation    string `json:"operation"`
	Device       string `json:"device"`
	MountPoint   string `json:"mount_point,omitempty"`
	SizeGiB      string `json:"size_gib,omitempty"`
	FreeStart    string `json:"free_start,omitempty"`
	Confirmation string `json:"confirmation,omitempty"`
	Fingerprint  string `json:"fingerprint,omitempty"`
}

type AdminStorageActionPlan struct {
	Operation         string `json:"operation"`
	Device            string `json:"device"`
	Filesystem        string `json:"filesystem,omitempty"`
	TargetFilesystem  string `json:"target_filesystem,omitempty"`
	UUID              string `json:"uuid,omitempty"`
	MountPoint        string `json:"mount_point,omitempty"`
	CurrentMountPoint string `json:"current_mount_point,omitempty"`
	Role              string `json:"role,omitempty"`
	Confirmation      string `json:"confirmation,omitempty"`
	Fingerprint       string `json:"fingerprint"`
	SizeGiB           string `json:"size_gib,omitempty"`
	FreeStart         string `json:"free_start,omitempty"`
	FreeEnd           string `json:"free_end,omitempty"`
	PlannedEnd        string `json:"planned_end,omitempty"`
	CreatedDevice     string `json:"created_device,omitempty"`
	SizeBytes         uint64 `json:"size_bytes,omitempty"`
	Destructive       bool   `json:"destructive,omitempty"`
}

type AdminStorageActionResponse struct {
	OK       bool                   `json:"ok"`
	Error    string                 `json:"error,omitempty"`
	Warnings []string               `json:"warnings"`
	Proposed AdminStorageActionPlan `json:"proposed"`
	Applied  bool                   `json:"applied"`
}

func (c *Client) AdminStorageActionPlan(ctx context.Context, session string, request AdminStorageActionRequest) (AdminStorageActionResponse, error) {
	return c.adminStorageActionChange(ctx, "/v1/admin/storage-actions/plan", session, request, 20*time.Second)
}

func (c *Client) AdminStorageActionApply(ctx context.Context, session string, request AdminStorageActionRequest) (AdminStorageActionResponse, error) {
	return c.adminStorageActionChange(ctx, "/v1/admin/storage-actions/apply", session, request, 100*time.Second)
}

func (c *Client) adminStorageActionChange(ctx context.Context, path, session string, request AdminStorageActionRequest, timeout time.Duration) (AdminStorageActionResponse, error) {
	var out AdminStorageActionResponse
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
	client.Timeout = timeout
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
		return out, fmt.Errorf("management API returned invalid storage action response: %w", err)
	}
	return out, nil
}
