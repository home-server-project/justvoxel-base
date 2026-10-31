package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const localTestOrigin = "https://voxel.example"
const localTestRedirect = "https://app.example/callback"
const localTestVerifier = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"

type localOAuthFixture struct {
	store      *webUIStore
	connection gatewayConnection
	client     localOAuthClient
	resource   string
}

func newLocalOAuthFixture(t *testing.T) localOAuthFixture {
	t.Helper()
	store, _ := openTestWebUIStore(t)
	c, token, err := store.gatewaySave(gatewayConnection{Name: "Home Server AI", Purpose: "OpenAI", AuthMode: "oauth_local", Level: 2, Enabled: true, AllInstances: true}, true, false, gatewayTestActor)
	if err != nil || token != "" || c.Fingerprint != "" {
		t.Fatalf("local creation issued a credential or failed: %v", err)
	}
	if err = store.gatewaySetEnabled(true, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	client, err := store.localOAuthRegister(localOAuthClient{ClientName: "App-supplied name", RedirectURIs: []string{localTestRedirect}, TokenEndpointAuthMethod: "none", GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"}})
	if err != nil {
		t.Fatal(err)
	}
	return localOAuthFixture{store, c, client, localTestOrigin + "/mcp/" + c.ID}
}
func (f localOAuthFixture) parameters() map[string]string {
	return map[string]string{"response_type": "code", "client_id": f.client.ClientID, "redirect_uri": localTestRedirect, "code_challenge": base64.RawURLEncoding.EncodeToString(gatewayDigest(localTestVerifier)), "code_challenge_method": "S256", "resource": f.resource, "scope": localOAuthScope, "state": "unchanged + /?& state"}
}
func (f localOAuthFixture) request(t *testing.T) string {
	t.Helper()
	id, err := f.store.localOAuthBegin(localTestOrigin, f.parameters())
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func (f localOAuthFixture) code(t *testing.T) string {
	t.Helper()
	target, err := f.store.localOAuthDecide(localTestOrigin, f.request(t), true)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("iss") != localTestOrigin || u.Query().Get("state") != f.parameters()["state"] {
		t.Fatal("authorization response lost issuer or state")
	}
	return u.Query().Get("code")
}
func (f localOAuthFixture) exchangeParameters(code string) map[string]string {
	return map[string]string{"grant_type": "authorization_code", "client_id": f.client.ClientID, "code": code, "redirect_uri": localTestRedirect, "code_verifier": localTestVerifier, "resource": f.resource}
}
func (f localOAuthFixture) tokens(t *testing.T) localOAuthTokens {
	t.Helper()
	tokens, err := f.store.localOAuthExchange(localTestOrigin, f.exchangeParameters(f.code(t)))
	if err != nil {
		t.Fatal(err)
	}
	return tokens
}
func TestLocalOAuthMigration5PreservesConnectionsAndUsers(t *testing.T) {
	migrations := webUIStoreMigrations
	t.Cleanup(func() { webUIStoreMigrations = migrations })
	webUIStoreMigrations = migrations[:5]
	path := filepath.Join(t.TempDir(), "identity.db")
	old, err := openWebUIStore(path)
	if err != nil {
		t.Fatal(err)
	}
	user, err := old.createWebUser("Existing", webUserRoleViewer, "existing-private-password")
	if err != nil {
		t.Fatal(err)
	}
	c, token := createTestGatewayConnection(t, old, 2)
	if err = old.close(); err != nil {
		t.Fatal(err)
	}
	webUIStoreMigrations = migrations
	upgraded, err := openWebUIStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.close()
	users, err := upgraded.listWebUsers()
	if err != nil || len(users) != 1 || users[0].ID != user.ID {
		t.Fatal("migration altered browser users")
	}
	current, err := upgraded.gatewayAuthenticate(token)
	if err != nil || current.ID != c.ID {
		t.Fatal("migration altered legacy connection")
	}
	var version int
	if err = upgraded.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || version != 6 {
		t.Fatal("missing migration 6")
	}
}
func TestLocalOAuthConnectionValidation(t *testing.T) {
	c := gatewayConnection{Name: "AI", AuthMode: "oauth_local", Level: 1, AllInstances: true}
	if err := validateGatewayConnection(c); err != nil {
		t.Fatal(err)
	}
	c.OAuth = &gatewayOAuthBinding{}
	if validateGatewayConnection(c) == nil {
		t.Fatal("local mode accepted external issuer binding")
	}
}
func TestLocalOAuthPublicRegistrationRedirectValidation(t *testing.T) {
	f := newLocalOAuthFixture(t)
	raw, err := json.Marshal(f.client)
	if err != nil || bytes.Contains(raw, []byte("secret")) {
		t.Fatal("public registration contains a secret")
	}
	var stored string
	if err = f.store.db.QueryRow(`SELECT record_json FROM gateway_oauth_clients WHERE id=?`, f.client.ClientID).Scan(&stored); err != nil || strings.Contains(stored, "secret") {
		t.Fatal("stored client contains secret")
	}
	for _, tc := range []struct {
		uri, kind string
		valid     bool
	}{
		{"https://app.example/cb", "", true}, {"http://localhost:1234/cb", "native", true}, {"http://127.0.0.1/cb", "native", true}, {"http://[::1]:8910/cb", "native", true},
		{"http://localhost/cb", "web", false}, {"http://remote.example/cb", "native", false}, {"https://user@app.example/cb", "", false}, {"https://app.example/cb#fragment", "", false}, {"https://app.example/cb#", "", false}, {"https://app.example:999999/cb", "", false}, {"https://app.example:bad/cb", "", false}, {"//app.example/cb", "", false}, {"https:///cb", "", false}, {"https://app.example/%xx", "", false}, {"https://app.example/" + strings.Repeat("x", 2048), "", false},
	} {
		t.Run(tc.uri[:min(60, len(tc.uri))], func(t *testing.T) {
			c := f.client
			c.ClientID = ""
			c.RedirectURIs = []string{tc.uri}
			c.ApplicationType = tc.kind
			if (validateLocalOAuthClient(c) == nil) != tc.valid {
				t.Fatal("redirect validation mismatch")
			}
		})
	}
	for _, change := range []func(*localOAuthClient){
		func(c *localOAuthClient) { c.TokenEndpointAuthMethod = "client_secret_basic" }, func(c *localOAuthClient) { c.GrantTypes = []string{"client_credentials"} }, func(c *localOAuthClient) { c.ResponseTypes = []string{"token"} }, func(c *localOAuthClient) { c.RedirectURIs = make([]string, 11) }, func(c *localOAuthClient) { c.ClientName = strings.Repeat("x", 101) },
	} {
		c := f.client
		c.ClientID = ""
		change(&c)
		if validateLocalOAuthClient(c) == nil {
			t.Fatal("unsupported client metadata accepted")
		}
	}
}
func TestLocalOAuthAuthorizationValidation(t *testing.T) {
	f := newLocalOAuthFixture(t)
	for _, change := range []func(map[string]string){
		func(p map[string]string) { p["response_type"] = "token" }, func(p map[string]string) { p["client_id"] = "unknown" }, func(p map[string]string) { p["redirect_uri"] = "https://attacker.example/cb" }, func(p map[string]string) { p["code_challenge_method"] = "plain" }, func(p map[string]string) { p["code_challenge"] = "wrong" }, func(p map[string]string) { p["scope"] = "justvoxel:connect admin" }, func(p map[string]string) { p["resource"] = localTestOrigin + "/mcp" }, func(p map[string]string) { p["resource"] += "/extra" }, func(p map[string]string) { p["resource"] = "https://other.example/mcp/" + f.connection.ID }, func(p map[string]string) { p["state"] = strings.Repeat("x", 2049) }, func(p map[string]string) { p["unexpected"] = "value" },
	} {
		p := f.parameters()
		change(p)
		if _, err := f.store.localOAuthBegin(localTestOrigin, p); err == nil {
			t.Fatal("invalid authorization request accepted")
		}
	}
	if _, err := f.store.localOAuthBegin("http://voxel.example", f.parameters()); err == nil {
		t.Fatal("HTTP issuer accepted")
	}
	id, err := localOAuthResourceConnection(localTestOrigin, f.resource)
	if err != nil || id != f.connection.ID {
		t.Fatal("incorrect resource identity")
	}
	pending := f.request(t)
	if !localOAuthRequestID.MatchString(pending) {
		t.Fatal("invalid opaque request ID")
	}
	_, c, client, err := f.store.localOAuthPending(f.store.db, localTestOrigin, pending)
	if err != nil || c.Name != "Home Server AI" || client.ClientID != f.client.ClientID {
		t.Fatal("consent not authoritative")
	}
	target, err := f.store.localOAuthDecide(localTestOrigin, pending, false)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(target)
	if u.Query().Get("error") != "access_denied" || u.Query().Get("iss") != localTestOrigin || u.Query().Get("state") != f.parameters()["state"] {
		t.Fatal("cancel response lost validated binding")
	}
	if _, err = f.store.localOAuthDecide(localTestOrigin, pending, true); err == nil {
		t.Fatal("request reused after cancel")
	}
}
func TestLocalOAuthCodeBindingsExpiryAndOneTime(t *testing.T) {
	for _, field := range []string{"client_id", "redirect_uri", "resource", "code_verifier"} {
		t.Run(field, func(t *testing.T) {
			f := newLocalOAuthFixture(t)
			code := f.code(t)
			p := f.exchangeParameters(code)
			p[field] += "wrong"
			if _, err := f.store.localOAuthExchange(localTestOrigin, p); err == nil {
				t.Fatal("wrong code binding accepted")
			}
			p = f.exchangeParameters(code)
			if _, err := f.store.localOAuthExchange(localTestOrigin, p); err != nil {
				t.Fatal("failed exchange consumed valid code", err)
			}
			if _, err := f.store.localOAuthExchange(localTestOrigin, p); err == nil {
				t.Fatal("code replay accepted")
			}
		})
	}
	f := newLocalOAuthFixture(t)
	code := f.code(t)
	clock := f.store.now()
	f.store.now = func() time.Time { return clock.Add(5 * time.Minute) }
	if _, err := f.store.localOAuthExchange(localTestOrigin, f.exchangeParameters(code)); err == nil {
		t.Fatal("expired code accepted")
	}
	f = newLocalOAuthFixture(t)
	request := f.request(t)
	clock = f.store.now()
	f.store.now = func() time.Time { return clock.Add(10 * time.Minute) }
	if _, err := f.store.localOAuthDecide(localTestOrigin, request, true); err == nil {
		t.Fatal("expired request accepted")
	}
}
func TestLocalOAuthDigestsAndRefreshRotation(t *testing.T) {
	f := newLocalOAuthFixture(t)
	code := f.code(t)
	var stored []byte
	if err := f.store.db.QueryRow(`SELECT digest FROM gateway_oauth_codes`).Scan(&stored); err != nil || !bytes.Equal(stored, gatewayDigest(code)) || bytes.Contains(stored, []byte(code)) {
		t.Fatal("code not digest-only")
	}
	tokens, err := f.store.localOAuthExchange(localTestOrigin, f.exchangeParameters(code))
	if err != nil {
		t.Fatal(err)
	}
	if tokens.ExpiresIn != 3600 || tokens.Scope != localOAuthScope || tokens.TokenType != "Bearer" {
		t.Fatal("incorrect token response")
	}
	for _, item := range []struct{ table, token string }{{"gateway_oauth_access", tokens.AccessToken}, {"gateway_oauth_refresh", tokens.RefreshToken}} {
		if err = f.store.db.QueryRow(`SELECT digest FROM ` + item.table).Scan(&stored); err != nil || len(stored) != 32 || !bytes.Equal(stored, gatewayDigest(item.token)) || bytes.Contains(stored, []byte(item.token)) {
			t.Fatal("plaintext token at rest")
		}
	}
	p := map[string]string{"grant_type": "refresh_token", "client_id": f.client.ClientID, "resource": f.resource, "refresh_token": tokens.RefreshToken}
	wrong := map[string]string{}
	for k, v := range p {
		wrong[k] = v
	}
	wrong["client_id"] = "wrong"
	if _, err = f.store.localOAuthExchange(localTestOrigin, wrong); err == nil {
		t.Fatal("wrong refresh client accepted")
	}
	wrong["client_id"] = f.client.ClientID
	wrong["resource"] += "wrong"
	if _, err = f.store.localOAuthExchange(localTestOrigin, wrong); err == nil {
		t.Fatal("wrong refresh resource accepted")
	}
	rotated, err := f.store.localOAuthExchange(localTestOrigin, p)
	if err != nil || rotated.RefreshToken == tokens.RefreshToken {
		t.Fatal("refresh rotation failed", err)
	}
	if _, err = f.store.localOAuthExchange(localTestOrigin, p); err == nil {
		t.Fatal("old refresh accepted")
	}
	clock := f.store.now()
	f.store.now = func() time.Time { return clock.Add(time.Hour) }
	if _, err = f.store.gatewayAuthenticate(tokens.AccessToken, f.resource); err == nil {
		t.Fatal("expired access accepted")
	}
	f.store.now = func() time.Time { return clock.Add(30 * 24 * time.Hour) }
	p["refresh_token"] = rotated.RefreshToken
	if _, err = f.store.localOAuthExchange(localTestOrigin, p); err == nil {
		t.Fatal("expired refresh accepted")
	}
}
func TestLocalOAuthLiveAuthorizationAndResourceIsolation(t *testing.T) {
	f := newLocalOAuthFixture(t)
	tokens := f.tokens(t)
	for _, resource := range []string{"", localTestOrigin + "/mcp", f.resource + "wrong"} {
		if _, err := f.store.gatewayAuthenticate(tokens.AccessToken, resource); err == nil {
			t.Fatal("wrong resource accepted")
		}
	}
	current, err := f.store.gatewayAuthenticate(tokens.AccessToken, f.resource)
	if err != nil || current.ID != f.connection.ID {
		t.Fatal("valid access denied", err)
	}
	current.Level = 1
	current.AllInstances = false
	current.InstanceIDs = []string{"jv-aaaaaa"}
	current, _, err = f.store.gatewaySave(current, false, false, gatewayTestActor)
	if err != nil {
		t.Fatal(err)
	}
	current, err = f.store.gatewayAuthenticate(tokens.AccessToken, f.resource)
	if err != nil || current.Level != 1 || current.AllInstances || !current.allowsInstance("jv-aaaaaa") || current.allowsInstance("jv-bbbbbb") {
		t.Fatal("permissions frozen in token")
	}
	current.Enabled = false
	if _, _, err = f.store.gatewaySave(current, false, false, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.gatewayAuthenticate(tokens.AccessToken, f.resource); err == nil {
		t.Fatal("disabled connection accepted")
	}
	p := map[string]string{"grant_type": "refresh_token", "client_id": f.client.ClientID, "resource": f.resource, "refresh_token": tokens.RefreshToken}
	if _, err = f.store.localOAuthExchange(localTestOrigin, p); err == nil {
		t.Fatal("disabled refresh accepted")
	}
	current.Enabled = true
	if _, _, err = f.store.gatewaySave(current, false, false, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	if err = f.store.gatewaySetEnabled(false, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.gatewayAuthenticate(tokens.AccessToken, f.resource); err == nil {
		t.Fatal("disabled gateway accepted")
	}
	if _, err = f.store.localOAuthExchange(localTestOrigin, p); err == nil {
		t.Fatal("disabled gateway refreshed")
	}
}
func TestLocalOAuthDeleteAndDurableFactoryRevocation(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(map[bool]string{false: "delete", true: "factory"}[reset], func(t *testing.T) {
			f := newLocalOAuthFixture(t)
			tokens := f.tokens(t)
			f.request(t)
			f.code(t)
			var err error
			if reset {
				err = f.store.gatewayFactoryRevoke()
			} else {
				_, err = f.store.gatewayCredentialAction(f.connection.ID, "delete", gatewayTestActor)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = f.store.gatewaySetEnabled(true, gatewayTestActor); err != nil {
				t.Fatal(err)
			}
			if _, err = f.store.gatewayAuthenticate(tokens.AccessToken, f.resource); err == nil {
				t.Fatal("revoked token resurrected")
			}
			for _, table := range []string{"gateway_oauth_requests", "gateway_oauth_codes", "gateway_oauth_access", "gateway_oauth_refresh"} {
				var count int
				if err = f.store.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
					t.Fatal("OAuth state survived revocation", table, err)
				}
			}
			if reset {
				var count int
				if err = f.store.db.QueryRow(`SELECT count(*) FROM gateway_oauth_clients`).Scan(&count); err != nil || count != 0 {
					t.Fatal("clients survived reset")
				}
			}
			replacement, _, err := f.store.gatewaySave(f.connection, true, false, gatewayTestActor)
			if err != nil || replacement.ID == f.connection.ID {
				t.Fatal("connection identity reused")
			}
			if _, err = f.store.gatewayAuthenticate(tokens.AccessToken, localTestOrigin+"/mcp/"+replacement.ID); err == nil {
				t.Fatal("old token accepted after recreation")
			}
		})
	}
}

func TestLocalOAuthConsentRequiresPrimaryHumanSession(t *testing.T) {
	f := newLocalOAuthFixture(t)
	requestID := f.request(t)
	now := time.Now()
	for _, actor := range []session{
		{Username: "viewer", Role: roleViewer, AuthSource: authSourceWebUI},
		{Username: "operator", Role: roleOperator, AuthSource: authSourceWebUI},
		{Username: "another-admin", Role: roleAdministrator, AuthSource: authSourceSystem},
		{Username: "voxel", Role: roleAdministrator, AuthSource: authSourceExternal},
		{Username: "voxel", Role: roleAdministrator, AuthSource: authSourceLocalRoot},
		{Username: "voxel", Role: roleAdministrator, AuthSource: authSourceSystem, MustChange: true},
		{Username: "voxel", Role: roleAdministrator, AuthSource: authSourceSystem},
	} {
		actor.Created = now
		actor.LastSeen = now
		s := &server{store: f.store, sessions: map[string]session{"browser-session": actor}}
		raw, _ := json.Marshal(localOAuthInput{Action: "inspect", Origin: localTestOrigin, RequestID: requestID})
		r := httptest.NewRequest("POST", "/v1/gateway/oauth", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer browser-session")
		rr := httptest.NewRecorder()
		s.gatewayLocalOAuth(rr, r)
		allowed := actor.Username == "voxel" && actor.AuthSource == authSourceSystem && !actor.MustChange
		if (rr.Code == 200) != allowed {
			t.Fatalf("incorrect approval boundary for %s/%s: %d", actor.Username, actor.AuthSource, rr.Code)
		}
	}
}

func TestLocalOAuthFactoryRevocationSurvivesStoreReopen(t *testing.T) {
	f := newLocalOAuthFixture(t)
	tokens := f.tokens(t)
	request := f.request(t)
	code := f.code(t)
	var sequence int
	var name, path string
	if err := f.store.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := f.store.gatewayFactoryRevoke(); err != nil {
		t.Fatal(err)
	}
	// Destructive reset cleanup is deliberately not run. Its later failure cannot
	// undo the separately committed gateway revocation, including after restart.
	if err := f.store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openWebUIStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if _, err = reopened.gatewayAuthenticate(tokens.AccessToken, f.resource); err == nil {
		t.Fatal("access survived durable revocation")
	}
	if _, err = reopened.localOAuthDecide(localTestOrigin, request, true); err == nil {
		t.Fatal("request survived durable revocation")
	}
	if _, err = reopened.localOAuthExchange(localTestOrigin, f.exchangeParameters(code)); err == nil {
		t.Fatal("code survived durable revocation")
	}
	if _, err = reopened.localOAuthExchange(localTestOrigin, map[string]string{"grant_type": "refresh_token", "client_id": f.client.ClientID, "resource": f.resource, "refresh_token": tokens.RefreshToken}); err == nil {
		t.Fatal("refresh survived durable revocation")
	}
}
