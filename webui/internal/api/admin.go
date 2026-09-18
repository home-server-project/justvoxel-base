package api

import (
	"context"
	"fmt"
	"net/http"
)

type SessionInfo struct {
	Username   string `json:"username"`
	Role       string `json:"role"`
	AuthSource string `json:"auth_source"`
	MustChange bool   `json:"must_change"`
}

type AdminUser struct {
	ID                int64   `json:"id"`
	Username          string  `json:"username"`
	Role              string  `json:"role"`
	Enabled           bool    `json:"enabled"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
	PasswordChangedAt string  `json:"password_changed_at"`
	RestartUsed       int     `json:"restart_used"`
	RestartLimit      int     `json:"restart_limit"`
	LastRestartAt     *string `json:"last_restart_at,omitempty"`
	BackupUsed        int     `json:"backup_used"`
	BackupLimit       int     `json:"backup_limit"`
	LastBackupAt      *string `json:"last_backup_at,omitempty"`
}

type AdminUsersResponse struct {
	PrimaryAdministrator struct {
		Username string `json:"username"`
		Role     string `json:"role"`
		Managed  bool   `json:"managed"`
	} `json:"primary_administrator"`
	Users              []AdminUser `json:"users"`
	MinimumPasswordLen int         `json:"minimum_password_len"`
}

func (c *Client) Session(ctx context.Context, session string) (SessionInfo, error) {
	var out SessionInfo
	err := c.do(ctx, http.MethodGet, "/v1/session", session, nil, &out)
	return out, err
}

func (c *Client) AdminUsers(ctx context.Context, session string) (AdminUsersResponse, error) {
	var out AdminUsersResponse
	err := c.do(ctx, http.MethodGet, "/v1/admin/users", session, nil, &out)
	return out, err
}

func (c *Client) AdminCreateUser(ctx context.Context, session, username, role, password string) (AdminUser, error) {
	var out AdminUser
	err := c.do(ctx, http.MethodPost, "/v1/admin/users", session, map[string]string{
		"username": username,
		"role":     role,
		"password": password,
	}, &out)
	return out, err
}

func (c *Client) AdminSetUserRole(ctx context.Context, session string, id int64, role string) (AdminUser, error) {
	var out AdminUser
	err := c.do(ctx, http.MethodPatch, fmt.Sprintf("/v1/admin/users/%d/role", id), session, map[string]string{"role": role}, &out)
	return out, err
}

func (c *Client) AdminSetUserEnabled(ctx context.Context, session string, id int64, enabled bool) (AdminUser, error) {
	var out AdminUser
	err := c.do(ctx, http.MethodPatch, fmt.Sprintf("/v1/admin/users/%d/enabled", id), session, map[string]bool{"enabled": enabled}, &out)
	return out, err
}

func (c *Client) AdminSetUserPassword(ctx context.Context, session string, id int64, password string) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/admin/users/%d/password", id), session, map[string]string{"password": password}, nil)
}

func (c *Client) AdminDeleteUser(ctx context.Context, session string, id int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/v1/admin/users/%d", id), session, nil, nil)
}

func (c *Client) AdminResetRestartAllowance(ctx context.Context, session string, id int64) (AdminUser, error) {
	var out AdminUser
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/admin/users/%d/restart-allowance/reset", id), session, nil, &out)
	return out, err
}

func (c *Client) AdminResetBackupAllowance(ctx context.Context, session string, id int64) (AdminUser, error) {
	var out AdminUser
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/admin/users/%d/backup-allowance/reset", id), session, nil, &out)
	return out, err
}
