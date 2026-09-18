package api

import "context"

type AuthStatus struct {
	Mode               string `json:"mode"`
	Username           string `json:"username"`
	MinimumPasswordLen int    `json:"minimum_password_len"`
}

type AuthModeChange struct {
	Mode               string `json:"mode"`
	SystemPassword     string `json:"system_password"`
	NewWebPassword     string `json:"new_web_password,omitempty"`
	ConfirmWebPassword string `json:"confirm_web_password,omitempty"`
}

type AuthModeChangeResponse struct {
	OK             bool   `json:"ok"`
	AuthMode       string `json:"auth_mode"`
	Reauthenticate bool   `json:"reauthenticate"`
}

func (c *Client) AuthStatus(ctx context.Context, session string) (AuthStatus, error) {
	var out AuthStatus
	err := c.do(ctx, "GET", "/v1/auth", session, nil, &out)
	return out, err
}

func (c *Client) ChangeAuthMode(ctx context.Context, session string, change AuthModeChange) (AuthModeChangeResponse, error) {
	var out AuthModeChangeResponse
	err := c.do(ctx, "POST", "/v1/auth/mode", session, change, &out)
	return out, err
}
