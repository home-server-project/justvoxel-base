package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	adminSystemUpdatesPath      = "/v1/admin/system/updates"
	adminSystemUpdateRebootPath = "/v1/admin/system/update-reboot"
)

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

type AdminSystemUpdateRebootOptions struct {
	ConfirmPlayers  bool
	BackupMinecraft bool
	WarningSeconds  int
}

type AdminSystemUpdateRebootStatus struct {
	OK                   bool     `json:"ok"`
	State                string   `json:"state"`
	Accepted             bool     `json:"accepted,omitempty"`
	ConfirmationRequired bool     `json:"confirmation_required,omitempty"`
	Reason               string   `json:"reason,omitempty"`
	Message              string   `json:"message,omitempty"`
	Online               int      `json:"online,omitempty"`
	Players              []string `json:"players,omitempty"`
	WarningSeconds       int      `json:"warning_seconds,omitempty"`
	BackupMinecraft      bool     `json:"backup_minecraft,omitempty"`
	DeadlineUnix         int64    `json:"deadline_unix,omitempty"`
}

type adminSystemUpdateRebootRequest struct {
	ActionConfirmed bool `json:"action_confirmed"`
	ConfirmPlayers  bool `json:"confirm_players"`
	BackupMinecraft bool `json:"backup_minecraft"`
	WarningSeconds  int  `json:"warning_seconds"`
}

func (c *Client) AdminSystemUpdateRebootStatus(ctx context.Context, session string) (AdminSystemUpdateRebootStatus, error) {
	var out AdminSystemUpdateRebootStatus
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix"+adminSystemUpdateRebootPath, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)
	client := *c.http
	client.Timeout = 15 * time.Second
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
	if err := decodeAdminSystemUpdateRebootStatus(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid system update reboot status: %w", err)
	}
	return out, nil
}

func (c *Client) AdminSystemUpdateReboot(ctx context.Context, session string, options AdminSystemUpdateRebootOptions) (AdminSystemUpdateRebootStatus, error) {
	var out AdminSystemUpdateRebootStatus
	if options.WarningSeconds == 0 {
		options.WarningSeconds = 60
	}
	payload, err := json.Marshal(adminSystemUpdateRebootRequest{
		ActionConfirmed: true,
		ConfirmPlayers:  options.ConfirmPlayers,
		BackupMinecraft: options.BackupMinecraft,
		WarningSeconds:  options.WarningSeconds,
	})
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+adminSystemUpdateRebootPath, bytes.NewReader(payload))
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)
	client := *c.http
	client.Timeout = 15 * time.Second
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
	if err := decodeAdminSystemUpdateRebootStatus(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid system update reboot response: %w", err)
	}
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
		return out, nil
	}
	message := out.Message
	if message == "" {
		message = out.Reason
	}
	return out, &ResponseError{StatusCode: resp.StatusCode, Message: message}
}

func decodeAdminSystemUpdateRebootStatus(reader io.Reader, target *AdminSystemUpdateRebootStatus) error {
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
	if target.State == "" {
		return errors.New("missing system update reboot state")
	}
	if target.Players == nil {
		target.Players = []string{}
	}
	return nil
}
