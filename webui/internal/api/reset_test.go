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
				Body:       io.NopCloser(strings.NewReader(`{
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
				Body:       io.NopCloser(strings.NewReader(`{
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

	plan, err := client.AdminFactoryResetPlan(context.Background(), "session-token")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != "factory" || plan.PlanFingerprint != resetTestFingerprint ||
		plan.ExternalStorageAction != "preserve" || plan.NetworkStorageAction != "preserve" {
		t.Fatalf("unexpected factory reset plan: %#v", plan)
	}

	applied, err := client.AdminFactoryResetApply(context.Background(), "session-token", AdminFactoryResetApplyRequest{
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
				return c.AdminCurrentMinecraftResetOperation(context.Background(), "session-token")
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
