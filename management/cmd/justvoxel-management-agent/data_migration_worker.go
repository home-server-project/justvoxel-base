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

const adminDataMigrationTransactionHelper = "/usr/libexec/justvoxel/mjust/admin-data-migration-transaction-json"

var dataMigrationWorkerTimeout = 8 * time.Hour

type dataMigrationTransactionRequest struct {
	OperationID            string                    `json:"operation_id"`
	PlanFingerprint        string                    `json:"plan_fingerprint"`
	Request                adminDataMigrationRequest `json:"request"`
	TargetIdentity         string                    `json:"target_identity"`
	ConfigIdentity         string                    `json:"config_identity"`
	ExpectedMinecraftState string                    `json:"expected_minecraft_state"`
	PlayersConfirmed       bool                      `json:"players_confirmed"`
}

type dataMigrationTransactionEvent struct {
	Event   string `json:"event"`
	State   string `json:"state,omitempty"`
	Stage   string `json:"stage,omitempty"`
	Status  string `json:"status"`
	Outcome string `json:"outcome,omitempty"`
}

var runAdminDataMigrationTransactionHelper = func(ctx context.Context, request dataMigrationTransactionRequest, handle func(dataMigrationTransactionEvent) error) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, adminDataMigrationTransactionHelper)
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
		var event dataMigrationTransactionEvent
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return errors.New("Minecraft data migration helper returned invalid progress data")
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return errors.New("Minecraft data migration helper returned invalid progress data")
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

var startDataMigrationWorker = func(s *server, operationID string, plan dataMigrationExecutionPlan) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), dataMigrationWorkerTimeout)
		defer cancel()
		if err := executeDataMigrationTransaction(ctx, s.operations, operationID, plan); err != nil {
			log.Printf("Minecraft data migration operation %s finished with error: %v", operationID, err)
		}
	}()
}

func executeDataMigrationTransaction(parent context.Context, store *operationStore, operationID string, plan dataMigrationExecutionPlan) error {
	if store == nil {
		return errors.New("operation store is unavailable")
	}
	operation, err := store.get(operationID)
	if err != nil {
		return err
	}
	if operation.OperationType != operationTypeDataMigration || operation.PlanFingerprint == "" {
		return errors.New("invalid Minecraft data migration operation identity")
	}
	if !validAdminDataMigrationRequest(plan.Request) ||
		plan.TargetIdentity == "" ||
		plan.ConfigIdentity == "" ||
		(plan.ExpectedMinecraftState != "running" && plan.ExpectedMinecraftState != "stopped") {
		return markDataMigrationNeedsAttention(store, operationID, "invalid_execution_plan", "Minecraft data migration execution data is incomplete; administrator attention is required.")
	}
	if _, err := store.transition(operationID, operationValidating, "migration_preflight", "Revalidating the reviewed Minecraft data migration before changing storage."); err != nil {
		return err
	}

	request := dataMigrationTransactionRequest{
		OperationID:            operationID,
		PlanFingerprint:        operation.PlanFingerprint,
		Request:                plan.Request,
		TargetIdentity:         plan.TargetIdentity,
		ConfigIdentity:         plan.ConfigIdentity,
		ExpectedMinecraftState: plan.ExpectedMinecraftState,
		PlayersConfirmed:       plan.PlayersConfirmed,
	}

	finalSeen := false
	err = runAdminDataMigrationTransactionHelper(parent, request, func(event dataMigrationTransactionEvent) error {
		switch event.Event {
		case "progress":
			return applyDataMigrationProgress(store, operationID, event)
		case "result":
			if finalSeen {
				return errors.New("Minecraft data migration helper returned multiple final results")
			}
			finalSeen = true
			return applyDataMigrationResult(store, operationID, event)
		default:
			return errors.New("Minecraft data migration helper returned an unsupported event")
		}
	})
	if err != nil {
		_ = markDataMigrationNeedsAttention(store, operationID, "migration_backend_interrupted", "Minecraft data migration backend stopped unexpectedly; preserved migration state requires administrator attention.")
		return err
	}
	if !finalSeen {
		err := errors.New("Minecraft data migration helper ended without a final result")
		_ = markDataMigrationNeedsAttention(store, operationID, "migration_backend_incomplete", "Minecraft data migration backend ended without a final safety result; administrator attention is required.")
		return err
	}
	return nil
}

func applyDataMigrationProgress(store *operationStore, operationID string, event dataMigrationTransactionEvent) error {
	if event.Stage == "" || event.Status == "" {
		return errors.New("Minecraft data migration progress event is incomplete")
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
			if _, err := store.transition(operationID, operationFailed, "migration_failed", "Minecraft data migration did not complete successfully; rollback is starting."); err != nil {
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
		return errors.New("Minecraft data migration rollback progress arrived in an invalid operation state")
	default:
		return errors.New("Minecraft data migration progress event has an invalid state")
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

func applyDataMigrationResult(store *operationStore, operationID string, event dataMigrationTransactionEvent) error {
	if event.Status == "" {
		return errors.New("Minecraft data migration result event is incomplete")
	}
	current, err := store.get(operationID)
	if err != nil {
		return err
	}

	switch event.Outcome {
	case "succeeded":
		if current.State != operationVerifying {
			return errors.New("Minecraft data migration success arrived before verification")
		}
		_, err = store.transition(operationID, operationSucceeded, "completed", event.Status)
		return err
	case "rolled_back":
		if current.State != operationFailed && current.State != operationRollingBack {
			if _, err := store.transition(operationID, operationFailed, "migration_failed", "Minecraft data migration did not complete; safe rollback state is being recorded."); err != nil {
				return err
			}
		}
		current, err = store.get(operationID)
		if err != nil {
			return err
		}
		if current.State == operationFailed {
			if _, err := store.transition(operationID, operationRollingBack, "rollback", "Finalizing Minecraft data migration rollback state."); err != nil {
				return err
			}
		}
		_, err = store.transition(operationID, operationRolledBack, "migration_rolled_back", event.Status)
		return err
	case "needs_attention":
		return markDataMigrationNeedsAttention(store, operationID, "migration_needs_attention", event.Status)
	default:
		return errors.New("Minecraft data migration result event has an invalid outcome")
	}
}

func markDataMigrationNeedsAttention(store *operationStore, operationID, stage, status string) error {
	current, err := store.get(operationID)
	if err != nil {
		return err
	}
	if current.State == operationNeedsAttention {
		_, err = store.updateProgress(operationID, operationNeedsAttention, stage, status)
		return err
	}
	if current.State == operationSucceeded || current.State == operationRolledBack {
		return errors.New("completed Minecraft data migration cannot require attention")
	}
	_, err = store.transition(operationID, operationNeedsAttention, stage, status)
	return err
}
