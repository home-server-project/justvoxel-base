package api

import (
	"context"
	"net/http"
	"net/url"
)

type PlusHostLogs struct {
	OK      bool   `json:"ok"`
	Service string `json:"service"`
	Output  string `json:"output"`
}

func (c *Client) PlusHostLogs(ctx context.Context, session, service string) (PlusHostLogs, error) {
	var out PlusHostLogs
	err := c.do(ctx, http.MethodGet, "/v1/admin/plus/host-logs?service="+url.QueryEscape(service), session, nil, &out)
	return out, err
}

type PlusSetupComponents struct {
	Database bool `json:"database"`
	Cache    bool `json:"cache"`
	Panel    bool `json:"panel"`
	Wings    bool `json:"wings"`
	Drydock  bool `json:"drydock"`
}

type PlusSetupRequest struct {
	Components      PlusSetupComponents `json:"components"`
	UseDomain       bool                `json:"use_domain"`
	Host            string              `json:"host"`
	UseTLS          bool                `json:"use_tls"`
	Certificate     string              `json:"certificate"`
	PrivateKey      string              `json:"private_key"`
	StorageMount    string              `json:"storage_mount"`
	SeparateAccount bool                `json:"separate_account"`
	Username        string              `json:"username"`
	Email           string              `json:"email"`
	Password        string              `json:"password"`
}

type PlusSetupMount struct {
	Target string `json:"target"`
	FSType string `json:"fstype"`
}

type PlusSetupState struct {
	Prepared bool             `json:"prepared"`
	Deployed bool             `json:"deployed"`
	Mounts   []PlusSetupMount `json:"mounts"`
}

type PlusSetupResponse struct {
	OK       bool   `json:"ok"`
	Prepared bool   `json:"prepared"`
	Message  string `json:"message"`
}

func (c *Client) PlusSetupState(ctx context.Context, session string) (PlusSetupState, error) {
	var out PlusSetupState
	err := c.do(ctx, http.MethodGet, "/v1/admin/plus/setup/state", session, nil, &out)
	return out, err
}

func (c *Client) PlusSetupPrepare(ctx context.Context, session string, request PlusSetupRequest) (PlusSetupResponse, error) {
	var out PlusSetupResponse
	err := c.do(ctx, http.MethodPost, "/v1/admin/plus/setup/prepare", session, request, &out)
	return out, err
}
