package main

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"
)

type adminMigrationRecoveryApplyRequest struct {
    PlanFingerprint string `json:"plan_fingerprint"`
    Transaction string `json:"transaction"`
    FinalizeConfirmed bool `json:"finalize_confirmed"`
}
type adminMigrationRecoveryApplyResponse struct {
    OK bool `json:"ok"`
    Code string `json:"code,omitempty"`
    Error string `json:"error,omitempty"`
    Created bool `json:"created"`
    Operation *operationJournal `json:"operation,omitempty"`
}
type migrationRecoveryExecutionPlan struct { Transaction string; StateIdentity string }

func (s *server) adminMigrationRecoveryApply(w http.ResponseWriter, r *http.Request) {
    actor, ok := s.requireAdministrator(w, r); if !ok { return }
    if s.operations == nil { writeMigrationRecoveryApplyFailure(w, http.StatusServiceUnavailable, "operation_status_unavailable", "server migration operation tracking is unavailable"); return }
    var request adminMigrationRecoveryApplyRequest
    if !decodeJSON(w, r, &request) { return }
    if !operationFingerprintPattern.MatchString(request.PlanFingerprint) || request.Transaction == "" { writeMigrationRecoveryApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid migration recovery apply request"); return }
    if !request.FinalizeConfirmed { writeMigrationRecoveryApplyFailure(w, http.StatusBadRequest, "finalize_confirmation_required", "explicit migration recovery finalization confirmation is required"); return }
    current, err := s.operations.currentMigration()
    if err != nil { writeMigrationRecoveryApplyFailure(w, http.StatusInternalServerError, "operation_status_failed", "current server migration operation could not be read"); return }
    if current != nil {
        if current.OperationType == operationTypeMigrationRecovery && current.PlanFingerprint == request.PlanFingerprint { writeJSON(w, http.StatusOK, adminMigrationRecoveryApplyResponse{OK:true, Created:false, Operation:current}); return }
        writeMigrationRecoveryApplyFailure(w, http.StatusConflict, "migration_busy", "another server migration operation is already active"); return
    }
    helper, fingerprint, planErr := authoritativeMigrationRecoveryPlan(r.Context(), request.Transaction)
    if planErr != nil { writeMigrationRecoveryApplyFailure(w, http.StatusServiceUnavailable, "preflight_unavailable", planErr.Error()); return }
    if !helper.OK { writeMigrationRecoveryApplyFailure(w, http.StatusBadRequest, valueOr(helper.Code, "invalid_plan"), boundedMigrationRecoveryError(helper.Error)); return }
    if fingerprint != request.PlanFingerprint { writeMigrationRecoveryApplyFailure(w, http.StatusConflict, "stale_plan", "the reviewed migration recovery state changed; review recovery again"); return }
    if helper.Context == nil || helper.Context.StateIdentity == "" { writeMigrationRecoveryApplyFailure(w, http.StatusInternalServerError, "invalid_plan", "migration recovery planning returned incomplete execution data"); return }
    operation, created, err := s.operations.beginMigrationRecovery(request.PlanFingerprint)
    if err != nil {
        if errors.Is(err, errMigrationOperationBusy) || errors.Is(err, errMigrationLockBusy) { writeMigrationRecoveryApplyFailure(w, http.StatusConflict, "migration_busy", "another server migration operation is already active"); return }
        writeMigrationRecoveryApplyFailure(w, http.StatusInternalServerError, "operation_create_failed", "migration recovery operation could not be created"); return
    }
    if s.store != nil { _ = s.store.recordAuditEvent(actor, "migration_recovery", request.Transaction, true, "Migration recovery finalization accepted") }
    status := http.StatusAccepted; if !created { status = http.StatusOK }
    writeJSON(w, status, adminMigrationRecoveryApplyResponse{OK:true, Created:created, Operation:&operation})
    if created { startMigrationRecoveryWorker(s, operation.OperationID, migrationRecoveryExecutionPlan{Transaction:request.Transaction, StateIdentity:helper.Context.StateIdentity}) }
}

func authoritativeMigrationRecoveryPlan(parent context.Context, transaction string) (adminMigrationRecoveryHelperResponse, string, error) {
    var helper adminMigrationRecoveryHelperResponse
    payload, _ := json.Marshal(adminMigrationRecoveryPlanRequest{Transaction:transaction})
    ctx, cancel := context.WithTimeout(parent, adminMigrationRecoveryTimeout); defer cancel()
    output, err := runAdminMigrationRecoveryHelper(ctx, "plan", payload)
    if err != nil { return helper, "", err }
    if err := decodeAdminMigrationRecoveryJSON(output, &helper); err != nil || helper.SchemaVersion != "v1" { return helper, "", errors.New("migration recovery planner returned invalid data") }
    if !helper.OK { return helper, "", nil }
    fingerprint, err := adminMigrationRecoveryFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
    return helper, fingerprint, err
}

func writeMigrationRecoveryApplyFailure(w http.ResponseWriter, status int, code, message string) {
    writeJSON(w, status, adminMigrationRecoveryApplyResponse{OK:false, Code:code, Error:message, Created:false})
}
