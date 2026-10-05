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
