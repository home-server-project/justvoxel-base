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

type MinecraftContainerStatus struct {
	InstanceID      string `json:"instance_id"`
	Image           string `json:"image"`
	State           string `json:"state"`
	Running         bool   `json:"running"`
	PlanFingerprint string `json:"plan_fingerprint"`
}
type MinecraftContainerUpdateRequest struct {
	PlanFingerprint     string `json:"plan_fingerprint"`
	RestartNow          bool   `json:"restart_now"`
	BackupBeforeRestart bool   `json:"backup_before_restart"`
	ConfirmPlayers      bool   `json:"confirm_players"`
}

func (c *Client) minecraftContainerRequest(ctx context.Context, session, id, method, action string, body any, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://unix/v1/minecraft/instances/"+url.PathEscape(id)+"/container/"+action, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+session)
	req.Header.Set("Content-Type", "application/json")
	client := *c.http
	client.Timeout = 95 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return ErrUnauthorized
	}
	if resp.StatusCode == 403 {
		return ErrPasswordChangeRequired
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readResponseError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
func (c *Client) MinecraftInstanceContainerStatus(ctx context.Context, session, id string) (MinecraftContainerStatus, error) {
	var out MinecraftContainerStatus
	err := c.minecraftContainerRequest(ctx, session, id, "GET", "status", nil, &out)
	if err == nil && out.InstanceID != id {
		err = fmt.Errorf("selected container identity mismatch")
	}
	return out, err
}
func (c *Client) MinecraftInstanceContainerUpdate(ctx context.Context, session, id string, request MinecraftContainerUpdateRequest) (PersistentOperationResponse, error) {
	var out PersistentOperationResponse
	err := c.minecraftContainerRequest(ctx, session, id, "POST", "update", request, &out)
	return out, err
}
func (c *Client) MinecraftInstanceContainerOperation(ctx context.Context, session, id, operation string) (PersistentOperationResponse, error) {
	var out PersistentOperationResponse
	err := c.minecraftContainerRequest(ctx, session, id, "GET", "update/current?operation_id="+url.QueryEscape(operation), nil, &out)
	return out, err
}
func (c *Client) MinecraftInstanceContainerAcknowledge(ctx context.Context, session, id, operation string) (PersistentOperationResponse, error) {
	var out PersistentOperationResponse
	err := c.minecraftContainerRequest(ctx, session, id, "POST", "update/acknowledge", map[string]string{"operation_id": operation}, &out)
	return out, err
}
