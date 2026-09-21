package main

import (
	"context"
	"errors"
	"testing"
)

func TestExecuteDataMigrationTransactionSuccessTracksProgress(t *testing.T) {
	store := openTestOperationStore(t)
	operation, _, err := store.beginDataMigration(testDataMigrationFingerprint)
	if err != nil {
		t.Fatal(err)
	}

	old := runAdminDataMigrationTransactionHelper
	defer func() { runAdminDataMigrationTransactionHelper = old }()
	runAdminDataMigrationTransactionHelper = func(_ context.Context, request dataMigrationTransactionRequest, handle func(dataMigrationTransactionEvent) error) error {
		if request.OperationID != operation.OperationID ||
			request.TargetIdentity == "" ||
			request.ConfigIdentity == "" ||
			request.ExpectedMinecraftState != "running" {
			t.Fatalf("unexpected migration transaction request: %#v", request)
		}
		for _, event := range []dataMigrationTransactionEvent{
			{Event: "progress", State: "validating", Stage: "target_recheck", Status: "Checking target."},
			{Event: "progress", State: "running", Stage: "copying", Status: "Copying data."},
			{Event: "progress", State: "verifying", Stage: "minecraft_runtime", Status: "Checking Minecraft."},
			{Event: "result", Outcome: "succeeded", Status: "Migration completed."},
		} {
			if err := handle(event); err != nil {
				return err
			}
		}
		return nil
	}

	err = executeDataMigrationTransaction(context.Background(), store, operation.OperationID, dataMigrationExecutionPlan{
		Request: adminDataMigrationRequest{
			Operation: "use_partition", Device: "/dev/vdb1",
			MountPoint: "/var/mnt/justvoxel-data", Path: "/var/mnt/justvoxel-data/minecraft", SizeGiB: "all",
		},
		TargetIdentity:         "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ConfigIdentity:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ExpectedMinecraftState: "running",
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := store.get(operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != operationSucceeded || loaded.Stage != "completed" {
		t.Fatalf("successful migration journal = %#v", loaded)
	}
	if current, err := store.currentDataMigration(); err != nil || current != nil {
		t.Fatalf("current migration after success = %#v err=%v", current, err)
	}
}

func TestExecuteDataMigrationTransactionRollbackTracksTerminalState(t *testing.T) {
	store := openTestOperationStore(t)
	operation, _, err := store.beginDataMigration(testDataMigrationFingerprint)
	if err != nil {
		t.Fatal(err)
	}

	old := runAdminDataMigrationTransactionHelper
	defer func() { runAdminDataMigrationTransactionHelper = old }()
	runAdminDataMigrationTransactionHelper = func(_ context.Context, _ dataMigrationTransactionRequest, handle func(dataMigrationTransactionEvent) error) error {
		for _, event := range []dataMigrationTransactionEvent{
			{Event: "progress", State: "validating", Stage: "target_recheck", Status: "Checking target."},
			{Event: "progress", State: "running", Stage: "switching", Status: "Switching configuration."},
			{Event: "progress", State: "rolling_back", Stage: "rollback", Status: "Restoring original configuration."},
			{Event: "result", Outcome: "rolled_back", Status: "Original configuration restored."},
		} {
			if err := handle(event); err != nil {
				return err
			}
		}
		return nil
	}

	if err := executeDataMigrationTransaction(context.Background(), store, operation.OperationID, dataMigrationExecutionPlan{
		Request: adminDataMigrationRequest{
			Operation: "use_partition", Device: "/dev/vdb1",
			MountPoint: "/var/mnt/justvoxel-data", Path: "/var/mnt/justvoxel-data/minecraft", SizeGiB: "all",
		},
		TargetIdentity:         "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ConfigIdentity:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ExpectedMinecraftState: "running",
	}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := store.get(operation.OperationID)
	if loaded.State != operationRolledBack || loaded.Rollback.State != "succeeded" {
		t.Fatalf("rolled-back migration journal = %#v", loaded)
	}
}

func TestExecuteDataMigrationTransactionUnexpectedBackendFailureNeedsAttention(t *testing.T) {
	store := openTestOperationStore(t)
	operation, _, err := store.beginDataMigration(testDataMigrationFingerprint)
	if err != nil {
		t.Fatal(err)
	}

	old := runAdminDataMigrationTransactionHelper
	defer func() { runAdminDataMigrationTransactionHelper = old }()
	runAdminDataMigrationTransactionHelper = func(_ context.Context, _ dataMigrationTransactionRequest, handle func(dataMigrationTransactionEvent) error) error {
		if err := handle(dataMigrationTransactionEvent{Event: "progress", State: "running", Stage: "copying", Status: "Copying data."}); err != nil {
			return err
		}
		return errors.New("backend died")
	}

	err = executeDataMigrationTransaction(context.Background(), store, operation.OperationID, dataMigrationExecutionPlan{
		Request: adminDataMigrationRequest{
			Operation: "use_partition", Device: "/dev/vdb1",
			MountPoint: "/var/mnt/justvoxel-data", Path: "/var/mnt/justvoxel-data/minecraft", SizeGiB: "all",
		},
		TargetIdentity:         "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ConfigIdentity:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ExpectedMinecraftState: "running",
	})
	if err == nil {
		t.Fatal("unexpected backend failure returned nil")
	}
	loaded, _ := store.get(operation.OperationID)
	if loaded.State != operationNeedsAttention {
		t.Fatalf("backend failure journal = %#v", loaded)
	}
	current, _ := store.currentDataMigration()
	if current == nil || current.OperationID != operation.OperationID {
		t.Fatalf("needs-attention migration was not kept current: %#v", current)
	}
}
