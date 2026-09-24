package api

import (
	"context"
	"net/http"
)

type SystemMonitorProfile struct {
	System       bool `json:"system"`
	CPU          bool `json:"cpu"`
	Memory       bool `json:"memory"`
	Load         bool `json:"load"`
	Filesystem   bool `json:"filesystem"`
	DiskIO       bool `json:"diskio"`
	Network      bool `json:"network"`
	Processes    bool `json:"processes"`
	Containers   bool `json:"containers"`
	Sensors      bool `json:"sensors"`
	Alerts       bool `json:"alerts"`
	ProcessCount int  `json:"process_count"`
}

func (c *Client) SystemMonitorProfile(ctx context.Context, session string) (SystemMonitorProfile, error) {
	var out SystemMonitorProfile
	err := c.do(ctx, http.MethodGet, "/v1/system-monitor/profile", session, nil, &out)
	return out, err
}

func (c *Client) AdminSetSystemMonitorProfile(ctx context.Context, session string, profile SystemMonitorProfile) (SystemMonitorProfile, error) {
	var out SystemMonitorProfile
	err := c.do(ctx, http.MethodPost, "/v1/admin/system-monitor/profile", session, profile, &out)
	return out, err
}
