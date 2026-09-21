package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const dataMigrationTestFingerprint = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
const dataMigrationTestOperationID = "12345678-1234-4123-8123-abcdefabcdef"

func TestAdminDataMigrationDiscoveryPreservesCandidateGroups(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != adminDataMigrationDiscoveryPath {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		body := `{"ok":true,"schema_version":"v1","warnings":[],"whole_disks":[{"path":"/dev/vdb","model":"Virtual Disk","transport":"virtio","filesystem":"","mountpoint":"","size_bytes":10737418240,"system_disk":false}],"partitions":[{"path":"/dev/vdc1","parent":"/dev/vdc","filesystem":"xfs","mountpoint":"/mnt/data","size_bytes":8589934592,"system_disk":false}],"blank_partitions":[],"free_space_disks":[]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	response, err := client.AdminDataMigrationDiscovery(context.Background(), "session-token")
	if err != nil {
		t.Fatal(err)
	}
	if len(response.WholeDisks) != 1 || response.WholeDisks[0].Path != "/dev/vdb" ||
		len(response.Partitions) != 1 || response.Partitions[0].Mountpoint != "/mnt/data" {
		t.Fatalf("unexpected migration discovery: %#v", response)
	}
}

func TestAdminDataMigrationPlanPreservesWarningsAndRequirements(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request AdminDataMigrationPlanRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Operation != "erase_disk" || request.Device != "/dev/vdb" {
			t.Fatalf("unexpected migration plan request: %#v", request)
		}
		body := `{"ok":true,"schema_version":"v1","plan_fingerprint":"` + dataMigrationTestFingerprint + `","normalized":{"operation":"erase_disk","device":"/dev/vdb","model":"Virtual Disk","transport":"virtio","filesystem":"xfs","mount_point":"/var/mnt/justvoxel-data","path":"/var/mnt/justvoxel-data/minecraft","size_gib":"all","size_bytes":10737418240,"data_bytes":2147483648,"target_capacity_bytes":10737418240,"current_data_path":"/var/lib/justvoxel/minecraft","free_start":"","free_end":"","planned_end":"","free_mib":0,"planned_mib":0},"warnings":[{"code":"destructive_disk","message":"This erases the entire selected disk."}],"requirements":{"migration_confirmation_required":true,"destructive_confirmation_required":true,"confirmation_phrase":"ERASE /dev/vdb","players_confirmation_required":true,"minecraft_state":"running","online":1,"players":["PlayerOne"],"exact_space_validation_on_apply":true,"cold_backup_required":true,"copy_verification_required":true,"runtime_validation_required":true}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	response, err := client.AdminDataMigrationPlan(context.Background(), "session-token", AdminDataMigrationPlanRequest{
		Operation: "erase_disk", Device: "/dev/vdb", MountPoint: "/var/mnt/justvoxel-data",
		Path: "/var/mnt/justvoxel-data/minecraft", SizeGiB: "all",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || len(response.Warnings) != 1 || response.Requirements == nil ||
		response.Requirements.ConfirmationPhrase != "ERASE /dev/vdb" ||
		!response.Requirements.PlayersConfirmationRequired {
		t.Fatalf("unexpected migration plan: %#v", response)
	}
}

func TestAdminDataMigrationPlanPreservesAuthoritativeRejection(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		body := `{"ok":false,"schema_version":"v1","code":"insufficient_space","error":"Selected target is not large enough for the current Minecraft data.","warnings":[]}`
		return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	response, err := client.AdminDataMigrationPlan(context.Background(), "session-token", AdminDataMigrationPlanRequest{
		Operation: "use_partition", Device: "/dev/vdb1", MountPoint: "/var/mnt/data",
		Path: "/var/mnt/data/minecraft", SizeGiB: "all",
	})
	if err == nil || response.OK || response.Code != "insufficient_space" || !strings.Contains(response.Error, "not large enough") {
		t.Fatalf("authoritative rejection not preserved: response=%#v err=%v", response, err)
	}
}

func TestAdminDataMigrationApplySendsConfirmationsAndPersistentOperation(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request AdminDataMigrationApplyRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if !request.MigrationConfirmed || !request.PlayersConfirmed ||
			request.Confirmation != "ERASE /dev/vdb" || request.PlanFingerprint != dataMigrationTestFingerprint {
			t.Fatalf("unexpected migration apply request: %#v", request)
		}
		body := `{"ok":true,"created":true,"operation":{"schema_version":"v1","operation_id":"` + dataMigrationTestOperationID + `","operation_type":"data_migration","plan_fingerprint":"` + dataMigrationTestFingerprint + `","state":"queued","stage":"queued","status":"Minecraft data migration operation queued.","started_at":"2026-09-20T12:00:00Z","updated_at":"2026-09-20T12:00:00Z","rollback":{"state":"not_started"}}}`
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	response, err := client.AdminDataMigrationApply(context.Background(), "session-token", AdminDataMigrationApplyRequest{
		PlanFingerprint: dataMigrationTestFingerprint,
		Request: AdminDataMigrationPlanRequest{
			Operation: "erase_disk", Device: "/dev/vdb", MountPoint: "/var/mnt/justvoxel-data",
			Path: "/var/mnt/justvoxel-data/minecraft", SizeGiB: "all",
		},
		MigrationConfirmed: true, Confirmation: "ERASE /dev/vdb", PlayersConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Operation == nil || response.Operation.OperationID != dataMigrationTestOperationID ||
		response.Operation.OperationType != "data_migration" {
		t.Fatalf("unexpected migration apply response: %#v", response)
	}
}
