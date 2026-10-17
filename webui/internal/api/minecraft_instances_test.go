package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestIndependentInstanceCreateClientPreservesPartialIdentity(t *testing.T) {
	for _, partial := range []bool{false, true} {
		client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != "POST" || r.URL.Path != "/v1/minecraft/instances" || r.Header.Get("Authorization") != "Bearer session-token" {
				t.Fatalf("wrong instance endpoint/authentication: %s %s", r.Method, r.URL.Path)
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 2 || body["display_name"] == nil || body["runtime"] == nil {
				t.Fatal("creation must not submit client identity or storage")
			}
			var runtime map[string]any
			if err := json.Unmarshal(body["runtime"], &runtime); err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{"id", "rcon_password", "data_path", "uid", "gid", "container_name", "quadlet"} {
				if _, found := runtime[private]; found {
					t.Fatalf("private creation field: %s", private)
				}
			}
			if runtime["motd"] != "Welcome players" || string(body["display_name"]) != `"Family Survival"` {
				t.Fatal("name and MOTD merged")
			}
			code, response := 201, `{"ok":true,"id":"jv-created1","state":"stopped"}`
			if partial {
				code, response = 409, `{"error":"Activation needs recovery.","id":"jv-created1","recovery_required":true}`
			}
			return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
		})}}
		response, err := client.CreateMinecraftInstance(context.Background(), "session-token", CreateMinecraftInstanceRequest{DisplayName: "Family Survival", Runtime: InstanceRuntimeSettings{Software: "paper", MOTD: "Welcome players"}})
		if response.ID != "jv-created1" {
			t.Fatal("identity discarded")
		}
		if partial {
			var typed *ResponseError
			if !errors.As(err, &typed) || typed.StatusCode != 409 || !response.RecoveryRequired {
				t.Fatal("partial creation error discarded")
			}
		} else if err != nil || !response.OK || response.State != "stopped" {
			t.Fatalf("create result: %+v %v", response, err)
		}
	}
}

func TestIndependentInstanceBackupClientUsesSelectedInstance(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "PUT" || r.URL.Path != "/v1/minecraft/instances/jv-created1/backups/settings" || r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatal("wrong backup target")
		}
		var settings InstanceBackupSettings
		if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
			t.Fatal(err)
		}
		if !settings.Enabled || settings.Time != "06:30" || settings.Retention != 9 {
			t.Fatal("backup settings mapping")
		}
		return &http.Response{StatusCode: 409, Body: io.NopCloser(strings.NewReader(`{"error":"Daily backups must start at least 30 minutes apart."}`)), Header: make(http.Header)}, nil
	})}}
	err := client.SetMinecraftInstanceBackupSettings(context.Background(), "session-token", "jv-created1", InstanceBackupSettings{Enabled: true, Time: "06:30", Retention: 9})
	if message, ok := ErrorMessage(err); !ok || !strings.Contains(message, "30 minutes") {
		t.Fatal("schedule error lost")
	}
}

func TestIndependentInstanceOverviewClientUsesExistingContracts(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		calls := 0
		client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Header.Get("Authorization") != "Bearer session-token" {
				t.Fatal("authentication missing")
			}
			response := `{"id":"jv-family1","state":"recovery_required","start_on_boot":true,"message":"Safe recovery message"}`
			if calls == 1 {
				if r.Method != "GET" || r.URL.Path != "/v1/minecraft/instances/jv-family1/status" {
					t.Fatal("wrong status target")
				}
			} else {
				if r.Method != "POST" || r.URL.Path != "/v1/minecraft/instances/jv-family1/"+action {
					t.Fatal("wrong lifecycle target")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != `{}` {
					t.Fatal("lifecycle added override arguments")
				}
				response = `{"ok":true,"id":"jv-family1"}`
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
		})}}
		status, err := client.MinecraftInstanceStatus(context.Background(), "session-token", "jv-family1")
		if err != nil || status.ID != "jv-family1" || status.State != "recovery_required" || !status.StartOnBoot {
			t.Fatalf("status mapping: %+v %v", status, err)
		}
		if err := client.MinecraftInstanceAction(context.Background(), "session-token", "jv-family1", action); err != nil {
			t.Fatal(err)
		}
		if err := client.MinecraftInstanceAction(context.Background(), "session-token", "jv-family1", "delete"); err == nil || calls != 2 {
			t.Fatal("unsupported action reached agent")
		}
	}
}

func TestIndependentInstanceLifecycleClientPreservesRefusal(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 409, Body: io.NopCloser(strings.NewReader(`{"error":"Player status is unavailable. Server was not interrupted."}`)), Header: make(http.Header)}, nil
	})}}
	err := client.MinecraftInstanceAction(context.Background(), "session-token", "jv-family1", "stop")
	if message, ok := ErrorMessage(err); !ok || !strings.Contains(message, "Player status is unavailable") {
		t.Fatal("refusal discarded")
	}
}

func TestPhase7SelectedWrappersNeverUseLegacyContracts(t *testing.T) {
	requests := []string{}
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if !strings.HasPrefix(r.URL.Path, "/v1/minecraft/instances/jv-family1/") {
			t.Fatal("legacy or foreign target", r.URL.Path)
		}
		body := `{"ok":true}`
		if strings.HasSuffix(r.URL.Path, "/players") {
			body = `{"id":"jv-family1","state":"offline"}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	ctx := context.Background()
	if e := client.SetMinecraftInstanceConfiguration(ctx, "session-token", "jv-family1", InstanceRuntimeSettings{JavaMemory: "4G"}); e != nil {
		t.Fatal(e)
	}
	if _, e := client.MinecraftInstancePlayers(ctx, "session-token", "jv-family1"); e != nil {
		t.Fatal(e)
	}
	if e := client.RenameMinecraftInstance(ctx, "session-token", "jv-family1", "Renamed"); e != nil {
		t.Fatal(e)
	}
	if e := client.DeleteMinecraftInstance(ctx, "session-token", "jv-family1", "Renamed"); e != nil {
		t.Fatal(e)
	}
	for _, action := range []string{"inventory", "status", "settings", "now", "configure", "restore/plan", "restore/apply", "restore/status", "restore/recover"} {
		if _, e := client.MinecraftInstanceBackups(ctx, "session-token", "jv-family1", action, struct{}{}); e != nil {
			t.Fatal(action, e)
		}
	}
	for _, want := range []string{"PUT /v1/minecraft/instances/jv-family1/configuration", "GET /v1/minecraft/instances/jv-family1/players", "POST /v1/minecraft/instances/jv-family1/backups", "POST /v1/minecraft/instances/jv-family1/name", "POST /v1/minecraft/instances/jv-family1/delete", "POST /v1/minecraft/instances/jv-family1/backups/restore/apply"} {
		found := false
		for _, request := range requests {
			if request == want {
				found = true
			}
		}
		if !found {
			t.Fatal(want)
		}
	}
}
