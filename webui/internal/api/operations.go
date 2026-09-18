package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type ManualBackupResponse struct {
	OK                         bool   `json:"ok"`
	Message                    string `json:"message"`
	Error                      string `json:"error,omitempty"`
	Reason                     string `json:"reason,omitempty"`
	BackupUsed                 int    `json:"backup_used,omitempty"`
	BackupLimit                int    `json:"backup_limit,omitempty"`
	RetryAfterSeconds          int    `json:"retry_after_seconds,omitempty"`
	AdministratorResetRequired bool   `json:"administrator_reset_required,omitempty"`
	OperatorUsage              *struct {
		BackupUsed      int `json:"backup_used"`
		BackupLimit     int `json:"backup_limit"`
		CooldownSeconds int `json:"cooldown_seconds"`
	} `json:"operator_usage,omitempty"`
}

type TextOutputResponse struct {
	Output string `json:"output"`
}

type LogsResponse struct {
	Lines []string `json:"lines"`
}

type PublicActivityEvent struct {
	OccurredAt string `json:"occurred_at"`
	Action     string `json:"action"`
	Success    bool   `json:"success"`
}

type PublicActivityResponse struct {
	Events []PublicActivityEvent `json:"events"`
}

type AuditEvent struct {
	ID            int64  `json:"id"`
	OccurredAt    string `json:"occurred_at"`
	ActorUsername string `json:"actor_username"`
	ActorRole     string `json:"actor_role"`
	Action        string `json:"action"`
	Target        string `json:"target,omitempty"`
	Success       bool   `json:"success"`
	Context       string `json:"context,omitempty"`
}

type AuditResponse struct {
	Events []AuditEvent `json:"events"`
}

type Notification struct {
	ID         int64   `json:"id"`
	CreatedAt  string  `json:"created_at"`
	Kind       string  `json:"kind"`
	UserID     *int64  `json:"user_id,omitempty"`
	Title      string  `json:"title"`
	Message    string  `json:"message"`
	ResolvedAt *string `json:"resolved_at,omitempty"`
	ResolvedBy *string `json:"resolved_by,omitempty"`
}

type NotificationsResponse struct {
	Notifications []Notification `json:"notifications"`
}

func (c *Client) ManualBackup(ctx context.Context, session string) (ManualBackupResponse, error) {
	var out ManualBackupResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/v1/backups/manual", nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)
	resp, err := c.http.Do(req)
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
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out); err != nil {
		return out, fmt.Errorf("management API returned invalid backup response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := out.Error
		if message == "" {
			message = out.Message
		}
		return out, &ResponseError{StatusCode: resp.StatusCode, Message: message}
	}
	return out, nil
}

func (c *Client) Whitelist(ctx context.Context, session string) (TextOutputResponse, error) {
	var out TextOutputResponse
	err := c.do(ctx, http.MethodGet, "/v1/whitelist", session, nil, &out)
	return out, err
}

func (c *Client) WhitelistChange(ctx context.Context, session, platform, action, name string) (TextOutputResponse, error) {
	var out TextOutputResponse
	err := c.do(ctx, http.MethodPost, "/v1/whitelist", session, map[string]string{
		"platform": platform,
		"action":   action,
		"name":     name,
	}, &out)
	return out, err
}

func (c *Client) MinecraftLogs(ctx context.Context, session string, limit int) (LogsResponse, error) {
	var out LogsResponse
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/logs/minecraft?limit=%d", limit), session, nil, &out)
	return out, err
}

func (c *Client) Activity(ctx context.Context, session string, limit int) (PublicActivityResponse, error) {
	var out PublicActivityResponse
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/activity?limit=%d", limit), session, nil, &out)
	return out, err
}

func (c *Client) AdminAudit(ctx context.Context, session string, limit int) (AuditResponse, error) {
	var out AuditResponse
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/admin/audit?limit=%d", limit), session, nil, &out)
	return out, err
}

func (c *Client) AdminNotifications(ctx context.Context, session string, openOnly bool) (NotificationsResponse, error) {
	var out NotificationsResponse
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/admin/notifications?open=%t", openOnly), session, nil, &out)
	return out, err
}

func (c *Client) AdminResolveNotification(ctx context.Context, session string, id int64) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/admin/notifications/%d/resolve", id), session, nil, nil)
}
