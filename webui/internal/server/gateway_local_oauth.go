package server

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const oauthMaxBody = 32 << 10

var oauthRequestPattern = regexp.MustCompile(`^jvoar_[A-Za-z0-9_-]{43}$`)
var oauthConnectionPattern = regexp.MustCompile(`^jvc_[A-Za-z0-9_-]{43}$`)

type gatewayOAuthAPI interface {
	GatewayOAuth(context.Context, string, any) (json.RawMessage, error)
	GatewayResourceCatalog(context.Context, string, string) (api.GatewayCatalog, error)
	GatewayResourceExecute(context.Context, string, string, api.GatewayCall) (api.GatewayResult, error)
}
type gatewayResourceClient struct {
	externalGatewayAPI
	oauth    gatewayOAuthAPI
	resource string
}

func (c gatewayResourceClient) GatewayCatalog(ctx context.Context, token string) (api.GatewayCatalog, error) {
	return c.oauth.GatewayResourceCatalog(ctx, token, c.resource)
}
func (c gatewayResourceClient) GatewayExecute(ctx context.Context, token string, call api.GatewayCall) (api.GatewayResult, error) {
	return c.oauth.GatewayResourceExecute(ctx, token, c.resource, call)
}

func oauthContinuation(r *http.Request) string {
	values := r.URL.Query()
	if r.Method == "POST" {
		values = r.PostForm
	}
	v := values["oauth_request"]
	if len(v) != 1 || !oauthRequestPattern.MatchString(v[0]) {
		return ""
	}
	return v[0]
}
func oauthContinuePath(path, id string) string {
	if !oauthRequestPattern.MatchString(id) {
		return path
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "oauth_request=" + url.QueryEscape(id)
}
func oauthResumePath(id string) string {
	if !oauthRequestPattern.MatchString(id) {
		return "/"
	}
	return "/oauth/authorize?request=" + url.QueryEscape(id)
}
func (a *App) oauthHTTPS(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(a.gatewayOrigin(r), "https://") {
		gatewayJSON(w, 400, map[string]string{"error": "invalid_request", "error_description": "JustVoxel OAuth requires HTTPS."})
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	return true
}

// Keep the established CSRF cookie and same-origin checks, using the same
// trusted HTTPS presentation as OAuth discovery for this OAuth-only request.
func (a *App) oauthValidCSRF(r *http.Request) bool {
	presented := *a
	presented.config.ExternalScheme = strings.SplitN(a.gatewayOrigin(r), "://", 2)[0]
	return presented.validCSRF(r)
}
func (a *App) oauthCall(r *http.Request, session, action string, extra map[string]any) (json.RawMessage, error) {
	input := map[string]any{"action": action, "origin": a.gatewayOrigin(r)}
	for k, v := range extra {
		input[k] = v
	}
	client, ok := a.api.(gatewayOAuthAPI)
	if !ok {
		return nil, api.ErrUnauthorized
	}
	return client.GatewayOAuth(r.Context(), session, input)
}
func (a *App) oauthMetadata(w http.ResponseWriter, r *http.Request) {
	if !a.oauthHTTPS(w, r) {
		return
	}
	origin := a.gatewayOrigin(r)
	gatewayJSON(w, 200, map[string]any{
		"issuer": origin, "authorization_endpoint": origin + "/oauth/authorize", "token_endpoint": origin + "/oauth/token", "registration_endpoint": origin + "/oauth/register",
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{"justvoxel:connect"}, "token_endpoint_auth_methods_supported": []string{"none"}, "authorization_response_iss_parameter_supported": true,
	})
}
func (a *App) oauthRegister(w http.ResponseWriter, r *http.Request) {
	if !a.oauthHTTPS(w, r) {
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		gatewayJSON(w, 400, map[string]string{"error": "invalid_client_metadata"})
		return
	}
	var registration struct {
		ClientName      string   `json:"client_name,omitempty"`
		RedirectURIs    []string `json:"redirect_uris"`
		AuthMethod      string   `json:"token_endpoint_auth_method"`
		GrantTypes      []string `json:"grant_types"`
		ResponseTypes   []string `json:"response_types"`
		ApplicationType string   `json:"application_type,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, oauthMaxBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&registration) != nil || decoder.Decode(new(any)) != io.EOF {
		gatewayJSON(w, 400, map[string]string{"error": "invalid_client_metadata"})
		return
	}
	// Preserve strict Agent validation and expose no client secret.
	out, err := a.oauthCall(r, "", "register", map[string]any{"client": registration})
	if err != nil {
		gatewayJSON(w, 400, map[string]string{"error": "invalid_client_metadata"})
		return
	}
	gatewayJSON(w, 201, out)
}
func oauthParameters(values url.Values) (map[string]string, bool) {
	out := map[string]string{}
	if len(values) > 12 {
		return nil, false
	}
	for k, v := range values {
		if len(k) > 64 || len(v) != 1 || len(v[0]) > 2048 {
			return nil, false
		}
		out[k] = v[0]
	}
	return out, true
}
func (a *App) oauthToken(w http.ResponseWriter, r *http.Request) {
	if !a.oauthHTTPS(w, r) {
		return
	}
	if r.Header.Get("Authorization") != "" || r.URL.RawQuery != "" {
		gatewayJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, oauthMaxBody)
	if r.ParseForm() != nil || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		gatewayJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	p, ok := oauthParameters(r.PostForm)
	if !ok {
		gatewayJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	if p["grant_type"] != "authorization_code" && p["grant_type"] != "refresh_token" {
		gatewayJSON(w, 400, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	out, err := a.oauthCall(r, "", "token", map[string]any{"parameters": p})
	if err != nil {
		gatewayJSON(w, 400, map[string]string{"error": "invalid_grant"})
		return
	}
	gatewayJSON(w, 200, out)
}
func (a *App) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	if !a.oauthHTTPS(w, r) {
		return
	}
	var requestID string
	if r.Method == "POST" {
		r.Body = http.MaxBytesReader(w, r.Body, oauthMaxBody)
		if r.ParseForm() != nil || r.URL.RawQuery != "" || !a.oauthValidCSRF(r) {
			http.Error(w, "Invalid approval request", 403)
			return
		}
		p, ok := oauthParameters(r.PostForm)
		if !ok || len(p) != 3 || (p["decision"] != "allow" && p["decision"] != "cancel") {
			http.Error(w, "Invalid approval request", 400)
			return
		}
		requestID = p["request"]
	} else {
		if len(r.URL.RawQuery) > 16384 {
			http.Error(w, "Invalid authorization request", 400)
			return
		}
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			http.Error(w, "Invalid authorization request", 400)
			return
		}
		p, ok := oauthParameters(values)
		if !ok {
			http.Error(w, "Invalid authorization request", 400)
			return
		}
		if id, exists := p["request"]; exists {
			if len(p) != 1 || !oauthRequestPattern.MatchString(id) {
				http.Error(w, "Invalid authorization request", 400)
				return
			}
			requestID = id
		} else {
			out, err := a.oauthCall(r, "", "begin", map[string]any{"parameters": p})
			if err != nil {
				http.Error(w, "Invalid authorization request", 400)
				return
			}
			var result struct {
				RequestID string `json:"request_id"`
			}
			if json.Unmarshal(out, &result) != nil || !oauthRequestPattern.MatchString(result.RequestID) {
				http.Error(w, "Authorization unavailable", 503)
				return
			}
			// Drop all original parameters before login or consent; only the opaque ID continues.
			requestID = result.RequestID
		}
	}
	if !oauthRequestPattern.MatchString(requestID) {
		http.Error(w, "Invalid authorization request", 400)
		return
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, oauthContinuePath("/login", requestID), http.StatusSeeOther)
		return
	}
	identityClient, ok := a.api.(interface {
		Session(context.Context, string) (api.SessionInfo, error)
	})
	if !ok {
		http.Error(w, "Authorization unavailable", 503)
		return
	}
	identity, err := identityClient.Session(r.Context(), session)
	if err != nil {
		a.clearSessionCookies(w)
		http.Redirect(w, r, oauthContinuePath("/login", requestID), http.StatusSeeOther)
		return
	}
	if identity.Username != "voxel" || identity.Role != "administrator" || (identity.AuthSource != "system" && identity.AuthSource != "separate") {
		http.Error(w, "Only the primary voxel administrator may approve this connection.", 403)
		return
	}
	if identity.MustChange || mustChange(r) {
		http.Redirect(w, r, oauthContinuePath("/password", requestID), http.StatusSeeOther)
		return
	}
	if r.Method == "POST" {
		out, err := a.oauthCall(r, session, "decide", map[string]any{"request_id": requestID, "allow": r.PostForm.Get("decision") == "allow"})
		if err != nil {
			http.Error(w, "Authorization request expired or unavailable.", 400)
			return
		}
		var result struct {
			Redirect string `json:"redirect"`
		}
		if json.Unmarshal(out, &result) != nil || result.Redirect == "" {
			http.Error(w, "Authorization unavailable", 503)
			return
		}
		http.Redirect(w, r, result.Redirect, http.StatusSeeOther)
		return
	}
	out, err := a.oauthCall(r, session, "inspect", map[string]any{"request_id": requestID})
	if err != nil {
		http.Error(w, "Authorization request expired or unavailable.", 400)
		return
	}
	var consent struct {
		RequestID   string `json:"request_id"`
		ClientName  string `json:"client_name"`
		RedirectURI string `json:"redirect_uri"`
		Connection  struct {
			Name         string   `json:"name"`
			Purpose      string   `json:"purpose"`
			Level        int      `json:"level"`
			AllInstances bool     `json:"all_instances"`
			InstanceIDs  []string `json:"instance_ids"`
		} `json:"connection"`
	}
	if json.Unmarshal(out, &consent) != nil {
		http.Error(w, "Authorization unavailable", 503)
		return
	}
	provider := consent.Connection.Purpose
	if provider == "" {
		provider = "An app"
	}
	level := map[int]string{1: "Level 1 — Read Only", 2: "Level 2 — Basic Management", 3: "Level 3 — Full Management", 4: "Level 4 — Full System Control"}[consent.Connection.Level]
	scope := "All servers"
	if !consent.Connection.AllInstances {
		scope = strings.Join(consent.Connection.InstanceIDs, ", ")
		if scope == "" {
			scope = "No servers selected"
		}
	}
	data := struct{ Provider, Name, Level, Scope, ClientName, RequestID, CSRF string }{provider, consent.Connection.Name, level, scope, consent.ClientName, requestID, csrfFromRequest(r)}
	// The consent form posts locally. Permit only its already-validated callback
	// origin through CSP as well, so browsers can follow the OAuth POST redirect.
	callback, parseErr := url.Parse(consent.RedirectURI)
	if parseErr != nil || callback.Host == "" {
		http.Error(w, "Authorization unavailable", 503)
		return
	}
	policy := w.Header().Get("Content-Security-Policy")
	w.Header().Set("Content-Security-Policy", strings.Replace(policy, "form-action 'self'", "form-action 'self' "+callback.Scheme+"://"+callback.Host, 1))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(w, "oauth-consent.html", data); err != nil {
		http.Error(w, "Authorization unavailable", 503)
	}
}
func (a *App) oauthResourceMetadata(w http.ResponseWriter, r *http.Request) {
	if !a.oauthHTTPS(w, r) {
		return
	}
	id := r.PathValue("connection")
	if !oauthConnectionPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	origin := a.gatewayOrigin(r)
	resource := origin + "/mcp/" + id
	if _, err := a.oauthCall(r, "", "resource", map[string]any{"parameters": map[string]string{"resource": resource}}); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == "OPTIONS" {
		w.WriteHeader(204)
		return
	}
	gatewayJSON(w, 200, map[string]any{"resource": resource, "authorization_servers": []string{origin}, "scopes_supported": []string{"justvoxel:connect"}, "bearer_methods_supported": []string{"header"}, "resource_name": "JustVoxel AI & API Connections"})
}
