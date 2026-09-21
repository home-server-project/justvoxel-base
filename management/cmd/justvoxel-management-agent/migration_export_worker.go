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

const adminMigrationExportTransactionHelper = "/usr/libexec/justvoxel/mjust/admin-migration-export-transaction-json"

var migrationExportWorkerTimeout = 8 * time.Hour

type migrationExportTransactionRequest struct {
	OperationID            string                            `json:"operation_id"`
	PlanFingerprint        string                            `json:"plan_fingerprint"`
	Request                adminMigrationExportTargetRequest `json:"request"`
	ConfigIdentity         string                            `json:"config_identity"`
	DataIdentity           string                            `json:"data_identity"`
	TargetIdentity         string                            `json:"target_identity"`
	ExpectedMinecraftState string                            `json:"expected_minecraft_state"`
	PlayersConfirmed       bool                              `json:"players_confirmed"`
	SMBPassword            string                            `json:"smb_password,omitempty"`
}

type migrationExportTransactionEvent struct {
	Event   string `json:"event"`
	State   string `json:"state,omitempty"`
	Stage   string `json:"stage,omitempty"`
	Status  string `json:"status"`
	Outcome string `json:"outcome,omitempty"`
}

var runAdminMigrationExportTransactionHelper = func(ctx context.Context, request migrationExportTransactionRequest, handle func(migrationExportTransactionEvent) error) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, adminMigrationExportTransactionHelper)
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
		var event migrationExportTransactionEvent
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return errors.New("server migration export helper returned invalid progress data")
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return errors.New("server migration export helper returned invalid progress data")
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

var startMigrationExportWorker = func(s *server, operationID string, plan migrationExportExecutionPlan) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), migrationExportWorkerTimeout)
		defer cancel()
		if err := executeMigrationExportTransaction(ctx, s.operations, operationID, plan); err != nil {
			log.Printf("Server migration export operation %s finished with error: %v", operationID, err)
		}
	}()
}

func executeMigrationExportTransaction(parent context.Context, store *operationStore, operationID string, plan migrationExportExecutionPlan) error {
	if store == nil {
		return errors.New("operation store is unavailable")
	}
	operation, err := store.get(operationID)
	if err != nil {
		return err
	}
	if operation.OperationType != operationTypeMigrationExport || operation.PlanFingerprint == "" {
		return errors.New("invalid server migration export operation identity")
	}
	if !validAdminMigrationExportTargetRequest(plan.Request) ||
		plan.ConfigIdentity == "" ||
		plan.DataIdentity == "" ||
		plan.TargetIdentity == "" ||
		(plan.ExpectedMinecraftState != "running" && plan.ExpectedMinecraftState != "stopped") {
		return markMigrationExportNeedsAttention(store, operationID, "invalid_execution_plan", "Server migration export execution data is incomplete; administrator attention is required.")
	}
	if plan.Request.Kind == "smb" && plan.SMBPassword == "" {
		return markMigrationExportNeedsAttention(store, operationID, "invalid_execution_plan", "Server migration export SMB execution credential is missing; administrator attention is required.")
	}
	if _, err := store.transition(operationID, operationValidating, "export_preflight", "Revalidating the reviewed server migration export before touching Minecraft."); err != nil {
		return err
	}

	request := migrationExportTransactionRequest{
		OperationID:            operationID,
		PlanFingerprint:        operation.PlanFingerprint,
		Request:                plan.Request,
		ConfigIdentity:         plan.ConfigIdentity,
		DataIdentity:           plan.DataIdentity,
		TargetIdentity:         plan.TargetIdentity,
		ExpectedMinecraftState: plan.ExpectedMinecraftState,
		PlayersConfirmed:       plan.PlayersConfirmed,
		SMBPassword:            plan.SMBPassword,
	}
	plan.SMBPassword = ""

	finalSeen := false
	err = runAdminMigrationExportTransactionHelper(parent, request, func(event migrationExportTransactionEvent) error {
		switch event.Event {
		case "progress":
			return applyMigrationExportProgress(store, operationID, event)
		case "result":
			if finalSeen {
				return errors.New("server migration export helper returned multiple final results")
			}
			finalSeen = true
			return applyMigrationExportResult(store, operationID, event)
		default:
			return errors.New("server migration export helper returned an unsupported event")
		}
	})
	request.SMBPassword = ""
	if err != nil {
		_ = markMigrationExportNeedsAttention(store, operationID, "export_backend_interrupted", "Server migration export backend stopped unexpectedly; runtime or transport state requires administrator attention.")
		return err
	}
	if !finalSeen {
		err := errors.New("server migration export helper ended without a final result")
		_ = markMigrationExportNeedsAttention(store, operationID, "export_backend_incomplete", "Server migration export backend ended without a final safety result; administrator attention is required.")
		return err
	}
	return nil
}

func applyMigrationExportProgress(store *operationStore, operationID string, event migrationExportTransactionEvent) error {
	if event.Stage == "" || event.Status == "" {
		return errors.New("server migration export progress event is incomplete")
	}

	var desired operationState
	switch event.State {
	case string(operationValidating):
		desired = operationValidating
	case string(operationRunning):
		desired = operationRunning
	case string(operationVerifying):
		desired = operationVerifying
	default:
		return errors.New("server migration export progress event has an invalid state")
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

func applyMigrationExportResult(store *operationStore, operationID string, event migrationExportTransactionEvent) error {
	if event.Status == "" {
		return errors.New("server migration export result event is incomplete")
	}
	current, err := store.get(operationID)
	if err != nil {
		return err
	}

	switch event.Outcome {
	case "succeeded":
		if current.State != operationVerifying {
			return errors.New("server migration export success arrived before verification")
		}
		_, err = store.transition(operationID, operationSucceeded, "completed", event.Status)
		return err
	case "rolled_back":
		if current.State != operationFailed && current.State != operationRollingBack {
			if _, err := store.transition(operationID, operationFailed, "export_failed", "Server migration export did not complete; safe recovery state is being recorded."); err != nil {
				return err
			}
		}
		current, err = store.get(operationID)
		if err != nil {
			return err
		}
		if current.State == operationFailed {
			if _, err := store.transition(operationID, operationRollingBack, "rollback", "Finalizing server migration export recovery state."); err != nil {
				return err
			}
		}
		_, err = store.transition(operationID, operationRolledBack, "export_rolled_back", event.Status)
		return err
	case "needs_attention":
		return markMigrationExportNeedsAttention(store, operationID, "export_needs_attention", event.Status)
	default:
		return errors.New("server migration export result event has an invalid outcome")
	}
}

func markMigrationExportNeedsAttention(store *operationStore, operationID, stage, status string) error {
	current, err := store.get(operationID)
	if err != nil {
		return err
	}
	if current.State == operationNeedsAttention {
		_, err = store.updateProgress(operationID, operationNeedsAttention, stage, status)
		return err
	}
	if current.State == operationSucceeded || current.State == operationRolledBack {
		return errors.New("completed server migration export cannot require attention")
	}
	_, err = store.transition(operationID, operationNeedsAttention, stage, status)
	return err
}
