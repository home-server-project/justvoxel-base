package main

import (
	"context"
	"errors"
	"testing"
)

const testMigrationExportFingerprint = "sha256:6666666666666666666666666666666666666666666666666666666666666666"

func testMigrationExportExecutionPlan() migrationExportExecutionPlan {
	return migrationExportExecutionPlan{
		Request:                testMigrationExportRequest(),
		ConfigIdentity:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		DataIdentity:           "1:2:3:4",
		TargetIdentity:         "local:5:6:7",
		ExpectedMinecraftState: "running",
	}
}

func TestExecuteMigrationExportTransactionSuccessTracksProgress(t *testing.T) {
	store := openTestOperationStore(t)
	operation, _, err := store.beginMigrationExport(testMigrationExportFingerprint)
	if err != nil {
		t.Fatal(err)
	}

	old := runAdminMigrationExportTransactionHelper
	defer func() { runAdminMigrationExportTransactionHelper = old }()
	runAdminMigrationExportTransactionHelper = func(_ context.Context, request migrationExportTransactionRequest, handle func(migrationExportTransactionEvent) error) error {
		if request.OperationID != operation.OperationID ||
			request.ConfigIdentity == "" ||
			request.DataIdentity == "" ||
			request.TargetIdentity == "" ||
			request.ExpectedMinecraftState != "running" {
			t.Fatalf("unexpected export transaction request: %#v", request)
		}
		for _, event := range []migrationExportTransactionEvent{
			{Event: "progress", State: "validating", Stage: "export_preflight", Status: "Checking export."},
			{Event: "progress", State: "running", Stage: "exporting", Status: "Creating export."},
			{Event: "progress", State: "verifying", Stage: "minecraft_runtime", Status: "Checking Minecraft."},
			{Event: "result", Outcome: "succeeded", Status: "Export completed."},
		} {
			if err := handle(event); err != nil {
				return err
			}
		}
		return nil
	}

	if err := executeMigrationExportTransaction(context.Background(), store, operation.OperationID, testMigrationExportExecutionPlan()); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.get(operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != operationSucceeded || loaded.Stage != "completed" {
		t.Fatalf("successful export journal = %#v", loaded)
	}
	if current, err := store.currentMigration(); err != nil || current != nil {
		t.Fatalf("current migration after export success = %#v err=%v", current, err)
	}
}

func TestExecuteMigrationExportTransactionSafeFailureTracksRolledBack(t *testing.T) {
	store := openTestOperationStore(t)
	operation, _, err := store.beginMigrationExport(testMigrationExportFingerprint)
	if err != nil {
		t.Fatal(err)
	}

	old := runAdminMigrationExportTransactionHelper
	defer func() { runAdminMigrationExportTransactionHelper = old }()
	runAdminMigrationExportTransactionHelper = func(_ context.Context, _ migrationExportTransactionRequest, handle func(migrationExportTransactionEvent) error) error {
		for _, event := range []migrationExportTransactionEvent{
			{Event: "progress", State: "validating", Stage: "export_preflight", Status: "Checking export."},
			{Event: "progress", State: "running", Stage: "target_prepare", Status: "Preparing target."},
			{Event: "result", Outcome: "rolled_back", Status: "Export did not complete; runtime state is safe."},
		} {
			if err := handle(event); err != nil {
				return err
			}
		}
		return nil
	}

	if err := executeMigrationExportTransaction(context.Background(), store, operation.OperationID, testMigrationExportExecutionPlan()); err != nil {
		t.Fatal(err)
	}
	loaded, _ := store.get(operation.OperationID)
	if loaded.State != operationRolledBack || loaded.Rollback.State != "succeeded" {
		t.Fatalf("safe export failure journal = %#v", loaded)
	}
}

func TestExecuteMigrationExportTransactionUnexpectedBackendFailureNeedsAttention(t *testing.T) {
	store := openTestOperationStore(t)
	operation, _, err := store.beginMigrationExport(testMigrationExportFingerprint)
	if err != nil {
		t.Fatal(err)
	}

	old := runAdminMigrationExportTransactionHelper
	defer func() { runAdminMigrationExportTransactionHelper = old }()
	runAdminMigrationExportTransactionHelper = func(_ context.Context, _ migrationExportTransactionRequest, handle func(migrationExportTransactionEvent) error) error {
		if err := handle(migrationExportTransactionEvent{Event: "progress", State: "running", Stage: "exporting", Status: "Creating export."}); err != nil {
			return err
		}
		return errors.New("backend died")
	}

	err = executeMigrationExportTransaction(context.Background(), store, operation.OperationID, testMigrationExportExecutionPlan())
	if err == nil {
		t.Fatal("unexpected export backend failure returned nil")
	}
	loaded, _ := store.get(operation.OperationID)
	if loaded.State != operationNeedsAttention {
		t.Fatalf("backend failure journal = %#v", loaded)
	}
	current, _ := store.currentMigration()
	if current == nil || current.OperationID != operation.OperationID {
		t.Fatalf("needs-attention export was not kept current: %#v", current)
	}
}
