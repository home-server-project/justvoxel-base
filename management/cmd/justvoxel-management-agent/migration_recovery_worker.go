package main

import (
    "bufio"
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "os/exec"
    "time"
)

const adminMigrationRecoveryTransactionHelper = "/usr/libexec/justvoxel/mjust/admin-migration-recovery-transaction-json"
var adminMigrationRecoveryTransactionTimeout = 15 * time.Minute
var startMigrationRecoveryWorker = defaultStartMigrationRecoveryWorker

type migrationRecoveryTransactionEvent struct { Event string `json:"event"`; State string `json:"state,omitempty"`; Stage string `json:"stage,omitempty"`; Status string `json:"status"`; Outcome string `json:"outcome,omitempty"` }

func defaultStartMigrationRecoveryWorker(s *server, operationID string, plan migrationRecoveryExecutionPlan) {
    go func() { _ = runMigrationRecoveryWorker(context.Background(), s.operations, operationID, plan) }()
}

func runMigrationRecoveryWorker(parent context.Context, store *operationStore, operationID string, plan migrationRecoveryExecutionPlan) error {
    if store == nil { return errors.New("operation store unavailable") }
    if _, err := store.transition(operationID, operationValidating, "recovery_preflight", "Revalidating retained migration recovery state."); err != nil { return err }
    request, _ := json.Marshal(map[string]string{"transaction":plan.Transaction, "state_identity":plan.StateIdentity})
    ctx, cancel := context.WithTimeout(parent, adminMigrationRecoveryTransactionTimeout); defer cancel()
    cmd := exec.CommandContext(ctx, adminMigrationRecoveryTransactionHelper); cmd.Stdin = bytes.NewReader(request)
    output, err := cmd.Output()
    if err != nil { _ = markMigrationRecoveryAttention(store, operationID, "recovery_backend_failed", "Migration recovery finalization stopped; retained recovery state requires review."); return err }
    scanner := bufio.NewScanner(bytes.NewReader(output)); finalSeen := false
    for scanner.Scan() {
        var event migrationRecoveryTransactionEvent
        if err := json.Unmarshal(scanner.Bytes(), &event); err != nil { _ = markMigrationRecoveryAttention(store, operationID, "recovery_backend_invalid", "Migration recovery backend returned invalid progress data."); return err }
        if event.Event == "progress" {
            var next operationState; switch event.State { case string(operationValidating): next=operationValidating; case string(operationRunning): next=operationRunning; case string(operationVerifying): next=operationVerifying; default: return errors.New("invalid recovery progress state") }
            current, _ := store.get(operationID); if current.State == next { _, err = store.updateProgress(operationID, next, event.Stage, event.Status) } else { _, err = store.transition(operationID, next, event.Stage, event.Status) }; if err != nil { return err }
        } else if event.Event == "result" {
            if finalSeen || event.Outcome != "succeeded" { return errors.New("invalid recovery result") }; finalSeen=true
            if _, err := store.transition(operationID, operationSucceeded, "completed", event.Status); err != nil { return err }
        } else { return errors.New("unsupported recovery event") }
    }
    if err := scanner.Err(); err != nil { return err }
    if !finalSeen { _ = markMigrationRecoveryAttention(store, operationID, "recovery_backend_incomplete", "Migration recovery backend ended without a final result."); return errors.New("recovery backend incomplete") }
    return nil
}

func markMigrationRecoveryAttention(store *operationStore, operationID, stage, status string) error {
    current, err := store.get(operationID); if err != nil { return err }
    if current.State == operationNeedsAttention { _, err = store.updateProgress(operationID, operationNeedsAttention, stage, status); return err }
    if current.State == operationSucceeded || current.State == operationRolledBack { return errors.New("completed recovery cannot require attention") }
    _, err = store.transition(operationID, operationNeedsAttention, stage, status); return err
}
