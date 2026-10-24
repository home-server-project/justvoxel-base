package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

type TerminalStatus struct {
	State string `json:"state"`
	Ready bool   `json:"ready"`
}

func (c *Client) AdminTerminal(ctx context.Context, session string) (TerminalStatus, error) {
	var out TerminalStatus
	err := c.do(ctx, http.MethodGet, "/v1/admin/terminal", session, nil, &out)
	return out, err
}
func (c *Client) AdminTerminalAction(ctx context.Context, session, action string) error {
	if action != "start" && action != "stop" {
		return fmt.Errorf("invalid Terminal action")
	}
	client := *c.http
	client.Timeout = 25 * time.Second
	caller := &Client{http: &client}
	return caller.do(ctx, http.MethodPost, "/v1/admin/terminal/"+action, session, struct{}{}, nil)
}

// TerminalProxy uses the existing Management API transport, including for Upgrade.
// Only the Agent owns access to the terminal backend and its trusted header.
func (c *Client) TerminalProxy(session string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport: c.http.Transport,
		Rewrite: func(p *httputil.ProxyRequest) {
			p.Out.URL.Scheme = "http"
			p.Out.URL.Host = "unix"
			p.Out.URL.Path = "/v1/admin/terminal/proxy/" + strings.TrimPrefix(p.In.URL.Path, "/terminal/")
			p.Out.URL.RawPath = ""
			p.Out.URL.User = nil
			p.Out.Host = p.In.Host
			if origin := p.In.Header.Values("Origin"); len(origin) > 0 {
				p.Out.Header["Origin"] = append([]string(nil), origin...)
			}
			for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "X-JustVoxel-Terminal", "X-JustVoxel-User", "X-Real-IP", "Forwarded"} {
				p.Out.Header.Del(name)
			}
			for name := range p.Out.Header {
				if strings.HasPrefix(strings.ToLower(name), "x-forwarded-") {
					p.Out.Header.Del(name)
				}
			}
			p.Out.Header.Set("Authorization", "Bearer "+session)
		},
	}
}
