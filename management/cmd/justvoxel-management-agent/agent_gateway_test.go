package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var gatewayTestActor = session{Username: systemAdminUsername, Role: roleAdministrator}

func createTestGatewayConnection(t *testing.T, store *webUIStore, level int) (gatewayConnection, string) {
	t.Helper()
	c, token, err := store.gatewaySave(gatewayConnection{Name: "Automation", Purpose: "API Client", AuthMode: "token", Level: level, Enabled: true, AllInstances: true}, true, true, gatewayTestActor)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.gatewaySetEnabled(true, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	return c, token
}
func gatewayTestRequest(s *server, token string, call gatewayCall) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(call)
	r := httptest.NewRequest("POST", "/v1/gateway/execute", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.gatewayExecute(w, r)
	return w
}
func gatewayTestCatalog(t *testing.T, s *server, token string) []gatewayOperation {
	t.Helper()
	r := httptest.NewRequest("GET", "/v1/gateway/catalog", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.gatewayCatalog(w, r)
	if w.Code != 200 {
		t.Fatalf("catalog %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Operations []gatewayOperation `json:"operations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Operations
}
func TestGatewayMigrationFromExistingIdentityStore(t *testing.T) {
	migrations := webUIStoreMigrations
	t.Cleanup(func() { webUIStoreMigrations = migrations })
	webUIStoreMigrations = migrations[:len(migrations)-1]
	path := filepath.Join(t.TempDir(), "identity.db")
	old, err := openWebUIStore(path)
	if err != nil {
		t.Fatal(err)
	}
	user, err := old.createWebUser("Existing", webUserRoleViewer, "existing-private-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = old.close(); err != nil {
		t.Fatal(err)
	}
	webUIStoreMigrations = migrations
	upgraded, err := openWebUIStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.close()
	snapshot, err := upgraded.gatewaySnapshot()
	if err != nil || snapshot.Enabled || len(snapshot.Connections) != 0 {
		t.Fatalf("migration did not leave gateway disabled: %+v %v", snapshot, err)
	}
	users, err := upgraded.listWebUsers()
	if err != nil || len(users) != 1 || users[0].ID != user.ID {
		t.Fatal("migration altered existing browser identity")
	}
	var version int
	if err = upgraded.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || version != 5 {
		t.Fatalf("migration version %d: %v", version, err)
	}
}
func TestGatewayTokenLifecycleAndMultipleConnections(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	one, token := createTestGatewayConnection(t, store, 1)
	two, other := createTestGatewayConnection(t, store, 3)
	if one.ID == two.ID || token == other || !strings.HasPrefix(token, "jvapi_") || len(token) < 49 {
		t.Fatal("independent high entropy identities required")
	}
	var raw string
	var digest []byte
	if err := store.db.QueryRow(`SELECT record_json,token_digest FROM gateway_connections WHERE id=?`, one.ID).Scan(&raw, &digest); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, token) || bytes.Equal(digest, []byte(token)) || len(digest) != 32 {
		t.Fatal("plaintext credential persisted")
	}
	snap, err := store.gatewaySnapshot()
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(snap)
	if bytes.Contains(public, []byte(token)) || bytes.Contains(public, []byte("token_digest")) {
		t.Fatal("credential exposed in connection settings")
	}
	authenticated, err := store.gatewayAuthenticate(token)
	if err != nil || authenticated.ID != one.ID {
		t.Fatal("token authentication failed")
	}
	rotated, err := store.gatewayCredentialAction(one.ID, "rotate", gatewayTestActor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.gatewayAuthenticate(token); err == nil {
		t.Fatal("rotation retained old credential")
	}
	if _, err = store.gatewayAuthenticate(rotated); err != nil {
		t.Fatal(err)
	}
	if _, err = store.gatewayCredentialAction(one.ID, "revoke", gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	if _, err = store.gatewayAuthenticate(rotated); err == nil {
		t.Fatal("revoked token accepted")
	}
	if _, err = store.gatewayAuthenticate(other); err != nil {
		t.Fatal("revocation affected another connection")
	}
	if _, err = store.gatewayCredentialAction(two.ID, "delete", gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	if _, err = store.gatewayAuthenticate(other); err == nil {
		t.Fatal("deleted connection accepted")
	}
	var audit string
	rows, err := store.db.Query(`SELECT action || target || context FROM audit_events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		if err = rows.Scan(&audit); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(audit, token) || strings.Contains(audit, rotated) || strings.Contains(audit, other) {
			t.Fatal("credential in audit")
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
}
func TestGatewayDisableExpirationAndLiveLevels(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	c, token := createTestGatewayConnection(t, store, 1)
	s := &server{store: store}
	for _, level := range []int{1, 2, 3, 4, 1, 4, 3, 2} {
		c.Level = level
		var err error
		c, _, err = store.gatewaySave(c, false, true, gatewayTestActor)
		if err != nil {
			t.Fatal(err)
		}
		current, err := store.gatewayAuthenticate(token)
		if err != nil || current.Level != level {
			t.Fatalf("same token level %d: %v", level, err)
		}
		ops := gatewayTestCatalog(t, s, token)
		for _, op := range ops {
			if op.MinLevel > level {
				t.Fatalf("cached visibility grants %s", op.Name)
			}
		}
		// A stale tool/API operation name is checked again at execution.
		if level < 4 {
			rr := gatewayTestRequest(s, token, gatewayCall{Operation: "systemd", Body: json.RawMessage(`{"action":"daemon-reload"}`)})
			if rr.Code != 403 {
				t.Fatalf("stale systemd request level %d: %d", level, rr.Code)
			}
		}
		if level == 2 {
			rr := gatewayTestRequest(s, token, gatewayCall{Operation: "instance_stop", InstanceID: "jv-abcdef"})
			if rr.Code != 403 {
				t.Fatal("Level 2 Stop accepted")
			}
		}
	}
	c.Enabled = false
	if _, _, err := store.gatewaySave(c, false, false, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	if _, err := store.gatewayAuthenticate(token); err == nil {
		t.Fatal("disabled connection accepted")
	}
	c.Enabled = true
	if _, _, err := store.gatewaySave(c, false, false, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	if err := store.gatewaySetEnabled(false, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	if _, err := store.gatewayAuthenticate(token); err == nil {
		t.Fatal("globally disabled gateway accepted request")
	}
	if err := store.gatewaySetEnabled(true, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	if _, err := store.gatewayAuthenticate(token); err != nil {
		t.Fatal("re-enable failed")
	}
	past := time.Now().Add(-time.Second)
	c.ExpiresAt = &past
	if _, _, err := store.gatewaySave(c, false, false, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	if _, err := store.gatewayAuthenticate(token); err == nil {
		t.Fatal("expired connection accepted")
	}
}
func TestGatewayPolicyMatrixScopeAndNoSecurityOrShell(t *testing.T) {
	s := &server{}
	for _, op := range s.gatewayOperations() {
		if strings.Contains(op.Path, "factory") || strings.Contains(op.Path, "gateway") || strings.Contains(op.Path, "terminal") || strings.Contains(op.Path, "users") || strings.Contains(op.Path, "password") || strings.Contains(op.Path, "shell") {
			t.Fatalf("security/shell capability exposed: %s", op.Name)
		}
		for level := 1; level <= 4; level++ {
			c := gatewayConnection{Level: level, AllInstances: true}
			if got := gatewayAllowed(c, op, gatewayCall{InstanceID: "jv-abcdef"}); got != (level >= op.MinLevel) {
				t.Fatalf("%s level %d authorization %v", op.Name, level, got)
			}
		}
		scoped := gatewayConnection{Level: 4, InstanceIDs: []string{"jv-abcdef"}}
		if op.Instance {
			if gatewayAllowed(scoped, op, gatewayCall{InstanceID: "jv-123456"}) {
				t.Fatal("foreign instance allowed")
			}
			if !gatewayAllowed(scoped, op, gatewayCall{InstanceID: "jv-abcdef"}) {
				t.Fatal("allowed instance denied")
			}
			if gatewayAllowed(scoped, op, gatewayCall{InstanceID: "Friendly name"}) {
				t.Fatal("friendly name accepted")
			}
			if gatewayAllowed(scoped, op, gatewayCall{InstanceID: "jv-abcdef", Body: json.RawMessage(`{"target_id":"jv-123456"}`)}) {
				t.Fatal("nested scope bypass")
			}
		}
		if op.AllInstances && gatewayAllowed(scoped, op, gatewayCall{}) {
			t.Fatal("selected scope can mutate appliance-wide state")
		}
	}
	store, _ := openTestWebUIStore(t)
	_, token := createTestGatewayConnection(t, store, 3)
	s.store = store
	for _, operation := range []string{"shell", "gatewayAdmin", "factory_reset", "/v1/admin/gateway"} {
		if rr := gatewayTestRequest(s, token, gatewayCall{Operation: operation}); rr.Code != 404 {
			t.Fatalf("unexpected route %s: %d", operation, rr.Code)
		}
	}
}
func TestGatewaySystemdUsesOnlyValidatedArgv(t *testing.T) {
	for _, action := range []string{"status", "start", "stop", "restart", "enable", "disable", "mask", "unmask"} {
		if args, ok := gatewaySystemdArguments(action, "example@instance.service"); !ok || len(args) < 3 || args[len(args)-2] != "--" {
			t.Fatalf("invalid argv %s: %v", action, args)
		}
	}
	for _, unit := range []string{"--help", "/etc/example.service", "test.service;id", "a.service\nreboot", "a..service", "*.service", "$(id).service", "-a.service"} {
		if _, ok := gatewaySystemdArguments("restart", unit); ok {
			t.Fatalf("unit injection: %q", unit)
		}
	}
	for _, action := range []string{"shell", "cat /etc/shadow", "restart;reboot", "edit", "set-environment", "link"} {
		if _, ok := gatewaySystemdArguments(action, "a.service"); ok {
			t.Fatal("unsafe systemd verb accepted")
		}
	}
	if args, ok := gatewaySystemdArguments("daemon-reload", ""); !ok || len(args) != 1 {
		t.Fatal("daemon-reload validation")
	}
	if _, ok := gatewaySystemdArguments("daemon-reload", "a.service"); ok {
		t.Fatal("daemon-reload accepted arguments")
	}
	store, _ := openTestWebUIStore(t)
	_, token := createTestGatewayConnection(t, store, 4)
	old := gatewaySystemctl
	t.Cleanup(func() { gatewaySystemctl = old })
	var got []string
	gatewaySystemctl = func(_ context.Context, args ...string) ([]byte, error) {
		got = args
		return []byte("Id=a.service\nActiveState=active\nEnvironment=private\n"), nil
	}
	rr := gatewayTestRequest(&server{store: store}, token, gatewayCall{Operation: "systemd", Body: json.RawMessage(`{"action":"status","unit":"a.service"}`)})
	if rr.Code != 200 || len(got) == 0 || strings.Contains(rr.Body.String(), "private") {
		t.Fatalf("systemd result %d: %s", rr.Code, rr.Body.String())
	}
}
func TestGatewayFactoryRevokeDrainsAcceptedExecution(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	_, token := createTestGatewayConnection(t, store, 4)
	s := &server{store: store}
	entered := make(chan struct{})
	release := make(chan struct{})
	old := gatewaySystemctl
	t.Cleanup(func() { gatewaySystemctl = old })
	gatewaySystemctl = func(_ context.Context, _ ...string) ([]byte, error) {
		close(entered)
		<-release
		return nil, nil
	}
	executed := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		executed <- gatewayTestRequest(s, token, gatewayCall{Operation: "systemd", Body: json.RawMessage(`{"action":"daemon-reload"}`)})
	}()
	<-entered

	writerAttempted := make(chan bool, 1)
	revoked := make(chan error, 1)
	go func() {
		// TryLock makes exclusion observable without relying on when the
		// scheduler runs the blocking Lock or on an arbitrary waiting period.
		locked := s.gatewayMu.TryLock()
		writerAttempted <- locked
		if !locked {
			s.gatewayMu.Lock()
		}
		err := store.gatewayFactoryRevoke()
		s.gatewayMu.Unlock()
		revoked <- err
	}()
	lockedWhileExecuting := <-writerAttempted
	var revokeErr error
	revokedWhileExecuting := false
	select {
	case revokeErr = <-revoked:
		revokedWhileExecuting = true
	default:
	}
	close(release)
	rr := <-executed
	if !revokedWhileExecuting {
		revokeErr = <-revoked
	}
	if lockedWhileExecuting || revokedWhileExecuting {
		t.Fatal("gateway security write was not excluded by the accepted operation")
	}
	if rr.Code != 200 {
		t.Fatalf("accepted operation failed: %d %s", rr.Code, rr.Body.String())
	}
	if revokeErr != nil {
		t.Fatal(revokeErr)
	}
	if rr = gatewayTestRequest(s, token, gatewayCall{Operation: "appliance_status"}); rr.Code != 401 {
		t.Fatalf("revoked token accepted: %d %s", rr.Code, rr.Body.String())
	}
	snapshot, err := store.gatewaySnapshot()
	if err != nil || snapshot.Enabled {
		t.Fatalf("gateway did not remain disabled: %+v %v", snapshot, err)
	}
}

func TestGatewayOperatorIndependentQuotasAndSharedCooldown(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	a, _ := createTestGatewayConnection(t, store, 2)
	b, _ := createTestGatewayConnection(t, store, 2)
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	actor := session{Username: a.ID, Role: roleOperator, ConnectionID: a.ID}
	for _, kind := range []string{"restart", "backup"} {
		t.Run(kind, func(t *testing.T) {
			begin := func(id string) (func(), func() (int, error), bool, error) {
				if kind == "restart" {
					g, av, err := store.beginOperatorRestartPrincipal(0, id)
					if g == nil {
						return func() {}, nil, av.Allowed, err
					}
					return g.cancel, func() (int, error) { return g.acceptTarget(actor, "jv-abcdef") }, av.Allowed, err
				}
				g, av, err := store.beginOperatorBackupPrincipal(0, id)
				if g == nil {
					return func() {}, nil, av.Allowed, err
				}
				return g.cancel, func() (int, error) { return g.acceptTarget(actor, "jv-abcdef") }, av.Allowed, err
			}
			for used := 1; used <= 2; used++ {
				cancel, accept, allowed, err := begin(a.ID)
				if err != nil || !allowed {
					t.Fatalf("allowance %d %v", used, err)
				}
				if count, err := accept(); err != nil || count != used {
					t.Fatalf("account %d %v", count, err)
				}
				cancel()
				cancel, _, allowed, err = begin(b.ID)
				cancel()
				if err != nil || allowed {
					t.Fatal("second connection bypassed shared cooldown")
				}
				now = now.Add(16 * time.Minute)
			}
			cancel, _, allowed, err := begin(a.ID)
			cancel()
			if err != nil || allowed {
				t.Fatal("quota bypass")
			}
			cancel, accept, allowed, err := begin(b.ID)
			if err != nil || !allowed {
				t.Fatal("independent allowance unavailable")
			}
			if used, err := accept(); err != nil || used != 1 {
				t.Fatal("usage shared across identities")
			}
			cancel()
			now = now.Add(16 * time.Minute)
		})
	}
}
func TestGatewayOpaqueSelectorsAndSanitization(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	a, token := createTestGatewayConnection(t, store, 3)
	b, _ := createTestGatewayConnection(t, store, 3)
	result, err := store.gatewayPublicResult(a.ID, map[string]any{"device": "/dev/sdb", "rcon_password": "hidden", "logs": "jvapi_secret-example /var/lib/private\npassword=secret\n-----BEGIN PRIVATE KEY-----\nprivatekeycontent\n-----END PRIVATE KEY-----", "runtime": map[string]any{"max_players": 10, "rcon_password": "hidden"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	for _, private := range []string{"/dev/", "/var/", "hidden", "jvapi_secret", "privatekeycontent", token} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("private gateway result")
		}
	}
	selector := result.(map[string]any)["device"].(string)
	if !strings.HasPrefix(selector, "jvref_") {
		t.Fatal("opaque selector missing")
	}
	if resolved, err := store.gatewayResolveReferences(a.ID, selector); err != nil || resolved != "/dev/sdb" {
		t.Fatal("selector round trip")
	}
	if _, err := store.gatewayResolveReferences(b.ID, selector); err == nil {
		t.Fatal("cross-connection selector accepted")
	}
}
func TestGatewayPrimaryHumanAdministrationAndLevel4Confirmation(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	// A normal Administrator (tester) is not primary.
	rr := httptest.NewRecorder()
	s.gatewayAdmin(rr, surfaceRequest("GET", "/v1/admin/gateway", ""))
	if rr.Code != 403 {
		t.Fatal("non-primary administrator can manage connections")
	}
	// External developer principals remain separate from primary human authority.
	r := httptest.NewRequest("GET", "/v1/admin/gateway", nil).WithContext(context.WithValue(context.Background(), gatewayPrincipalKey{}, session{Username: systemAdminUsername, Role: roleAdministrator, ConnectionID: "external"}))
	rr = httptest.NewRecorder()
	s.gatewayAdmin(rr, r)
	if rr.Code != 403 {
		t.Fatal("external principal can approve itself")
	}
	c, token := createTestGatewayConnection(t, s.store, 1)
	c.Level = 4
	if _, _, err := s.store.gatewaySave(c, false, false, gatewayTestActor); err == nil {
		t.Fatal("Level 4 missing acknowledgement accepted")
	}
	c, _, err := s.store.gatewaySave(c, false, true, gatewayTestActor)
	if err != nil {
		t.Fatal(err)
	}
	c.Level = 1
	c, _, err = s.store.gatewaySave(c, false, false, gatewayTestActor)
	if err != nil {
		t.Fatal(err)
	}
	c.Level = 4
	if _, _, err = s.store.gatewaySave(c, false, false, gatewayTestActor); err == nil {
		t.Fatal("repeat Level 4 transition bypassed confirmation")
	}
	current, err := s.store.gatewayAuthenticate(token)
	if err != nil || current.Level != 1 {
		t.Fatal("failed confirmation changed live authorization")
	}
}
func TestGatewayFactoryRevokeIsDurableAcrossCleanupFailure(t *testing.T) {
	useEmptyFactoryResetRuntime(t)
	useTestMinecraftInstances(t)
	s := surfaceTestServer(t, roleAdministrator)
	s.operations = openTestOperationStore(t)
	a, token := createTestGatewayConnection(t, s.store, 4)
	oauth := gatewayConnection{Name: "OAuth disabled", AuthMode: "oauth", Level: 1, Enabled: false, AllInstances: true, OAuth: &gatewayOAuthBinding{Issuer: "https://issuer.example", Audience: "https://voxel.example/mcp", Scope: "justvoxel:connect", Subject: "automation", JWKSURI: "https://issuer.example/jwks"}}
	if _, _, err := s.store.gatewaySave(oauth, true, false, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.gatewayReference(a.ID, "/dev/sdb"); err != nil {
		t.Fatal(err)
	}
	old := runAdminFactoryResetHelper
	t.Cleanup(func() { runAdminFactoryResetHelper = old })
	runAdminFactoryResetHelper = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "plan" {
			return []byte(validFactoryResetPlanHelper), nil
		}
		if _, err := s.store.gatewayAuthenticate(token); err == nil {
			t.Fatal("destructive helper reached before revocation")
		}
		return nil, errors.New("simulated cleanup failure")
	}
	fingerprint := exactFactoryResetFingerprint(t)
	op, _, err := s.operations.beginFactoryReset(fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err = executeFactoryReset(context.Background(), s, op.OperationID, fingerprint); err == nil {
		t.Fatal("injected cleanup failure was lost")
	}
	current, err := s.operations.get(op.OperationID)
	if err != nil || current.State != operationNeedsAttention {
		t.Fatalf("recovery state %v %v", current, err)
	}
	for _, table := range []string{"gateway_connections", "gateway_usage", "gateway_references"} {
		var count int
		if err = s.store.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("reset retained %s: %d %v", table, count, err)
		}
	}
	snap, err := s.store.gatewaySnapshot()
	if err != nil || snap.Enabled {
		t.Fatal("gateway did not remain disabled")
	}
	if _, err = s.store.gatewayAuthenticate(token); err == nil {
		t.Fatal("failure restored token")
	}
	// A repeated revocation and identity reset cannot resurrect credentials.
	if err = s.store.gatewayFactoryRevoke(); err != nil {
		t.Fatal(err)
	}
	if err = s.store.resetFactoryState(); err != nil {
		t.Fatal(err)
	}
	if err = s.store.gatewaySetEnabled(true, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.gatewayAuthenticate(token); err == nil {
		t.Fatal("retry resurrected token")
	}
}

func TestGatewayAdministrationRequiresBothLevel4Acknowledgements(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	human := s.sessions["token"]
	human.Username = systemAdminUsername
	s.sessions["token"] = human
	c, token := createTestGatewayConnection(t, s.store, 1)
	for _, tc := range []struct {
		ack    bool
		slider int
		want   int
	}{{false, 0, 400}, {true, 0, 400}, {false, 100, 400}, {true, 99, 400}, {true, 100, 200}} {
		updated := c
		updated.Level = 4
		body, _ := json.Marshal(map[string]any{"action": "apply", "connection": updated, "acknowledged": tc.ack, "slider": tc.slider})
		rr := httptest.NewRecorder()
		s.gatewayAdmin(rr, surfaceRequest("POST", "/v1/admin/gateway", string(body)))
		if rr.Code != tc.want {
			t.Fatalf("ack %v slider %d: %d %s", tc.ack, tc.slider, rr.Code, rr.Body.String())
		}
	}
	current, err := s.store.gatewayAuthenticate(token)
	if err != nil || current.Level != 4 {
		t.Fatal("confirmed Level 4 did not retain the original token")
	}
	if len(s.sessions) != 1 {
		t.Fatal("external request created a browser session")
	}
}
func TestGatewayDispatchEnforcesChangedScopeWithSameCredential(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	c, token := createTestGatewayConnection(t, store, 3)
	c.AllInstances = false
	c.InstanceIDs = []string{"jv-abcdef"}
	if _, _, err := store.gatewaySave(c, false, false, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	s := &server{store: store}
	for _, call := range []gatewayCall{{Operation: "instance_status", InstanceID: "jv-123456"}, {Operation: "instance_stop", InstanceID: "Friendly name"}, {Operation: "network"}, {Operation: "instance_create"}} {
		if rr := gatewayTestRequest(s, token, call); rr.Code != 403 {
			t.Fatalf("scoped dispatch accepted %s: %d", call.Operation, rr.Code)
		}
	}
	c.InstanceIDs = []string{"jv-123456"}
	if _, _, err := store.gatewaySave(c, false, false, gatewayTestActor); err != nil {
		t.Fatal(err)
	}
	if rr := gatewayTestRequest(s, token, gatewayCall{Operation: "instance_status", InstanceID: "jv-abcdef"}); rr.Code != 403 {
		t.Fatal("prior scope survived Apply")
	}
}
