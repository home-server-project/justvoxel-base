package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestGatewayOAuthValidationAndExplicitEnrollment(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b64 := func(v []byte) string { return base64.RawURLEncoding.EncodeToString(v) }
	jwks := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("access token forwarded to authorization server")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "EC", "kid": "test-key", "alg": "ES256", "crv": "P-256", "x": b64(key.X.FillBytes(make([]byte, 32))), "y": b64(key.Y.FillBytes(make([]byte, 32)))}}})
	}))
	defer jwks.Close()
	old := gatewayOAuthHTTP
	gatewayOAuthHTTP = jwks.Client()
	t.Cleanup(func() {
		gatewayOAuthHTTP = old
		gatewayKeyCache.Lock()
		clear(gatewayKeyCache.keys)
		gatewayKeyCache.Unlock()
	})
	binding := gatewayOAuthBinding{Issuer: "https://issuer.example", Audience: "https://voxel.example/mcp", JWKSURI: jwks.URL, Scope: "justvoxel:connect", Subject: "automation", ClientID: "client-1"}
	claims := func() jwt.MapClaims {
		return jwt.MapClaims{"iss": binding.Issuer, "aud": binding.Audience, "sub": binding.Subject, "client_id": binding.ClientID, "scope": "other justvoxel:connect", "exp": time.Now().Add(time.Hour).Unix(), "nbf": time.Now().Add(-time.Minute).Unix()}
	}
	sign := func(c jwt.MapClaims, k *ecdsa.PrivateKey) string {
		t.Helper()
		token := jwt.NewWithClaims(jwt.SigningMethodES256, c)
		token.Header["kid"] = "test-key"
		raw, err := token.SignedString(k)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	valid := sign(claims(), key)
	if err = verifyGatewayOAuth(valid, &binding); err != nil {
		t.Fatal("valid resource token rejected")
	}
	cases := []struct {
		name   string
		change func(jwt.MapClaims)
	}{
		{"issuer", func(c jwt.MapClaims) { c["iss"] = "https://unknown.example" }},
		{"audience", func(c jwt.MapClaims) { c["aud"] = "https://other-resource.example" }},
		{"missing audience", func(c jwt.MapClaims) { delete(c, "aud") }},
		{"scope", func(c jwt.MapClaims) { c["scope"] = "justvoxel:other" }},
		{"subject", func(c jwt.MapClaims) { c["sub"] = "unknown" }},
		{"client", func(c jwt.MapClaims) { c["client_id"] = "other-client" }},
		{"expired", func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() }},
		{"missing expiry", func(c jwt.MapClaims) { delete(c, "exp") }},
		{"not yet valid", func(c jwt.MapClaims) { c["nbf"] = time.Now().Add(time.Hour).Unix() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := claims()
			tc.change(c)
			if verifyGatewayOAuth(sign(c, key), &binding) == nil {
				t.Fatal("invalid claims accepted")
			}
		})
	}
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"malformed", valid + "garbage", sign(claims(), otherKey)} {
		if verifyGatewayOAuth(invalid, &binding) == nil {
			t.Fatal("invalid signature or JWT accepted")
		}
	}
	store, _ := openTestWebUIStore(t)
	if err = store.gatewaySetEnabled(true, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	if _, err = store.gatewayAuthenticate(valid); err == nil {
		t.Fatal("unknown OAuth identity automatically enrolled")
	}
	c := gatewayConnection{Name: "OAuth client", AuthMode: "oauth", Level: 1, Enabled: true, AllInstances: true, OAuth: &binding}
	c, token, err := store.gatewaySave(c, true, false, gatewayTestActor)
	if err != nil || token != "" {
		t.Fatal("OAuth should not issue credentials")
	}
	if got, err := store.gatewayAuthenticate(valid); err != nil || got.ID != c.ID {
		t.Fatal("bound identity rejected")
	}
	// Two matching bindings are ambiguous, never an arbitrary permission choice.
	duplicate, _, err := store.gatewaySave(c, true, false, gatewayTestActor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.gatewayAuthenticate(valid); err == nil {
		t.Fatal("ambiguous identity accepted")
	}
	if _, err = store.gatewayCredentialAction(duplicate.ID, "delete", gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	c.Level = 3
	if _, _, err = store.gatewaySave(c, false, false, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	if got, err := store.gatewayAuthenticate(valid); err != nil || got.Level != 3 {
		t.Fatal("OAuth level change not live")
	}
	// A second configured issuer and a distinct identity work independently.
	second := binding
	second.Issuer = "https://issuer-two.example"
	second.Subject = "second-automation"
	c.ID = ""
	c.Name = "Second issuer"
	c.OAuth = &second
	if _, _, err = store.gatewaySave(c, true, false, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	secondClaims := claims()
	secondClaims["iss"] = second.Issuer
	secondClaims["sub"] = second.Subject
	if _, err = store.gatewayAuthenticate(sign(secondClaims, key)); err != nil {
		t.Fatal("second configured issuer rejected")
	}
	if err = store.gatewayFactoryRevoke(); err != nil {
		t.Fatal(err)
	}
	if _, err = store.gatewayAuthenticate(valid); err == nil {
		t.Fatal("Factory Reset retained OAuth binding")
	}
}
