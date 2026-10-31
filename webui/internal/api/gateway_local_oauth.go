package api

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

// OAuth state is owned by the Agent; the WebUI only presents protocol responses.
func (c *Client) GatewayOAuth(ctx context.Context, session string, input any) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, "POST", "/v1/gateway/oauth", session, input, &out)
	return out, err
}
func (c *Client) GatewayResourceCatalog(ctx context.Context, token, resource string) (GatewayCatalog, error) {
	var out GatewayCatalog
	err := c.do(ctx, "GET", "/v1/gateway/catalog?resource="+url.QueryEscape(resource), token, nil, &out)
	return out, err
}
func (c *Client) GatewayResourceExecute(ctx context.Context, token, resource string, call GatewayCall) (GatewayResult, error) {
	caller := *c
	httpClient := *c.http
	httpClient.Timeout = 19 * time.Minute
	caller.http = &httpClient
	var out GatewayResult
	err := caller.do(ctx, "POST", "/v1/gateway/execute?resource="+url.QueryEscape(resource), token, call, &out)
	return out, err
}
