package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type adminSetupRecoverRequest struct {
	OperationID   string `json:"operation_id"`
	RetryRecovery bool   `json:"retry_recovery"`
}

func (s *server) adminSetupRecover(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "setup operation tracking is unavailable")
		return
	}
	var request adminSetupRecoverRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid setup recovery request")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF || !validOperationID(request.OperationID) || !request.RetryRecovery {
		writeError(w, http.StatusBadRequest, "invalid setup recovery request")
		return
	}
	current, err := s.operations.currentSetup()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "current setup operation could not be read")
		return
	}
	if current == nil || current.OperationID != request.OperationID {
		writeError(w, http.StatusNotFound, "current setup operation was not found")
		return
	}
	if current.OperationType != operationTypeSetup || current.State != operationNeedsAttention || current.Stage != "storage_rollback" {
		writeError(w, http.StatusConflict, "setup recovery is unavailable in the current state")
		return
	}
	if _, err := s.operations.transition(current.OperationID, operationRollingBack, "storage_recovery", "Retrying storage recovery from the preserved transaction."); err != nil {
		writeError(w, http.StatusConflict, "setup recovery could not be started")
		return
	}
	operation := recoverSetupStorage(context.Background(), s.operations, *current)
	writeJSON(w, http.StatusOK, adminOperationResponse{Operation: &operation})
}

func recoverSetupStorage(ctx context.Context, store *operationStore, current operationJournal) operationJournal {
	payload := struct {
		SchemaVersion   string `json:"schema_version"`
		OperationID     string `json:"operation_id"`
		PlanFingerprint string `json:"plan_fingerprint"`
	}{operationSchemaVersion, current.OperationID, current.PlanFingerprint}
	request, err := json.Marshal(payload)
	var response setupStorageTransactionResponse
	if err == nil {
		response, err = runSetupStorageRecoveryAction(ctx, request)
	}
	store.appendSetupStorageHelperEvidenceBestEffort(current.OperationID, "recover", response.Evidence)
	if err != nil {
		store.appendSetupHelperFailureBestEffort(current.OperationID, "storage", "recover", err)
	}
	if err == nil && response.OK && response.Phase == "storage_rolled_back" && response.RollbackState == "succeeded" && response.RollbackResult == "rolled_back" {
		store.appendSetupStorageEvidenceBestEffort(current.OperationID, "rolled_back", nil, "recovery_verified")
		if finished, transitionErr := store.transition(current.OperationID, operationRolledBack, "setup_rolled_back", "Storage recovery completed; setup was rolled back."); transitionErr == nil {
			return finished
		} else {
			err = transitionErr
		}
	}
	if err == nil {
		message := response.Error
		if message == "" {
			message = "storage recovery could not be confirmed"
		}
		err = errors.New(message)
	}
	store.appendSetupStorageFailureEvidenceBestEffort(current.OperationID, "recover", nil, err)
	if failed, transitionErr := store.transition(current.OperationID, operationNeedsAttention, "storage_rollback", "Storage recovery could not be confirmed; review the setup log before retrying."); transitionErr == nil {
		return failed
	}
	latest, _ := store.get(current.OperationID)
	return latest
}
