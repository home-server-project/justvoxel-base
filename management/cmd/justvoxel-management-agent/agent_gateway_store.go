package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"
)

var errGatewayCredential = errors.New("external credential unavailable")

type gatewayPrincipalKey struct{}
type gatewayOAuthBinding struct {
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
	Scope    string `json:"scope"`
	Subject  string `json:"subject,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	JWKSURI  string `json:"jwks_uri"`
}
type gatewayConnection struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Purpose      string               `json:"purpose,omitempty"`
	AuthMode     string               `json:"auth_mode"`
	Level        int                  `json:"level"`
	Enabled      bool                 `json:"enabled"`
	ExpiresAt    *time.Time           `json:"expires_at,omitempty"`
	AllInstances bool                 `json:"all_instances"`
	InstanceIDs  []string             `json:"instance_ids"`
	CreatedAt    time.Time            `json:"created_at"`
	UpdatedAt    time.Time            `json:"updated_at"`
	LastActivity *time.Time           `json:"last_activity,omitempty"`
	Fingerprint  string               `json:"fingerprint,omitempty"`
	OAuth        *gatewayOAuthBinding `json:"oauth,omitempty"`
}
type gatewaySnapshot struct {
	Enabled     bool                `json:"enabled"`
	Connections []gatewayConnection `json:"connections"`
}

func gatewayRandom(prefix string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func gatewayDigest(token string) []byte { d := sha256.Sum256([]byte(token)); return d[:] }
func gatewayHTTPS(raw string) bool {
	u, e := url.Parse(raw)
	return len(raw) <= 2048 && e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == "" && u.RawQuery == ""
}
func validateGatewayConnection(c gatewayConnection) error {
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 100 || len(c.Purpose) > 100 || c.Level < 1 || c.Level > 4 {
		return errors.New("invalid connection settings")
	}
	for _, r := range c.Name + c.Purpose {
		if unicode.IsControl(r) {
			return errors.New("invalid connection label")
		}
	}
	for _, id := range c.InstanceIDs {
		if !instanceIDPattern.MatchString(id) {
			return errInstanceID
		}
	}
	if c.AllInstances && len(c.InstanceIDs) != 0 {
		return errors.New("choose all instances or selected instances")
	}
	switch c.AuthMode {
	case "token":
		if c.OAuth != nil {
			return errors.New("token connections cannot have OAuth bindings")
		}
	case "oauth":
		o := c.OAuth
		if o == nil || !gatewayHTTPS(o.Issuer) || !gatewayHTTPS(o.JWKSURI) || !gatewayHTTPS(o.Audience) || o.Scope != "justvoxel:connect" || (o.Subject == "" && o.ClientID == "") || len(o.Subject) > 512 || len(o.ClientID) > 512 {
			return errors.New("OAuth requires HTTPS issuer, JWKS and audience, justvoxel:connect scope and a stable subject or client binding")
		}
	default:
		return errors.New("invalid authentication mode")
	}
	return nil
}
func (s *webUIStore) gatewaySnapshot() (gatewaySnapshot, error) {
	out := gatewaySnapshot{Connections: []gatewayConnection{}}
	if s == nil || s.db == nil {
		return out, errGatewayCredential
	}
	if err := s.db.QueryRow(`SELECT enabled FROM gateway_settings WHERE id=1`).Scan(&out.Enabled); err != nil {
		return out, err
	}
	rows, err := s.db.Query(`SELECT record_json,fingerprint FROM gateway_connections ORDER BY id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var c gatewayConnection
		if err = rows.Scan(&raw, &c.Fingerprint); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			return out, err
		}
		out.Connections = append(out.Connections, c)
	}
	return out, rows.Err()
}
func (s *webUIStore) gatewaySave(c gatewayConnection, create, confirm4 bool, actor session) (gatewayConnection, string, error) {
	if err := validateGatewayConnection(c); err != nil {
		return c, "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return c, "", err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	token := ""
	var digest []byte
	fingerprint := ""
	old := gatewayConnection{}
	if create {
		id, e := gatewayRandom("jvc_")
		if e != nil {
			return c, "", e
		}
		c.ID = id
		c.CreatedAt = now
		c.LastActivity = nil
		c.Fingerprint = ""
	} else {
		var raw string
		if err = tx.QueryRow(`SELECT record_json,token_digest,fingerprint FROM gateway_connections WHERE id=?`, c.ID).Scan(&raw, &digest, &fingerprint); err != nil {
			return c, "", err
		}
		if err = json.Unmarshal([]byte(raw), &old); err != nil {
			return c, "", err
		}
		if old.AuthMode != c.AuthMode {
			return c, "", errors.New("authentication mode is immutable; create a separate connection")
		}
		c.CreatedAt = old.CreatedAt
		c.LastActivity = old.LastActivity
	}
	if c.Level == 4 && old.Level != 4 && !confirm4 {
		return c, "", errors.New("Level 4 acknowledgement and confirmation slider are required")
	}
	c.UpdatedAt = now
	if create && c.AuthMode == "token" {
		token, err = gatewayRandom("jvapi_")
		if err != nil {
			return c, "", err
		}
		digest = gatewayDigest(token)
		fingerprint = "SHA256:" + hex.EncodeToString(digest[:6]) + " …" + token[len(token)-4:]
	}
	c.Fingerprint = fingerprint
	raw, err := json.Marshal(c)
	if err != nil {
		return c, "", err
	}
	if create {
		_, err = tx.Exec(`INSERT INTO gateway_connections(id,record_json,token_digest,fingerprint,updated_at) VALUES(?,?,?,?,?)`, c.ID, string(raw), digest, fingerprint, formatWebUIStoreTime(now))
		if err == nil {
			_, err = tx.Exec(`INSERT INTO gateway_usage(connection_id,updated_at) VALUES(?,?)`, c.ID, formatWebUIStoreTime(now))
		}
	} else {
		_, err = tx.Exec(`UPDATE gateway_connections SET record_json=?,updated_at=? WHERE id=?`, string(raw), formatWebUIStoreTime(now), c.ID)
	}
	if err != nil {
		return c, "", err
	}
	actions := []string{"connection_created"}
	if !create {
		actions = []string{"connection_updated"}
		if old.Enabled != c.Enabled {
			if c.Enabled {
				actions = append(actions, "connection_enabled")
			} else {
				actions = append(actions, "connection_disabled")
			}
		}
		if old.Level != c.Level {
			actions = append(actions, "access_level_changed")
			if c.Level == 4 {
				actions = append(actions, "level4_enabled")
			}
			if old.Level == 4 {
				actions = append(actions, "level4_disabled")
			}
		}
	}
	if create && c.Level == 4 {
		actions = append(actions, "level4_enabled")
	}
	if c.OAuth != nil {
		a, _ := json.Marshal(old.OAuth)
		b, _ := json.Marshal(c.OAuth)
		if string(a) != string(b) {
			if create {
				actions = append(actions, "oauth_binding_created")
			} else {
				actions = append(actions, "oauth_binding_changed")
			}
		}
	}
	for _, action := range actions {
		if err = insertAuditEventTx(tx, now, actor, action, c.ID+" "+c.Name, true, ""); err != nil {
			return c, "", err
		}
	}
	if err = tx.Commit(); err != nil {
		return c, "", err
	}
	return c, token, nil
}
func (s *webUIStore) gatewayCredentialAction(id, action string, actor session) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var raw string
	var c gatewayConnection
	if err = tx.QueryRow(`SELECT record_json FROM gateway_connections WHERE id=?`, id).Scan(&raw); err != nil {
		return "", err
	}
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return "", err
	}
	token := ""
	switch action {
	case "delete":
		_, err = tx.Exec(`DELETE FROM gateway_connections WHERE id=?`, id)
	case "rotate", "revoke":
		if c.AuthMode != "token" {
			return "", errors.New("only scoped API tokens can be rotated or revoked")
		}
		var digest []byte
		c.Fingerprint = ""
		if action == "rotate" {
			token, err = gatewayRandom("jvapi_")
			if err != nil {
				return "", err
			}
			digest = gatewayDigest(token)
			c.Fingerprint = "SHA256:" + hex.EncodeToString(digest[:6]) + " …" + token[len(token)-4:]
		}
		c.UpdatedAt = s.now().UTC()
		rawBytes, _ := json.Marshal(c)
		_, err = tx.Exec(`UPDATE gateway_connections SET token_digest=?,fingerprint=?,record_json=?,updated_at=? WHERE id=?`, digest, c.Fingerprint, string(rawBytes), formatWebUIStoreTime(c.UpdatedAt), id)
	case "reset-allowance":
		_, err = tx.Exec(`UPDATE gateway_usage SET restart_used=0,backup_used=0 WHERE connection_id=?`, id)
	default:
		return "", errors.New("invalid connection action")
	}
	if err != nil {
		return "", err
	}
	if err = insertAuditEventTx(tx, s.now(), actor, "connection_"+action, id+" "+c.Name, true, ""); err != nil {
		return "", err
	}
	return token, tx.Commit()
}
func (s *webUIStore) gatewaySetEnabled(enabled bool, actor session) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE gateway_settings SET enabled=? WHERE id=1`, enabled); err != nil {
		return err
	}
	if err = insertAuditEventTx(tx, s.now(), actor, "gateway_enabled_changed", "gateway", true, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// This transaction commits independently of reset cleanup and is never rolled back by a later reset failure.
func (s *webUIStore) gatewayFactoryRevoke() error {
	if s == nil || s.db == nil {
		return errGatewayCredential
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE gateway_settings SET enabled=0 WHERE id=1`); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM gateway_connections`); err != nil {
		return err
	}
	if err = insertAuditEventTx(tx, s.now(), session{Username: systemAdminUsername, Role: roleAdministrator}, "factory_reset_gateway_revocation", "gateway", true, ""); err != nil {
		return err
	}
	return tx.Commit()
}
func (c gatewayConnection) allowsInstance(id string) bool {
	if !instanceIDPattern.MatchString(id) {
		return false
	}
	if c.AllInstances {
		return true
	}
	for _, allowed := range c.InstanceIDs {
		if allowed == id {
			return true
		}
	}
	return false
}
func (s *webUIStore) gatewayAuthenticate(token string) (gatewayConnection, error) {
	empty := gatewayConnection{}
	if len(token) > 16384 || token == "" {
		return empty, errGatewayCredential
	}
	snapshot, err := s.gatewaySnapshot()
	if err != nil || !snapshot.Enabled {
		return empty, errGatewayCredential
	}
	var matched []gatewayConnection
	if strings.HasPrefix(token, "jvapi_") {
		digest := gatewayDigest(token)
		var id string
		var stored []byte
		if err = s.db.QueryRow(`SELECT id,token_digest FROM gateway_connections WHERE token_digest=?`, digest).Scan(&id, &stored); err != nil || subtle.ConstantTimeCompare(digest, stored) != 1 {
			return empty, errGatewayCredential
		}
		for _, c := range snapshot.Connections {
			if c.ID == id && c.AuthMode == "token" {
				matched = append(matched, c)
			}
		}
	} else {
		for _, c := range snapshot.Connections {
			if c.AuthMode == "oauth" && c.Enabled && (c.ExpiresAt == nil || s.now().Before(*c.ExpiresAt)) && verifyGatewayOAuth(token, c.OAuth) == nil {
				matched = append(matched, c)
			}
		}
	}
	if len(matched) != 1 {
		return empty, errGatewayCredential
	}
	c := matched[0]
	if !c.Enabled || (c.ExpiresAt != nil && !s.now().Before(*c.ExpiresAt)) {
		return empty, errGatewayCredential
	}
	return c, nil
}
func (s *webUIStore) gatewayActivity(id string) error {
	// JSON update avoids overwriting a concurrent administrator permission/scope change.
	_, err := s.db.Exec(`UPDATE gateway_connections SET record_json=json_set(record_json,'$.last_activity',?) WHERE id=?`, s.now().UTC().Format(time.RFC3339Nano), id)
	return err
}
