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

const adminSystemActionsStatusPath = "/v1/admin/system/actions"

var (
	adminSystemActionsStatusTimeout = 15 * time.Second
	adminSystemActionTimeout        = 190 * time.Second
)

type AdminSystemFirmwareStatus struct {
	Available    bool   `json:"available"`
	EFI          bool   `json:"efi"`
	DisplayState string `json:"display_state"`
	Reason       string `json:"reason,omitempty"`
}

type AdminSystemActionsStatus struct {
	OK                bool                      `json:"ok"`
	Variant           string                    `json:"variant"`
	MinecraftState    string                    `json:"minecraft_state"`
	StagedUpdate      bool                      `json:"staged_update"`
	OSStatusAvailable bool                      `json:"os_status_available"`
	Firmware          AdminSystemFirmwareStatus `json:"firmware"`
}

type AdminSystemActionResponse struct {
	OK                   bool     `json:"ok"`
	Action               string   `json:"action,omitempty"`
	Accepted             bool     `json:"accepted,omitempty"`
	Message              string   `json:"message,omitempty"`
	Reason               string   `json:"reason,omitempty"`
	ConfirmationRequired bool     `json:"confirmation_required,omitempty"`
	Online               int      `json:"online,omitempty"`
	Players              []string `json:"players,omitempty"`
}

type adminSystemActionRequest struct {
	ActionConfirmed bool `json:"action_confirmed"`
	ConfirmPlayers  bool `json:"confirm_players"`
}

func (c *Client) AdminSystemActionsStatus(ctx context.Context, session string) (AdminSystemActionsStatus, error) {
	var out AdminSystemActionsStatus
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix"+adminSystemActionsStatusPath, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)

	client := *c.http
	client.Timeout = adminSystemActionsStatusTimeout
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
	if err := decodeAdminSystemActionsStatus(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid system action status: %w", err)
	}
	return out, nil
}

func (c *Client) AdminSystemAction(ctx context.Context, session, action string, confirmPlayers bool) (AdminSystemActionResponse, error) {
	var out AdminSystemActionResponse
	path, err := adminSystemActionPath(action)
	if err != nil {
		return out, err
	}
	payload, err := json.Marshal(adminSystemActionRequest{
		ActionConfirmed: true,
		ConfirmPlayers:  confirmPlayers,
	})
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+path, bytes.NewReader(payload))
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)

	client := *c.http
	client.Timeout = adminSystemActionTimeout
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
	if err := decodeAdminSystemActionResponse(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid system action response: %w", err)
	}
	if out.Action != "" && out.Action != action {
		return out, errors.New("management API returned mismatched system action")
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

func adminSystemActionPath(action string) (string, error) {
	switch action {
	case "reboot", "poweroff", "firmware-reboot":
		return "/v1/admin/system/" + action, nil
	default:
		return "", fmt.Errorf("unsupported system action %q", action)
	}
}

func decodeAdminSystemActionsStatus(reader io.Reader, target *AdminSystemActionsStatus) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 16*1024))
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
	return nil
}

func decodeAdminSystemActionResponse(reader io.Reader, target *AdminSystemActionResponse) error {
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
	if target.Players == nil {
		target.Players = []string{}
	}
	return nil
}
