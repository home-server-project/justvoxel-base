package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const adminMigrationExportApplyRequestLimit = 16 * 1024

type adminMigrationExportApplyRequest struct {
	PlanFingerprint  string                            `json:"plan_fingerprint"`
	Request          adminMigrationExportTargetRequest `json:"request"`
	ExportConfirmed  bool                              `json:"export_confirmed"`
	PlayersConfirmed bool                              `json:"players_confirmed"`
	SMBPassword      string                            `json:"smb_password,omitempty"`
}

type adminMigrationExportApplyResponse struct {
	OK        bool              `json:"ok"`
	Code      string            `json:"code,omitempty"`
	Error     string            `json:"error,omitempty"`
	Created   bool              `json:"created"`
	Operation *operationJournal `json:"operation,omitempty"`
}

type migrationExportExecutionPlan struct {
	Request                adminMigrationExportTargetRequest
	ConfigIdentity         string
	DataIdentity           string
	TargetIdentity         string
	ExpectedMinecraftState string
	PlayersConfirmed       bool
	SMBPassword            string
}

func registerAdminMigrationExportRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/migration/export", s.adminMigrationExportDiscover)
	mux.HandleFunc("POST /v1/admin/migration/export/plan", s.adminMigrationExportPlan)
	mux.HandleFunc("POST /v1/admin/migration/export/apply", s.adminMigrationExportApply)
}

func (s *server) adminMigrationExportApply(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	if s.operations == nil {
		writeAdminMigrationExportApplyFailure(w, http.StatusServiceUnavailable, "operation_status_unavailable", "Server migration operation tracking is unavailable")
		return
	}

	var request adminMigrationExportApplyRequest
	if !decodeAdminMigrationExportApplyRequest(w, r, &request) {
		return
	}
	if !operationFingerprintPattern.MatchString(request.PlanFingerprint) {
		writeAdminMigrationExportApplyFailure(w, http.StatusBadRequest, "invalid_plan_fingerprint", "invalid reviewed server migration export fingerprint")
		return
	}
	if !request.ExportConfirmed {
		writeAdminMigrationExportApplyFailure(w, http.StatusBadRequest, "export_confirmation_required", "explicit server migration export confirmation is required")
		return
	}

	current, err := s.operations.currentMigration()
	if err != nil {
		writeAdminMigrationExportApplyFailure(w, http.StatusInternalServerError, "operation_status_failed", "current server migration operation could not be read")
		return
	}
	if current != nil {
		if current.OperationType == operationTypeMigrationExport && current.PlanFingerprint == request.PlanFingerprint {
			writeJSON(w, http.StatusOK, adminMigrationExportApplyResponse{OK: true, Created: false, Operation: current})
			return
		}
		writeAdminMigrationExportApplyFailure(w, http.StatusConflict, "migration_busy", "another server migration operation is already active")
		return
	}

	plan, planningErr := authoritativeAdminMigrationExportPlan(r.Context(), request.Request)
	if planningErr != nil {
		writeAdminMigrationExportApplyFailure(w, planningErr.status, "preflight_unavailable", planningErr.message)
		return
	}
	if !plan.OK {
		code := plan.Code
		if code == "" {
			code = "invalid_plan"
		}
		writeAdminMigrationExportApplyFailure(w, http.StatusBadRequest, code, plan.Error)
		return
	}
	if plan.PlanFingerprint != request.PlanFingerprint {
		writeAdminMigrationExportApplyFailure(w, http.StatusConflict, "stale_plan", "the reviewed server migration export changed; review the export target again")
		return
	}
	if plan.Context == nil || plan.Normalized == nil || plan.Requirements == nil {
		writeAdminMigrationExportApplyFailure(w, http.StatusInternalServerError, "invalid_plan", "server migration export planning returned incomplete execution data")
		return
	}
	if plan.Requirements.PlayersConfirmationRequired && !request.PlayersConfirmed {
		writeAdminMigrationExportApplyFailure(w, http.StatusBadRequest, "players_confirmation_required", "players are online; explicit interruption confirmation is required")
		return
	}
	if plan.Requirements.SMBPasswordRequired && request.SMBPassword == "" {
		writeAdminMigrationExportApplyFailure(w, http.StatusBadRequest, "smb_password_required", "SMB password is required for this export target")
		return
	}
	if !plan.Requirements.SMBPasswordRequired && request.SMBPassword != "" {
		writeAdminMigrationExportApplyFailure(w, http.StatusBadRequest, "unexpected_smb_password", "SMB password is only accepted for an SMB export target")
		return
	}

	operation, created, err := s.operations.beginMigrationExport(request.PlanFingerprint)
	if err != nil {
		if errors.Is(err, errMigrationOperationBusy) || errors.Is(err, errMigrationLockBusy) {
			current, currentErr := s.operations.currentMigration()
			if currentErr == nil && current != nil &&
				current.OperationType == operationTypeMigrationExport &&
				current.PlanFingerprint == request.PlanFingerprint {
				writeJSON(w, http.StatusOK, adminMigrationExportApplyResponse{OK: true, Created: false, Operation: current})
				return
			}
			writeAdminMigrationExportApplyFailure(w, http.StatusConflict, "migration_busy", "another server migration operation is already active")
			return
		}
		writeAdminMigrationExportApplyFailure(w, http.StatusInternalServerError, "operation_create_failed", "server migration export operation could not be created")
		return
	}

	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "migration_export", plan.Normalized.TargetDisplay, true, "Server migration export operation accepted")
	}

	status := http.StatusAccepted
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, adminMigrationExportApplyResponse{OK: true, Created: created, Operation: &operation})

	if created {
		startMigrationExportWorker(s, operation.OperationID, migrationExportExecutionPlan{
			Request:                request.Request,
			ConfigIdentity:         plan.Context.ConfigIdentity,
			DataIdentity:           plan.Context.DataIdentity,
			TargetIdentity:         plan.Context.TargetIdentity,
			ExpectedMinecraftState: plan.Requirements.MinecraftState,
			PlayersConfirmed:       request.PlayersConfirmed,
			SMBPassword:            request.SMBPassword,
		})
	}
}

func decodeAdminMigrationExportApplyRequest(w http.ResponseWriter, r *http.Request, target *adminMigrationExportApplyRequest) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, adminMigrationExportApplyRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAdminMigrationExportApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeAdminMigrationExportApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return false
	}
	if !validAdminMigrationExportTargetRequest(target.Request) ||
		len(target.SMBPassword) > 4096 ||
		strings.ContainsAny(target.SMBPassword, "\r\n") {
		writeAdminMigrationExportApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid server migration export request")
		return false
	}
	return true
}

func writeAdminMigrationExportApplyFailure(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, adminMigrationExportApplyResponse{OK: false, Code: code, Error: message, Created: false})
}
