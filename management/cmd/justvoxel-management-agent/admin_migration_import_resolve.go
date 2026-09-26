package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type adminMigrationImportResolveRequest struct {
	OperationID      string `json:"operation_id"`
	KeepCurrentState bool   `json:"keep_current_state"`
}

type adminMigrationImportResolveCheck struct {
	OK            bool   `json:"ok"`
	SchemaVersion string `json:"schema_version"`
	SafeToResolve bool   `json:"safe_to_resolve"`
	Code          string `json:"code,omitempty"`
	Error         string `json:"error,omitempty"`
}

func (s *server) adminMigrationImportResolve(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "server migration operation tracking is unavailable")
		return
	}

	var request adminMigrationImportResolveRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid server migration Import resolution request")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid server migration Import resolution request")
		return
	}
	if !validOperationID(request.OperationID) {
		writeError(w, http.StatusBadRequest, "invalid server migration Import operation")
		return
	}
	if !request.KeepCurrentState {
		writeError(w, http.StatusBadRequest, "explicit keep-current-state confirmation is required")
		return
	}

	current, err := s.operations.currentMigration()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "current server migration operation could not be read")
		return
	}
	if current == nil || current.OperationID != request.OperationID {
		writeError(w, http.StatusNotFound, "server migration Import operation was not found")
		return
	}
	if current.OperationType != operationTypeMigrationImport || current.State != operationNeedsAttention {
		writeError(w, http.StatusConflict, "server migration Import cannot be resolved in its current state")
		return
	}

	output, runErr := runAdminMigrationRecoveryHelper(r.Context(), "resolve-check", nil)
	if runErr != nil {
		writeError(w, http.StatusServiceUnavailable, "current server state could not be validated for Import resolution")
		return
	}
	var check adminMigrationImportResolveCheck
	if err := decodeAdminMigrationRecoveryJSON(output, &check); err != nil || check.SchemaVersion != "v1" {
		writeError(w, http.StatusInternalServerError, "Import resolution safety check returned invalid data")
		return
	}
	if !check.OK || !check.SafeToResolve {
		message := boundedMigrationRecoveryError(check.Error)
		if message == "" {
			message = "The failed Server Import still has recovery or runtime state that requires review."
		}
		writeError(w, http.StatusConflict, message)
		return
	}

	operation, err := s.operations.resolveMigrationImport(
		request.OperationID,
		"Failed Server Import was dismissed after the current runtime passed validation and no retained recovery state remained.",
	)
	if err != nil {
		if errors.Is(err, errOperationNotFound) {
			writeError(w, http.StatusNotFound, "server migration Import operation was not found")
			return
		}
		writeError(w, http.StatusConflict, "server migration Import cannot be resolved in its current state")
		return
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "migration_import_resolve", operation.OperationID, true, "Failed Server Import resolved by keeping the validated current server")
	}
	writeJSON(w, http.StatusOK, adminOperationResponse{Operation: &operation})
}
