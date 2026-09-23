package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminSystemUpdateRebootStatusRequiresAdministrator(t *testing.T) {
	old := runAdminSystemUpdateRebootHelper
	defer func() { runAdminSystemUpdateRebootHelper = old }()
	called := false
	runAdminSystemUpdateRebootHelper = func(_ context.Context, _ ...string) ([]byte, int, error) {
		called = true
		return []byte(`{"ok":true,"state":"idle"}`), 0, nil
	}

	s := roleServerForTest(roleOperator)
	rr := httptest.NewRecorder()
	s.adminSystemUpdateRebootStatus(rr, authorizedRequest(http.MethodGet, "http://unix/v1/admin/system/update-reboot", ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator status = %d, want 403", rr.Code)
	}
	if called {
		t.Fatal("update reboot helper ran for forbidden operator request")
	}
}

func TestAdminSystemUpdateRebootForwardsApprovedOptions(t *testing.T) {
	old := runAdminSystemUpdateRebootHelper
	defer func() { runAdminSystemUpdateRebootHelper = old }()
	runAdminSystemUpdateRebootHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		want := []string{"request", "--confirm-players", "--backup-minecraft", "--warning-seconds=10"}
		if len(args) != len(want) {
			t.Fatalf("helper args = %#v, want %#v", args, want)
		}
		for i := range want {
			if args[i] != want[i] {
				t.Fatalf("helper args = %#v, want %#v", args, want)
			}
		}
		return []byte(`{"ok":true,"state":"queued","accepted":true,"warning_seconds":10,"backup_minecraft":true}`), 0, nil
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemUpdateReboot(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/update-reboot", `{"action_confirmed":true,"confirm_players":true,"backup_minecraft":true,"warning_seconds":10}`))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("update reboot = %d, want 202: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminSystemUpdateRebootReturnsPlayerConfirmation(t *testing.T) {
	old := runAdminSystemUpdateRebootHelper
	defer func() { runAdminSystemUpdateRebootHelper = old }()
	runAdminSystemUpdateRebootHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		if strings.Join(args, " ") != "request --backup-minecraft --warning-seconds=60" {
			t.Fatalf("unexpected helper args: %#v", args)
		}
		return []byte(`{"ok":false,"state":"confirmation_required","confirmation_required":true,"reason":"players_online","online":2,"players":["Alex","Steve"],"warning_seconds":60,"backup_minecraft":true}`), 10, errors.New("confirmation required")
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemUpdateReboot(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/update-reboot", `{"action_confirmed":true,"confirm_players":false,"backup_minecraft":true,"warning_seconds":60}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("player confirmation = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"confirmation_required":true`) || !strings.Contains(rr.Body.String(), "Steve") {
		t.Fatalf("missing player confirmation details: %s", rr.Body.String())
	}
}

func TestAdminSystemUpdateRebootRejectsInvalidWarningBeforeHelper(t *testing.T) {
	old := runAdminSystemUpdateRebootHelper
	defer func() { runAdminSystemUpdateRebootHelper = old }()
	called := false
	runAdminSystemUpdateRebootHelper = func(_ context.Context, _ ...string) ([]byte, int, error) {
		called = true
		return nil, 0, nil
	}

	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.adminSystemUpdateReboot(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/system/update-reboot", `{"action_confirmed":true,"warning_seconds":30}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid warning = %d, want 400: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("helper ran for invalid warning")
	}
}

func TestAdminSystemUpdateRebootStatusAllowsLocalRoot(t *testing.T) {
	old := runAdminSystemUpdateRebootHelper
	defer func() { runAdminSystemUpdateRebootHelper = old }()
	runAdminSystemUpdateRebootHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		if len(args) != 1 || args[0] != "status" {
			t.Fatalf("unexpected helper args: %#v", args)
		}
		return []byte(`{"ok":true,"state":"countdown","deadline_unix":1234,"warning_seconds":10}`), 0, nil
	}

	s := &server{sessions: make(map[string]session)}
	rr := httptest.NewRecorder()
	s.adminSystemUpdateRebootStatus(rr, requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/system/update-reboot", 0))
	if rr.Code != http.StatusOK {
		t.Fatalf("local-root status = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"state":"countdown"`) {
		t.Fatalf("unexpected status: %s", rr.Body.String())
	}
}
