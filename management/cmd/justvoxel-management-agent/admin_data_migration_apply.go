package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const adminDataMigrationApplyRequestLimit = 12 * 1024

type adminDataMigrationApplyRequest struct {
	PlanFingerprint    string                    `json:"plan_fingerprint"`
	Request            adminDataMigrationRequest `json:"request"`
	MigrationConfirmed bool                      `json:"migration_confirmed"`
	Confirmation       string                    `json:"confirmation,omitempty"`
	PlayersConfirmed   bool                      `json:"players_confirmed"`
}

type adminDataMigrationApplyResponse struct {
	OK        bool              `json:"ok"`
	Code      string            `json:"code,omitempty"`
	Error     string            `json:"error,omitempty"`
	Created   bool              `json:"created"`
	Operation *operationJournal `json:"operation,omitempty"`
}

type dataMigrationExecutionPlan struct {
	Request                adminDataMigrationRequest
	TargetIdentity         string
	ConfigIdentity         string
	ExpectedMinecraftState string
	PlayersConfirmed       bool
}

func registerAdminDataMigrationRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/data-migration", s.adminDataMigrationDiscover)
	mux.HandleFunc("POST /v1/admin/data-migration/plan", s.adminDataMigrationPlan)
	mux.HandleFunc("POST /v1/admin/data-migration/apply", s.adminDataMigrationApply)
}

func (s *server) adminDataMigrationApply(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	if s.operations == nil {
		writeAdminDataMigrationApplyFailure(w, http.StatusServiceUnavailable, "operation_status_unavailable", "Minecraft data migration operation tracking is unavailable")
		return
	}

	var request adminDataMigrationApplyRequest
	if !decodeAdminDataMigrationApplyRequest(w, r, &request) {
		return
	}
	if !operationFingerprintPattern.MatchString(request.PlanFingerprint) {
		writeAdminDataMigrationApplyFailure(w, http.StatusBadRequest, "invalid_plan_fingerprint", "invalid reviewed Minecraft data migration fingerprint")
		return
	}
	if !request.MigrationConfirmed {
		writeAdminDataMigrationApplyFailure(w, http.StatusBadRequest, "migration_confirmation_required", "explicit Minecraft data migration confirmation is required")
		return
	}

	current, err := s.operations.currentDataMigration()
	if err != nil {
		writeAdminDataMigrationApplyFailure(w, http.StatusInternalServerError, "operation_status_failed", "current Minecraft data migration operation could not be read")
		return
	}
	if current != nil {
		if current.PlanFingerprint == request.PlanFingerprint {
			writeJSON(w, http.StatusOK, adminDataMigrationApplyResponse{OK: true, Created: false, Operation: current})
			return
		}
		writeAdminDataMigrationApplyFailure(w, http.StatusConflict, "migration_busy", "another Minecraft data migration operation is already active")
		return
	}

	plan, planningErr := authoritativeAdminDataMigrationPlan(r.Context(), request.Request)
	if planningErr != nil {
		writeAdminDataMigrationApplyFailure(w, planningErr.status, "preflight_unavailable", planningErr.message)
		return
	}
	if !plan.OK {
		code := plan.Code
		if code == "" {
			code = "invalid_plan"
		}
		writeAdminDataMigrationApplyFailure(w, http.StatusBadRequest, code, plan.Error)
		return
	}
	if plan.PlanFingerprint != request.PlanFingerprint {
		writeAdminDataMigrationApplyFailure(w, http.StatusConflict, "stale_plan", "the reviewed Minecraft data migration changed; review the target again")
		return
	}
	if plan.Context == nil || plan.Normalized == nil || plan.Requirements == nil {
		writeAdminDataMigrationApplyFailure(w, http.StatusInternalServerError, "invalid_plan", "Minecraft data migration planning returned incomplete execution data")
		return
	}
	if plan.Requirements.DestructiveConfirmationRequired && request.Confirmation != plan.Requirements.ConfirmationPhrase {
		writeAdminDataMigrationApplyFailure(w, http.StatusBadRequest, "destructive_confirmation_required", "destructive storage confirmation does not match the reviewed target")
		return
	}
	if plan.Requirements.PlayersConfirmationRequired && !request.PlayersConfirmed {
		writeAdminDataMigrationApplyFailure(w, http.StatusBadRequest, "players_confirmation_required", "players are online; explicit interruption confirmation is required")
		return
	}

	operation, created, err := s.operations.beginDataMigration(request.PlanFingerprint)
	if err != nil {
		if errors.Is(err, errDataMigrationOperationBusy) || errors.Is(err, errDataMigrationLockBusy) {
			current, currentErr := s.operations.currentDataMigration()
			if currentErr == nil && current != nil && current.PlanFingerprint == request.PlanFingerprint {
				writeJSON(w, http.StatusOK, adminDataMigrationApplyResponse{OK: true, Created: false, Operation: current})
				return
			}
			writeAdminDataMigrationApplyFailure(w, http.StatusConflict, "migration_busy", "another Minecraft data migration operation is already active")
			return
		}
		writeAdminDataMigrationApplyFailure(w, http.StatusInternalServerError, "operation_create_failed", "Minecraft data migration operation could not be created")
		return
	}

	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "migrate_minecraft_data", request.Request.Device, true, "Minecraft data migration operation accepted")
	}

	status := http.StatusAccepted
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, adminDataMigrationApplyResponse{OK: true, Created: created, Operation: &operation})

	if created {
		startDataMigrationWorker(s, operation.OperationID, dataMigrationExecutionPlan{
			Request:                request.Request,
			TargetIdentity:         plan.Context.TargetIdentity,
			ConfigIdentity:         plan.Context.ConfigIdentity,
			ExpectedMinecraftState: plan.Requirements.MinecraftState,
			PlayersConfirmed:       request.PlayersConfirmed,
		})
	}
}

func decodeAdminDataMigrationApplyRequest(w http.ResponseWriter, r *http.Request, target *adminDataMigrationApplyRequest) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, adminDataMigrationApplyRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAdminDataMigrationApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeAdminDataMigrationApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return false
	}
	if target.Request.SizeGiB == "" {
		target.Request.SizeGiB = "all"
	}
	if !validAdminDataMigrationRequest(target.Request) {
		writeAdminDataMigrationApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid Minecraft data migration request")
		return false
	}
	return true
}

func writeAdminDataMigrationApplyFailure(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, adminDataMigrationApplyResponse{OK: false, Code: code, Error: message, Created: false})
}
