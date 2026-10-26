package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type MinecraftNetworkConfig struct {
	WhitelistEnforced bool     `json:"whitelist_enforced"`
	Enabled           bool     `json:"enabled"`
	JavaPort          int      `json:"java_port"`
	BedrockPort       int      `json:"bedrock_port"`
	Members           []string `json:"members"`
	WelcomeMessage    string   `json:"welcome_message"`
}
type MinecraftNetworkPlayer struct {
	Name     string `json:"name"`
	Platform string `json:"platform"`
}
type MinecraftNetworkServer struct {
	ID       string `json:"id"`
	Software string `json:"software"`
	Eligible bool   `json:"eligible"`
	State    string `json:"state"`
}
type MinecraftNetworkStatus struct {
	RuntimeReady      bool                     `json:"runtime_ready"`
	Config            MinecraftNetworkConfig   `json:"config"`
	Servers           []MinecraftNetworkServer `json:"servers"`
	Whitelist         []MinecraftNetworkPlayer `json:"whitelist"`
	WhitelistEnforced bool                     `json:"whitelist_enforced"`
	RecoveryRequired  bool                     `json:"recovery_required"`
}
type MinecraftConnection struct {
	ID             string `json:"id"`
	NetworkMember  bool   `json:"network_member"`
	JavaPort       int    `json:"java_port"`
	BedrockPort    int    `json:"bedrock_port"`
	BedrockEnabled bool   `json:"bedrock_enabled"`
}

func (c *Client) MinecraftNetwork(ctx context.Context, session string) (MinecraftNetworkStatus, error) {
	var out MinecraftNetworkStatus
	err := c.do(ctx, http.MethodGet, "/v1/minecraft/network", session, nil, &out)
	return out, err
}
func (c *Client) ConfigureMinecraftNetwork(ctx context.Context, session string, config MinecraftNetworkConfig) error {
	client := *c.http
	client.Timeout = 250 * time.Second
	caller := &Client{http: &client}
	var out struct {
		OK bool `json:"ok"`
	}
	return caller.do(ctx, http.MethodPut, "/v1/minecraft/network", session, config, &out)
}
func (c *Client) RecoverMinecraftNetwork(ctx context.Context, session string) error {
	client := *c.http
	client.Timeout = 130 * time.Second
	caller := &Client{http: &client}
	var out struct {
		OK bool `json:"ok"`
	}
	return caller.do(ctx, http.MethodPost, "/v1/minecraft/network/recover", session, struct{}{}, &out)
}
func (c *Client) MinecraftNetworkWhitelist(ctx context.Context, session, platform, action, name string) error {
	var out struct {
		OK bool `json:"ok"`
	}
	client := *c.http
	client.Timeout = 70 * time.Second
	caller := &Client{http: &client}
	return caller.do(ctx, http.MethodPost, "/v1/minecraft/network/whitelist", session, map[string]string{"platform": platform, "action": action, "name": name}, &out)
}
func (c *Client) MinecraftInstanceConnection(ctx context.Context, session, id string) (MinecraftConnection, error) {
	var out MinecraftConnection
	err := c.do(ctx, http.MethodGet, "/v1/minecraft/instances/"+url.PathEscape(id)+"/connection", session, nil, &out)
	return out, err
}
