package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type gatewayTestAPI struct {
	filesTestAPI
	level              int
	disabled           bool
	executions         int
	adminCalls         int
	downgradeAtExecute bool
}

func gatewayTestOperations() []api.GatewayOperation {
	return []api.GatewayOperation{
		{Name: "appliance_status", Method: "GET", Path: "/api/v1/status", MinLevel: 1},
		{Name: "instance_restart", Method: "POST", Path: "/api/v1/instances/{id}/restart", MinLevel: 2, Instance: true},
		{Name: "instance_stop", Method: "POST", Path: "/api/v1/instances/{id}/stop", MinLevel: 3, Instance: true},
		{Name: "systemd", Method: "POST", Path: "/api/v1/system/systemd", MinLevel: 4, AllInstances: true},
	}
}
func (f *gatewayTestAPI) GatewayCatalog(_ context.Context, token string) (api.GatewayCatalog, error) {
	if token != "external-test-credential" || f.disabled {
		return api.GatewayCatalog{}, api.ErrUnauthorized
	}
	result := api.GatewayCatalog{Operations: []api.GatewayOperation{}, Routes: gatewayTestOperations()}
	for _, op := range gatewayTestOperations() {
		if f.level >= op.MinLevel {
			result.Operations = append(result.Operations, op)
		}
	}
	return result, nil
}
func (f *gatewayTestAPI) GatewayExecute(_ context.Context, token string, call api.GatewayCall) (api.GatewayResult, error) {
	if f.downgradeAtExecute {
		f.level = 1
	}
	if token != "external-test-credential" || f.disabled {
		return api.GatewayResult{}, api.ErrUnauthorized
	}
	for _, op := range gatewayTestOperations() {
		if op.Name == call.Operation && f.level >= op.MinLevel {
			f.executions++
			return api.GatewayResult{Status: 200, Data: json.RawMessage(`{"ok":true}`)}, nil
		}
	}
	return api.GatewayResult{}, api.ErrPasswordChangeRequired
}
func (f *gatewayTestAPI) GatewayMetadata(context.Context) (api.GatewayMetadata, error) {
	return api.GatewayMetadata{Resources: map[string][]string{"https://voxel.example/mcp": {"https://issuer.example"}}}, nil
}
func (f *gatewayTestAPI) GatewayAdmin(_ context.Context, _ string, body json.RawMessage) (json.RawMessage, error) {
	f.adminCalls++
	if len(body) > 0 {
		return json.RawMessage(`{"ok":true,"token":"one-time-example"}`), nil
	}
	return json.RawMessage(`{"enabled":false,"connections":[{"id":"first","name":"AI agent"},{"id":"second","name":"Automation"}]}`), nil
}
func newGatewayTestApp(t *testing.T, level int) (*App, *gatewayTestAPI) {
	t.Helper()
	f := &gatewayTestAPI{filesTestAPI: filesTestAPI{role: "administrator", username: "voxel"}, level: level}
	a, err := New(f, Config{ExternalScheme: "https"})
	if err != nil {
		t.Fatal(err)
	}
	return a, f
}
func externalGatewayRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer external-test-credential")
	r.Header.Set("Content-Type", "application/json")
	return r
}
func TestGatewayRESTAuthorizationAndOpenAPI(t *testing.T) {
	for level := 1; level <= 4; level++ {
		a, f := newGatewayTestApp(t, level)
		for _, op := range gatewayTestOperations() {
			path := strings.ReplaceAll(op.Path, "{id}", "jv-abcdef")
			rr := httptest.NewRecorder()
			a.Handler().ServeHTTP(rr, externalGatewayRequest(op.Method, path, `{}`))
			want := 200
			if level < op.MinLevel {
				want = 403
			}
			if rr.Code != want {
				t.Fatalf("level %d %s: %d %s", level, op.Name, rr.Code, rr.Body.String())
			}
		}
		rr := httptest.NewRecorder()
		a.Handler().ServeHTTP(rr, externalGatewayRequest("GET", "/api/v1/openapi.json", ""))
		var document map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &document); err != nil {
			t.Fatal(err)
		}
		if rr.Code != 200 || document["openapi"] != "3.1.2" {
			t.Fatal("OpenAPI discovery missing")
		}
		paths := document["paths"].(map[string]any)
		for _, op := range gatewayTestOperations() {
			_, visible := paths[op.Path]
			if visible != (level >= op.MinLevel) {
				t.Fatal("OpenAPI/catalog authorization drift")
			}
		}
		f.disabled = true
		rr = httptest.NewRecorder()
		a.Handler().ServeHTTP(rr, externalGatewayRequest("GET", "/api/v1/status", ""))
		if rr.Code != 401 {
			t.Fatal("gateway disable did not deny next request")
		}
	}
}
func TestGatewayExternalCredentialBoundaryAndLimits(t *testing.T) {
	a, _ := newGatewayTestApp(t, 4)
	for _, tc := range []struct {
		method, path string
	}{{"POST", "/mcp"}, {"GET", "/api/v1/status"}, {"GET", "/api/v1/openapi.json"}} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
		rr := httptest.NewRecorder()
		a.Handler().ServeHTTP(rr, r)
		if rr.Code != 401 || !strings.Contains(rr.Header().Get("WWW-Authenticate"), "resource_metadata=") {
			t.Fatalf("browser cookie authorized external %s %s: %d", tc.method, tc.path, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, externalGatewayRequest("POST", "/api/v1/system/systemd", strings.Repeat(" ", gatewayMaxBody+1)))
	if rr.Code != 413 {
		t.Fatal("unbounded external body")
	}
	for _, path := range []string{"/api/v1/shell", "/api/v1/admin/gateway", "/api/v1/reset/factory"} {
		rr = httptest.NewRecorder()
		a.Handler().ServeHTTP(rr, externalGatewayRequest("POST", path, `{}`))
		if rr.Code != 403 {
			t.Fatal("uncurated REST route exposed")
		}
	}
}
func mcpRequest(a *App, method string, params map[string]any) *httptest.ResponseRecorder {
	requestParams := make(map[string]any, len(params)+1)
	for key, value := range params {
		requestParams[key] = value
	}
	requestParams["_meta"] = map[string]any{
		"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
	}
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": requestParams})
	r := externalGatewayRequest("POST", "/mcp", string(payload))
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", "2026-07-28")
	r.Header.Set("Mcp-Method", method)
	if method == "tools/call" {
		if name, ok := params["name"].(string); ok && name != "" {
			r.Header.Set("Mcp-Name", name)
		}
	}
	rr := httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, r)
	return rr
}
func TestGatewayMCPToolVisibilityAndStatelessAuthentication(t *testing.T) {
	a, f := newGatewayTestApp(t, 1)
	for level := 1; level <= 4; level++ {
		f.level = level
		rr := mcpRequest(a, "tools/list", map[string]any{})
		if rr.Code != 200 || rr.Header().Get("Mcp-Session-Id") != "" {
			t.Fatalf("stateless SDK response %d %s", rr.Code, rr.Body.String())
		}
		var result struct {
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		visible := map[string]bool{}
		for _, tool := range result.Result.Tools {
			visible[tool.Name] = true
		}
		for _, op := range gatewayTestOperations() {
			if visible[op.Name] != (level >= op.MinLevel) {
				t.Fatalf("MCP %s visibility at level %d: %s", op.Name, level, rr.Body.String())
			}
		}
	}
	// A client cached systemd at Level 4, but the next request observes Level 1.
	f.level = 1
	before := f.executions
	rr := mcpRequest(a, "tools/call", map[string]any{"name": "systemd", "arguments": map[string]any{"body": map[string]any{"action": "daemon-reload"}}})
	if f.executions != before || !strings.Contains(strings.ToLower(rr.Body.String()), "error") {
		t.Fatal("cached tool bypassed downgrade")
	}
	// A downgrade between listing and execution is also rejected by the Agent.
	f.level = 4
	f.downgradeAtExecute = true
	rr = mcpRequest(a, "tools/call", map[string]any{"name": "systemd", "arguments": map[string]any{"body": map[string]any{"action": "daemon-reload"}}})
	if f.executions != before || !strings.Contains(rr.Body.String(), "isError") {
		t.Fatal("execution did not reauthorize")
	}
	f.disabled = true
	rr = mcpRequest(a, "tools/list", map[string]any{})
	if rr.Code != 401 {
		t.Fatal("MCP reused stale authentication")
	}
}
func TestGatewayProtectedResourceMetadata(t *testing.T) {
	a, _ := newGatewayTestApp(t, 1)
	r := httptest.NewRequest("GET", "https://voxel.example/.well-known/oauth-protected-resource/mcp", nil)
	rr := httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, r)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "https://issuer.example") || !strings.Contains(rr.Body.String(), "justvoxel:connect") || rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("resource metadata %d %s", rr.Code, rr.Body.String())
	}
}
func TestGatewayReverseProxyProtectedResourceMetadata(t *testing.T) {
	f := &gatewayTestAPI{level: 1}
	a, err := New(f, Config{ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}
	for _, proto := range []string{"https", " \tHtTpS \t", " https , http", "javascript", "http, https"} {
		t.Run(proto, func(t *testing.T) {
			scheme := "https"
			if proto == "javascript" || proto == "http, https" {
				scheme = "http"
			}
			request := func(method, path string) *http.Request {
				r := httptest.NewRequest(method, "http://voxel.example"+path, nil)
				r.Header.Set("X-Forwarded-Proto", proto)
				r.Header.Set("X-Forwarded-Host", "untrusted.example")
				if r.TLS != nil || r.Host != "voxel.example" {
					t.Fatal("expected internal HTTP request with public Host")
				}
				return r
			}
			rr := httptest.NewRecorder()
			a.Handler().ServeHTTP(rr, request("GET", "/.well-known/oauth-protected-resource/mcp"))
			var metadata struct {
				Resource             string   `json:"resource"`
				AuthorizationServers []string `json:"authorization_servers"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &metadata); err != nil {
				t.Fatal(err)
			}
			if rr.Code != 200 || metadata.Resource != scheme+"://voxel.example/mcp" {
				t.Fatalf("resource metadata %d %s", rr.Code, rr.Body.String())
			}
			if scheme == "https" && (len(metadata.AuthorizationServers) != 1 || metadata.AuthorizationServers[0] != "https://issuer.example") {
				t.Fatalf("HTTPS audience issuer missing: %s", rr.Body.String())
			}
			rr = httptest.NewRecorder()
			a.Handler().ServeHTTP(rr, request("POST", "/mcp"))
			want := `resource_metadata="` + scheme + `://voxel.example/.well-known/oauth-protected-resource/mcp"`
			if rr.Code != 401 || !strings.Contains(rr.Header().Get("WWW-Authenticate"), want) {
				t.Fatalf("gateway challenge %d %q", rr.Code, rr.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func TestGatewayBrowserAdministrationIsPrimaryOnlyAndCSRFProtected(t *testing.T) {
	for _, tc := range []struct {
		role, name string
		allowed    bool
	}{{"viewer", "Viewer", false}, {"operator", "Operator", false}, {"administrator", "other", false}, {"administrator", "voxel", true}} {
		a, f := newGatewayTestApp(t, 4)
		f.role = tc.role
		f.username = tc.name
		r := authenticatedSystemActionRequest("GET", "/api/agent-gateway", "")
		rr := httptest.NewRecorder()
		a.Handler().ServeHTTP(rr, r)
		if (rr.Code == 200) != tc.allowed {
			t.Fatalf("primary boundary %s/%s: %d", tc.role, tc.name, rr.Code)
		}
		if !tc.allowed && f.adminCalls != 0 {
			t.Fatal("non-primary reached privileged gateway admin")
		}
	}
	a, f := newGatewayTestApp(t, 4)
	r := externalGatewayRequest("POST", "/api/agent-gateway", `{"action":"enable","enabled":true}`)
	rr := httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, r)
	if rr.Code == 200 || f.adminCalls != 0 {
		t.Fatal("external bearer authenticated browser administration")
	}
	r = authenticatedSystemActionRequest("POST", "/api/agent-gateway", "request=%7B%7D")
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, r)
	if rr.Code != 403 || f.adminCalls != 0 {
		t.Fatal("CSRF bypass")
	}
	body := url.Values{"csrf": {"token"}, "request": {`{"action":"create","connection":{"name":"automation"}}`}}.Encode()
	r = authenticatedSystemActionRequest("POST", "/api/agent-gateway", body)
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, r)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "one-time-example") || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("one-time response %d %s", rr.Code, rr.Body.String())
	}
	r = authenticatedSystemActionRequest("GET", "/api/agent-gateway", "")
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, r)
	if strings.Contains(rr.Body.String(), "one-time-example") || !strings.Contains(rr.Body.String(), "second") {
		t.Fatal("token redisplay or missing independent connections")
	}
}
