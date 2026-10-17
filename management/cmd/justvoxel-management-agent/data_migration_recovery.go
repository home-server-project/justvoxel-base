package main

import (
	"context"
	"net/http"
	"sync"
)

var dataMigrationRecoveryMu sync.Mutex

// Recovery is the same appliance-wide operation, never an instance route. A
// legacy journal is retained read-only when immutable ownership cannot be proven.
func (s *server) adminDataMigrationRecover(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	var request struct {
		OperationID string `json:"operation_id"`
		Confirm     bool   `json:"confirm_recovery"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if !request.Confirm || !validOperationID(request.OperationID) {
		writeError(w, 400, "Explicit migration recovery confirmation is required.")
		return
	}
	if s.operations == nil {
		writeError(w, 503, "Operation tracking is unavailable.")
		return
	}
	if !dataMigrationRecoveryMu.TryLock() {
		writeError(w, 409, "Migration recovery is already running.")
		return
	}
	current, err := s.operations.currentDataMigration()
	if err != nil || current == nil || current.OperationID != request.OperationID || current.State != operationNeedsAttention {
		dataMigrationRecoveryMu.Unlock()
		writeError(w, 409, "Migration recovery is unavailable in this state.")
		return
	}
	if _, err = instanceRuntimes.loadMigration(request.OperationID); err != nil {
		dataMigrationRecoveryMu.Unlock()
		writeError(w, 409, "This migration has no verified participant evidence. Preserved legacy recovery state remains read-only.")
		return
	}
	operation, err := s.operations.transition(request.OperationID, operationRollingBack, "migration_recovery", "Recovering original appliance storage and recorded instance states.")
	if err != nil {
		dataMigrationRecoveryMu.Unlock()
		writeError(w, 409, "Migration recovery could not begin.")
		return
	}
	writeJSON(w, http.StatusAccepted, adminDataMigrationApplyResponse{OK: true, Created: false, Operation: &operation})
	go func() {
		defer dataMigrationRecoveryMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), dataMigrationWorkerTimeout)
		defer cancel()
		final := false
		err := runAdminDataMigrationTransactionHelper(ctx, dataMigrationTransactionRequest{OperationID: request.OperationID, PlanFingerprint: operation.PlanFingerprint, Recovery: true}, func(e dataMigrationTransactionEvent) error {
			if e.Event == "progress" {
				return applyDataMigrationProgress(s.operations, request.OperationID, e)
			}
			if e.Event == "result" && !final {
				final = true
				return applyDataMigrationResult(s.operations, request.OperationID, e)
			}
			return errInstanceRecovery
		})
		if err != nil || !final {
			_ = markDataMigrationNeedsAttention(s.operations, request.OperationID, "migration_recovery", "Migration recovery was interrupted or could not be verified. Preserved evidence remains available for retry.")
		}
	}()
}
