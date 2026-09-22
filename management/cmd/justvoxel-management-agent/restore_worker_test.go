package main

import (
	"context"
	"errors"
	"testing"
)

func TestExecuteRestoreTransactionSuccessTracksProgress(t *testing.T) {
	store := openTestOperationStore(t)
	operation, _, err := store.beginRestore(testRestoreFingerprint)
	if err != nil {
		t.Fatal(err)
	}

	old := runAdminRestoreTransactionHelper
	defer func() { runAdminRestoreTransactionHelper = old }()
	runAdminRestoreTransactionHelper = func(_ context.Context, request restoreTransactionRequest, handle func(restoreTransactionEvent) error) error {
		if request.OperationID != operation.OperationID || request.ArchiveIdentity == "" {
			t.Fatalf("unexpected transaction request: %#v", request)
		}
		for _, event := range []restoreTransactionEvent{
			{Event: "progress", State: "validating", Stage: "archive_integrity", Status: "Checking archive."},
			{Event: "progress", State: "running", Stage: "staging", Status: "Preparing staging."},
			{Event: "progress", State: "verifying", Stage: "minecraft_runtime", Status: "Checking Minecraft."},
			{Event: "result", Outcome: "succeeded", Status: "Restore completed."},
		} {
			if err := handle(event); err != nil {
				return err
			}
		}
		return nil
	}

	err = executeRestoreTransaction(context.Background(), store, operation.OperationID, restoreExecutionPlan{
		BackupID: "minecraft-2026-09-20-043000.tar.gz", Mode: "world", ArchiveIdentity: "8:1:123:456",
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.get(operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != operationSucceeded || loaded.Stage != "completed" {
		t.Fatalf("successful Restore journal = %#v", loaded)
	}
	if current, err := store.currentRestore(); err != nil || current != nil {
		t.Fatalf("current Restore after success = %#v err=%v", current, err)
	}
}

func TestExecuteRestoreTransactionRollbackTracksTerminalState(t *testing.T) {
	store := openTestOperationStore(t)
	operation, _, err := store.beginRestore(testRestoreFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	old := runAdminRestoreTransactionHelper
	defer func() { runAdminRestoreTransactionHelper = old }()
	runAdminRestoreTransactionHelper = func(_ context.Context, _ restoreTransactionRequest, handle func(restoreTransactionEvent) error) error {
		for _, event := range []restoreTransactionEvent{
			{Event: "progress", State: "validating", Stage: "archive_safety", Status: "Checking archive."},
			{Event: "progress", State: "running", Stage: "switching", Status: "Switching data."},
			{Event: "progress", State: "rolling_back", Stage: "rollback", Status: "Restoring original data."},
			{Event: "result", Outcome: "rolled_back", Status: "Original data restored."},
		} {
			if err := handle(event); err != nil {
				return err
			}
		}
		return nil
	}
	if err := executeRestoreTransaction(context.Background(), store, operation.OperationID, restoreExecutionPlan{
		BackupID: "minecraft-2026-09-20-043000.tar.gz", Mode: "full", ArchiveIdentity: "8:1:123:456",
	}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := store.get(operation.OperationID)
	if loaded.State != operationRolledBack || loaded.Rollback.State != "succeeded" {
		t.Fatalf("rolled-back Restore journal = %#v", loaded)
	}
}

func TestExecuteRestoreTransactionUnexpectedBackendFailureNeedsAttention(t *testing.T) {
	store := openTestOperationStore(t)
	operation, _, err := store.beginRestore(testRestoreFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	old := runAdminRestoreTransactionHelper
	defer func() { runAdminRestoreTransactionHelper = old }()
	runAdminRestoreTransactionHelper = func(_ context.Context, _ restoreTransactionRequest, handle func(restoreTransactionEvent) error) error {
		if err := handle(restoreTransactionEvent{Event: "progress", State: "running", Stage: "staging", Status: "Preparing staging."}); err != nil {
			return err
		}
		return errors.New("backend died")
	}
	err = executeRestoreTransaction(context.Background(), store, operation.OperationID, restoreExecutionPlan{
		BackupID: "minecraft-2026-09-20-043000.tar.gz", Mode: "world", ArchiveIdentity: "8:1:123:456",
	})
	if err == nil {
		t.Fatal("unexpected backend failure returned nil")
	}
	loaded, _ := store.get(operation.OperationID)
	if loaded.State != operationNeedsAttention {
		t.Fatalf("backend failure journal = %#v", loaded)
	}
	current, _ := store.currentRestore()
	if current == nil || current.OperationID != operation.OperationID {
		t.Fatalf("needs-attention Restore was not kept current: %#v", current)
	}
}

func TestExecuteRestoreTransactionNeedsAttentionResultStaysCurrent(t *testing.T) {
	store := openTestOperationStore(t)
	operation, _, err := store.beginRestore(testRestoreFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	old := runAdminRestoreTransactionHelper
	defer func() { runAdminRestoreTransactionHelper = old }()
	runAdminRestoreTransactionHelper = func(_ context.Context, _ restoreTransactionRequest, handle func(restoreTransactionEvent) error) error {
		return handle(restoreTransactionEvent{Event: "result", Outcome: "needs_attention", Status: "Recovery state requires review."})
	}
	if err := executeRestoreTransaction(context.Background(), store, operation.OperationID, restoreExecutionPlan{
		BackupID: "minecraft-2026-09-20-043000.tar.gz", Mode: "world", ArchiveIdentity: "8:1:123:456",
	}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := store.get(operation.OperationID)
	if loaded.State != operationNeedsAttention {
		t.Fatalf("needs-attention result journal = %#v", loaded)
	}
}
