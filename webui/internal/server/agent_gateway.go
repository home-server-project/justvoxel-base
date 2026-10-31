package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const gatewayMaxBody = 1 << 20

type externalGatewayAPI interface {
	GatewayCatalog(context.Context, string) (api.GatewayCatalog, error)
	GatewayExecute(context.Context, string, api.GatewayCall) (api.GatewayResult, error)
	GatewayMetadata(context.Context) (api.GatewayMetadata, error)
	GatewayAdmin(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func (a *App) registerAgentGatewayRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/", a.gatewayREST)
	mux.HandleFunc("POST /api/v1/", a.gatewayREST)
	mux.HandleFunc("PUT /api/v1/", a.gatewayREST)
	mux.HandleFunc("POST /mcp", a.gatewayMCP)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", a.gatewayResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", a.gatewayResourceMetadata)
	mux.HandleFunc("OPTIONS /.well-known/oauth-protected-resource/mcp", a.gatewayResourceMetadata)
	mux.HandleFunc("OPTIONS /.well-known/oauth-protected-resource", a.gatewayResourceMetadata)
	mux.HandleFunc("GET /api/agent-gateway", a.gatewayAdmin)
	mux.HandleFunc("POST /api/agent-gateway", a.gatewayAdmin)
}
func gatewayJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (a *App) gatewayOrigin(r *http.Request) string {
	scheme := "http"
	forwardedProto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	if r.TLS != nil || a.config.ExternalScheme == "https" || strings.EqualFold(strings.TrimSpace(forwardedProto), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
func (a *App) gatewayUnauthorized(w http.ResponseWriter, r *http.Request) {
	// Quote the URL; never echo bearer values or backend error strings.
	metadata := a.gatewayOrigin(r) + "/.well-known/oauth-protected-resource/mcp"
	w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+strings.ReplaceAll(metadata, `"`, "")+`", scope="justvoxel:connect"`)
	gatewayJSON(w, 401, map[string]any{"error": map[string]string{"code": "authentication_required", "message": "A valid enabled JustVoxel connection is required."}})
}
func (a *App) gatewayAuthenticate(w http.ResponseWriter, r *http.Request) (externalGatewayAPI, string, api.GatewayCatalog, bool) {
	client, ok := a.api.(externalGatewayAPI)
	if !ok {
		gatewayJSON(w, 503, map[string]string{"error": "gateway_unavailable"})
		return nil, "", api.GatewayCatalog{}, false
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") || len(h) > 16400 {
		a.gatewayUnauthorized(w, r)
		return nil, "", api.GatewayCatalog{}, false
	}
	token := strings.TrimPrefix(h, "Bearer ")
	if token == "" {
		a.gatewayUnauthorized(w, r)
		return nil, "", api.GatewayCatalog{}, false
	}
	// Cookies never authenticate external transport requests.
	catalog, err := client.GatewayCatalog(r.Context(), token)
	if err != nil {
		a.gatewayUnauthorized(w, r)
		return nil, "", api.GatewayCatalog{}, false
	}
	return client, token, catalog, true
}
func gatewayMatch(op api.GatewayOperation, r *http.Request) (map[string]string, bool) {
	if r.Method != op.Method {
		return nil, false
	}
	pattern := strings.Split(op.Path, "/")
	actual := strings.Split(r.URL.Path, "/")
	if len(pattern) != len(actual) {
		return nil, false
	}
	params := map[string]string{}
	for i, part := range pattern {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			if actual[i] == "" || strings.ContainsAny(actual[i], "/\\\x00") {
				return nil, false
			}
			params[strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")] = actual[i]
		} else if part != actual[i] {
			return nil, false
		}
	}
	return params, true
}
func (a *App) gatewayREST(w http.ResponseWriter, r *http.Request) {
	client, token, catalog, ok := a.gatewayAuthenticate(w, r)
	if !ok {
		return
	}
	if r.URL.Path == "/api/v1/openapi.json" && r.Method == "GET" {
		gatewayJSON(w, 200, gatewayOpenAPI(catalog))
		return
	}
	var chosen *api.GatewayOperation
	var params map[string]string
	routes := catalog.Routes
	if len(routes) == 0 {
		routes = catalog.Operations
	}
	for _, op := range routes {
		if p, ok := gatewayMatch(op, r); ok {
			chosen = &op
			params = p
			break
		}
	}
	if chosen == nil {
		gatewayJSON(w, 403, map[string]string{"error": "operation_not_permitted"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, gatewayMaxBody))
	if err != nil {
		gatewayJSON(w, 413, map[string]string{"error": "request_too_large"})
		return
	}
	if len(body) > 0 && !json.Valid(body) {
		gatewayJSON(w, 400, map[string]string{"error": "invalid_json"})
		return
	}
	query := map[string]string{}
	for key, values := range r.URL.Query() {
		if len(values) != 1 {
			gatewayJSON(w, 400, map[string]string{"error": "duplicate_query_parameter"})
			return
		}
		query[key] = values[0]
	}
	call := api.GatewayCall{Operation: chosen.Name, Parameters: params, Query: query, Body: body}
	if chosen.Instance {
		call.InstanceID = params["id"]
		delete(params, "id")
	}
	result, err := client.GatewayExecute(r.Context(), token, call)
	if err != nil {
		if errors.Is(err, api.ErrUnauthorized) {
			a.gatewayUnauthorized(w, r)
		} else {
			gatewayJSON(w, 403, map[string]string{"error": "operation_denied_or_unavailable"})
		}
		return
	}
	gatewayJSON(w, result.Status, result.Data)
}
func gatewayOpenAPI(catalog api.GatewayCatalog) map[string]any {
	paths := map[string]any{}
	for _, op := range catalog.Operations {
		item, ok := paths[op.Path].(map[string]any)
		if !ok {
			item = map[string]any{}
			paths[op.Path] = item
		}
		params := []any{}
		for _, part := range strings.Split(op.Path, "/") {
			if strings.HasPrefix(part, "{") {
				params = append(params, map[string]any{"name": strings.Trim(part, "{}"), "in": "path", "required": true, "schema": map[string]any{"type": "string", "maxLength": 128}})
			}
		}
		operation := map[string]any{"operationId": op.Name, "summary": strings.ReplaceAll(op.Name, "_", " "), "x-justvoxel-min-level": op.MinLevel, "x-justvoxel-all-instances": op.AllInstances, "parameters": params, "security": []any{map[string]any{"bearerAuth": []string{}}}, "responses": map[string]any{"200": map[string]any{"description": "Sanitized appliance result"}, "202": map[string]any{"description": "Accepted operation; retain its ID and inspect status"}, "400": map[string]any{"description": "Invalid request"}, "401": map[string]any{"description": "Invalid, disabled, revoked or expired credential"}, "403": map[string]any{"description": "Current permission or instance scope denied"}, "409": map[string]any{"description": "Safety check, confirmation, recovery or operation conflict"}, "429": map[string]any{"description": "Operator allowance or cooldown"}}}
		if op.Method != "GET" {
			operation["requestBody"] = map[string]any{"required": false, "content": map[string]any{"application/json": map[string]any{"schema": op.BodySchema}}}
		}
		item[strings.ToLower(op.Method)] = operation
	}
	return map[string]any{"openapi": "3.1.2", "info": map[string]string{"title": "JustVoxel AI & API Connections", "version": "1"}, "paths": paths, "components": map[string]any{"securitySchemes": map[string]any{"bearerAuth": map[string]any{"type": "http", "scheme": "bearer", "description": "Scoped JustVoxel API token or OAuth access token for the configured resource"}}}}
}
func (a *App) gatewayMCP(w http.ResponseWriter, r *http.Request) {
	client, token, catalog, ok := a.gatewayAuthenticate(w, r)
	if !ok {
		return
	}
	// A request gets a fresh server and authoritative tool list. The SDK handles
	// negotiation, JSON-RPC and Streamable HTTP; no persistent sessions are stored.
	server := mcp.NewServer(&mcp.Implementation{Name: "JustVoxel", Version: "1"}, nil)
	for _, operation := range catalog.Operations {
		op := operation
		schema := gatewayToolSchema(op)
		server.AddTool(&mcp.Tool{Name: op.Name, Description: strings.ReplaceAll(op.Name, "_", " "), InputSchema: schema}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return gatewayMCPExecute(ctx, client, token, op.Name, request.Params.Arguments)
		})
	}
	// Preserve SDK tool visibility while letting the Agent audit known denied
	// mutation attempts (including stale cached names) against current policy.
	visible := map[string]bool{}
	for _, op := range catalog.Operations {
		visible[op.Name] = true
	}
	hidden := map[string]bool{}
	for _, op := range catalog.Routes {
		if !visible[op.Name] && op.Method != "GET" {
			hidden[op.Name] = true
		}
	}
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method == "tools/call" {
				if params, ok := request.GetParams().(*mcp.CallToolParamsRaw); ok && hidden[params.Name] {
					return gatewayMCPExecute(ctx, client, token, params.Name, params.Arguments)
				}
			}
			return next(ctx, method, request)
		}
	})
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: gatewayMaxBody, PropagateRequestCancellation: true})
	w.Header().Set("Cache-Control", "no-store")
	http.NewCrossOriginProtection().Handler(transport).ServeHTTP(w, r)
}
func gatewayToolSchema(op api.GatewayOperation) map[string]any {
	if op.BodySchema == nil {
		op.BodySchema = map[string]any{"type": "object"}
	}
	properties := map[string]any{"instance_id": map[string]any{"type": "string", "pattern": "^jv-[a-z0-9]+$"}, "parameters": map[string]any{"type": "object", "additionalProperties": map[string]string{"type": "string"}}, "query": map[string]any{"type": "object", "additionalProperties": map[string]string{"type": "string"}}, "body": op.BodySchema}
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if op.Instance {
		schema["required"] = []string{"instance_id"}
	}
	return schema
}
func (a *App) gatewayResourceMetadata(w http.ResponseWriter, r *http.Request) {
	client, ok := a.api.(externalGatewayAPI)
	if !ok {
		gatewayJSON(w, 503, map[string]string{"error": "gateway_unavailable"})
		return
	}
	metadata, err := client.GatewayMetadata(r.Context())
	if err != nil {
		gatewayJSON(w, 503, map[string]string{"error": "gateway_unavailable"})
		return
	}
	resource := a.gatewayOrigin(r) + "/mcp"
	// A pinned OAuth audience must identify this protected resource. The Host is
	// presentation only and never selects an issuer, JWKS URI or accepted audience.
	w.Header().Set("Cache-Control", "no-store")
	auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource: resource, AuthorizationServers: metadata.Resources[resource],
		ScopesSupported: []string{"justvoxel:connect"}, BearerMethodsSupported: []string{"header"},
		ResourceName: "JustVoxel AI & API Connections",
	}).ServeHTTP(w, r)
}
func (a *App) gatewayAdmin(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.fileBrowserIdentity(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(externalGatewayAPI)
	if !ok {
		gatewayJSON(w, 503, map[string]string{"error": "gateway_unavailable"})
		return
	}
	var body json.RawMessage
	if r.Method == "POST" {
		r.Body = http.MaxBytesReader(w, r.Body, gatewayMaxBody)
		if r.ParseForm() != nil {
			gatewayJSON(w, 400, map[string]string{"error": "invalid_request"})
			return
		}
		if !a.validCSRF(r) {
			gatewayJSON(w, 403, map[string]string{"error": "invalid_csrf"})
			return
		}
		raw := []byte(r.FormValue("request"))
		if !json.Valid(raw) {
			gatewayJSON(w, 400, map[string]string{"error": "invalid_json"})
			return
		}
		body = raw
	}
	result, err := client.GatewayAdmin(r.Context(), session, body)
	if err != nil {
		gatewayJSON(w, 400, map[string]string{"error": "Connection settings could not be saved. Check settings and Level 4 confirmation."})
		return
	}
	gatewayJSON(w, 200, result)
}

func gatewayMCPExecute(ctx context.Context, client externalGatewayAPI, token, name string, arguments json.RawMessage) (*mcp.CallToolResult, error) {
	var input struct {
		InstanceID string            `json:"instance_id"`
		Parameters map[string]string `json:"parameters"`
		Query      map[string]string `json:"query"`
		Body       json.RawMessage   `json:"body"`
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Invalid operation arguments."}}}, nil
	}
	result, err := client.GatewayExecute(ctx, token, api.GatewayCall{Operation: name, InstanceID: input.InstanceID, Parameters: input.Parameters, Query: input.Query, Body: input.Body})
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Operation denied or unavailable. Refresh connection permissions and tools."}}}, nil
	}
	return &mcp.CallToolResult{IsError: result.Status >= 400, Content: []mcp.Content{&mcp.TextContent{Text: string(result.Data)}}}, nil
}
