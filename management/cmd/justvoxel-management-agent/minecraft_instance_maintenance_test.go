package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstancePlayerProtocol(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		online int
		valid  bool
	}{
		{"There are 0 of a max of 20 players online:", 0, true},
		{"There are 2 of a max of 20 players online: Alex, .Bedrock", 2, true},
		{"There are 0 of a max of 20 players online: Alex", 0, false},
		{"There are 2 of a max of 20 players online: Alex", 0, false},
		{"There are 21 of a max of 20 players online: Alex", 0, false},
		{"There are 0 of a max of 0 players online:", 0, false},
		{"prefix There are 0 of a max of 20 players online:", 0, false},
		{"There are 0 of a max of 20 players online:\nprivate data", 0, false},
		{strings.Repeat("x", 8193), 0, false},
	} {
		p, err := parseInstancePlayers("jv-a1b2c3", []byte(tc.raw))
		if (err == nil) != tc.valid || (tc.valid && p.Online != tc.online) || (!tc.valid && p.State != "unknown") {
			t.Fatalf("incorrect protocol result: %+v %v", p, err)
		}
	}
}
func TestInstanceAdministratorCountdownIsolation(t *testing.T) {
	for _, action := range []string{"stop", "restart"} {
		t.Run(action, func(t *testing.T) {
			f := newInstanceActivationFixture(t)
			f.activateBoth()
			a, b := f.configs[0].ID, f.configs[1].ID
			for _, id := range []string{a, b} {
				if err := f.backend.action(context.Background(), id, "start"); err != nil {
					t.Fatal(err)
				}
			}
			f.players = "There are 1 of a max of 20 players online: Alex"
			elapsed := time.Duration(0)
			warnings := []string{}
			base := f.backend.command
			f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				if command == "podman" && len(args) == 4 && strings.HasPrefix(args[3], "say ") {
					if args[1] != "minecraft-"+a {
						t.Fatal("announcement escaped target")
					}
					warnings = append(warnings, args[3])
					return nil, nil
				}
				if command == "systemctl" && len(args) == 4 && args[0] == "--no-block" && args[1] == "stop" {
					if args[3] != "minecraft-"+a+".service" {
						t.Fatal("stop escaped target")
					}
					f.states[a] = "deactivating"
					return nil, nil
				}
				return base(ctx, command, args...)
			}
			f.backend.pause = func(context.Context, time.Duration) error { return nil }
			f.backend.pause = func(_ context.Context, d time.Duration) error {
				elapsed += d
				if elapsed >= 60*time.Second {
					f.states[a] = "inactive"
					f.containers[a] = "exited"
				}
				return nil
			}
			if err := f.backend.actionWithWarnings(context.Background(), a, action, true); err != nil {
				t.Fatal(err)
			}
			if elapsed != 60*time.Second || len(warnings) != 9 {
				t.Fatalf("countdown differs: %v %v", elapsed, warnings)
			}
			if f.states[b] != "active" || f.containers[b] != "running" {
				t.Fatal("interrupted other running instance")
			}
			want := "inactive"
			if action == "restart" {
				want = "active"
			}
			if f.states[a] != want {
				t.Fatal("incorrect selected final state")
			}
			for _, call := range f.calls {
				if strings.Contains(call, "kill") || strings.Contains(call, "minecraft.service") {
					t.Fatal("unsafe or legacy action", call)
				}
			}
		})
	}
}
func TestInstanceCountdownFailureDoesNotRestart(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	id := f.configs[0].ID
	if err := f.backend.action(context.Background(), id, "start"); err != nil {
		t.Fatal(err)
	}
	f.players = "There are 1 of a max of 20 players online: Alex"
	base := f.backend.command
	f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		// Inspect argv before fixture-specific handlers can consume a command.
		// Only observation, announcements and the initial graceful stop are allowed.
		switch filepath.Base(command) {
		case "systemctl":
			observation := len(args) > 0 && args[0] == "show"
			gracefulStop := len(args) == 4 && args[0] == "--no-block" && args[1] == "stop" && args[2] == "--" && args[3] == "minecraft-"+id+".service"
			if !observation && !gracefulStop {
				t.Fatalf("unsafe countdown fallback: %s %q", command, args)
			}
		case "podman":
			observation := len(args) > 0 && (args[0] == "inspect" || args[0] == "ps")
			rcon := len(args) == 4 && args[0] == "exec" && args[1] == "minecraft-"+id && args[2] == "rcon-cli" && (args[3] == "list" || strings.HasPrefix(args[3], "say "))
			if !observation && !rcon {
				t.Fatalf("unsafe countdown fallback: %s %q", command, args)
			}
		case "kill", "killall", "pkill":
			t.Fatalf("unsafe countdown fallback: %s %q", command, args)
		}
		if command == "podman" && len(args) == 4 && strings.HasPrefix(args[3], "say ") {
			return nil, nil
		}
		if command == "systemctl" && len(args) == 4 && args[1] == "stop" {
			f.states[id] = "deactivating"
			return nil, nil
		}
		return base(ctx, command, args...)
	}
	f.backend.pause = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
	if err := f.backend.actionWithWarnings(context.Background(), id, "restart", true); err == nil {
		t.Fatal("countdown failure accepted")
	}
}
func TestInstanceCrossplayRenderingAndReservations(t *testing.T) {
	for _, software := range []string{"paper", "purpur"} {
		t.Run(software, func(t *testing.T) {
			store, snapshot, configs := instanceRuntimeFixture(t)
			configs[0].Software = software
			configs[0].BedrockEnabled = true
			configs[0].BedrockPort = 19133
			files, err := store.renderInstanceRuntimes(snapshot, configs)
			if err != nil {
				t.Fatal(err)
			}
			q := string(files[0].Quadlet)
			for _, want := range []string{"PublishPort=19133:19132/udp", "instance-crossplay-start " + configs[0].ID, "Before=justvoxel-minecraft-shutdown-guard.service", "Volume=" + files[0].DataPath + ":/data:Z"} {
				if !strings.Contains(q, want) {
					t.Fatal("missing", want)
				}
			}
			if strings.Contains(string(files[1].Quadlet), "PublishPort=19133") || files[0].DataPath == files[1].DataPath {
				t.Fatal("shared cross-play runtime")
			}
			configs[0].BedrockPort = 19132
			if _, err = store.renderInstanceRuntimes(snapshot, configs); err != nil {
				t.Fatal("unused default UDP port rejected", err)
			}
			configs[0].BedrockPort = 19133
			configs[1].Software = "purpur"
			configs[1].BedrockEnabled = true
			configs[1].BedrockPort = 19133
			if _, err = store.renderInstanceRuntimes(snapshot, configs); err == nil {
				t.Fatal("stopped-instance UDP collision accepted")
			}
		})
	}
}
func TestInstanceUDPFirewallOwnershipAndRollback(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	id := f.configs[0].ID
	old, err := f.backend.readRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	next := old
	next.Runtime.BedrockEnabled = true
	next.Runtime.BedrockPort = 19133
	// An already-open permanent UDP port must not be adopted or removed.
	external := instanceFirewallRule{Zone: "public", Port: 19133, Permanent: true, Protocol: "udp"}
	f.firewall[external] = true
	changes, owned, err := f.backend.planFirewall(context.Background(), old, next)
	if err != nil {
		t.Fatal(err)
	}
	if instanceRuleContains(owned, external) {
		t.Fatal("adopted external UDP rule")
	}
	for _, change := range changes {
		if err = f.backend.changeFirewall(context.Background(), change); err != nil {
			t.Fatal(err)
		}
	}
	next.Firewall = owned
	changes, _, err = f.backend.planFirewall(context.Background(), next, old)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range changes {
		if err = f.backend.changeFirewall(context.Background(), change); err != nil {
			t.Fatal(err)
		}
	}
	if !f.firewall[external] {
		t.Fatal("removed unowned UDP rule")
	}
	for rule, present := range f.firewall {
		if present && rule.protocol() == "udp" && rule != external {
			t.Fatal("owned UDP rule leaked")
		}
	}
	if !instanceRuleForRuntime(instanceFirewallRule{Port: old.Runtime.JavaPort}, old.Runtime) {
		t.Fatal("Phase 3A TCP ownership rejected")
	}
}
func TestInstanceVersionChangeRequiresEmptyData(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	id := f.configs[0].ID
	old, err := f.backend.readRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(old.Storage.DataPath, "level.dat"), []byte("world"), 0600); err != nil {
		t.Fatal(err)
	}
	next := old
	next.Runtime.Version = "1.21.5"
	records, err := f.backend.records()
	if err != nil {
		t.Fatal(err)
	}
	if err = f.backend.apply(context.Background(), old, instanceReplaceRecord(records, next), f.snapshot); err == nil || !strings.Contains(err.Error(), "Phase 4") {
		t.Fatal("existing-world version change accepted", err)
	}
	got, err := f.backend.readRecord(id)
	if err != nil || got.Runtime.Version != old.Runtime.Version {
		t.Fatal("refusal changed version")
	}
	if err = os.Remove(filepath.Join(old.Storage.DataPath, "level.dat")); err != nil {
		t.Fatal(err)
	}
	if err = f.backend.apply(context.Background(), old, instanceReplaceRecord(records, next), f.snapshot); err != nil {
		t.Fatal("empty-instance version change refused", err)
	}
}
func TestInstanceMaintenanceAuthenticationAndBounds(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	id := f.configs[0].ID
	previous := instanceRuntimes
	instanceRuntimes = f.backend
	t.Cleanup(func() { instanceRuntimes = previous })
	s := adminServerForTest()
	mux := http.NewServeMux()
	registerInstanceRuntimeRoutes(mux, s)
	for _, route := range []struct{ method, path, body string }{
		{"GET", "players", ""}, {"GET", "logs", ""}, {"GET", "version", ""},
		{"POST", "console", `{"command":"say hello"}`}, {"POST", "version/verify", `{"version":"1.21.5"}`},
	} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(route.method, "/v1/minecraft/instances/"+id+"/"+route.path, strings.NewReader(route.body)))
		if rr.Code != http.StatusUnauthorized {
			t.Fatal("unauthenticated maintenance", route.path, rr.Code)
		}
	}
	for _, command := range []string{"stop", "op Alex", "say hello\nstop", "say ", strings.Repeat("a", 257)} {
		payload, _ := json.Marshal(map[string]string{"command": command})
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/console", string(payload)))
		if rr.Code != http.StatusBadRequest {
			t.Fatal("unsafe console accepted", command, rr.Code)
		}
	}
	secret := f.configs[0].RCONPassword
	text := instanceDiagnosticText([]byte("RCON_PASSWORD="+secret+"\n"+secret+"\nTOKEN: private\nENVIRONMENT: private\nServer ready\n"+strings.Repeat("x", 65536)), secret)
	if len(text) > 16384+len("\n[truncated]") || !strings.HasSuffix(text, "\n[truncated]") {
		t.Fatal("unbounded diagnostics or missing truncation marker")
	}
	for _, sensitive := range []string{secret, "RCON_PASSWORD=", "TOKEN: private", "ENVIRONMENT: private"} {
		if strings.Contains(text, sensitive) {
			t.Fatal("sensitive diagnostic content retained")
		}
	}
	wantPrefix := "[private diagnostic line omitted]\n[redacted]\n[private diagnostic line omitted]\n[private diagnostic line omitted]\nServer ready\n"
	if !strings.HasPrefix(text, wantPrefix) {
		t.Fatal("incorrect omission markers or harmless diagnostics lost")
	}
	if got := instanceDiagnosticText([]byte(secret[:10]+"\x00"+secret[10:]), secret); got != "[redacted]" {
		t.Fatal("control normalization exposed credential")
	}
	base := f.backend.command
	f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "journalctl" {
			if len(args) != 7 || args[1] != "-u" || args[2] != "minecraft-"+id+".service" {
				t.Fatal("wrong log target")
			}
			return []byte("RCON_PASSWORD=" + secret + "\nServer ready"), nil
		}
		return base(ctx, command, args...)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest("GET", "/v1/minecraft/instances/"+id+"/logs", ""))
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), secret) || !strings.Contains(rr.Body.String(), "Server ready") {
		t.Fatal("log response", rr.Code, rr.Body.String())
	}
	// Runtime/console failures never echo command stderr, credentials or argv.
	f.backend.command = func(context.Context, string, ...string) ([]byte, error) { return []byte(secret), errors.New(secret) }
	_, err := f.backend.rcon(context.Background(), id, "list")
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("RCON failure leaked secret")
	}
}

func TestInstanceConsoleRolesAndMaintenanceLock(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	id := f.configs[0].ID
	oldBackend, oldLock := instanceRuntimes, pluginRuntimeLockPath
	instanceRuntimes = f.backend
	pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
	t.Cleanup(func() { instanceRuntimes = oldBackend; pluginRuntimeLockPath = oldLock })
	for _, role := range []principalRole{roleViewer, roleOperator} {
		mux := http.NewServeMux()
		registerInstanceRuntimeRoutes(mux, roleServerForTest(role))
		for _, path := range []string{"console", "version/verify"} {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/"+path, `{}`))
			if rr.Code != http.StatusForbidden {
				t.Fatal("role gained console/version mutation", role, path, rr.Code)
			}
		}
		for _, path := range []string{"players", "version"} {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, authorizedRequest("GET", "/v1/minecraft/instances/"+id+"/"+path, ""))
			if rr.Code != http.StatusOK {
				t.Fatal("role lost read access", role, path, rr.Code)
			}
		}
	}
	s := adminServerForTest()
	s.operations = openTestOperationStore(t)
	mux := http.NewServeMux()
	registerInstanceRuntimeRoutes(mux, s)
	lock, err := acquirePluginRuntimeLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/console", `{"command":"say hello"}`))
	if rr.Code != http.StatusConflict {
		t.Fatal("console bypassed maintenance lock", rr.Code)
	}
}

func TestIndependentRestartQuotaCooldownAndAudit(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	a, b := f.configs[0].ID, f.configs[1].ID
	for _, id := range []string{a, b} {
		if err := f.backend.action(context.Background(), id, "start"); err != nil {
			t.Fatal(err)
		}
	}
	previous, previousLock := instanceRuntimes, pluginRuntimeLockPath
	instanceRuntimes = f.backend
	pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
	t.Cleanup(func() { instanceRuntimes = previous; pluginRuntimeLockPath = previousLock })
	store, _ := openTestWebUIStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return now }
	user, err := store.createWebUser("InstanceOperator", webUserRoleOperator, "operator-password")
	if err != nil {
		t.Fatal(err)
	}
	s := operatorServerForStore(store, user)
	s.operations = openTestOperationStore(t)
	mux := http.NewServeMux()
	registerInstanceRuntimeRoutes(mux, s)
	restart := func(id string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/restart", `{}`))
		return rr
	}
	f.players = "There are 1 of a max of 20 players online: Alex"
	confirmationCalls := len(f.calls)
	if rr := restart(a); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"confirmation_required":true`) || !strings.Contains(rr.Body.String(), `"reason":"players_online"`) {
		t.Fatal("online-player confirmation missing", rr.Code, rr.Body.String())
	}
	used, last, err := store.operatorRestartUsage(user.ID)
	if err != nil || used != 0 || last != nil {
		t.Fatal("online-player confirmation consumed quota or advanced cooldown", used, last, err)
	}
	var globalLast string
	if err = store.db.QueryRow(`SELECT COALESCE(last_restart_at, '') FROM operator_global_state WHERE id=1`).Scan(&globalLast); err != nil || globalLast != "" {
		t.Fatal("online-player confirmation advanced global cooldown", globalLast, err)
	}
	var confirmationAudits int
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'minecraft_restart' AND success = 1`).Scan(&confirmationAudits); err != nil || confirmationAudits != 0 {
		t.Fatal("online-player confirmation wrote successful restart audit", confirmationAudits, err)
	}
	for _, call := range f.calls[confirmationCalls:] {
		if strings.Contains(call, "say Server shutting down") || strings.HasPrefix(call, "systemctl ") && !strings.HasPrefix(call, "systemctl show ") {
			t.Fatal("online-player confirmation executed restart", call)
		}
		if strings.Contains(call, "minecraft-"+b) {
			t.Fatal("online-player confirmation touched sibling instance", call)
		}
	}
	if f.states[a] != "active" || f.containers[a] != "running" || f.states[b] != "active" || f.containers[b] != "running" {
		t.Fatal("online-player confirmation changed running instance state")
	}
	f.players = "There are 0 of a max of 20 players online:"
	if rr := restart(a); rr.Code != http.StatusOK {
		t.Fatal(rr.Code, rr.Body.String())
	}
	used, _, err = store.operatorRestartUsage(user.ID)
	if err != nil || used != 1 {
		t.Fatal("accepted restart did not consume quota", used, err)
	}
	before := len(f.calls)
	rr := restart(b)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("other instance bypassed global cooldown: status=%d body=%q headers=%v", rr.Code, rr.Body.Bytes(), rr.Header())
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatalf("cooldown response missing Retry-After: status=%d body=%q headers=%v", rr.Code, rr.Body.Bytes(), rr.Header())
	}
	var cooldown struct {
		Reason            string `json:"reason"`
		RestartUsed       int    `json:"restart_used"`
		RestartLimit      int    `json:"restart_limit"`
		RetryAfterSeconds int    `json:"retry_after_seconds"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &cooldown); err != nil {
		t.Fatalf("decode cooldown response: %v; status=%d body=%q headers=%v", err, rr.Code, rr.Body.Bytes(), rr.Header())
	}
	if cooldown.Reason != "restart_cooldown" || cooldown.RestartUsed != 1 || cooldown.RestartLimit != operatorRestartLimit || cooldown.RetryAfterSeconds <= 0 {
		t.Fatalf("incorrect cooldown response: %+v; status=%d body=%q headers=%v", cooldown, rr.Code, rr.Body.Bytes(), rr.Header())
	}
	if len(f.calls) != before {
		t.Fatal("cooldown invoked instance commands")
	}
	used, last, err = store.operatorRestartUsage(user.ID)
	if err != nil || used != 1 || last == nil || !last.Equal(now) {
		t.Fatal("cooldown rejection changed allowance or restart time", used, last, err)
	}
	if f.states[a] != "active" || f.containers[a] != "running" || f.states[b] != "active" || f.containers[b] != "running" {
		t.Fatal("cooldown rejection changed running instance state")
	}
	var audited int
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'minecraft_restart' AND target = ? AND success = 1`, b).Scan(&audited); err != nil || audited != 0 {
		t.Fatal("cooldown rejection wrote successful restart audit", audited, err)
	}
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'minecraft_restart' AND target = ? AND success = 1`, a).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 1 {
		t.Fatal("restart target audit missing", audited)
	}
	// Cooldown is global even though execution and audit target the selected ID.
	now = now.Add(operatorRestartCooldown + time.Minute)
	if rr := restart(b); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"id":"`+b+`"`) {
		t.Fatal(rr.Code, rr.Body.String())
	}
	for _, call := range f.calls[before:] {
		if strings.Contains(call, "minecraft-"+a) {
			t.Fatal("selected restart touched sibling instance", call)
		}
	}
	if !strings.Contains(strings.Join(f.calls[before:], "\n"), "systemctl --no-block start -- minecraft-"+b+".service") {
		t.Fatal("selected restart did not start selected instance")
	}
	if f.states[a] != "active" || f.containers[a] != "running" || f.states[b] != "active" || f.containers[b] != "running" {
		t.Fatal("selected restart changed sibling state or left selected instance stopped")
	}
	used, _, err = store.operatorRestartUsage(user.ID)
	if err != nil || used != 2 {
		t.Fatal("selected restart did not consume global operator allowance", used, err)
	}
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'minecraft_restart' AND target = ? AND success = 1`, b).Scan(&audited); err != nil || audited != 1 {
		t.Fatal("selected restart target audit missing", audited, err)
	}
	now = now.Add(16 * time.Minute)
	if rr := restart(a); rr.Code != http.StatusConflict {
		t.Fatal("exhausted quota accepted", rr.Code)
	}
}

func TestInstanceUDPActivationFailureAndRecovery(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	id := f.configs[0].ID
	old, err := f.backend.readRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := net.ListenPacket("udp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := packet.LocalAddr().(*net.UDPAddr).Port
	packet.Close()
	next := old
	next.Runtime.BedrockEnabled = true
	next.Runtime.BedrockPort = port
	records, err := f.backend.records()
	if err != nil {
		t.Fatal(err)
	}
	f.failReload = 2
	if err = f.backend.apply(context.Background(), old, instanceReplaceRecord(records, next), f.snapshot); !errors.Is(err, errInstanceRecovery) {
		t.Fatal("incomplete UDP rollback did not retain recovery", err)
	}
	pending, err := f.backend.pending(id)
	if err != nil || !pending {
		t.Fatal("lost pending evidence", err)
	}
	if err = f.backend.recover(context.Background(), id); err != nil {
		t.Fatal("UDP rollback retry failed", err)
	}
	restored, err := f.backend.readRecord(id)
	if err != nil || restored.Runtime.BedrockEnabled {
		t.Fatal("rollback changed cross-play configuration", err)
	}
	for rule, present := range f.firewall {
		if present && rule.protocol() == "udp" {
			t.Fatal("UDP firewall rollback leaked rule", rule)
		}
	}
	if err = f.backend.apply(context.Background(), restored, instanceReplaceRecord(records, next), f.snapshot); err != nil {
		t.Fatal("UDP activation retry failed", err)
	}
	committed, err := f.backend.readRecord(id)
	if err != nil || !committed.Runtime.BedrockEnabled {
		t.Fatal("UDP activation did not commit", err)
	}
	if len(committed.Firewall) != 4 {
		t.Fatal("expected TCP/UDP runtime and permanent ownership", committed.Firewall)
	}
}
