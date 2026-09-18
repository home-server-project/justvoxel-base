package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type AdminBackupStorageRequest struct {
	Type       string `json:"type"`
	Path       string `json:"path"`
	Device     string `json:"device,omitempty"`
	MountPoint string `json:"mount_point,omitempty"`
	Source     string `json:"source,omitempty"`
	Username   string `json:"username,omitempty"`
	Password   string `json:"password,omitempty"`
	Domain     string `json:"domain,omitempty"`
}

type AdminBackupStorageTarget struct {
	Status            string `json:"status,omitempty"`
	StatusDetail      string `json:"status_detail,omitempty"`
	Type              string `json:"type"`
	Path              string `json:"path"`
	Device            string `json:"device,omitempty"`
	MountPoint        string `json:"mount_point,omitempty"`
	ExpectedUUID      string `json:"expected_uuid,omitempty"`
	ExpectedSource    string `json:"expected_source,omitempty"`
	Filesystem        string `json:"filesystem,omitempty"`
	Model             string `json:"model,omitempty"`
	SizeBytes         uint64 `json:"size_bytes,omitempty"`
	AvailableBytes    uint64 `json:"available_bytes,omitempty"`
	FilesystemBytes   uint64 `json:"filesystem_bytes,omitempty"`
	SamePhysicalDisk  bool   `json:"same_physical_disk,omitempty"`
	AlreadyMounted    bool   `json:"already_mounted,omitempty"`
	CredentialsNeeded bool   `json:"credentials_required,omitempty"`
}

type AdminBackupStorageResponse struct {
	OK       bool                     `json:"ok"`
	Error    string                   `json:"error,omitempty"`
	Current  AdminBackupStorageTarget `json:"current"`
	Proposed AdminBackupStorageTarget `json:"proposed"`
	Warnings []string                 `json:"warnings"`
	Changed  bool                     `json:"changed"`
	Applied  bool                     `json:"applied"`
}

func (c *Client) AdminBackupStorageStatus(ctx context.Context, session string) (AdminBackupStorageResponse, error) {
	var out AdminBackupStorageResponse
	err := c.do(ctx, http.MethodGet, "/v1/admin/backup-storage", session, nil, &out)
	return out, err
}

func (c *Client) AdminBackupStoragePlan(ctx context.Context, session string, request AdminBackupStorageRequest) (AdminBackupStorageResponse, error) {
	return c.adminBackupStorageChange(ctx, "/v1/admin/backup-storage/plan", session, request)
}

func (c *Client) AdminBackupStorageApply(ctx context.Context, session string, request AdminBackupStorageRequest) (AdminBackupStorageResponse, error) {
	return c.adminBackupStorageChange(ctx, "/v1/admin/backup-storage/apply", session, request)
}

func (c *Client) adminBackupStorageChange(ctx context.Context, path, session string, request AdminBackupStorageRequest) (AdminBackupStorageResponse, error) {
	var out AdminBackupStorageResponse
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
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("management API returned invalid backup storage response: %w", err)
	}
	return out, nil
}
