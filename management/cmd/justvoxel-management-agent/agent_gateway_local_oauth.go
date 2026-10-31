package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const localOAuthScope = "justvoxel:connect"
const localOAuthBodyLimit = 32 << 10

var localOAuthHostname = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
var localOAuthID = regexp.MustCompile(`^jvc_[A-Za-z0-9_-]{43}$`)
var localOAuthPKCE = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
var localOAuthRequestID = regexp.MustCompile(`^jvoar_[A-Za-z0-9_-]{43}$`)

type localOAuthClient struct {
	ClientID                string   `json:"client_id,omitempty"`
	ClientName              string   `json:"client_name,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	ApplicationType         string   `json:"application_type,omitempty"`
}
type localOAuthAuthorization struct {
	ConnectionID  string `json:"connection_id"`
	ClientID      string `json:"client_id"`
	RedirectURI   string `json:"redirect_uri"`
	Resource      string `json:"resource"`
	Scope         string `json:"scope"`
	Challenge     string `json:"code_challenge"`
	State         string `json:"state"`
	StateSupplied bool   `json:"state_supplied"`
}
type localOAuthInput struct {
	Action     string            `json:"action"`
	Origin     string            `json:"origin"`
	Client     *localOAuthClient `json:"client,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
	RequestID  string            `json:"request_id,omitempty"`
	Allow      bool              `json:"allow"`
}
type localOAuthTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

func validateLocalOAuthClient(c localOAuthClient) error {
	if c.ClientID != "" || c.TokenEndpointAuthMethod != "none" || len(c.ClientName) > 100 || len(c.RedirectURIs) < 1 || len(c.RedirectURIs) > 10 || len(c.GrantTypes) < 1 || len(c.GrantTypes) > 2 || len(c.ResponseTypes) != 1 || c.ResponseTypes[0] != "code" || (c.ApplicationType != "" && c.ApplicationType != "web" && c.ApplicationType != "native") {
		return errGatewayCredential
	}
	for _, r := range c.ClientName {
		if unicode.IsControl(r) {
			return errGatewayCredential
		}
	}
	seen := map[string]bool{}
	for _, grant := range c.GrantTypes {
		if seen[grant] || (grant != "authorization_code" && grant != "refresh_token") {
			return errGatewayCredential
		}
		seen[grant] = true
	}
	if !seen["authorization_code"] {
		return errGatewayCredential
	}
	seen = map[string]bool{}
	for _, raw := range c.RedirectURIs {
		u, err := url.Parse(raw)
		if err != nil || len(raw) > 2048 || raw == "" || strings.Contains(raw, "#") || strings.ContainsAny(raw, "\\\r\n\t ") || u.Opaque != "" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || seen[raw] {
			return errGatewayCredential
		}
		if _, err := url.ParseQuery(u.RawQuery); err != nil {
			return errGatewayCredential
		}
		if strings.HasSuffix(u.Host, ":") {
			return errGatewayCredential
		}
		if net.ParseIP(u.Hostname()) == nil && !localOAuthHostname.MatchString(u.Hostname()) {
			return errGatewayCredential
		}
		if p := u.Port(); p != "" {
			n := 0
			for _, r := range p {
				if r < '0' || r > '9' {
					return errGatewayCredential
				}
				n = n*10 + int(r-'0')
				if n > 65535 {
					return errGatewayCredential
				}
			}
			if n == 0 {
				return errGatewayCredential
			}
		}
		if u.Scheme != "https" {
			host := u.Hostname()
			if u.Scheme != "http" || c.ApplicationType != "native" || (host != "localhost" && host != "127.0.0.1" && host != "::1") {
				return errGatewayCredential
			}
		}
		seen[raw] = true
	}
	return nil
}
func validLocalOAuthOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && gatewayHTTPS(origin) && u.Hostname() != "" && u.Path == "" && !strings.ContainsAny(origin, "\\\r\n\t ")
}
func localOAuthResourceConnection(origin, resource string) (string, error) {
	if !validLocalOAuthOrigin(origin) || !strings.HasPrefix(resource, origin+"/mcp/") {
		return "", errGatewayCredential
	}
	id := strings.TrimPrefix(resource, origin+"/mcp/")
	if !localOAuthID.MatchString(id) || resource != origin+"/mcp/"+id {
		return "", errGatewayCredential
	}
	return id, nil
}

type localOAuthQuery interface{ QueryRow(string, ...any) *sql.Row }

func (s *webUIStore) localOAuthConnection(q localOAuthQuery, id string) (gatewayConnection, error) {
	var c gatewayConnection
	var raw string
	var enabled bool
	if err := q.QueryRow(`SELECT c.record_json,s.enabled FROM gateway_connections c CROSS JOIN gateway_settings s WHERE c.id=? AND s.id=1`, id).Scan(&raw, &enabled); err != nil {
		return c, err
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	if !enabled || !c.Enabled || c.AuthMode != "oauth_local" || (c.ExpiresAt != nil && !s.now().Before(*c.ExpiresAt)) {
		return c, errGatewayCredential
	}
	return c, nil
}
func localOAuthLoadClient(q localOAuthQuery, id string) (localOAuthClient, error) {
	var c localOAuthClient
	var raw string
	if err := q.QueryRow(`SELECT record_json FROM gateway_oauth_clients WHERE id=?`, id).Scan(&raw); err != nil {
		return c, err
	}
	err := json.Unmarshal([]byte(raw), &c)
	return c, err
}
func (s *webUIStore) localOAuthRegister(c localOAuthClient) (localOAuthClient, error) {
	if err := validateLocalOAuthClient(c); err != nil {
		return c, err
	}
	id, err := gatewayRandom("jvocl_")
	if err != nil {
		return c, err
	}
	c.ClientID = id
	raw, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM gateway_oauth_clients`).Scan(&count); err != nil {
		return c, err
	}
	if count >= 4096 {
		return c, errGatewayCredential
	}
	_, err = s.db.Exec(`INSERT INTO gateway_oauth_clients(id,record_json) VALUES(?,?)`, id, string(raw))
	return c, err
}
func (s *webUIStore) localOAuthBegin(origin string, p map[string]string) (string, error) {
	allowed := map[string]bool{"response_type": true, "client_id": true, "redirect_uri": true, "code_challenge": true, "code_challenge_method": true, "resource": true, "scope": true, "state": true}
	for k, v := range p {
		if !allowed[k] || len(v) > 2048 {
			return "", errGatewayCredential
		}
	}
	challenge, err := base64.RawURLEncoding.DecodeString(p["code_challenge"])
	if err != nil || len(challenge) != sha256.Size || base64.RawURLEncoding.EncodeToString(challenge) != p["code_challenge"] || p["response_type"] != "code" || p["code_challenge_method"] != "S256" || p["scope"] != localOAuthScope {
		return "", errGatewayCredential
	}
	id, err := localOAuthResourceConnection(origin, p["resource"])
	if err != nil {
		return "", err
	}
	if _, err = s.localOAuthConnection(s.db, id); err != nil {
		return "", err
	}
	c, err := localOAuthLoadClient(s.db, p["client_id"])
	if err != nil {
		return "", err
	}
	found := false
	for _, u := range c.RedirectURIs {
		if u == p["redirect_uri"] {
			found = true
		}
	}
	if !found {
		return "", errGatewayCredential
	}
	_, hasState := p["state"]
	a := localOAuthAuthorization{ConnectionID: id, ClientID: c.ClientID, RedirectURI: p["redirect_uri"], Resource: p["resource"], Scope: localOAuthScope, Challenge: p["code_challenge"], State: p["state"], StateSupplied: hasState}
	raw, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	request, err := gatewayRandom("jvoar_")
	if err != nil {
		return "", err
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM gateway_oauth_requests`).Scan(&count); err != nil {
		return "", err
	}
	if count >= 8192 {
		return "", errGatewayCredential
	}
	_, err = s.db.Exec(`INSERT INTO gateway_oauth_requests(id,connection_id,client_id,record_json,expires) VALUES(?,?,?,?,?)`, request, id, c.ClientID, string(raw), s.now().Add(10*time.Minute).Unix())
	return request, err
}
func (s *webUIStore) localOAuthPending(q localOAuthQuery, origin, id string) (localOAuthAuthorization, gatewayConnection, localOAuthClient, error) {
	var a localOAuthAuthorization
	var c gatewayConnection
	var client localOAuthClient
	var raw string
	if !localOAuthRequestID.MatchString(id) {
		return a, c, client, errGatewayCredential
	}
	err := q.QueryRow(`SELECT record_json FROM gateway_oauth_requests WHERE id=? AND expires>?`, id, s.now().Unix()).Scan(&raw)
	if err != nil {
		return a, c, client, err
	}
	if err = json.Unmarshal([]byte(raw), &a); err != nil {
		return a, c, client, err
	}
	cid, err := localOAuthResourceConnection(origin, a.Resource)
	if err != nil || cid != a.ConnectionID {
		return a, c, client, errGatewayCredential
	}
	c, err = s.localOAuthConnection(q, cid)
	if err != nil {
		return a, c, client, err
	}
	client, err = localOAuthLoadClient(q, a.ClientID)
	return a, c, client, err
}
func (s *webUIStore) localOAuthDecide(origin, id string, allow bool) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	a, _, _, err := s.localOAuthPending(tx, origin, id)
	if err != nil {
		return "", err
	}
	result, err := tx.Exec(`DELETE FROM gateway_oauth_requests WHERE id=?`, id)
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return "", errGatewayCredential
	}
	target, err := url.Parse(a.RedirectURI)
	if err != nil {
		return "", err
	}
	values := target.Query()
	for _, key := range []string{"code", "state", "iss", "error", "error_description", "error_uri"} {
		values.Del(key)
	}
	if allow {
		code, e := gatewayRandom("jvac_")
		if e != nil {
			return "", e
		}
		raw, e := json.Marshal(a)
		if e != nil {
			return "", e
		}
		if _, err = tx.Exec(`INSERT INTO gateway_oauth_codes(digest,connection_id,client_id,record_json,expires) VALUES(?,?,?,?,?)`, gatewayDigest(code), a.ConnectionID, a.ClientID, string(raw), s.now().Add(5*time.Minute).Unix()); err != nil {
			return "", err
		}
		values.Set("code", code)
	} else {
		values.Set("error", "access_denied")
	}
	if a.StateSupplied {
		values.Set("state", a.State)
	}
	values.Set("iss", origin)
	target.RawQuery = values.Encode()
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return target.String(), nil
}
func (s *webUIStore) localOAuthExchange(origin string, p map[string]string) (localOAuthTokens, error) {
	empty := localOAuthTokens{}
	allowed := map[string]bool{"grant_type": true, "client_id": true, "code": true, "redirect_uri": true, "code_verifier": true, "resource": true, "refresh_token": true, "scope": true}
	for k, v := range p {
		if !allowed[k] || len(v) > 2048 {
			return empty, errGatewayCredential
		}
	}
	id, err := localOAuthResourceConnection(origin, p["resource"])
	if err != nil {
		return empty, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if _, err = s.localOAuthConnection(tx, id); err != nil {
		return empty, err
	}
	client, err := localOAuthLoadClient(tx, p["client_id"])
	if err != nil {
		return empty, err
	}
	now := s.now().Unix()
	var result sql.Result
	switch p["grant_type"] {
	case "authorization_code":
		if !strings.HasPrefix(p["code"], "jvac_") || !localOAuthPKCE.MatchString(p["code_verifier"]) {
			return empty, errGatewayCredential
		}
		var raw string
		if err = tx.QueryRow(`SELECT record_json FROM gateway_oauth_codes WHERE digest=? AND expires>?`, gatewayDigest(p["code"]), now).Scan(&raw); err != nil {
			return empty, err
		}
		var a localOAuthAuthorization
		if err = json.Unmarshal([]byte(raw), &a); err != nil {
			return empty, err
		}
		challenge := base64.RawURLEncoding.EncodeToString(gatewayDigest(p["code_verifier"]))
		if a.ClientID != client.ClientID || a.ConnectionID != id || a.Resource != p["resource"] || a.RedirectURI != p["redirect_uri"] || subtle.ConstantTimeCompare([]byte(challenge), []byte(a.Challenge)) != 1 {
			return empty, errGatewayCredential
		}
		result, err = tx.Exec(`DELETE FROM gateway_oauth_codes WHERE digest=? AND expires>?`, gatewayDigest(p["code"]), now)
	case "refresh_token":
		if !strings.HasPrefix(p["refresh_token"], "jvor_") || (p["scope"] != "" && p["scope"] != localOAuthScope) {
			return empty, errGatewayCredential
		}
		supportsRefresh := false
		for _, g := range client.GrantTypes {
			if g == "refresh_token" {
				supportsRefresh = true
			}
		}
		if !supportsRefresh {
			return empty, errGatewayCredential
		}
		result, err = tx.Exec(`DELETE FROM gateway_oauth_refresh WHERE digest=? AND connection_id=? AND client_id=? AND resource=? AND expires>?`, gatewayDigest(p["refresh_token"]), id, client.ClientID, p["resource"], now)
	default:
		return empty, errGatewayCredential
	}
	if err != nil {
		return empty, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return empty, errGatewayCredential
	}
	access, err := gatewayRandom("jvoa_")
	if err != nil {
		return empty, err
	}
	refresh, err := gatewayRandom("jvor_")
	if err != nil {
		return empty, err
	}
	if _, err = tx.Exec(`INSERT INTO gateway_oauth_access(digest,connection_id,client_id,resource,expires) VALUES(?,?,?,?,?)`, gatewayDigest(access), id, client.ClientID, p["resource"], now+3600); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(`INSERT INTO gateway_oauth_refresh(digest,connection_id,client_id,resource,expires) VALUES(?,?,?,?,?)`, gatewayDigest(refresh), id, client.ClientID, p["resource"], now+30*24*3600); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return localOAuthTokens{AccessToken: access, RefreshToken: refresh, TokenType: "Bearer", ExpiresIn: 3600, Scope: localOAuthScope}, nil
}

// Only the UID-restricted WebUI API can reach this endpoint. Browser approval
// additionally requires the existing authoritative primary-administrator session.
func (s *server) gatewayLocalOAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input localOAuthInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, localOAuthBodyLimit))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || !validLocalOAuthOrigin(input.Origin) {
		writeError(w, 400, "invalid OAuth request")
		return
	}
	s.gatewayMu.Lock()
	defer s.gatewayMu.Unlock()
	if s.operations != nil {
		current, err := s.operations.currentFactoryReset()
		if err != nil || (current != nil && operationIsCurrent(current.State)) {
			writeError(w, 503, "OAuth unavailable during Factory Reset")
			return
		}
	}
	if input.Action == "inspect" || input.Action == "decide" {
		actor, ok := s.requirePrimaryAdministrator(w, r)
		if !ok {
			return
		}
		if actor.AuthSource != authSourceSystem && actor.AuthSource != authSourceSeparate {
			writeError(w, 403, "primary human administrator session required")
			return
		}
	}
	// Bound the lifetime of transient state without retaining consumed credentials.
	for _, table := range []string{"gateway_oauth_requests", "gateway_oauth_codes", "gateway_oauth_access", "gateway_oauth_refresh"} {
		if _, err := s.store.db.Exec(`DELETE FROM `+table+` WHERE expires<=?`, s.store.now().Unix()); err != nil {
			writeError(w, 503, "OAuth unavailable")
			return
		}
	}
	var out any
	var err error
	switch input.Action {
	case "register":
		if input.Client == nil {
			err = errGatewayCredential
		} else {
			out, err = s.store.localOAuthRegister(*input.Client)
		}
	case "begin":
		var id string
		id, err = s.store.localOAuthBegin(input.Origin, input.Parameters)
		out = map[string]string{"request_id": id}
	case "resource":
		var id string
		id, err = localOAuthResourceConnection(input.Origin, input.Parameters["resource"])
		if err == nil {
			_, err = s.store.localOAuthConnection(s.store.db, id)
		}
		out = map[string]bool{"valid": err == nil}
	case "inspect":
		var a localOAuthAuthorization
		var c gatewayConnection
		var client localOAuthClient
		a, c, client, err = s.store.localOAuthPending(s.store.db, input.Origin, input.RequestID)
		out = map[string]any{"request_id": input.RequestID, "connection": c, "client_name": client.ClientName, "resource": a.Resource, "redirect_uri": a.RedirectURI}
	case "decide":
		var target string
		target, err = s.store.localOAuthDecide(input.Origin, input.RequestID, input.Allow)
		out = map[string]string{"redirect": target}
	case "token":
		out, err = s.store.localOAuthExchange(input.Origin, input.Parameters)
	default:
		err = errors.New("invalid OAuth operation")
	}
	if err != nil {
		writeError(w, 400, "invalid OAuth request or grant")
		return
	}
	writeJSON(w, 200, out)
}
