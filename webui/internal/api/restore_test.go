package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const restoreTestFingerprint = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const restoreTestOperationID = "12345678-1234-4123-8123-123456789abc"

func TestAdminRestoreBackupsClientPreservesMetadata(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != adminRestoreBackupsPath {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"backups":[{"id":"minecraft-2026-09-20-043000.tar.gz","created_at":"2026-09-20T08:30:00Z","size_bytes":12345,"metadata_status":"valid","metadata":{"created_at":"2026-09-20T08:30:00Z","minecraft":{"version_mode":"pinned","configured_version":"26.2","server_reported_version":"Paper 26.2"},"bedrock":{"enabled":false,"floodgate_configured":false},"justvoxel":{"variant":"justvoxel-vm"}}}]}`)),
			Header:     make(http.Header),
		}, nil
	})}}

	response, err := client.AdminRestoreBackups(context.Background(), "session-token")
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Backups) != 1 || response.Backups[0].Metadata == nil || response.Backups[0].Metadata.Minecraft.ConfiguredVersion != "26.2" {
		t.Fatalf("unexpected restore backups: %#v", response)
	}
}

func TestAdminRestorePlanClientPreservesWarningsAndRequirements(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request AdminRestorePlanRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.BackupID != "minecraft-2026-09-20-043000.tar.gz" || request.Mode != "world" {
			t.Fatalf("unexpected restore plan request: %#v", request)
		}
		body := `{"ok":true,"schema_version":"v1","plan_fingerprint":"` + restoreTestFingerprint + `","normalized":{"backup_id":"minecraft-2026-09-20-043000.tar.gz","mode":"world","created_at":"2026-09-20T08:30:00Z","size_bytes":12345,"metadata_status":"missing","current":{"version_mode":"pinned","version":"26.3","bedrock_enabled":false},"version_relation":"unknown","validation":{"archive_integrity":"pending_apply","archive_safety":"pending_apply","staging_space":"pending_apply"}},"warnings":[{"code":"metadata_missing","message":"Backup metadata is unavailable."}],"requirements":{"destructive_confirmation_required":true,"players_confirmation_required":true,"minecraft_state":"running","online":1,"players":["PlayerOne"],"archive_integrity_validation_on_apply":true,"archive_safety_validation_on_apply":true,"staging_space_validation_on_apply":true}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	response, err := client.AdminRestorePlan(context.Background(), "session-token", AdminRestorePlanRequest{
		BackupID: "minecraft-2026-09-20-043000.tar.gz", Mode: "world",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || len(response.Warnings) != 1 || response.Requirements == nil || !response.Requirements.PlayersConfirmationRequired {
		t.Fatalf("unexpected restore plan: %#v", response)
	}
}

func TestAdminRestorePlanClientPreservesAuthoritativeRejection(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		body := `{"ok":false,"schema_version":"v1","code":"backup_newer","error":"This backup was created for a newer pinned Minecraft version.","normalized":{"backup_id":"minecraft-2026-09-20-043000.tar.gz","mode":"full","created_at":"2026-09-20T08:30:00Z","size_bytes":12345,"metadata_status":"missing","current":{"version_mode":"pinned","version":"26.3","bedrock_enabled":false},"version_relation":"backup_newer","validation":{"archive_integrity":"pending_apply","archive_safety":"pending_apply","staging_space":"pending_apply"}},"warnings":[]}`
		return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	response, err := client.AdminRestorePlan(context.Background(), "session-token", AdminRestorePlanRequest{
		BackupID: "minecraft-2026-09-20-043000.tar.gz", Mode: "full",
	})
	if err == nil || response.OK || response.Code != "backup_newer" || !strings.Contains(response.Error, "newer pinned") {
		t.Fatalf("authoritative rejection not preserved: response=%#v err=%v", response, err)
	}
}

func TestAdminRestoreApplyClientSendsConfirmationsAndPersistentOperation(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request AdminRestoreApplyRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if !request.DestructiveConfirmed || !request.PlayersConfirmed || request.PlanFingerprint != restoreTestFingerprint {
			t.Fatalf("unexpected restore apply request: %#v", request)
		}
		body := `{"ok":true,"created":true,"operation":{"schema_version":"v1","operation_id":"` + restoreTestOperationID + `","operation_type":"restore","plan_fingerprint":"` + restoreTestFingerprint + `","state":"queued","stage":"queued","status":"Restore operation queued.","started_at":"2026-09-20T12:00:00Z","updated_at":"2026-09-20T12:00:00Z","rollback":{"state":"not_started"}}}`
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	response, err := client.AdminRestoreApply(context.Background(), "session-token", AdminRestoreApplyRequest{
		PlanFingerprint:      restoreTestFingerprint,
		Request:              AdminRestorePlanRequest{BackupID: "minecraft-2026-09-20-043000.tar.gz", Mode: "world"},
		DestructiveConfirmed: true,
		PlayersConfirmed:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Operation == nil || response.Operation.OperationID != restoreTestOperationID || response.Operation.OperationType != "restore" {
		t.Fatalf("unexpected restore apply response: %#v", response)
	}
}
