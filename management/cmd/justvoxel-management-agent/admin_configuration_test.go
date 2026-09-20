package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
