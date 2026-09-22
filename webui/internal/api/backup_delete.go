package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	adminBackupDeletePlanPath  = "/v1/admin/backups/delete/plan"
	adminBackupDeleteApplyPath = "/v1/admin/backups/delete/apply"
)

type AdminBackupDeleteRequest struct {
	BackupIDs    []string `json:"backup_ids"`
	Confirmation string   `json:"confirmation,omitempty"`
	Fingerprint  string   `json:"fingerprint,omitempty"`
}

type AdminBackupDeleteItem struct {
	ID        string `json:"id"`
	SizeBytes uint64 `json:"size_bytes"`
}

type AdminBackupDeletePlan struct {
	Backups        []AdminBackupDeleteItem `json:"backups"`
	Count          int                     `json:"count"`
	TotalSizeBytes uint64                  `json:"total_size_bytes"`
	Confirmation   string                  `json:"confirmation"`
	Fingerprint    string                  `json:"fingerprint"`
}

type AdminBackupDeleteResponse struct {
	OK       bool                  `json:"ok"`
	Error    string                `json:"error,omitempty"`
	Warnings []string              `json:"warnings"`
	Proposed AdminBackupDeletePlan `json:"proposed"`
	Deleted  int                   `json:"deleted,omitempty"`
	Applied  bool                  `json:"applied"`
}

func (c *Client) AdminBackupDeletePlan(ctx context.Context, session string, request AdminBackupDeleteRequest) (AdminBackupDeleteResponse, error) {
	return c.adminBackupDeleteChange(ctx, adminBackupDeletePlanPath, session, request, 25*time.Second)
}

func (c *Client) AdminBackupDeleteApply(ctx context.Context, session string, request AdminBackupDeleteRequest) (AdminBackupDeleteResponse, error) {
	return c.adminBackupDeleteChange(ctx, adminBackupDeleteApplyPath, session, request, 70*time.Second)
}

func (c *Client) adminBackupDeleteChange(ctx context.Context, path, session string, request AdminBackupDeleteRequest, timeout time.Duration) (AdminBackupDeleteResponse, error) {
	var out AdminBackupDeleteResponse
	if len(request.BackupIDs) < 1 || len(request.BackupIDs) > 100 {
		return out, errors.New("invalid backup delete selection")
	}
	for _, id := range request.BackupIDs {
		if !restoreBackupIDPattern.MatchString(id) {
			return out, errors.New("invalid backup id")
		}
	}
	if request.Fingerprint != "" && !restoreFingerprintPattern.MatchString(request.Fingerprint) {
		return out, errors.New("invalid backup delete fingerprint")
	}

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
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("management API returned invalid backup delete response: %w", err)
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, &ResponseError{StatusCode: resp.StatusCode, Message: out.Error}
	}
	return out, nil
}
