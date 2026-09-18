package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type AdminStorageProvisionRequest struct {
	Operation    string `json:"operation"`
	Device       string `json:"device"`
	MountPoint   string `json:"mount_point"`
	Path         string `json:"path"`
	SizeGiB      string `json:"size_gib,omitempty"`
	Confirmation string `json:"confirmation,omitempty"`
	Fingerprint  string `json:"fingerprint,omitempty"`
}

type AdminStorageProvisionCandidate struct {
	Path       string `json:"path"`
	Parent     string `json:"parent,omitempty"`
	Model      string `json:"model,omitempty"`
	Transport  string `json:"transport,omitempty"`
	Filesystem string `json:"filesystem,omitempty"`
	Mountpoint string `json:"mountpoint,omitempty"`
	SizeBytes  uint64 `json:"size_bytes"`
	FreeBytes  uint64 `json:"free_bytes,omitempty"`
	SystemDisk bool   `json:"system_disk,omitempty"`
}

type AdminStorageProvisionPlan struct {
	Operation          string `json:"operation"`
	Device             string `json:"device"`
	Model              string `json:"model,omitempty"`
	Transport          string `json:"transport,omitempty"`
	SizeBytes          uint64 `json:"size_bytes"`
	MountPoint         string `json:"mount_point"`
	Path               string `json:"path"`
	SizeGiB            string `json:"size_gib,omitempty"`
	FreeStart          string `json:"free_start,omitempty"`
	FreeEnd            string `json:"free_end,omitempty"`
	PlannedEnd         string `json:"planned_end,omitempty"`
	FreeMiB            uint64 `json:"free_mib,omitempty"`
	PlannedMiB         uint64 `json:"planned_mib,omitempty"`
	Confirmation       string `json:"confirmation"`
	Fingerprint        string `json:"fingerprint"`
	SystemDisk         bool   `json:"system_disk,omitempty"`
	Partition          string `json:"partition,omitempty"`
	Filesystem         string `json:"filesystem,omitempty"`
	ExpectedUUID       string `json:"expected_uuid,omitempty"`
	ExpectedSource     string `json:"expected_source,omitempty"`
	PartitionSizeBytes uint64 `json:"partition_size_bytes,omitempty"`
}

type AdminStorageProvisionResponse struct {
	OK              bool                             `json:"ok"`
	Error           string                           `json:"error,omitempty"`
	Warnings        []string                         `json:"warnings"`
	WholeDisks      []AdminStorageProvisionCandidate `json:"whole_disks,omitempty"`
	BlankPartitions []AdminStorageProvisionCandidate `json:"blank_partitions,omitempty"`
	FreeSpaceDisks  []AdminStorageProvisionCandidate `json:"free_space_disks,omitempty"`
	Proposed        AdminStorageProvisionPlan        `json:"proposed"`
	Applied         bool                             `json:"applied"`
}

func (c *Client) AdminStorageProvisionDiscover(ctx context.Context, session string) (AdminStorageProvisionResponse, error) {
	var out AdminStorageProvisionResponse
	err := c.do(ctx, http.MethodGet, "/v1/admin/storage-provision", session, nil, &out)
	return out, err
}

func (c *Client) AdminStorageProvisionPlan(ctx context.Context, session string, request AdminStorageProvisionRequest) (AdminStorageProvisionResponse, error) {
	return c.adminStorageProvisionChange(ctx, "/v1/admin/storage-provision/plan", session, request, 20*time.Second)
}

func (c *Client) AdminStorageProvisionApply(ctx context.Context, session string, request AdminStorageProvisionRequest) (AdminStorageProvisionResponse, error) {
	return c.adminStorageProvisionChange(ctx, "/v1/admin/storage-provision/apply", session, request, 100*time.Second)
}

func (c *Client) adminStorageProvisionChange(ctx context.Context, path, session string, request AdminStorageProvisionRequest, timeout time.Duration) (AdminStorageProvisionResponse, error) {
	var out AdminStorageProvisionResponse
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
		return out, fmt.Errorf("management API returned invalid storage provisioning response: %w", err)
	}
	return out, nil
}
