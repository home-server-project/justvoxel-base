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

func TestMigrationRecoveryResolveClientUsesAdministratorEndpoint(t *testing.T) {
	const operationID = "12345678-1234-4123-8123-123456789abc"
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/minecraft/instances/jv-a1b2c3/migration/recovery/resolve" || r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatalf("unexpected resolve request %s %s", r.Method, r.URL.Path)
		}
		var request AdminMigrationRecoveryResolveRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.OperationID != operationID || !request.KeepCurrentState {
			t.Fatalf("unexpected resolve body %#v, err=%v", request, err)
		}
		body := `{"operation":{"operation_id":"` + operationID + `","instance_id":"jv-a1b2c3","operation_type":"migration_recovery","state":"resolved"}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	response, err := client.AdminMigrationRecoveryResolve(WithMigrationInstance(context.Background(), "jv-a1b2c3"), "session-token", AdminMigrationRecoveryResolveRequest{
		OperationID: operationID, KeepCurrentState: true,
	})
	if err != nil || response.Operation == nil || response.Operation.State != "resolved" {
		t.Fatalf("resolve response = %#v, err=%v", response, err)
	}
}

func TestServerMigrationDiscoveryClientsUseAuthoritativeEndpoints(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		call func(*Client) error
	}{
		{
			name: "export",
			path: adminMigrationExportPath,
			body: `{"ok":true,"schema_version":"v1","suggested_filename":"justvoxel-migration-test.tar.gz","configured_backup_available":true,"devices":[],"target_kinds":["local","backup"]}`,
			call: func(client *Client) error {
				_, err := client.AdminMigrationExportDiscovery(WithMigrationInstance(context.Background(), "jv-a1b2c3"), "session-token")
				return err
			},
		},
		{
			name: "import",
			path: adminMigrationImportPath,
			body: `{"ok":true,"schema_version":"v1","configured":true,"source_kinds":["local","backup"],"defaults":{"data_path":"/var/lib/justvoxel/minecraft","backup_path":"/var/lib/justvoxel/backups","java_memory":"4G","container_memory":"6G","timezone":"UTC","java_port":25565,"bedrock_port":19132,"backup_keep":7,"backup_daily_time":"04:30","backup_automatic":true}}`,
			call: func(client *Client) error {
				_, err := client.AdminMigrationImportDiscovery(WithMigrationInstance(context.Background(), "jv-a1b2c3"), "session-token")
				return err
			},
		},
		{
			name: "recovery",
			path: adminMigrationRecoveryPath,
			body: `{"ok":true,"schema_version":"v1","configured":true,"transactions":[]}`,
			call: func(client *Client) error {
				_, err := client.AdminMigrationRecoveryDiscovery(WithMigrationInstance(context.Background(), "jv-a1b2c3"), "session-token")
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.Path != strings.Replace(tc.path, "/v1/admin/migration/", "/v1/minecraft/instances/jv-a1b2c3/migration/", 1) {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer session-token" {
					t.Fatalf("missing bearer session: %q", r.Header.Get("Authorization"))
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}}
			if err := tc.call(client); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServerMigrationPlanAndApplyClientPaths(t *testing.T) {
	const fingerprint = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const operationID = "12345678-1234-4123-8123-123456789abc"

	tests := []struct {
		name   string
		path   string
		status int
		body   string
		call   func(*Client) error
	}{
		{
			name:   "export plan",
			path:   adminMigrationExportPlanPath,
			status: http.StatusOK,
			body:   `{"ok":true,"schema_version":"v1","plan_fingerprint":"` + fingerprint + `","warnings":[]}`,
			call: func(client *Client) error {
				_, err := client.AdminMigrationExportPlan(WithMigrationInstance(context.Background(), "jv-a1b2c3"), "session-token", AdminMigrationExportTargetRequest{Kind: "local", Path: "/tmp", Filename: "justvoxel-migration-test.tar.gz"})
				return err
			},
		},
		{
			name:   "import plan",
			path:   adminMigrationImportPlanPath,
			status: http.StatusOK,
			body:   `{"ok":true,"schema_version":"v1","plan_fingerprint":"` + fingerprint + `","warnings":[],"candidates":[],"source_entries":[]}`,
			call: func(client *Client) error {
				_, err := client.AdminMigrationImportPlan(WithMigrationInstance(context.Background(), "jv-a1b2c3"), "session-token", AdminMigrationImportRequest{})
				return err
			},
		},
		{
			name:   "recovery plan",
			path:   adminMigrationRecoveryPlanPath,
			status: http.StatusOK,
			body:   `{"ok":true,"schema_version":"v1","plan_fingerprint":"` + fingerprint + `","warnings":[]}`,
			call: func(client *Client) error {
				_, err := client.AdminMigrationRecoveryPlan(WithMigrationInstance(context.Background(), "jv-a1b2c3"), "session-token", AdminMigrationRecoveryPlanRequest{Transaction: "/var/lib/justvoxel/test"})
				return err
			},
		},
		{
			name:   "export apply",
			path:   adminMigrationExportApplyPath,
			status: http.StatusAccepted,
			body:   `{"ok":true,"created":true,"operation":{"schema_version":"v1","operation_id":"` + operationID + `","instance_id":"jv-a1b2c3","operation_type":"migration_export","plan_fingerprint":"` + fingerprint + `","state":"queued","stage":"queued","status":"Queued.","started_at":"x","updated_at":"x","rollback":{"state":"not_started"}}}`,
			call: func(client *Client) error {
				_, err := client.AdminMigrationExportApply(WithMigrationInstance(context.Background(), "jv-a1b2c3"), "session-token", AdminMigrationExportApplyRequest{PlanFingerprint: fingerprint})
				return err
			},
		},
		{
			name:   "import apply",
			path:   adminMigrationImportApplyPath,
			status: http.StatusAccepted,
			body:   `{"ok":true,"created":true,"operation":{"schema_version":"v1","operation_id":"` + operationID + `","instance_id":"jv-a1b2c3","operation_type":"migration_import","plan_fingerprint":"` + fingerprint + `","state":"queued","stage":"queued","status":"Queued.","started_at":"x","updated_at":"x","rollback":{"state":"not_started"}}}`,
			call: func(client *Client) error {
				_, err := client.AdminMigrationImportApply(WithMigrationInstance(context.Background(), "jv-a1b2c3"), "session-token", AdminMigrationImportApplyRequest{PlanFingerprint: fingerprint})
				return err
			},
		},
		{
			name:   "import resolve",
			path:   adminMigrationImportResolvePath,
			status: http.StatusOK,
			body:   `{"operation":{"schema_version":"v1","operation_id":"` + operationID + `","instance_id":"jv-a1b2c3","operation_type":"migration_import","plan_fingerprint":"` + fingerprint + `","state":"resolved","stage":"resolved","status":"Current server kept.","started_at":"x","updated_at":"x","finished_at":"x","rollback":{"state":"not_started"}}}`,
			call: func(client *Client) error {
				_, err := client.AdminMigrationImportResolve(WithMigrationInstance(context.Background(), "jv-a1b2c3"), "session-token", AdminMigrationImportResolveRequest{OperationID: operationID, KeepCurrentState: true})
				return err
			},
		},
		{
			name:   "recovery apply",
			path:   adminMigrationRecoveryApplyPath,
			status: http.StatusAccepted,
			body:   `{"ok":true,"created":true,"operation":{"schema_version":"v1","operation_id":"` + operationID + `","instance_id":"jv-a1b2c3","operation_type":"migration_recovery","plan_fingerprint":"` + fingerprint + `","state":"queued","stage":"queued","status":"Queued.","started_at":"x","updated_at":"x","rollback":{"state":"not_started"}}}`,
			call: func(client *Client) error {
				_, err := client.AdminMigrationRecoveryApply(WithMigrationInstance(context.Background(), "jv-a1b2c3"), "session-token", AdminMigrationRecoveryApplyRequest{PlanFingerprint: fingerprint})
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPost || r.URL.Path != strings.Replace(tc.path, "/v1/admin/migration/", "/v1/minecraft/instances/jv-a1b2c3/migration/", 1) {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}}
			if err := tc.call(client); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServerMigrationDiscoveryRejectsUnknownResponseFields(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		body := `{"ok":true,"schema_version":"v1","configured":true,"transactions":[],"unexpected":"field"}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	if _, err := client.AdminMigrationRecoveryDiscovery(WithMigrationInstance(context.Background(), "jv-a1b2c3"), "session-token"); err == nil {
		t.Fatal("unknown migration response field unexpectedly accepted")
	}
}

func TestServerMigrationClientMapsAuthorizationErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{status: http.StatusUnauthorized, want: ErrUnauthorized},
		{status: http.StatusForbidden, want: ErrPasswordChangeRequired},
	} {
		client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: tc.status,
				Body:       io.NopCloser(strings.NewReader(`{"error":"denied"}`)),
				Header:     make(http.Header),
			}, nil
		})}}
		_, err := client.AdminMigrationRecoveryDiscovery(WithMigrationInstance(context.Background(), "jv-a1b2c3"), "session-token")
		if !errors.Is(err, tc.want) {
			t.Fatalf("status %d error = %v, want %v", tc.status, err, tc.want)
		}
	}
}

func TestServerMigrationImportPlanAcceptsRichPaperCandidate(t *testing.T) {
	const fingerprint = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	body := `{"ok":true,"schema_version":"v1","plan_fingerprint":"` + fingerprint + `","warnings":[],"source_entries":[],"candidates":[{"root":"/tmp/source/minecraft","root_relative":"minecraft","sourceType":"itzg-paper","supported":true,"levelName":"world","minecraftVersion":"26.2","onlineMode":true,"gameMode":"survival","difficulty":"normal","whitelistEnabled":true,"enforceWhitelist":true,"maxPlayers":"10","motd":"JustVoxel","javaPortHint":"25565","pluginJarCount":3,"pluginJars":["Geyser-Spigot.jar","ViaVersion.jar","floodgate-spigot.jar"],"geyserEnabled":true,"geyserAuthType":"floodgate","bedrockPortHint":19132,"floodgateEnabled":true,"floodgateKeySha256":"abc"}]}`

	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/minecraft/instances/jv-a1b2c3/migration/import/plan" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	plan, err := client.AdminMigrationImportPlan(WithMigrationInstance(context.Background(), "jv-a1b2c3"), "session-token", AdminMigrationImportRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(plan.Candidates))
	}
	candidate := plan.Candidates[0]
	if candidate.LevelName != "world" || candidate.MinecraftVersion != "26.2" || candidate.PluginJarCount != 3 || !candidate.GeyserEnabled || !candidate.FloodgateEnabled {
		t.Fatalf("rich candidate was not preserved: %+v", candidate)
	}
	if candidate.OnlineMode == nil || !*candidate.OnlineMode || candidate.WhitelistEnabled == nil || !*candidate.WhitelistEnabled || candidate.EnforceWhitelist == nil || !*candidate.EnforceWhitelist {
		t.Fatalf("candidate boolean metadata was not preserved: %+v", candidate)
	}
}

func TestMigrationClientRequiresSelectionBeforeTransport(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("transport ran without immutable selection")
		return nil, nil
	})}}
	for _, id := range []string{"", "Family", "jv-../wrong"} {
		if _, err := client.AdminMigrationImportDiscovery(WithMigrationInstance(context.Background(), id), "session"); err == nil {
			t.Fatal("invalid selection accepted", id)
		}
	}
}
func TestMigrationClientRejectsSiblingJournal(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"operation":{"operation_id":"23456789-1234-4abc-8def-123456789abc","instance_id":"jv-d4e5f6","operation_type":"migration_import","state":"running"}}`))}, nil
	})}}
	if _, err := client.AdminCurrentMigrationOperation(WithMigrationInstance(context.Background(), "jv-a1b2c3"), "session"); err == nil {
		t.Fatal("sibling journal accepted")
	}
}
