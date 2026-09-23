package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const adminSystemUpdatesPath = "/v1/admin/system/updates"

var (
	adminSystemUpdateStatusTimeout = 15 * time.Second
	adminSystemUpdateTimeout       = 16 * time.Minute
)

type AdminSystemUpdateDeployment struct {
	Image   string `json:"image,omitempty"`
	Version string `json:"version,omitempty"`
	Digest  string `json:"digest"`
}

type AdminSystemUpdateStatus struct {
	OK             bool                         `json:"ok"`
	ReadOnly       bool                         `json:"read_only"`
	RebootRequired bool                         `json:"reboot_required"`
	Running        AdminSystemUpdateDeployment  `json:"running"`
	Staged         *AdminSystemUpdateDeployment `json:"staged,omitempty"`
	Rollback       *AdminSystemUpdateDeployment `json:"rollback,omitempty"`
	Message        string                       `json:"message,omitempty"`
}

func (c *Client) AdminSystemUpdateStatus(ctx context.Context, session string) (AdminSystemUpdateStatus, error) {
	return c.adminSystemUpdateRequest(ctx, session, http.MethodGet, adminSystemUpdateStatusTimeout)
}

func (c *Client) AdminSystemUpdate(ctx context.Context, session string) (AdminSystemUpdateStatus, error) {
	return c.adminSystemUpdateRequest(ctx, session, http.MethodPost, adminSystemUpdateTimeout)
}

func (c *Client) adminSystemUpdateRequest(ctx context.Context, session, method string, timeout time.Duration) (AdminSystemUpdateStatus, error) {
	var out AdminSystemUpdateStatus
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+adminSystemUpdatesPath, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
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
	if err := decodeAdminSystemUpdateStatus(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid system update status: %w", err)
	}
	return out, nil
}

func decodeAdminSystemUpdateStatus(reader io.Reader, target *AdminSystemUpdateStatus) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 32*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	if !target.OK || target.Running.Digest == "" {
		return errors.New("incomplete system update status")
	}
	return nil
}
