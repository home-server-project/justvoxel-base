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

const resetTestFingerprint = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
const resetTestOperationID = "12345678-1234-4123-8123-123456789abc"

func TestFactoryResetPlanAndApplyClientContract(t *testing.T) {
	call := 0
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		call++
		if r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatalf("missing bearer session: %q", r.Header.Get("Authorization"))
		}
		switch call {
		case 1:
			if r.Method != http.MethodPost || r.URL.Path != adminFactoryResetPlanPath {
				t.Fatalf("unexpected plan request %s %s", r.Method, r.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
				  "ok":true,
				  "schema_version":"v1",
				  "mode":"factory",
				  "plan_fingerprint":"` + resetTestFingerprint + `",
				  "minecraft_configured":true,
				  "data_path":"/var/lib/justvoxel/minecraft",
				  "data_scope":"internal",
				  "data_action":"delete",
				  "backup_path":"/var/lib/justvoxel/backups",
				  "backup_scope":"internal",
				  "backup_action":"delete",
				  "config_backups_action":"delete",
				  "storage_layout_action":"preserve",
				  "external_storage_action":"preserve",
				  "network_storage_action":"preserve",
				  "authentication_action":"reset_to_system",
				  "webui_users_action":"delete",
				  "password_action":"expire",
				  "sessions_action":"invalidate",
				  "players_online":0,
				  "players":[],
				  "warnings":[]
				}`)),
				Header: make(http.Header),
			}, nil
		case 2:
			if r.Method != http.MethodPost || r.URL.Path != adminFactoryResetApplyPath {
				t.Fatalf("unexpected apply request %s %s", r.Method, r.URL.Path)
			}
			var request AdminFactoryResetApplyRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.PlanFingerprint != resetTestFingerprint || request.SystemPassword != "system-secret" || !request.ConfirmPlayers {
				t.Fatalf("unexpected factory reset request: %#v", request)
			}
			return &http.Response{
				StatusCode: http.StatusAccepted,
				Body: io.NopCloser(strings.NewReader(`{
				  "ok":true,
				  "created":true,
				  "operation":{
				    "schema_version":"v1",
				    "operation_id":"` + resetTestOperationID + `",
				    "operation_type":"factory_reset",
				    "plan_fingerprint":"` + resetTestFingerprint + `",
				    "state":"queued",
				    "stage":"queued",
				    "status":"Full factory reset operation queued.",
				    "started_at":"2026-09-25T20:00:00Z",
				    "updated_at":"2026-09-25T20:00:00Z",
				    "rollback":{"state":"not_started"}
				  }
				}`)),
				Header: make(http.Header),
			}, nil
		default:
			return nil, errors.New("unexpected request")
		}
	})}}

	ctx := WithMinecraftMaintenanceInstance(context.Background(), "jv-sibling", "reset/minecraft")
	plan, err := client.AdminFactoryResetPlan(ctx, "session-token")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != "factory" || plan.PlanFingerprint != resetTestFingerprint ||
		plan.ExternalStorageAction != "preserve" || plan.NetworkStorageAction != "preserve" {
		t.Fatalf("unexpected factory reset plan: %#v", plan)
	}

	applied, err := client.AdminFactoryResetApply(ctx, "session-token", AdminFactoryResetApplyRequest{
		PlanFingerprint: resetTestFingerprint,
		ConfirmPlayers:  true,
		SystemPassword:  "system-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !applied.OK || applied.Operation == nil || applied.Operation.OperationType != "factory_reset" {
		t.Fatalf("unexpected factory reset apply response: %#v", applied)
	}
}

func TestMinecraftResetClientRejectsInvalidFingerprintBeforeRequest(t *testing.T) {
	called := false
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected request")
	})}}
	_, err := client.AdminMinecraftResetApply(context.Background(), "session-token", AdminMinecraftResetApplyRequest{
		PlanFingerprint: "not-a-fingerprint",
	})
	if err == nil {
		t.Fatal("invalid Minecraft reset fingerprint unexpectedly accepted")
	}
	if called {
		t.Fatal("HTTP request made for invalid Minecraft reset fingerprint")
	}
}

func TestResetCurrentOperationPaths(t *testing.T) {
	paths := []struct {
		want string
		call func(*Client) (PersistentOperationResponse, error)
	}{
		{
			want: "/v1/admin/reset/minecraft/current-operation",
			call: func(c *Client) (PersistentOperationResponse, error) {
				return c.AdminCurrentMinecraftResetOperation(WithMinecraftMaintenanceInstance(context.Background(), "jv-family1", "reset/minecraft"), "session-token")
			},
		},
		{
			want: "/v1/admin/reset/factory/current-operation",
			call: func(c *Client) (PersistentOperationResponse, error) {
				return c.AdminCurrentFactoryResetOperation(context.Background(), "session-token")
			},
		},
	}
	for _, tc := range paths {
		t.Run(tc.want, func(t *testing.T) {
			client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != tc.want {
					t.Fatalf("path = %q, want %q", r.URL.Path, tc.want)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"operation":null}`)),
					Header:     make(http.Header),
				}, nil
			})}}
			response, err := tc.call(client)
			if err != nil {
				t.Fatal(err)
			}
			if response.Operation != nil {
				t.Fatalf("operation = %#v, want nil", response.Operation)
			}
		})
	}
}

func TestFactoryResetResolveClientContract(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != adminFactoryResetResolvePath {
			t.Fatalf("unexpected resolve request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatalf("missing bearer session: %q", r.Header.Get("Authorization"))
		}
		var request struct {
			OperationID      string `json:"operation_id"`
			KeepCurrentState bool   `json:"keep_current_state"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.OperationID != resetTestOperationID || !request.KeepCurrentState {
			t.Fatalf("unexpected factory reset resolve request: %#v", request)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
			  "operation":{
			    "schema_version":"v1",
			    "operation_id":"` + resetTestOperationID + `",
			    "operation_type":"factory_reset",
			    "plan_fingerprint":"` + resetTestFingerprint + `",
			    "state":"resolved",
			    "stage":"resolved",
			    "status":"Current server state kept.",
			    "started_at":"2026-09-25T20:00:00Z",
			    "updated_at":"2026-09-25T20:05:00Z",
			    "finished_at":"2026-09-25T20:05:00Z",
			    "rollback":{"state":"not_started"}
			  }
			}`)),
			Header: make(http.Header),
		}, nil
	})}}

	response, err := client.AdminFactoryResetResolve(context.Background(), "session-token", resetTestOperationID)
	if err != nil {
		t.Fatal(err)
	}
	if response.Operation == nil || response.Operation.State != "resolved" || response.Operation.OperationType != "factory_reset" {
		t.Fatalf("unexpected factory reset resolve response: %#v", response)
	}
}

func TestMinecraftResetAllClientIgnoresSelectedServer(t *testing.T) {
	for _, selection := range []string{"", "jv-family1", "jv-sibling"} {
		t.Run(selection, func(t *testing.T) {
			ctx := context.Background()
			if selection != "" {
				ctx = WithMinecraftMaintenanceInstance(ctx, selection, "reset/minecraft")
			}
			client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.RawQuery != "" {
					t.Fatal("selection query sent", r.URL)
				}
				body := ""
				switch r.URL.Path {
				case adminMinecraftResetPlanPath:
					body = `{"ok":true,"schema_version":"v1","mode":"minecraft","plan_fingerprint":"` + resetTestFingerprint + `","participants":[{"id":"jv-family1","display_name":"Family","state":"running","players_online":1,"players":["Alex"],"data_scope":"internal","data_action":"delete"},{"id":"jv-sibling","display_name":"Creative","state":"stopped","players_online":0,"players":[],"data_scope":"network","data_action":"preserve"}],"total_players_online":1,"players_online":1,"players":["Alex"],"warnings":[],"backup_action":"preserve","storage_layout_action":"preserve","authentication_action":"preserve","webui_users_action":"preserve"}`
				case adminMinecraftResetApplyPath:
					body = `{"ok":true,"created":true,"operation":{"operation_id":"` + resetTestOperationID + `","operation_type":"minecraft_reset","state":"queued"}}`
				default:
					t.Fatal("selected route used", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}}
			plan, err := client.AdminMinecraftResetPlan(ctx, "session")
			if err != nil || len(plan.Participants) != 2 || plan.TotalPlayersOnline != 1 {
				t.Fatal(plan, err)
			}
			applied, err := client.AdminMinecraftResetApply(ctx, "session", AdminMinecraftResetApplyRequest{PlanFingerprint: plan.PlanFingerprint, ConfirmPlayers: true})
			if err != nil || applied.Operation == nil || applied.Operation.InstanceID != "" {
				t.Fatal(applied, err)
			}
		})
	}
}

func TestHistoricalResetRecoveryUsesStoredImmutableTarget(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Path {
		case "/v1/admin/reset/minecraft/current-operation":
			body = `{"operation":{"operation_id":"` + resetTestOperationID + `","operation_type":"minecraft_reset","instance_id":"jv-family1","state":"needs_attention"}}`
		case "/v1/minecraft/instances/jv-family1/reset/plan":
			body = `{"ok":true,"mode":"minecraft","instance_id":"jv-family1","plan_fingerprint":"` + resetTestFingerprint + `","data_action":"preserve","plugins_action":"remove_managed_jars"}`
		case "/v1/minecraft/instances/jv-family1/reset/apply":
			body = `{"ok":true,"operation":{"operation_id":"` + resetTestOperationID + `","operation_type":"minecraft_reset","instance_id":"jv-family1","state":"queued"}}`
		default:
			t.Fatal("historical target changed", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	plan, err := client.HistoricalMinecraftResetPlan(context.Background(), "session")
	if err != nil || plan.InstanceID != "jv-family1" || plan.PluginsAction != "remove_managed_jars" {
		t.Fatal(plan, err)
	}
	result, err := client.HistoricalMinecraftResetApply(context.Background(), "session", AdminMinecraftResetApplyRequest{PlanFingerprint: plan.PlanFingerprint})
	if err != nil || result.Operation == nil || result.Operation.InstanceID != "jv-family1" {
		t.Fatal(result, err)
	}
}
