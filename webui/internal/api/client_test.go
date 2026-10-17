package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSelectedInstanceActionAllowlist(t *testing.T) {
	calls := 0
	expectedAction := ""
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/minecraft/instances/jv-a1b2c3/"+expectedAction {
			t.Fatal(r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})}}
	for _, action := range []string{"start", "stop", "restart"} {
		expectedAction = action
		if _, err := client.MinecraftInstanceActionConfirmed(context.Background(), "token", "jv-a1b2c3", action, false); err != nil {
			t.Fatalf("%s rejected: %v", action, err)
		}
	}
	for _, action := range []string{"shell", "restart/../../shell", "", "poweroff"} {
		if _, err := client.MinecraftInstanceActionConfirmed(context.Background(), "token", "jv-a1b2c3", action, false); err == nil {
			t.Fatalf("unsupported action %q was accepted", action)
		}
	}
	if calls != 3 {
		t.Fatalf("unexpected request count: %d", calls)
	}
}

func TestSelectedInstanceRestartConfirmationPath(t *testing.T) {
	calls := 0
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v1/minecraft/instances/jv-a1b2c3/restart" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatal(r.URL, r.Header)
		}
		var body struct {
			ConfirmPlayers bool `json:"confirm_players"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.ConfirmPlayers != (calls == 2) {
			t.Fatal("confirmation flag", body)
		}
		response := `{"confirmation_required":true,"online":1,"players":["Alex"]}`
		if body.ConfirmPlayers {
			response = `{"ok":true}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
	})}}
	for _, confirm := range []bool{false, true} {
		response, err := client.MinecraftInstanceActionConfirmed(context.Background(), "token", "jv-a1b2c3", "restart", confirm)
		if err != nil || response.ConfirmationRequired == confirm {
			t.Fatal(response, err)
		}
	}
	for _, id := range []string{"", "friendly", "jv-a1b2c3/../other"} {
		if _, err := client.MinecraftInstanceActionConfirmed(context.Background(), "token", id, "restart", false); err == nil {
			t.Fatal(id)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestSelectedInstanceLogsPathAndLimit(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Path != "/v1/minecraft/instances/jv-a1b2c3/logs" || r.URL.Query().Get("limit") != "150" {
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"lines":["selected log"]}`)), Header: make(http.Header)}, nil
	})}}
	response, err := client.MinecraftLogs(context.Background(), "token", "jv-a1b2c3", 150)
	if err != nil || len(response.Lines) != 1 || response.Lines[0] != "selected log" {
		t.Fatal(response, err)
	}
	if _, err := client.MinecraftLogs(context.Background(), "token", "friendly", 150); err == nil {
		t.Fatal("invalid log target accepted")
	}
}
