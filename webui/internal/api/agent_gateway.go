package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type GatewayOperation struct {
	Name         string         `json:"name"`
	Method       string         `json:"method"`
	Path         string         `json:"path"`
	MinLevel     int            `json:"min_level"`
	Instance     bool           `json:"instance"`
	AllInstances bool           `json:"all_instances"`
	BodySchema   map[string]any `json:"body_schema,omitempty"`
}
type GatewayCatalog struct {
	Operations []GatewayOperation `json:"operations"`
	Routes     []GatewayOperation `json:"routes"`
}
type GatewayCall struct {
	Operation  string            `json:"operation"`
	InstanceID string            `json:"instance_id,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
	Query      map[string]string `json:"query,omitempty"`
	Body       json.RawMessage   `json:"body,omitempty"`
}
type GatewayResult struct {
	Status int             `json:"status"`
	Data   json.RawMessage `json:"data"`
}
type GatewayMetadata struct {
	Resources map[string][]string `json:"resources"`
}

func (c *Client) GatewayCatalog(ctx context.Context, token string) (GatewayCatalog, error) {
	var out GatewayCatalog
	e := c.do(ctx, http.MethodGet, "/v1/gateway/catalog", token, nil, &out)
	return out, e
}
func (c *Client) GatewayExecute(ctx context.Context, token string, call GatewayCall) (GatewayResult, error) {
	var out GatewayResult
	// Existing operations (player-aware restart, provisioning, migration) may
	// outlive the default read timeout. Clone the client, preserving its Unix
	// transport and the WebUI listener's bounded request model.
	caller := *c
	httpClient := *c.http
	httpClient.Timeout = 19 * time.Minute
	caller.http = &httpClient
	e := caller.do(ctx, http.MethodPost, "/v1/gateway/execute", token, call, &out)
	return out, e
}
func (c *Client) GatewayMetadata(ctx context.Context) (GatewayMetadata, error) {
	var out GatewayMetadata
	e := c.do(ctx, http.MethodGet, "/v1/gateway/metadata", "", nil, &out)
	return out, e
}
func (c *Client) GatewayAdmin(ctx context.Context, session string, body json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	method := http.MethodGet
	if len(body) > 0 {
		method = http.MethodPost
	}
	e := c.do(ctx, method, "/v1/admin/gateway", session, body, &out)
	return out, e
}
