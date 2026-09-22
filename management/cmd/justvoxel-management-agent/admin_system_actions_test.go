package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminSystemActionsStatusRequiresAdministrator(t *testing.T) {
	old := runAdminSystemActionHelper
	defer func() { runAdminSystemActionHelper = old }()
	called := false
	runAdminSystemActionHelper = func(_ context.Context, _ ...string) ([]byte, int, error) {
		called = true
		return nil, 0, nil
	}

	s := roleServerForTest(roleViewer)
	rr := httptest.NewRecorder()
	s.adminSystemActionsStatus(rr, authorizedRequest(http.MethodGet, "http://unix/v1/admin/system/actions", ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want 403", rr.Code)
	}
	if called {
		t.Fatal("system action helper ran for forbidden viewer request")
	}
}

func TestAdminSystemActionsStatusAllowsLocalRoot(t *testing.T) {
	old := runAdminSystemActionHelper
	defer func() { runAdminSystemActionHelper = old }()
	runAdminSystemActionHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		if len(args) != 1 || args[0] != "status" {
			t.Fatalf("unexpected status helper args: %#v", args)
		}
		return []byte(`{"ok":true,"variant":"vm","minecraft_state":"running","staged_update":true,"os_status_available":true,"firmware":{"available":false,"efi":true,"display_state":"unknown","reason":"not_hws"}}`), 0, nil
	}

	s := &server{sessions: make(map[string]session)}
	rr := httptest.NewRecorder()
	s.adminSystemActionsStatus(rr, requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/system/actions", 0))
	if rr.Code != http.StatusOK {
		t.Fatalf("local-root status = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"staged_update":true`) || !strings.Contains(rr.Body.String(), `"variant":"vm"`) {
		t.Fatalf("unexpected status response: %s", rr.Body.String())
	}
}

func TestAdminSystemActionRequiresExplicitActionConfirmation(t *testing.T) {
	old := runAdminSystemActionHelper
	defer func() { runAdminSystemActionHelper = old }()
	called := false
	runAdminSystemActionHelper = func(_ context.Context, _ ...string) ([]byte, int, error) {
		called = true
		return nil, 0, nil
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemAction(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/reboot", `{"action_confirmed":false,"confirm_players":false}`), "reboot")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed reboot = %d, want 400: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("system action helper ran without explicit action confirmation")
	}
}

func TestAdminSystemActionReturnsPlayerConfirmationRequirement(t *testing.T) {
	old := runAdminSystemActionHelper
	defer func() { runAdminSystemActionHelper = old }()
	runAdminSystemActionHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		if len(args) != 2 || args[0] != "apply" || args[1] != "reboot" {
			t.Fatalf("unexpected helper args: %#v", args)
		}
		return []byte(`{"ok":false,"action":"reboot","confirmation_required":true,"reason":"players_online","online":1,"players":["Steve"]}`), 10, errors.New("confirmation required")
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemAction(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/reboot", `{"action_confirmed":true,"confirm_players":false}`), "reboot")
	if rr.Code != http.StatusOK {
		t.Fatalf("player confirmation response = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"confirmation_required":true`) || !strings.Contains(rr.Body.String(), "Steve") {
		t.Fatalf("missing player confirmation detail: %s", rr.Body.String())
	}
}

func TestAdminSystemActionConfirmedPlayersUsesOnlyAllowlistedFlag(t *testing.T) {
	old := runAdminSystemActionHelper
	defer func() { runAdminSystemActionHelper = old }()
	runAdminSystemActionHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		if len(args) != 3 || args[0] != "apply" || args[1] != "poweroff" || args[2] != "--confirm-players" {
			t.Fatalf("unexpected confirmed helper args: %#v", args)
		}
		return []byte(`{"ok":true,"action":"poweroff","accepted":true,"message":"Power off JustVoxel accepted."}`), 0, nil
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemAction(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/poweroff", `{"action_confirmed":true,"confirm_players":true}`), "poweroff")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("confirmed poweroff = %d, want 202: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminFirmwareRebootUnavailableIsConflict(t *testing.T) {
	old := runAdminSystemActionHelper
	defer func() { runAdminSystemActionHelper = old }()
	runAdminSystemActionHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		if len(args) != 2 || args[0] != "apply" || args[1] != "firmware-reboot" {
			t.Fatalf("unexpected firmware helper args: %#v", args)
		}
		return []byte(`{"ok":false,"action":"firmware-reboot","reason":"not_hws","message":"Firmware / UEFI reboot is unavailable on this system."}`), 2, errors.New("not available")
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemAction(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/firmware-reboot", `{"action_confirmed":true,"confirm_players":false}`), "firmware-reboot")
	if rr.Code != http.StatusConflict {
		t.Fatalf("unavailable firmware reboot = %d, want 409: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminSystemActionRejectsUnknownJSONField(t *testing.T) {
	old := runAdminSystemActionHelper
	defer func() { runAdminSystemActionHelper = old }()
	called := false
	runAdminSystemActionHelper = func(_ context.Context, _ ...string) ([]byte, int, error) {
		called = true
		return nil, 0, nil
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemAction(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/reboot", `{"action_confirmed":true,"shell":"reboot now"}`), "reboot")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d, want 400: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("system action helper ran for request with unknown field")
	}
}

func TestAdminSystemActionRoutesExposeOnlyAllowlistedActions(t *testing.T) {
	s := adminServerForTest()
	mux := http.NewServeMux()
	registerAdminSystemActionRoutes(mux, s)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/shell", `{"action_confirmed":true}`))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unregistered system action = %d, want 404", rr.Code)
	}
}

func TestAdminSystemActionsStatusRejectsMalformedBackendData(t *testing.T) {
	old := runAdminSystemActionHelper
	defer func() { runAdminSystemActionHelper = old }()
	runAdminSystemActionHelper = func(_ context.Context, _ ...string) ([]byte, int, error) {
		return []byte(`{"ok":true,"variant":"hws","minecraft_state":"mystery","staged_update":false,"os_status_available":true,"firmware":{"available":true,"efi":true,"display_state":"connected"}}`), 0, nil
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemActionsStatus(rr, authorizedRequest(http.MethodGet, "http://unix/v1/admin/system/actions", ""))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("invalid backend status = %d, want 500: %s", rr.Code, rr.Body.String())
	}
}

func TestLocalRootCanRequestConfirmedSystemAction(t *testing.T) {
	old := runAdminSystemActionHelper
	defer func() { runAdminSystemActionHelper = old }()
	runAdminSystemActionHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		if len(args) != 2 || args[0] != "apply" || args[1] != "reboot" {
			t.Fatalf("unexpected local-root helper args: %#v", args)
		}
		return []byte(`{"ok":true,"action":"reboot","accepted":true,"message":"Reboot JustVoxel accepted."}`), 0, nil
	}

	s := &server{sessions: make(map[string]session)}
	req := localRootMinecraftRequest(http.MethodPost, "http://unix/v1/admin/system/reboot", `{"action_confirmed":true,"confirm_players":false}`)
	rr := httptest.NewRecorder()
	s.adminSystemAction(rr, req, "reboot")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("local-root reboot = %d, want 202: %s", rr.Code, rr.Body.String())
	}
}

func TestOperatorCannotRequestSystemAction(t *testing.T) {
	old := runAdminSystemActionHelper
	defer func() { runAdminSystemActionHelper = old }()
	called := false
	runAdminSystemActionHelper = func(_ context.Context, _ ...string) ([]byte, int, error) {
		called = true
		return nil, 0, nil
	}

	s := roleServerForTest(roleOperator)
	rr := httptest.NewRecorder()
	s.adminSystemAction(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/reboot", `{"action_confirmed":true,"confirm_players":false}`), "reboot")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator reboot = %d, want 403", rr.Code)
	}
	if called {
		t.Fatal("system action helper ran for forbidden operator request")
	}
}
