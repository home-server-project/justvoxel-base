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

const testOAuthRequest = "jvoar_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
const testOAuthConnection = "jvc_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
const testOAuthResource = "https://voxel.example/mcp/" + testOAuthConnection

type localOAuthTestAPI struct {
	*gatewayTestAPI
	action        string
	input         map[string]any
	boundResource string
	source        string
	decisionCalls int
}

func newLocalOAuthApp(t *testing.T) (*App, *localOAuthTestAPI) {
	t.Helper()
	f := &localOAuthTestAPI{gatewayTestAPI: &gatewayTestAPI{filesTestAPI: filesTestAPI{role: "administrator", username: "voxel"}, level: 2}, source: "system"}
	a, err := New(f, Config{ExternalScheme: "https", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	return a, f
}
func (f *localOAuthTestAPI) Session(ctx context.Context, token string) (api.SessionInfo, error) {
	identity, err := f.filesTestAPI.Session(ctx, token)
	identity.AuthSource = f.source
	return identity, err
}
func (f *localOAuthTestAPI) GatewayResourceCatalog(ctx context.Context, token, resource string) (api.GatewayCatalog, error) {
	f.boundResource = resource
	if resource != testOAuthResource {
		return api.GatewayCatalog{}, api.ErrUnauthorized
	}
	return f.GatewayCatalog(ctx, token)
}
func (f *localOAuthTestAPI) GatewayResourceExecute(ctx context.Context, token, resource string, call api.GatewayCall) (api.GatewayResult, error) {
	f.boundResource = resource
	if resource != testOAuthResource {
		return api.GatewayResult{}, api.ErrUnauthorized
	}
	return f.GatewayExecute(ctx, token, call)
}
func (f *localOAuthTestAPI) GatewayOAuth(_ context.Context, session string, input any) (json.RawMessage, error) {
	raw, _ := json.Marshal(input)
	_ = json.Unmarshal(raw, &f.input)
	f.action, _ = f.input["action"].(string)
	switch f.action {
	case "register":
		return json.RawMessage(`{"client_id":"public-client","token_endpoint_auth_method":"none"}`), nil
	case "resource":
		if f.input["parameters"].(map[string]any)["resource"] != testOAuthResource {
			return nil, api.ErrUnauthorized
		}
		return json.RawMessage(`{"valid":true}`), nil
	case "begin":
		return json.RawMessage(`{"request_id":"` + testOAuthRequest + `"}`), nil
	case "inspect":
		if session != "session-token" {
			return nil, api.ErrUnauthorized
		}
		return json.RawMessage(`{"request_id":"` + testOAuthRequest + `","client_name":"<untrusted app>","redirect_uri":"https://app.example/callback","connection":{"name":"Home Server AI","purpose":"OpenAI","level":2,"all_instances":true}}`), nil
	case "decide":
		f.decisionCalls++
		if session != "session-token" {
			return nil, api.ErrUnauthorized
		}
		if f.input["allow"] == true {
			return json.RawMessage(`{"redirect":"https://app.example/callback?code=example&state=unchanged&iss=https%3A%2F%2Fvoxel.example"}`), nil
		}
		return json.RawMessage(`{"redirect":"https://app.example/callback?error=access_denied&state=unchanged&iss=https%3A%2F%2Fvoxel.example"}`), nil
	case "token":
		return json.RawMessage(`{"access_token":"opaque-test","token_type":"Bearer","expires_in":3600}`), nil
	}
	return nil, api.ErrUnauthorized
}
func localOAuthWebRequest(method, path, body string, authenticated bool) *http.Request {
	r := httptest.NewRequest(method, "https://voxel.example"+path, strings.NewReader(body))
	if method == "POST" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if authenticated {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
		r.AddCookie(&http.Cookie{Name: csrfCookie, Value: "csrf-value"})
	}
	return r
}
func TestLocalOAuthDiscoveryAndConnectionChallenge(t *testing.T) {
	a, f := newLocalOAuthApp(t)
	rr := httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, localOAuthWebRequest("POST", "/mcp/"+testOAuthConnection, "", true))
	if rr.Code != 401 || !strings.Contains(rr.Header().Get("WWW-Authenticate"), `resource_metadata="https://voxel.example/.well-known/oauth-protected-resource/mcp/`+testOAuthConnection+`"`) {
		t.Fatal("missing connection-specific challenge", rr.Code, rr.Header())
	}
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, localOAuthWebRequest("GET", "/.well-known/oauth-protected-resource/mcp/"+testOAuthConnection, "", false))
	var resource struct {
		Resource string   `json:"resource"`
		Servers  []string `json:"authorization_servers"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resource)
	if rr.Code != 200 || resource.Resource != testOAuthResource || len(resource.Servers) != 1 || resource.Servers[0] != "https://voxel.example" {
		t.Fatal("wrong protected resource", rr.Body.String())
	}
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, localOAuthWebRequest("GET", "/.well-known/oauth-authorization-server", "", false))
	var metadata map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &metadata)
	if rr.Code != 200 || metadata["issuer"] != "https://voxel.example" || metadata["authorization_response_iss_parameter_supported"] != true {
		t.Fatal("missing authorization metadata")
	}
	for _, endpoint := range []string{"authorization_endpoint", "token_endpoint", "registration_endpoint"} {
		if !strings.HasPrefix(metadata[endpoint].(string), "https://voxel.example/oauth/") {
			t.Fatal("wrong endpoint")
		}
	}
	if metadata["jwks_uri"] != nil || metadata["client_id_metadata_document_supported"] != nil {
		t.Fatal("unsupported discovery advertised")
	}
	r := localOAuthWebRequest("POST", "/mcp/"+testOAuthConnection, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`, false)
	r.Header.Set("Authorization", "Bearer external-test-credential")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, r)
	if f.boundResource != testOAuthResource {
		t.Fatal("MCP omitted exact resource binding")
	}
}
func TestLocalOAuthHTTPSAndTrustedOrigin(t *testing.T) {
	a, _ := newLocalOAuthApp(t)
	a.config.ExternalScheme = "http"
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/oauth/authorize", "/.well-known/oauth-protected-resource/mcp/" + testOAuthConnection} {
		rr := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "http://voxel.example"+path, nil)
		r.Header.Set("X-Forwarded-Host", "attacker.example")
		a.Handler().ServeHTTP(rr, r)
		if rr.Code != 400 {
			t.Fatal("HTTP OAuth accepted", rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://voxel.example/.well-known/oauth-authorization-server", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "attacker.example")
	a.Handler().ServeHTTP(rr, r)
	if rr.Code != 200 || strings.Contains(rr.Body.String(), "attacker.example") {
		t.Fatal("untrusted host used")
	}
}
func TestLocalOAuthDCRAndTokenProxy(t *testing.T) {
	a, f := newLocalOAuthApp(t)
	r := localOAuthWebRequest("POST", "/oauth/register", `{"client_name":"App","redirect_uris":["https://app.example/callback"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"],"response_types":["code"]}`, false)
	r.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, r)
	if rr.Code != 201 || f.action != "register" || strings.Contains(rr.Body.String(), "secret") {
		t.Fatal("public registration proxy failed", rr.Body.String())
	}
	for _, body := range []string{`{"client_secret":"forbidden"}`, strings.Repeat("x", oauthMaxBody+1)} {
		r = localOAuthWebRequest("POST", "/oauth/register", body, false)
		rr = httptest.NewRecorder()
		a.Handler().ServeHTTP(rr, r)
		if rr.Code != 400 {
			t.Fatal("unbounded or secret registration accepted")
		}
	}
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, localOAuthWebRequest("POST", "/oauth/token", "grant_type=refresh_token&client_id=client&refresh_token=opaque&resource="+url.QueryEscape(testOAuthResource), false))
	if rr.Code != 200 || f.action != "token" {
		t.Fatal("token request not delegated")
	}
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, localOAuthWebRequest("POST", "/oauth/token", "grant_type=refresh_token&client_id=a&client_id=b", false))
	if rr.Code != 400 {
		t.Fatal("duplicate parameters accepted")
	}
}
func TestLocalOAuthNormalLoginContinuation(t *testing.T) {
	a, _ := newLocalOAuthApp(t)
	rr := httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, localOAuthWebRequest("GET", "/oauth/authorize?response_type=code&client_id=app", "", false))
	if rr.Code != 303 || rr.Header().Get("Location") != "/login?oauth_request="+testOAuthRequest {
		t.Fatal("authorization did not use normal login", rr.Header())
	}
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, localOAuthWebRequest("GET", "/login?oauth_request="+testOAuthRequest, "", false))
	if !strings.Contains(rr.Body.String(), `name="oauth_request" value="`+testOAuthRequest+`"`) {
		t.Fatal("opaque request not carried")
	}
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, localOAuthWebRequest("POST", "/login", "username=admin&password=secret&oauth_request="+testOAuthRequest, false))
	if rr.Header().Get("Location") != "/oauth/authorize?request="+testOAuthRequest {
		t.Fatal("normal login lost authorization continuation")
	}
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, localOAuthWebRequest("POST", "/login", "username=admin&password=secret&oauth_request=https%3A%2F%2Fevil.example", false))
	if rr.Header().Get("Location") != "/" {
		t.Fatal("external login continuation accepted")
	}
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, localOAuthWebRequest("GET", "/oauth/authorize?client_id=a&client_id=b&redirect_uri=https://evil.example", "", false))
	if rr.Code != 400 || rr.Header().Get("Location") != "" {
		t.Fatal("ambiguous authorization redirected")
	}
}
func TestLocalOAuthPrimaryConsentAndResponses(t *testing.T) {
	a, f := newLocalOAuthApp(t)
	rr := httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, localOAuthWebRequest("GET", "/oauth/authorize?request="+testOAuthRequest, "", true))
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	for _, text := range []string{"OpenAI wants to connect", "Home Server AI", "Level 2 — Basic Management", "All servers", "&lt;untrusted app&gt;"} {
		if !strings.Contains(rr.Body.String(), text) {
			t.Fatal("missing authoritative consent value", text)
		}
	}
	if !strings.Contains(rr.Header().Get("Content-Security-Policy"), "form-action 'self' https://app.example") {
		t.Fatal("CSP blocks validated OAuth callback")
	}
	for _, role := range []string{"viewer", "operator", "administrator"} {
		f.role = role
		f.username = "another-user"
		rr = httptest.NewRecorder()
		a.Handler().ServeHTTP(rr, localOAuthWebRequest("GET", "/oauth/authorize?request="+testOAuthRequest, "", true))
		if rr.Code != 403 {
			t.Fatal("nonprimary consent accepted")
		}
	}
	f.role = "administrator"
	f.username = "voxel"
	f.source = "external"
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, localOAuthWebRequest("GET", "/oauth/authorize?request="+testOAuthRequest, "", true))
	if rr.Code != 403 {
		t.Fatal("external self-approval accepted")
	}
	f.source = "system"
	for _, decision := range []string{"allow", "cancel"} {
		rr = httptest.NewRecorder()
		a.Handler().ServeHTTP(rr, localOAuthWebRequest("POST", "/oauth/authorize", "csrf=csrf-value&request="+testOAuthRequest+"&decision="+decision, true))
		u, _ := url.Parse(rr.Header().Get("Location"))
		if rr.Code != 303 || u.Host != "app.example" || u.Query().Get("iss") != "https://voxel.example" || u.Query().Get("state") != "unchanged" {
			t.Fatal("invalid authorization response")
		}
		if decision == "cancel" && u.Query().Get("error") != "access_denied" {
			t.Fatal("cancel missing error")
		}
		if decision == "allow" && u.Query().Get("code") == "" {
			t.Fatal("allow missing code")
		}
	}
	before := f.decisionCalls
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, localOAuthWebRequest("POST", "/oauth/authorize", "csrf=wrong&request="+testOAuthRequest+"&decision=allow", true))
	if rr.Code != 403 || f.decisionCalls != before {
		t.Fatal("approval bypassed CSRF")
	}
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, localOAuthWebRequest("POST", "/oauth/authorize", "csrf=csrf-value&request="+testOAuthRequest+"&decision=allow&level=4", true))
	if rr.Code != 400 || f.decisionCalls != before {
		t.Fatal("browser permissions accepted")
	}
}

func TestLocalOAuthProxyCSRFAndPasswordContinuation(t *testing.T) {
	a, _ := newLocalOAuthApp(t)
	a.config.ExternalScheme = "http"
	r := localOAuthWebRequest("POST", "/oauth/authorize", "csrf=csrf-value&request="+testOAuthRequest+"&decision=allow", true)
	r.Header.Set("Origin", "https://voxel.example")
	rr := httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, r)
	if rr.Code != 303 {
		t.Fatal("trusted HTTPS approval rejected by CSRF", rr.Code, rr.Body.String())
	}
	r = localOAuthWebRequest("POST", "/oauth/authorize", "csrf=csrf-value&request="+testOAuthRequest+"&decision=allow", true)
	r.Header.Set("Origin", "https://evil.example")
	rr = httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, r)
	if rr.Code != 403 {
		t.Fatal("foreign origin approved")
	}
	fake := newAuthFlowAPI()
	fake.fakeAPI.mustChange = true
	passwordApp, err := New(fake, Config{ExternalScheme: "http", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	passwordApp.Handler().ServeHTTP(rr, localOAuthWebRequest("POST", "/login", "username=admin&password=secret&oauth_request="+testOAuthRequest, false))
	if rr.Header().Get("Location") != "/password?oauth_request="+testOAuthRequest {
		t.Fatal("password-required login lost request")
	}
	rr = httptest.NewRecorder()
	passwordApp.Handler().ServeHTTP(rr, localOAuthWebRequest("GET", "/password?oauth_request="+testOAuthRequest, "", true))
	if !strings.Contains(rr.Body.String(), `name="oauth_request" value="`+testOAuthRequest+`"`) {
		t.Fatal("password form lost opaque request")
	}
	r = localOAuthWebRequest("POST", "/password", "csrf=csrf-value&current_password=secret&new_password=new-long-password&confirm_password=new-long-password&oauth_request="+testOAuthRequest, true)
	r.Header.Set("Origin", "https://voxel.example")
	rr = httptest.NewRecorder()
	passwordApp.Handler().ServeHTTP(rr, r)
	if rr.Header().Get("Location") != "/login?message=password-changed&oauth_request="+testOAuthRequest {
		t.Fatal("password change lost login continuation", rr.Code, rr.Header())
	}
}
