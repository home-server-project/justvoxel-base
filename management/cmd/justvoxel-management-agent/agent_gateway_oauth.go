package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Only administrator-pinned HTTPS JWKS endpoints are fetched. JWT jku/x5u headers
// never select a URL. No bearer token is forwarded to the authorization server.
var gatewayOAuthHTTP = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect denied") }}

type gatewayJWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}
type gatewayCachedKey struct {
	key     any
	expires time.Time
}

var gatewayKeyCache = struct {
	sync.Mutex
	keys map[string]gatewayCachedKey
}{keys: make(map[string]gatewayCachedKey)}

func gatewayJWKSKey(uri, kid, alg string) (any, error) {
	cacheID := uri + "\x00" + kid + "\x00" + alg
	gatewayKeyCache.Lock()
	cached := gatewayKeyCache.keys[cacheID]
	gatewayKeyCache.Unlock()
	if cached.key != nil && time.Now().Before(cached.expires) {
		return cached.key, nil
	}
	key, err := fetchGatewayJWKSKey(uri, kid, alg)
	if err != nil {
		return nil, err
	}
	gatewayKeyCache.Lock()
	// Cache only validated public keys, and bound memory even with issuer rotation.
	if len(gatewayKeyCache.keys) >= 256 {
		clear(gatewayKeyCache.keys)
	}
	gatewayKeyCache.keys[cacheID] = gatewayCachedKey{key, time.Now().Add(time.Minute)}
	gatewayKeyCache.Unlock()
	return key, nil
}
func fetchGatewayJWKSKey(uri, kid, alg string) (any, error) {
	if !gatewayHTTPS(uri) || kid == "" || len(kid) > 256 {
		return nil, errGatewayCredential
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, errGatewayCredential
	}
	resp, err := gatewayOAuthHTTP.Do(req)
	if err != nil {
		return nil, errGatewayCredential
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errGatewayCredential
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, errGatewayCredential
	}
	var set struct {
		Keys []gatewayJWK `json:"keys"`
	}
	if json.Unmarshal(raw, &set) != nil || len(set.Keys) > 100 {
		return nil, errGatewayCredential
	}
	var result any
	for _, k := range set.Keys {
		if k.Kid != kid || (k.Use != "" && k.Use != "sig") || (k.Alg != "" && k.Alg != alg) {
			continue
		}
		if result != nil {
			return nil, errGatewayCredential
		}
		switch {
		case k.Kty == "RSA" && (alg == "RS256" || alg == "RS384" || alg == "RS512"):
			n, e1 := base64.RawURLEncoding.DecodeString(k.N)
			e, e2 := base64.RawURLEncoding.DecodeString(k.E)
			if e1 != nil || e2 != nil || len(n) < 256 || len(n) > 1024 || len(e) < 1 || len(e) > 4 {
				return nil, errGatewayCredential
			}
			exponent := new(big.Int).SetBytes(e).Int64()
			if exponent < 3 || exponent%2 == 0 || exponent > 2147483647 {
				return nil, errGatewayCredential
			}
			modulus := new(big.Int).SetBytes(n)
			if modulus.BitLen() < 2048 || modulus.BitLen() > 8192 {
				return nil, errGatewayCredential
			}
			result = &rsa.PublicKey{N: modulus, E: int(exponent)}
		case k.Kty == "EC" && k.Crv == "P-256" && alg == "ES256":
			x, e1 := base64.RawURLEncoding.DecodeString(k.X)
			y, e2 := base64.RawURLEncoding.DecodeString(k.Y)
			if e1 != nil || e2 != nil || len(x) != 32 || len(y) != 32 {
				return nil, errGatewayCredential
			}
			key := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
			if !key.Curve.IsOnCurve(key.X, key.Y) {
				return nil, errGatewayCredential
			}
			result = key
		}
	}
	if result == nil {
		return nil, errGatewayCredential
	}
	return result, nil
}
func verifyGatewayOAuth(raw string, b *gatewayOAuthBinding) error {
	if b == nil || len(raw) > 16384 {
		return errGatewayCredential
	}
	// Unverified claims only select an already configured binding; signature and
	// all claims are still verified below. Unknown issuers cause no network fetch.
	hint := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(raw, hint); err != nil {
		return errGatewayCredential
	}
	if issuer, _ := hint["iss"].(string); issuer != b.Issuer {
		return errGatewayCredential
	}
	if b.Subject != "" {
		if subject, _ := hint["sub"].(string); subject != b.Subject {
			return errGatewayCredential
		}
	}
	if b.ClientID != "" {
		if client, _ := hint["client_id"].(string); client != b.ClientID {
			return errGatewayCredential
		}
	}
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return gatewayJWKSKey(b.JWKSURI, kid, t.Method.Alg())
	}, jwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "ES256"}), jwt.WithIssuer(b.Issuer), jwt.WithAudience(b.Audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || !token.Valid {
		return errGatewayCredential
	}
	scope, _ := claims["scope"].(string)
	found := false
	for _, s := range strings.Fields(scope) {
		if s == b.Scope {
			found = true
		}
	}
	subject, _ := claims["sub"].(string)
	client, _ := claims["client_id"].(string)
	if !found || (b.Subject == "" && b.ClientID == "") || (b.Subject != "" && subject != b.Subject) || (b.ClientID != "" && client != b.ClientID) {
		return errGatewayCredential
	}
	return nil
}
