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

type AdminStorageMountRequest struct {
	Operation   string `json:"operation"`
	Device      string `json:"device"`
	MountPoint  string `json:"mount_point,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type AdminStorageMountPlan struct {
	Operation         string `json:"operation,omitempty"`
	Device            string `json:"device"`
	Filesystem        string `json:"filesystem,omitempty"`
	UUID              string `json:"uuid,omitempty"`
	MountPoint        string `json:"mount_point,omitempty"`
	CurrentMountPoint string `json:"current_mount_point,omitempty"`
	Role              string `json:"role,omitempty"`
	Persistence       string `json:"persistence,omitempty"`
	Fingerprint       string `json:"fingerprint,omitempty"`
	SizeBytes         uint64 `json:"size_bytes,omitempty"`
	Mounted           bool   `json:"mounted,omitempty"`
	Changed           bool   `json:"changed,omitempty"`
	DirectoryCreated  bool   `json:"directory_created,omitempty"`
}

type AdminStorageMountResponse struct {
	OK                bool                  `json:"ok"`
	Error             string                `json:"error,omitempty"`
	Warnings          []string              `json:"warnings"`
	Proposed          AdminStorageMountPlan `json:"proposed"`
	Applied           bool                  `json:"applied"`
	MountPointRemoved bool                  `json:"mount_point_removed,omitempty"`
}

func (c *Client) AdminStorageMountStatus(ctx context.Context, session, device string) (AdminStorageMountResponse, error) {
	var out AdminStorageMountResponse
	path := "/v1/admin/storage-mounts/status?device=" + url.QueryEscape(device)
	err := c.do(ctx, http.MethodGet, path, session, nil, &out)
	return out, err
}

func (c *Client) AdminStorageMountPlan(ctx context.Context, session string, request AdminStorageMountRequest) (AdminStorageMountResponse, error) {
	return c.adminStorageMountChange(ctx, "/v1/admin/storage-mounts/plan", session, request, 20*time.Second)
}

func (c *Client) AdminStorageMountApply(ctx context.Context, session string, request AdminStorageMountRequest) (AdminStorageMountResponse, error) {
	return c.adminStorageMountChange(ctx, "/v1/admin/storage-mounts/apply", session, request, 100*time.Second)
}

func (c *Client) adminStorageMountChange(ctx context.Context, path, session string, request AdminStorageMountRequest, timeout time.Duration) (AdminStorageMountResponse, error) {
	var out AdminStorageMountResponse
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
		return out, fmt.Errorf("management API returned invalid permanent mount response: %w", err)
	}
	return out, nil
}
