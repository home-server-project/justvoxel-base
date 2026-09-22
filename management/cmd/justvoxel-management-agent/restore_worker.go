package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os/exec"
	"time"
)

const adminRestoreTransactionHelper = "/usr/libexec/justvoxel/mjust/admin-restore-transaction-json"

var restoreWorkerTimeout = 6 * time.Hour

type restoreTransactionRequest struct {
	OperationID      string `json:"operation_id"`
	PlanFingerprint  string `json:"plan_fingerprint"`
	BackupID         string `json:"backup_id"`
	Mode             string `json:"mode"`
	ArchiveIdentity  string `json:"archive_identity"`
	PlayersConfirmed bool   `json:"players_confirmed"`
}

type restoreTransactionEvent struct {
	Event   string `json:"event"`
	State   string `json:"state,omitempty"`
	Stage   string `json:"stage,omitempty"`
	Status  string `json:"status"`
	Outcome string `json:"outcome,omitempty"`
}

var runAdminRestoreTransactionHelper = func(ctx context.Context, request restoreTransactionRequest, handle func(restoreTransactionEvent) error) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, adminRestoreTransactionHelper)
	cmd.Stdin = bytes.NewReader(payload)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024), 64*1024)
	for scanner.Scan() {
		var event restoreTransactionEvent
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return errors.New("Restore transaction helper returned invalid progress data")
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return errors.New("Restore transaction helper returned invalid progress data")
		}
		if err := handle(event); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}

var startRestoreWorker = func(s *server, operationID string, plan restoreExecutionPlan) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), restoreWorkerTimeout)
		defer cancel()
		if err := executeRestoreTransaction(ctx, s.operations, operationID, plan); err != nil {
			log.Printf("Restore operation %s finished with error: %v", operationID, err)
		}
	}()
}

func executeRestoreTransaction(parent context.Context, store *operationStore, operationID string, plan restoreExecutionPlan) error {
	if store == nil {
		return errors.New("operation store is unavailable")
	}
	operation, err := store.get(operationID)
	if err != nil {
		return err
	}
	if operation.OperationType != operationTypeRestore || operation.PlanFingerprint == "" {
		return errors.New("invalid Restore operation identity")
	}
	if !adminRestoreBackupIDPattern.MatchString(plan.BackupID) || (plan.Mode != "world" && plan.Mode != "full") || plan.ArchiveIdentity == "" {
		return markRestoreNeedsAttention(store, operationID, "invalid_execution_plan", "Restore execution data is incomplete; administrator attention is required.")
	}
	if _, err := store.transition(operationID, operationValidating, "restore_preflight", "Revalidating the reviewed Restore before touching Minecraft data."); err != nil {
		return err
	}

	request := restoreTransactionRequest{
		OperationID: operationID,
		PlanFingerprint: operation.PlanFingerprint,
		BackupID: plan.BackupID,
		Mode: plan.Mode,
		ArchiveIdentity: plan.ArchiveIdentity,
		PlayersConfirmed: plan.PlayersConfirmed,
	}
	finalSeen := false
	err = runAdminRestoreTransactionHelper(parent, request, func(event restoreTransactionEvent) error {
		switch event.Event {
		case "progress":
			return applyRestoreProgress(store, operationID, event)
		case "result":
			if finalSeen {
				return errors.New("Restore transaction helper returned multiple final results")
			}
			finalSeen = true
			return applyRestoreResult(store, operationID, event)
		default:
			return errors.New("Restore transaction helper returned an unsupported event")
		}
	})
	if err != nil {
		_ = markRestoreNeedsAttention(store, operationID, "restore_backend_interrupted", "Restore backend stopped unexpectedly; preserved recovery state requires administrator attention.")
		return err
	}
	if !finalSeen {
		err := errors.New("Restore transaction helper ended without a final result")
		_ = markRestoreNeedsAttention(store, operationID, "restore_backend_incomplete", "Restore backend ended without a final safety result; administrator attention is required.")
		return err
	}
	return nil
}

func applyRestoreProgress(store *operationStore, operationID string, event restoreTransactionEvent) error {
	if event.Stage == "" || event.Status == "" {
		return errors.New("Restore progress event is incomplete")
	}
	var desired operationState
	switch event.State {
	case string(operationValidating):
		desired = operationValidating
	case string(operationRunning):
		desired = operationRunning
	case string(operationVerifying):
		desired = operationVerifying
	case string(operationRollingBack):
		current, err := store.get(operationID)
		if err != nil {
			return err
		}
		if current.State != operationFailed && current.State != operationRollingBack {
			if _, err := store.transition(operationID, operationFailed, "restore_failed", "Restore did not complete successfully; rollback is starting."); err != nil {
				return err
			}
		}
		current, err = store.get(operationID)
		if err != nil {
			return err
		}
		if current.State == operationFailed {
			_, err = store.transition(operationID, operationRollingBack, event.Stage, event.Status)
			return err
		}
		if current.State == operationRollingBack {
			_, err = store.updateProgress(operationID, operationRollingBack, event.Stage, event.Status)
			return err
		}
		return errors.New("Restore rollback progress arrived in an invalid operation state")
	default:
		return errors.New("Restore progress event has an invalid state")
	}

	current, err := store.get(operationID)
	if err != nil {
		return err
	}
	if current.State == desired {
		_, err = store.updateProgress(operationID, desired, event.Stage, event.Status)
		return err
	}
	_, err = store.transition(operationID, desired, event.Stage, event.Status)
	return err
}

func applyRestoreResult(store *operationStore, operationID string, event restoreTransactionEvent) error {
	if event.Status == "" {
		return errors.New("Restore result event is incomplete")
	}
	current, err := store.get(operationID)
	if err != nil {
		return err
	}

	switch event.Outcome {
	case "succeeded":
		if current.State != operationVerifying {
			return errors.New("Restore success arrived before verification")
		}
		_, err = store.transition(operationID, operationSucceeded, "completed", event.Status)
		return err
	case "rolled_back":
		if current.State != operationFailed && current.State != operationRollingBack {
			if _, err := store.transition(operationID, operationFailed, "restore_failed", "Restore did not complete successfully; safe rollback state is being recorded."); err != nil {
				return err
			}
		}
		current, err = store.get(operationID)
		if err != nil {
			return err
		}
		if current.State == operationFailed {
			if _, err := store.transition(operationID, operationRollingBack, "rollback", "Finalizing Restore rollback state."); err != nil {
				return err
			}
		}
		_, err = store.transition(operationID, operationRolledBack, "restore_rolled_back", event.Status)
		return err
	case "needs_attention":
		return markRestoreNeedsAttention(store, operationID, "restore_needs_attention", event.Status)
	default:
		return errors.New("Restore result event has an invalid outcome")
	}
}

func markRestoreNeedsAttention(store *operationStore, operationID, stage, status string) error {
	current, err := store.get(operationID)
	if err != nil {
		return err
	}
	if current.State == operationNeedsAttention {
		_, err = store.updateProgress(operationID, operationNeedsAttention, stage, status)
		return err
	}
	if current.State == operationSucceeded || current.State == operationRolledBack {
		return errors.New("completed Restore operation cannot require attention")
	}
	_, err = store.transition(operationID, operationNeedsAttention, stage, status)
	return err
}
