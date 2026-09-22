package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAdminSystemActionsStatusPreservesFirmwareCapability(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != adminSystemActionsStatusPath {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatalf("unexpected authorization header %q", r.Header.Get("Authorization"))
		}
		body := `{"ok":true,"variant":"hws","minecraft_state":"running","staged_update":true,"os_status_available":true,"firmware":{"available":true,"efi":true,"display_state":"connected"}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	status, err := client.AdminSystemActionsStatus(context.Background(), "session-token")
	if err != nil {
		t.Fatal(err)
	}
	if !status.OK || status.Variant != "hws" || !status.Firmware.Available || !status.Firmware.EFI {
		t.Fatalf("unexpected system action status: %#v", status)
	}
}

func TestAdminSystemActionSendsExplicitAndPlayerConfirmations(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/admin/system/reboot" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var request adminSystemActionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if !request.ActionConfirmed || !request.ConfirmPlayers {
			t.Fatalf("unexpected system action request: %#v", request)
		}
		body := `{"ok":true,"action":"reboot","accepted":true,"message":"Reboot accepted.","players":[]}`
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	result, err := client.AdminSystemAction(context.Background(), "session-token", "reboot", true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || !result.Accepted || result.Action != "reboot" {
		t.Fatalf("unexpected system action response: %#v", result)
	}
}

func TestAdminSystemActionPreservesPlayerConfirmationRequirement(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		body := `{"ok":false,"action":"poweroff","confirmation_required":true,"reason":"players_online","online":2,"players":["Alex","Steve"]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	result, err := client.AdminSystemAction(context.Background(), "session-token", "poweroff", false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ConfirmationRequired || result.Reason != "players_online" || result.Online != 2 || len(result.Players) != 2 {
		t.Fatalf("player confirmation requirement not preserved: %#v", result)
	}
}

func TestAdminSystemActionRejectsUnsupportedActionBeforeRequest(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		t.Fatal("transport should not be called for unsupported action")
		return nil, nil
	})}}
	if _, err := client.AdminSystemAction(context.Background(), "session-token", "shell", false); err == nil {
		t.Fatal("unsupported system action was accepted")
	}
}
