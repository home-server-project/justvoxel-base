package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const validAdminSetupPlanRequest = `{
  "server":{"motd":"Family server","max_players":10,"bedrock_enabled":true,"timezone":"America/Toronto"},
  "minecraft":{"java_memory":"4G","container_memory":"6G","java_port":25565,"bedrock_port":19132,"image_tag":"stable","version_policy":"recommended","version":""},
  "storage":{"type":"system","path":"/var/lib/justvoxel/minecraft","device":"","mount_point":""},
  "backups":{"automatic":true,"daily_time":"04:30","keep":7,"type":"system","path":"/var/lib/justvoxel/backups","device":"","mount_point":"","source":"","username":"","domain":""}
}`

const validAdminSetupPlanResponse = `{
  "ok":true,
  "schema_version":"v1",
  "normalized":{
    "server":{"motd":"Family server","max_players":10,"bedrock_enabled":true,"timezone":"America/Toronto"},
    "minecraft":{"java_memory":"4G","container_memory":"6G","java_port":25565,"bedrock_port":19132,"image_tag":"stable","requested_version_policy":"recommended","version_policy":"pinned","version":"1.21.8","system_memory_mib":8192,"system_reserve_mib":2048,"minecraft_uid":1000,"minecraft_gid":1000},
    "storage":{"type":"system","path":"/var/lib/justvoxel/minecraft","model":"JustVoxel system storage","system_disk":true},
    "backups":{"type":"system","path":"/var/lib/justvoxel/backups","model":"JustVoxel system storage","system_disk":true,"credentials_required":false,"automatic":true,"daily_time":"04:30","schedule":"*-*-* 04:30:00","keep":7}
  },
  "warnings":[{"code":"same_physical_disk","message":"Minecraft data and backups use JustVoxel system storage."}],
  "requirements":{"smb_password_required":false,"network_backup_validation_on_apply":false}
}`

func TestAdminSetupPlanRequiresAdministratorBeforeHelper(t *testing.T) {
	old := runAdminSetupPlanHelper
	defer func() { runAdminSetupPlanHelper = old }()
	called := false
	runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		called = true
		return []byte(validAdminSetupPlanResponse), nil
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		rr := httptest.NewRecorder()
		s.adminSetupPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/plan", validAdminSetupPlanRequest))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s status = %d, want 403", role, rr.Code)
		}
	}
	if called {
		t.Fatal("first-run setup helper ran for non-Administrator")
	}
}

func TestAdminSetupPlanReturnsStructuredNormalizedPlan(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminSetupPlanHelper
	defer func() { runAdminSetupPlanHelper = old }()

	runAdminSetupPlanHelper = func(_ context.Context, payload []byte) ([]byte, error) {
		body := string(payload)
		for _, want := range []string{`"version_policy":"recommended"`, `"automatic":true`, `"type":"system"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("helper payload missing %s: %s", want, body)
			}
		}
		if strings.Contains(strings.ToLower(body), "password") {
			t.Fatalf("planning payload unexpectedly contains password data: %s", body)
		}
		return []byte(validAdminSetupPlanResponse), nil
	}

	rr := httptest.NewRecorder()
	s.adminSetupPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/plan", validAdminSetupPlanRequest))
	if rr.Code != http.StatusOK {
		t.Fatalf("plan status = %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`"schema_version":"v1"`, `"plan_fingerprint":"sha256:`, `"version_policy":"pinned"`, `"same_physical_disk"`, `"smb_password_required":false`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("plan response missing %s: %s", want, rr.Body.String())
		}
	}
	var response adminSetupPlanResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.PlanFingerprint) != len("sha256:")+64 {
		t.Fatalf("fingerprint = %q, want sha256 plus 64 hex characters", response.PlanFingerprint)
	}
}

func TestAdminSetupPlanFingerprintIsStableAndExecutionRelevant(t *testing.T) {
	var response adminSetupPlanResponse
	if err := json.Unmarshal([]byte(validAdminSetupPlanResponse), &response); err != nil {
		t.Fatal(err)
	}
	first, err := adminSetupPlanFingerprint(response.SchemaVersion, response.Normalized, response.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	second, err := adminSetupPlanFingerprint(response.SchemaVersion, response.Normalized, response.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("same normalized plan produced different fingerprints: %q != %q", first, second)
	}
	if !strings.HasPrefix(first, "sha256:") || len(first) != len("sha256:")+64 {
		t.Fatalf("unexpected fingerprint format: %q", first)
	}

	response.Normalized.Minecraft.Version = "1.21.9"
	changed, err := adminSetupPlanFingerprint(response.SchemaVersion, response.Normalized, response.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("resolved Minecraft version change did not change the reviewed-plan fingerprint")
	}

	response.Normalized.Minecraft.Version = "1.21.8"
	response.Normalized.Minecraft.MinecraftUID = 1001
	changed, err = adminSetupPlanFingerprint(response.SchemaVersion, response.Normalized, response.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("Minecraft runtime UID change did not change the reviewed-plan fingerprint")
	}

	response.Normalized.Minecraft.MinecraftUID = 1000
	response.Normalized.Storage.ExpectedUUID = "replacement-storage-uuid"
	changed, err = adminSetupPlanFingerprint(response.SchemaVersion, response.Normalized, response.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("storage identity change did not change the reviewed-plan fingerprint")
	}
}

func TestAdminSetupPlanRejectsMalformedUnknownTrailingAndOversizedRequests(t *testing.T) {
	old := runAdminSetupPlanHelper
	defer func() { runAdminSetupPlanHelper = old }()
	called := false
	runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		called = true
		return []byte(validAdminSetupPlanResponse), nil
	}

	cases := []struct {
		name string
		body string
	}{
		{name: "malformed", body: `{"server":`},
		{name: "unknown password field", body: strings.Replace(validAdminSetupPlanRequest, `"domain":""`, `"domain":"","password":"secret"`, 1)},
		{name: "trailing json", body: validAdminSetupPlanRequest + ` {}`},
		{name: "oversized", body: strings.Replace(validAdminSetupPlanRequest, `"Family server"`, `"`+strings.Repeat("x", 17000)+`"`, 1)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := surfaceTestServer(t, roleAdministrator)
			rr := httptest.NewRecorder()
			s.adminSetupPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/plan", tc.body))
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "invalid JSON request") {
				t.Fatalf("unexpected response: %s", rr.Body.String())
			}
		})
	}
	if called {
		t.Fatal("helper ran for a rejected request")
	}
}

func TestAdminSetupPlanHelperFailureIsSanitized(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminSetupPlanHelper
	defer func() { runAdminSetupPlanHelper = old }()
	runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("sensitive helper stderr: /etc/justvoxel/private"), errors.New("exit status 1")
	}

	rr := httptest.NewRecorder()
	s.adminSetupPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/plan", validAdminSetupPlanRequest))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "/etc/justvoxel/private") || !strings.Contains(rr.Body.String(), "first-run setup planning is unavailable") {
		t.Fatalf("helper details leaked or bounded error missing: %s", rr.Body.String())
	}
}

func TestAdminSetupPlanTimeoutIsBounded(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	oldRunner := runAdminSetupPlanHelper
	oldTimeout := adminSetupPlanTimeout
	defer func() {
		runAdminSetupPlanHelper = oldRunner
		adminSetupPlanTimeout = oldTimeout
	}()
	adminSetupPlanTimeout = 5 * time.Millisecond
	runAdminSetupPlanHelper = func(ctx context.Context, _ []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	rr := httptest.NewRecorder()
	s.adminSetupPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/plan", validAdminSetupPlanRequest))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "first-run setup planning is unavailable") {
		t.Fatalf("bounded timeout error missing: %s", rr.Body.String())
	}
}

func TestAdminSetupPlanRejectsMissingRuntimeIdentity(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminSetupPlanHelper
	defer func() { runAdminSetupPlanHelper = old }()
	runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		var response map[string]any
		if err := json.Unmarshal([]byte(validAdminSetupPlanResponse), &response); err != nil {
			t.Fatal(err)
		}
		normalized := response["normalized"].(map[string]any)
		minecraft := normalized["minecraft"].(map[string]any)
		delete(minecraft, "minecraft_uid")
		delete(minecraft, "minecraft_gid")
		return json.Marshal(response)
	}

	rr := httptest.NewRecorder()
	s.adminSetupPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/plan", validAdminSetupPlanRequest))
	if rr.Code != http.StatusInternalServerError || !strings.Contains(rr.Body.String(), "planner returned incomplete data") {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminSetupPlanRejectsUnexpectedHelperFields(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminSetupPlanHelper
	defer func() { runAdminSetupPlanHelper = old }()
	runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(`{"ok":true,"schema_version":"v1","unexpected":"value","warnings":[]}`), nil
	}

	rr := httptest.NewRecorder()
	s.adminSetupPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/plan", validAdminSetupPlanRequest))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "planner returned invalid data") {
		t.Fatalf("strict helper response error missing: %s", rr.Body.String())
	}
}

func TestAdminSetupPlanBoundsHelperValidationError(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminSetupPlanHelper
	defer func() { runAdminSetupPlanHelper = old }()
	runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(`{"ok":false,"schema_version":"v1","code":"invalid_storage","error":"bad storage\ninternal detail","warnings":[]}`), nil
	}

	rr := httptest.NewRecorder()
	s.adminSetupPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/plan", validAdminSetupPlanRequest))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "first-run setup could not be validated") || strings.Contains(rr.Body.String(), "internal detail") {
		t.Fatalf("validation error was not bounded: %s", rr.Body.String())
	}
}

func TestAdminSetupPlanRouteExistsWithoutApplyRoute(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminSetupPlanHelper
	defer func() { runAdminSetupPlanHelper = old }()
	runAdminSetupPlanHelper = func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(validAdminSetupPlanResponse), nil
	}

	mux := http.NewServeMux()
	registerAdminSetupPlanRoutes(mux, s)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/plan", validAdminSetupPlanRequest))
	if rr.Code != http.StatusOK {
		t.Fatalf("registered plan route status = %d: %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, surfaceRequest(http.MethodPost, "/v1/admin/setup/apply", validAdminSetupPlanRequest))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("apply route status = %d, want 404", rr.Code)
	}
}
