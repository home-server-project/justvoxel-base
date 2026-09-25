package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func TestSharedMigrationServiceRejectsStaleFingerprintBeforeApply(t *testing.T) {
	client := &fakeStorageMinecraftMigrationAPI{plan: storageMigrationPlan()}
	request := api.AdminDataMigrationPlanRequest{
		Operation: "use_partition", Device: "/dev/vdb1",
		MountPoint: "/var/mnt/justvoxel-data", Path: "/var/mnt/justvoxel-data/minecraft", SizeGiB: "all",
	}
	plan, operation, err := dataMigrationApplyReviewed(context.Background(), client, "session-token", request, dataMigrationApplyProof{
		PlanFingerprint: "stale-fingerprint", MigrationConfirmed: true,
	})
	if err == nil {
		t.Fatal("stale migration fingerprint was accepted")
	}
	if operation != nil {
		t.Fatalf("stale migration unexpectedly created operation: %#v", operation)
	}
	if plan.PlanFingerprint != storageMigrationFingerprint {
		t.Fatalf("authoritative re-plan was not returned: %#v", plan)
	}
	if dataMigrationErrorStatus(err) != http.StatusConflict {
		t.Fatalf("stale fingerprint status=%d, want 409", dataMigrationErrorStatus(err))
	}
	if client.planCalls != 1 || client.applyCalls != 0 {
		t.Fatalf("stale fingerprint must re-plan but never apply: plan=%d apply=%d", client.planCalls, client.applyCalls)
	}
}

func TestSharedMigrationServiceValidatesPersistentOperationType(t *testing.T) {
	client := &fakeStorageMinecraftMigrationAPI{
		current: &api.PersistentOperation{OperationID: "other-op", OperationType: "backup"},
	}
	operation, err := dataMigrationCurrentOperation(context.Background(), client, "session-token")
	if err == nil {
		t.Fatal("non-migration persistent operation was accepted")
	}
	if operation != nil {
		t.Fatalf("invalid persistent operation returned: %#v", operation)
	}
	if dataMigrationErrorStatus(err) != http.StatusBadGateway {
		t.Fatalf("invalid persistent operation status=%d, want 502", dataMigrationErrorStatus(err))
	}
}
