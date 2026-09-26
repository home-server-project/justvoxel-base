package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const adminRestoreApplyRequestLimit = 8 * 1024

type adminRestoreApplyRequest struct {
	PlanFingerprint     string                  `json:"plan_fingerprint"`
	Request             adminRestorePlanRequest `json:"request"`
	DestructiveConfirmed bool                    `json:"destructive_confirmed"`
	PlayersConfirmed     bool                    `json:"players_confirmed"`
}

type adminRestoreApplyResponse struct {
	OK        bool              `json:"ok"`
	Code      string            `json:"code,omitempty"`
	Error     string            `json:"error,omitempty"`
	Created   bool              `json:"created"`
	Operation *operationJournal `json:"operation,omitempty"`
}

type restoreExecutionPlan struct {
	BackupID        string
	Mode            string
	ArchiveIdentity string
	PlayersConfirmed bool
}

func (s *server) adminRestoreApply(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeAdminRestoreApplyFailure(w, http.StatusServiceUnavailable, "operation_status_unavailable", "Restore operation tracking is unavailable")
		return
	}

	var request adminRestoreApplyRequest
	if !decodeAdminRestoreApplyRequest(w, r, &request) {
		return
	}
	if !operationFingerprintPattern.MatchString(request.PlanFingerprint) {
		writeAdminRestoreApplyFailure(w, http.StatusBadRequest, "invalid_plan_fingerprint", "invalid reviewed Restore fingerprint")
		return
	}
	if !request.DestructiveConfirmed {
		writeAdminRestoreApplyFailure(w, http.StatusBadRequest, "destructive_confirmation_required", "explicit destructive Restore confirmation is required")
		return
	}

	current, err := s.operations.currentRestore()
	if err != nil {
		writeAdminRestoreApplyFailure(w, http.StatusInternalServerError, "operation_status_failed", "current Restore operation could not be read")
		return
	}
	if current != nil {
		if current.PlanFingerprint == request.PlanFingerprint {
			writeJSON(w, http.StatusOK, adminRestoreApplyResponse{OK: true, Created: false, Operation: current})
			return
		}
		writeAdminRestoreApplyFailure(w, http.StatusConflict, "restore_busy", "another Restore operation is already active")
		return
	}

	plan, planningErr := authoritativeAdminRestorePlan(r.Context(), request.Request)
	if planningErr != nil {
		writeAdminRestoreApplyFailure(w, planningErr.status, "preflight_unavailable", planningErr.message)
		return
	}
	if !plan.OK {
		code := plan.Code
		if code == "" {
			code = "invalid_plan"
		}
		writeAdminRestoreApplyFailure(w, http.StatusBadRequest, code, plan.Error)
		return
	}
	if plan.PlanFingerprint != request.PlanFingerprint {
		writeAdminRestoreApplyFailure(w, http.StatusConflict, "stale_plan", "the reviewed Restore has changed; review the backup and Restore scope again")
		return
	}
	if plan.Context == nil || plan.Normalized == nil || plan.Requirements == nil {
		writeAdminRestoreApplyFailure(w, http.StatusInternalServerError, "invalid_plan", "Restore planning returned incomplete execution data")
		return
	}
	if plan.Requirements.PlayersConfirmationRequired && !request.PlayersConfirmed {
		writeAdminRestoreApplyFailure(w, http.StatusBadRequest, "players_confirmation_required", "players are online; explicit interruption confirmation is required")
		return
	}

	operation, created, err := s.operations.beginRestore(request.PlanFingerprint)
	if err != nil {
		if errors.Is(err, errFactoryResetOperationBusy) {
			writeAdminRestoreApplyFailure(w, http.StatusConflict, "factory_reset_needs_attention", "Full Factory Reset must be completed or retried before Restore can start")
			return
		}
		if errors.Is(err, errRestoreOperationBusy) || errors.Is(err, errRestoreLockBusy) {
			current, currentErr := s.operations.currentRestore()
			if currentErr == nil && current != nil && current.PlanFingerprint == request.PlanFingerprint {
				writeJSON(w, http.StatusOK, adminRestoreApplyResponse{OK: true, Created: false, Operation: current})
				return
			}
			writeAdminRestoreApplyFailure(w, http.StatusConflict, "restore_busy", "another Restore operation is already active")
			return
		}
		writeAdminRestoreApplyFailure(w, http.StatusInternalServerError, "operation_create_failed", "Restore operation could not be created")
		return
	}

	status := http.StatusAccepted
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, adminRestoreApplyResponse{OK: true, Created: created, Operation: &operation})
	if created {
		execution := restoreExecutionPlan{
			BackupID: request.Request.BackupID,
			Mode: request.Request.Mode,
			ArchiveIdentity: plan.Context.ArchiveIdentity,
			PlayersConfirmed: request.PlayersConfirmed,
		}
		startRestoreWorker(s, operation.OperationID, execution)
	}
}

func decodeAdminRestoreApplyRequest(w http.ResponseWriter, r *http.Request, target *adminRestoreApplyRequest) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, adminRestoreApplyRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAdminRestoreApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeAdminRestoreApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return false
	}
	if !adminRestoreBackupIDPattern.MatchString(target.Request.BackupID) || (target.Request.Mode != "world" && target.Request.Mode != "full") {
		writeAdminRestoreApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid Restore request")
		return false
	}
	return true
}

func writeAdminRestoreApplyFailure(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, adminRestoreApplyResponse{OK: false, Code: code, Error: message, Created: false})
}
