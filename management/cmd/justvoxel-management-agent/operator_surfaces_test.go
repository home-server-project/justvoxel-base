package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func surfaceTestServer(t *testing.T, role principalRole) *server {
	t.Helper()
	store, _ := openTestWebUIStore(t)
	now := time.Now()
	return &server{store: store, sessions: map[string]session{
		"token": {Username: "tester", Role: role, AuthSource: authSourceWebUI, WebUserID: 1, Created: now, LastSeen: now},
	}}
}

func surfaceRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if strings.HasPrefix(path, "/v1/minecraft/instances/") {
		parts := strings.Split(req.URL.Path, "/")
		if len(parts) > 4 {
			req.SetPathValue("id", parts[4])
		}
	}
	req.Header.Set("Authorization", "Bearer token")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func whitelistSurfaceFixture(t *testing.T) *instanceActivationFixture {
	t.Helper()
	f := newInstanceActivationFixture(t)
	old := instanceRuntimes
	instanceRuntimes = f.backend
	t.Cleanup(func() { instanceRuntimes = old })
	if err := f.apply("jv-a1b2c3"); err != nil {
		t.Fatal(err)
	}
	return f
}
func selectedWhitelistTestArgs(t *testing.T, args []string) []string {
	t.Helper()
	if len(args) < 3 || args[0] != "--instance" || args[1] != "jv-a1b2c3" {
		t.Fatalf("wrong immutable whitelist target: %#v", args)
	}
	return args[2:]
}
func serveWhitelistSurface(s *server, w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	registerOperationalRoutes(mux, s)
	registerInstanceMaintenanceRoutes(mux, s)
	mux.ServeHTTP(w, r)
}

func TestLocalRootCanUseWhitelistWithoutBearerSession(t *testing.T) {
	fixture := whitelistSurfaceFixture(t)
	_ = fixture
	oldDiscovery := runAdminDiscoveryHelper
	defer func() { runAdminDiscoveryHelper = oldDiscovery }()
	runAdminDiscoveryHelper = func(_ context.Context, action string) ([]byte, error) {
		if action != "configuration" {
			t.Fatal(action)
		}
		return []byte(`{"configured":true,"minecraft":{"whitelist_enabled":true}}`), nil
	}

	s := &server{sessions: make(map[string]session)}
	old := runWhitelistHelper
	defer func() { runWhitelistHelper = old }()

	calls := make([][]string, 0, 3)
	runWhitelistHelper = func(_ context.Context, args ...string) ([]byte, error) {
		args = selectedWhitelistTestArgs(t, args)
		calls = append(calls, append([]string(nil), args...))
		if len(args) == 1 && args[0] == "list" {
			if len(calls) == 1 {
				return []byte("There are 0 whitelisted player(s):\n"), nil
			}
			return []byte("There are 0 whitelisted player(s):\n"), nil
		}
		if len(args) == 2 && args[0] == "add-java" && args[1] == "Alex" {
			return []byte("Added Alex to the whitelist\nThere are 1 whitelisted player(s): Alex\n"), nil
		}
		t.Fatalf("unexpected helper args: %#v", args)
		return nil, nil
	}

	req := requestWithPeerUID(http.MethodGet, "http://unix/v1/minecraft/instances/jv-a1b2c3/whitelist", 0)
	rr := httptest.NewRecorder()
	serveWhitelistSurface(s, rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("local root whitelist list failed: %d %s", rr.Code, rr.Body.String())
	}

	req = requestWithPeerUID(http.MethodPost, "http://unix/v1/minecraft/instances/jv-a1b2c3/whitelist", 0)
	req.Body = io.NopCloser(strings.NewReader(`{"platform":"java","action":"add","name":"Alex"}`))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	serveWhitelistSurface(s, rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("local root whitelist change failed: %d %s", rr.Code, rr.Body.String())
	}
	if len(calls) != 3 || calls[2][0] != "add-java" || calls[2][1] != "Alex" {
		t.Fatalf("unexpected whitelist helper calls: %#v", calls)
	}
}

func TestLocalRootCanReadMinecraftLogsWithoutBearerSession(t *testing.T) {
	whitelistSurfaceFixture(t)
	s := &server{sessions: make(map[string]session)}
	old := readMinecraftLogs
	defer func() { readMinecraftLogs = old }()

	gotLimit := 0
	gotMode := ""
	readMinecraftLogs = func(_ context.Context, unit string, limit int, outputMode string) ([]byte, error) {
		if unit != "minecraft-jv-a1b2c3.service" {
			t.Fatal("wrong log target", unit)
		}
		gotLimit = limit
		if unit != "minecraft-jv-a1b2c3.service" {
			t.Fatal("wrong log target", unit)
		}
		gotMode = outputMode
		return []byte("line one\nline two\n"), nil
	}

	req := requestWithPeerUID(http.MethodGet, "http://unix/v1/minecraft/instances/jv-a1b2c3/logs?limit=50&format=cat", 0)
	rr := httptest.NewRecorder()
	serveWhitelistSurface(s, rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("local root Minecraft logs returned %d: %s", rr.Code, rr.Body.String())
	}
	if gotLimit != 50 || gotMode != "cat" {
		t.Fatalf("local root logs reader args = limit=%d mode=%q", gotLimit, gotMode)
	}
	if !strings.Contains(rr.Body.String(), `"line one"`) || !strings.Contains(rr.Body.String(), `"line two"`) {
		t.Fatalf("unexpected local root logs response: %s", rr.Body.String())
	}
}

func TestMinecraftLogsDefaultFormatRemainsShortISO(t *testing.T) {
	whitelistSurfaceFixture(t)
	s := surfaceTestServer(t, roleOperator)
	old := readMinecraftLogs
	defer func() { readMinecraftLogs = old }()

	gotMode := ""
	readMinecraftLogs = func(_ context.Context, unit string, _ int, outputMode string) ([]byte, error) {
		if unit != "minecraft-jv-a1b2c3.service" {
			t.Fatal("wrong log target", unit)
		}
		gotMode = outputMode
		return []byte("timestamped line\n"), nil
	}

	rr := httptest.NewRecorder()
	serveWhitelistSurface(s, rr, surfaceRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/logs", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("default Minecraft logs returned %d: %s", rr.Code, rr.Body.String())
	}
	if gotMode != "short-iso" {
		t.Fatalf("default Minecraft logs mode = %q, want short-iso", gotMode)
	}
}

func TestMinecraftLogsRejectUnsupportedFormat(t *testing.T) {
	whitelistSurfaceFixture(t)
	s := surfaceTestServer(t, roleOperator)
	old := readMinecraftLogs
	defer func() { readMinecraftLogs = old }()

	called := false
	readMinecraftLogs = func(_ context.Context, unit string, _ int, _ string) ([]byte, error) {
		called = true
		return nil, nil
	}

	rr := httptest.NewRecorder()
	serveWhitelistSurface(s, rr, surfaceRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/logs?format=json", ""))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unsupported Minecraft logs format returned %d: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("unsupported Minecraft logs format reached journal reader")
	}
}

func TestViewerCannotAccessWhitelistOrRawMinecraftLogs(t *testing.T) {
	fixture := whitelistSurfaceFixture(t)
	_ = fixture
	s := surfaceTestServer(t, roleViewer)

	rr := httptest.NewRecorder()
	serveWhitelistSurface(s, rr, surfaceRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/whitelist", ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer whitelist status = %d, want 403", rr.Code)
	}

	rr = httptest.NewRecorder()
	serveWhitelistSurface(s, rr, surfaceRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/logs", ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer logs status = %d, want 403", rr.Code)
	}
}

func TestOperatorWhitelistUsesOnlyFixedHelperActions(t *testing.T) {
	fixture := whitelistSurfaceFixture(t)
	_ = fixture
	s := surfaceTestServer(t, roleOperator)
	old := runWhitelistHelper
	defer func() { runWhitelistHelper = old }()
	calls := make([][]string, 0, 2)
	runWhitelistHelper = func(_ context.Context, args ...string) ([]byte, error) {
		args = selectedWhitelistTestArgs(t, args)
		calls = append(calls, append([]string(nil), args...))
		if len(args) == 1 && args[0] == "list" {
			return []byte("There are 0 whitelisted player(s):\n"), nil
		}
		return []byte("Added Alex\n"), nil
	}

	rr := httptest.NewRecorder()
	serveWhitelistSurface(s, rr, surfaceRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/whitelist", `{"platform":"java","action":"add","name":"Alex"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("operator whitelist status = %d: %s", rr.Code, rr.Body.String())
	}
	if len(calls) != 2 || len(calls[1]) != 2 || calls[1][0] != "add-java" || calls[1][1] != "Alex" {
		t.Fatalf("helper calls = %#v", calls)
	}
}

func TestAdministratorCanAddFirstJavaWhitelistEntry(t *testing.T) {
	fixture := whitelistSurfaceFixture(t)
	_ = fixture
	s := surfaceTestServer(t, roleAdministrator)
	old := runWhitelistHelper
	defer func() { runWhitelistHelper = old }()
	calls := make([][]string, 0, 2)
	runWhitelistHelper = func(_ context.Context, args ...string) ([]byte, error) {
		args = selectedWhitelistTestArgs(t, args)
		calls = append(calls, append([]string(nil), args...))
		if len(args) == 1 && args[0] == "list" {
			return []byte("There are 0 whitelisted player(s):\n"), nil
		}
		if len(args) != 2 || args[0] != "add-java" || args[1] != "FirstPlayer" {
			t.Fatalf("helper args = %#v", args)
		}
		return []byte("Added FirstPlayer to the whitelist\nThere are 1 whitelisted player(s): FirstPlayer\n"), nil
	}

	rr := httptest.NewRecorder()
	serveWhitelistSurface(s, rr, surfaceRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/whitelist", `{"platform":"java","action":"add","name":"FirstPlayer"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("administrator whitelist status = %d: %s", rr.Code, rr.Body.String())
	}
	if len(calls) != 2 {
		t.Fatalf("helper calls = %#v, want list then add-java", calls)
	}
}

func TestBedrockWhitelistStripsFloodgatePrefix(t *testing.T) {
	fixture := whitelistSurfaceFixture(t)
	_ = fixture
	s := surfaceTestServer(t, roleOperator)
	old := runWhitelistHelper
	defer func() { runWhitelistHelper = old }()
	calls := make([][]string, 0, 2)
	runWhitelistHelper = func(_ context.Context, args ...string) ([]byte, error) {
		args = selectedWhitelistTestArgs(t, args)
		calls = append(calls, append([]string(nil), args...))
		if len(args) == 1 && args[0] == "list" {
			return []byte("There are 0 whitelisted player(s):\n"), nil
		}
		return []byte("Added CatchaLlama\n"), nil
	}

	rr := httptest.NewRecorder()
	serveWhitelistSurface(s, rr, surfaceRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/whitelist", `{"platform":"bedrock","action":"add","name":".CatchaLlama"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("bedrock whitelist status = %d: %s", rr.Code, rr.Body.String())
	}
	if len(calls) != 2 || len(calls[1]) != 2 || calls[1][0] != "add-bedrock" || calls[1][1] != "CatchaLlama" {
		t.Fatalf("helper calls = %#v", calls)
	}
}

func TestBedrockFloodgateCacheErrorIsFriendly(t *testing.T) {
	whitelistSurfaceFixture(t)
	s := surfaceTestServer(t, roleOperator)
	old := runWhitelistHelper
	defer func() { runWhitelistHelper = old }()
	runWhitelistHelper = func(_ context.Context, args ...string) ([]byte, error) {
		args = selectedWhitelistTestArgs(t, args)
		if len(args) == 1 && args[0] == "list" {
			return []byte("There are 0 whitelisted player(s):\n"), nil
		}
		return []byte("Got an error from requesting the xuid of a Bedrock player: Unable to find user in our cache. Please try specifying their Floodgate UUID instead\n"), errors.New("exit status 1")
	}

	rr := httptest.NewRecorder()
	serveWhitelistSurface(s, rr, surfaceRequest(http.MethodPost, "/v1/minecraft/instances/jv-a1b2c3/whitelist", `{"platform":"bedrock","action":"add","name":"CatchaLlama"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bedrock error status = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Floodgate could not resolve this Bedrock player", "Floodgate UUID"} {
		if !strings.Contains(body, want) {
			t.Fatalf("friendly error missing %q: %s", want, body)
		}
	}
	if strings.Contains(strings.ToLower(body), "xuid") || strings.Contains(strings.ToLower(body), "our cache") {
		t.Fatalf("raw Floodgate error leaked: %s", body)
	}
}

func TestViewerGetsSanitizedActivityOnly(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	now := time.Now()
	s := &server{store: store, sessions: map[string]session{
		"token": {Username: "viewer", Role: roleViewer, AuthSource: authSourceWebUI, WebUserID: 1, Created: now, LastSeen: now},
	}}
	actor := session{Username: "operator-one", Role: roleOperator, AuthSource: authSourceWebUI, WebUserID: 7}
	if err := store.recordAuditEvent(actor, "minecraft_restart", "minecraft.service", false, "restart_cooldown private context"); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	s.publicActivity(rr, surfaceRequest(http.MethodGet, "/v1/activity", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("activity status = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "minecraft_restart") {
		t.Fatalf("activity missing action: %s", body)
	}
	for _, secret := range []string{"operator-one", "minecraft.service", "private context"} {
		if strings.Contains(body, secret) {
			t.Fatalf("sanitized activity leaked %q: %s", secret, body)
		}
	}
}

func TestWhitelistListExposesOnlyAuthoritativeStateForOperators(t *testing.T) {
	fixture := whitelistSurfaceFixture(t)
	_ = fixture
	oldDiscovery, oldWhitelist := runAdminDiscoveryHelper, runWhitelistHelper
	defer func() { runAdminDiscoveryHelper, runWhitelistHelper = oldDiscovery, oldWhitelist }()
	for _, enabled := range []bool{true, false} {
		record, err := fixture.backend.readRecord("jv-a1b2c3")
		if err != nil {
			t.Fatal(err)
		}
		record.Runtime.Whitelist = enabled
		dir, _, _ := fixture.backend.paths("jv-a1b2c3")
		if err = fixture.backend.writeJSON(dir+"/config.json", record, true); err != nil {
			t.Fatal(err)
		}
		runAdminDiscoveryHelper = func(_ context.Context, action string) ([]byte, error) {
			if action != "configuration" {
				t.Fatal(action)
			}
			return []byte(fmt.Sprintf(`{"configured":true,"minecraft":{"whitelist_enabled":%t,"enforce_whitelist":true,"motd":"private-configuration"}}`, enabled)), nil
		}
		runWhitelistHelper = func(_ context.Context, args ...string) ([]byte, error) {
			args = selectedWhitelistTestArgs(t, args)
			return []byte("There are 0 whitelisted player(s):"), nil
		}
		s := surfaceTestServer(t, roleOperator)
		rr := httptest.NewRecorder()
		serveWhitelistSurface(s, rr, surfaceRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/whitelist", ""))
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), fmt.Sprintf(`"whitelist_enabled":%t`, enabled)) || strings.Contains(rr.Body.String(), "private-configuration") {
			t.Fatal(rr.Body.String())
		}
		runWhitelistHelper = func(_ context.Context, args ...string) ([]byte, error) {
			args = selectedWhitelistTestArgs(t, args)
			return []byte("Minecraft must be running for whitelist changes."), fmt.Errorf("stopped")
		}
		rr = httptest.NewRecorder()
		serveWhitelistSurface(s, rr, surfaceRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/whitelist", ""))
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), fmt.Sprintf(`"whitelist_enabled":%t`, enabled)) || !strings.Contains(rr.Body.String(), "Whitelist list unavailable") {
			t.Fatal(rr.Body.String())
		}
	}
}

func TestWhitelistObsoleteAndStaleRoutesNeverReachHelper(t *testing.T) {
	whitelistSurfaceFixture(t)
	s := surfaceTestServer(t, roleOperator)
	old := runWhitelistHelper
	runWhitelistHelper = func(context.Context, ...string) ([]byte, error) {
		t.Fatal("invalid target reached helper")
		return nil, nil
	}
	t.Cleanup(func() { runWhitelistHelper = old })
	for _, path := range []string{"/v1/whitelist", "/v1/minecraft/instances/original/whitelist", "/v1/minecraft/instances/jv-absent1/whitelist"} {
		rr := httptest.NewRecorder()
		serveWhitelistSurface(s, rr, surfaceRequest("GET", path, ""))
		if rr.Code < 400 {
			t.Fatal(path, rr.Code, rr.Body.String())
		}
	}
}

func TestMinecraftLogsRejectInvalidAndRetiredSelections(t *testing.T) {
	whitelistSurfaceFixture(t)
	s := surfaceTestServer(t, roleOperator)
	old := readMinecraftLogs
	defer func() { readMinecraftLogs = old }()
	readMinecraftLogs = func(context.Context, string, int, string) ([]byte, error) {
		t.Error("invalid selection reached journal")
		return nil, nil
	}
	for _, path := range []string{"/v1/logs/minecraft", "/v1/minecraft/instances/original/logs", "/v1/minecraft/instances/jv-absent1/logs", "/v1/minecraft/instances/invalid/logs"} {
		rr := httptest.NewRecorder()
		serveWhitelistSurface(s, rr, surfaceRequest("GET", path, ""))
		if rr.Code < 400 {
			t.Fatal(path, rr.Code)
		}
	}
}

func TestMinecraftSelectedLogsBoundsAndRoles(t *testing.T) {
	whitelistSurfaceFixture(t)
	old := readMinecraftLogs
	defer func() { readMinecraftLogs = old }()
	for _, role := range []principalRole{roleAdministrator, roleOperator} {
		s := surfaceTestServer(t, role)
		for _, tc := range []struct {
			query string
			limit int
			mode  string
		}{
			{"", 100, "short-iso"}, {"?limit=1&format=cat", 1, "cat"}, {"?limit=500&format=short-iso", 200, "short-iso"},
		} {
			calls := 0
			readMinecraftLogs = func(_ context.Context, unit string, limit int, mode string) ([]byte, error) {
				calls++
				if unit != "minecraft-jv-a1b2c3.service" || limit != tc.limit || mode != tc.mode {
					t.Error(unit, limit, mode)
				}
				return []byte("selected server log"), nil
			}
			rr := httptest.NewRecorder()
			serveWhitelistSurface(s, rr, surfaceRequest("GET", "/v1/minecraft/instances/jv-a1b2c3/logs"+tc.query, ""))
			if rr.Code != 200 || calls != 1 {
				t.Fatal(role, rr.Code, calls, rr.Body.String())
			}
		}
	}
}
