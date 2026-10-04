package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminConfigurationCommandReturnsOnlyStdoutJSON(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "configuration-helper")
	script := "#!/bin/sh\nprintf '%s' '{\"ok\":true,\"applied\":true}'\nprintf '%s\\n' 'Harmless apply diagnostic' >&2\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	output, err := runAdminConfigurationCommand(context.Background(), helper, "apply", []byte(`{"bedrock_enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != `{"ok":true,"applied":true}` {
		t.Fatalf("protocol output = %q, want only stdout JSON", output)
	}
	var out adminConfigurationChangeResponse
	if err := json.Unmarshal(output, &out); err != nil {
		t.Fatalf("stdout JSON could not be decoded: %v", err)
	}
	if !out.OK || !out.Applied {
		t.Fatalf("unexpected decoded response: %#v", out)
	}
}

func TestLocalRootCanPlanAndApplyConfigurationWithoutBearerSession(t *testing.T) {
	s := surfaceTestServer(t, roleViewer)
	old := runAdminConfigurationHelper
	defer func() { runAdminConfigurationHelper = old }()

	calls := make([]string, 0, 2)
	runAdminConfigurationHelper = func(_ context.Context, action string, request []byte) ([]byte, error) {
		calls = append(calls, action)
		if !strings.Contains(string(request), `"max_players":12`) {
			t.Fatalf("local root request missing max_players: %s", request)
		}
		if action == "plan" {
			return []byte(`{"ok":true,"changes":[{"field":"max_players","label":"Maximum players","before":"10","after":"12","restart_required":true}],"warnings":[],"restart_required":true,"memory_restart_required":false,"memory_remaining_mib":2048,"proposed":{"configured":true,"minecraft":{"max_players":12},"backup":{}},"applied":false,"confirmation_required":false,"online":0,"players":[],"restarted":false,"restart_deferred":false}`), nil
		}
		if action == "apply" {
			return []byte(`{"ok":true,"changes":[{"field":"max_players","label":"Maximum players","before":"10","after":"12","restart_required":true}],"warnings":[],"restart_required":true,"memory_restart_required":false,"memory_remaining_mib":2048,"proposed":{"configured":true,"minecraft":{"max_players":12},"backup":{}},"applied":true,"confirmation_required":false,"online":0,"players":[],"restarted":false,"restart_deferred":true,"message":"Settings saved. Minecraft must be restarted later for the server changes to take effect."}`), nil
		}
		t.Fatalf("unexpected configuration action: %s", action)
		return nil, nil
	}

	body := `{"java_memory":"4G","container_memory":"6G","java_port":25565,"bedrock_enabled":false,"bedrock_port":19132,"timezone":"UTC","max_players":12,"motd":"JustVoxel","image_tag":"stable","version_policy":"pinned","version":"1.21.8","backup_keep":7,"backup_schedule":"*-*-* 04:30:00","backup_timer_enabled":true,"confirm_players":false}`

	req := requestWithPeerUID(http.MethodPost, "http://unix/v1/admin/configuration/plan", 0)
	req.Body = io.NopCloser(strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.adminConfigurationPlan(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("local root configuration plan returned %d: %s", rr.Code, rr.Body.String())
	}

	req = requestWithPeerUID(http.MethodPost, "http://unix/v1/admin/configuration/apply", 0)
	req.Body = io.NopCloser(strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	s.adminConfigurationApply(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("local root configuration apply returned %d: %s", rr.Code, rr.Body.String())
	}
	if len(calls) != 2 || calls[0] != "plan" || calls[1] != "apply" {
		t.Fatalf("local root configuration helper calls = %#v", calls)
	}
}

func TestAdminConfigurationChangeRequiresAdministrator(t *testing.T) {
	old := runAdminConfigurationHelper
	defer func() { runAdminConfigurationHelper = old }()
	called := false
	runAdminConfigurationHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		called = true
		return []byte(`{"ok":true,"changes":[],"warnings":[],"restart_required":false,"memory_remaining_mib":2048,"proposed":{"configured":true,"minecraft":{},"backup":{}},"applied":false}`), nil
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		rr := httptest.NewRecorder()
		s.adminConfigurationPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/configuration/plan", `{}`))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s plan status = %d, want 403", role, rr.Code)
		}
	}
	if called {
		t.Fatal("configuration helper ran for a non-Administrator")
	}
}

func TestAdminConfigurationPlanUsesFixedHelperAndReturnsReview(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminConfigurationHelper
	defer func() { runAdminConfigurationHelper = old }()

	runAdminConfigurationHelper = func(_ context.Context, action string, request []byte) ([]byte, error) {
		if action != "plan" {
			t.Fatalf("action = %q, want plan", action)
		}
		body := string(request)
		for _, want := range []string{`"java_memory":"4G"`, `"container_memory":"6G"`, `"max_players":10`} {
			if !strings.Contains(body, want) {
				t.Fatalf("helper request missing %s: %s", want, body)
			}
		}
		return []byte(`{"ok":true,"changes":[{"field":"motd","label":"Server welcome message","before":"Old","after":"New","restart_required":true}],"warnings":["review warning"],"restart_required":true,"memory_remaining_mib":2048,"proposed":{"configured":true,"minecraft":{"java_memory":"4G","container_memory":"6G","max_players":10,"motd":"New"},"backup":{}},"applied":false}`), nil
	}

	body := `{"java_memory":"4G","container_memory":"6G","java_port":25565,"bedrock_enabled":false,"bedrock_port":19132,"timezone":"UTC","max_players":10,"motd":"New","image_tag":"stable","version_policy":"pinned","version":"1.21.8","backup_keep":7,"backup_schedule":"*-*-* 04:30:00","backup_timer_enabled":true}`
	rr := httptest.NewRecorder()
	s.adminConfigurationPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/configuration/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("plan status = %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"Server welcome message", "review warning", `"restart_required":true`, `"applied":false`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("plan response missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestAdminConfigurationApplyReturnsMemoryRestartConfirmation(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminConfigurationHelper
	defer func() { runAdminConfigurationHelper = old }()
	runAdminConfigurationHelper = func(_ context.Context, action string, request []byte) ([]byte, error) {
		if action != "apply" {
			t.Fatalf("action = %q, want apply", action)
		}
		if !strings.Contains(string(request), `"confirm_players":false`) {
			t.Fatalf("request missing explicit confirmation state: %s", request)
		}
		return []byte(`{"ok":true,"changes":[{"field":"java_memory","label":"Minecraft game memory","before":"4G","after":"5G","restart_required":true}],"warnings":[],"restart_required":true,"memory_restart_required":true,"memory_remaining_mib":2048,"proposed":{"configured":true,"minecraft":{"java_memory":"5G","container_memory":"7G"},"backup":{}},"applied":false,"confirmation_required":true,"online":2,"players":["Alex","Steve"],"restarted":false,"restart_deferred":false}`), nil
	}

	body := `{"java_memory":"5G","container_memory":"7G","java_port":25565,"bedrock_enabled":false,"bedrock_port":19132,"timezone":"UTC","max_players":10,"motd":"New","image_tag":"stable","version_policy":"pinned","version":"1.21.8","backup_keep":7,"backup_schedule":"*-*-* 04:30:00","backup_timer_enabled":true,"confirm_players":false}`
	rr := httptest.NewRecorder()
	s.adminConfigurationApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/configuration/apply", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("apply status = %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`"memory_restart_required":true`, `"confirmation_required":true`, `"online":2`, `"players":["Alex","Steve"]`, `"applied":false`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("confirmation response missing %q: %s", want, rr.Body.String())
		}
	}
}
func TestAdminConfigurationApplyErrorsAreSanitized(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminConfigurationHelper
	defer func() { runAdminConfigurationHelper = old }()
	runAdminConfigurationHelper = func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return []byte("password=do-not-leak"), errors.New("exit status 1")
	}

	rr := httptest.NewRecorder()
	s.adminConfigurationApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/configuration/apply", `{}`))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("apply status = %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "do-not-leak") {
		t.Fatalf("helper output leaked: %s", rr.Body.String())
	}
}

func TestAdminConfigurationValidationFailureIsReturnedWithoutRawHelperData(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminConfigurationHelper
	defer func() { runAdminConfigurationHelper = old }()
	runAdminConfigurationHelper = func(_ context.Context, action string, _ []byte) ([]byte, error) {
		if action != "plan" {
			t.Fatalf("action = %q, want plan", action)
		}
		return []byte(`{"ok":false,"error":"Maximum Minecraft memory must be larger than Minecraft game memory.","changes":[],"warnings":[]}`), nil
	}

	rr := httptest.NewRecorder()
	s.adminConfigurationPlan(rr, surfaceRequest(http.MethodPost, "/v1/admin/configuration/plan", `{}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("validation status = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Maximum Minecraft memory") {
		t.Fatalf("validation message missing: %s", rr.Body.String())
	}
}

func TestAdminConfigurationWhitelistFieldIsOptionalAndPreservesExplicitFalse(t *testing.T) {
	for _, body := range []string{`{}`, `{"whitelist_enabled":true}`, `{"whitelist_enabled":false}`} {
		for _, action := range []string{"plan", "apply"} {
			old := runAdminConfigurationHelper
			called := false
			runAdminConfigurationHelper = func(_ context.Context, gotAction string, payload []byte) ([]byte, error) {
				called = true
				if gotAction != action {
					t.Fatal(gotAction)
				}
				var got map[string]any
				if err := json.Unmarshal(payload, &got); err != nil {
					t.Fatal(err)
				}
				var expected map[string]any
				if err := json.Unmarshal([]byte(body), &expected); err != nil {
					t.Fatal(err)
				}
				value, present := got["whitelist_enabled"]
				expectedValue, expectedPresent := expected["whitelist_enabled"]
				if present != expectedPresent || value != expectedValue {
					t.Fatalf("whitelist field lost: %s", payload)
				}
				if _, present := got["enforce_whitelist"]; present {
					t.Fatal("enforcement exposed")
				}
				return []byte(`{"ok":true,"applied":true}`), nil
			}
			s := surfaceTestServer(t, roleAdministrator)
			rr := httptest.NewRecorder()
			s.runAdminConfigurationChange(rr, surfaceRequest(http.MethodPost, "/v1/admin/configuration/"+action, body), action, session{})
			runAdminConfigurationHelper = old
			if rr.Code != http.StatusOK || !called {
				t.Fatalf("%s: %d %s", action, rr.Code, rr.Body.String())
			}
		}
	}
}
