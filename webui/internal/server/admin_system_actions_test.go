package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeSystemActionsAPI struct {
	fakeAPI
	role             string
	status           api.AdminSystemActionsStatus
	statusErr        error
	statusCalls      int
	actionResult     api.AdminSystemActionResponse
	actionErr        error
	actionCalls      int
	action           string
	playersConfirmed bool
}

func (f *fakeSystemActionsAPI) Session(_ context.Context, session string) (api.SessionInfo, error) {
	if session != "session-token" {
		return api.SessionInfo{}, api.ErrUnauthorized
	}
	role := f.role
	if role == "" {
		role = "administrator"
	}
	return api.SessionInfo{Username: "voxel", Role: role, AuthSource: "system"}, nil
}

func (f *fakeSystemActionsAPI) AdminSystemActionsStatus(_ context.Context, session string) (api.AdminSystemActionsStatus, error) {
	if session != "session-token" {
		return api.AdminSystemActionsStatus{}, api.ErrUnauthorized
	}
	f.statusCalls++
	return f.status, f.statusErr
}

func (f *fakeSystemActionsAPI) AdminSystemAction(_ context.Context, session, action string, confirmPlayers bool) (api.AdminSystemActionResponse, error) {
	if session != "session-token" {
		return api.AdminSystemActionResponse{}, api.ErrUnauthorized
	}
	f.actionCalls++
	f.action = action
	f.playersConfirmed = confirmPlayers
	return f.actionResult, f.actionErr
}

func TestSystemActionsStatusIsAdministratorOnlyAndPreservesHWECapability(t *testing.T) {
	client := &fakeSystemActionsAPI{status: api.AdminSystemActionsStatus{
		OK: true, Variant: "hwe", MinecraftState: "running",
		Firmware: api.AdminSystemFirmwareStatus{Available: true, EFI: true, DisplayState: "connected"},
	}}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	req := authenticatedSystemActionRequest(http.MethodGet, "http://example/api/system-actions", "")
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("system action status returned %d: %s", rr.Code, rr.Body.String())
	}
	var status api.AdminSystemActionsStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Variant != "hwe" || !status.Firmware.Available {
		t.Fatalf("unexpected system action status: %#v", status)
	}

	client.role = "operator"
	denied := authenticatedSystemActionRequest(http.MethodGet, "http://example/api/system-actions", "")
	deniedRR := httptest.NewRecorder()
	app.Handler().ServeHTTP(deniedRR, denied)
	if deniedRR.Code != http.StatusForbidden {
		t.Fatalf("operator system action status = %d, want 403", deniedRR.Code)
	}
	if client.statusCalls != 1 {
		t.Fatalf("Agent status called for non-administrator; calls=%d", client.statusCalls)
	}
}

func TestSystemActionRequiresCSRFBeforeAgentCall(t *testing.T) {
	client := &fakeSystemActionsAPI{}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://example/api/system-actions/reboot", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF returned %d, want 403", rr.Code)
	}
	if client.actionCalls != 0 {
		t.Fatalf("system action reached Agent without CSRF; calls=%d", client.actionCalls)
	}
}

func TestSystemActionPlayerConfirmationRoundTrip(t *testing.T) {
	client := &fakeSystemActionsAPI{actionResult: api.AdminSystemActionResponse{
		Action: "reboot", ConfirmationRequired: true, Reason: "players_online",
		Online: 2, Players: []string{"Alex", "Steve"},
	}}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	first := authenticatedSystemActionRequest(http.MethodPost, "http://example/api/system-actions/reboot", "csrf=token")
	firstRR := httptest.NewRecorder()
	app.Handler().ServeHTTP(firstRR, first)
	if firstRR.Code != http.StatusOK {
		t.Fatalf("initial reboot request returned %d: %s", firstRR.Code, firstRR.Body.String())
	}
	if client.action != "reboot" || client.playersConfirmed {
		t.Fatalf("unexpected initial Agent request: action=%q confirm_players=%v", client.action, client.playersConfirmed)
	}

	client.actionResult = api.AdminSystemActionResponse{OK: true, Action: "reboot", Accepted: true, Message: "Reboot accepted."}
	second := authenticatedSystemActionRequest(http.MethodPost, "http://example/api/system-actions/reboot", "csrf=token&confirm_players=yes")
	secondRR := httptest.NewRecorder()
	app.Handler().ServeHTTP(secondRR, second)
	if secondRR.Code != http.StatusAccepted {
		t.Fatalf("confirmed reboot request returned %d: %s", secondRR.Code, secondRR.Body.String())
	}
	if !client.playersConfirmed {
		t.Fatal("online-player confirmation was not forwarded to Agent")
	}
}

func TestSystemActionRejectsUnknownActionBeforeAgentCall(t *testing.T) {
	client := &fakeSystemActionsAPI{}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := authenticatedSystemActionRequest(http.MethodPost, "http://example/api/system-actions/shell", "csrf=token")
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown system action returned %d, want 400", rr.Code)
	}
	if client.actionCalls != 0 {
		t.Fatalf("unknown system action reached Agent; calls=%d", client.actionCalls)
	}
}

func authenticatedSystemActionRequest(method, target, form string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(form))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "null")
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
	return req
}
