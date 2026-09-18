package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

const adminSetupApplyRequestLimit = 24 * 1024

type adminSetupApplyRequest struct {
	PlanFingerprint string                `json:"plan_fingerprint"`
	Request         adminSetupPlanRequest `json:"request"`
}

type adminSetupApplyResponse struct {
	OK        bool              `json:"ok"`
	Code      string            `json:"code,omitempty"`
	Error     string            `json:"error,omitempty"`
	Created   bool              `json:"created"`
	Operation *operationJournal `json:"operation,omitempty"`
}

type setupApplyPreflightError struct {
	status  int
	message string
}

func (e *setupApplyPreflightError) Error() string { return e.message }

func registerAdminSetupApplyRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("POST /v1/admin/setup/apply", s.adminSetupApply)
}

func (s *server) adminSetupApply(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeAdminSetupApplyFailure(w, http.StatusServiceUnavailable, "operation_status_unavailable", "setup operation tracking is unavailable")
		return
	}

	var request adminSetupApplyRequest
	if !decodeAdminSetupApplyRequest(w, r, &request) {
		return
	}
	if !operationFingerprintPattern.MatchString(request.PlanFingerprint) {
		writeAdminSetupApplyFailure(w, http.StatusBadRequest, "invalid_plan_fingerprint", "invalid reviewed setup fingerprint")
		return
	}

	current, err := s.operations.currentSetup()
	if err != nil {
		writeAdminSetupApplyFailure(w, http.StatusInternalServerError, "operation_status_failed", "current setup operation could not be read")
		return
	}
	if current != nil {
		if current.PlanFingerprint == request.PlanFingerprint {
			writeJSON(w, http.StatusOK, adminSetupApplyResponse{OK: true, Created: false, Operation: current})
			return
		}
		writeAdminSetupApplyFailure(w, http.StatusConflict, "setup_busy", "another setup operation is already active")
		return
	}

	plan, preflightErr := authoritativeSetupPlanForApply(r.Context(), request.Request)
	if preflightErr != nil {
		writeAdminSetupApplyFailure(w, preflightErr.status, "preflight_unavailable", preflightErr.message)
		return
	}
	if !plan.OK {
		code := plan.Code
		if code == "" {
			code = "invalid_plan"
		}
		writeAdminSetupApplyFailure(w, http.StatusBadRequest, code, plan.Error)
		return
	}
	if plan.PlanFingerprint != request.PlanFingerprint {
		writeAdminSetupApplyFailure(w, http.StatusConflict, "stale_plan", "the reviewed setup has changed; return to Review before configuring")
		return
	}

	configured, preflightErr := setupAlreadyConfiguredForApply(r.Context())
	if preflightErr != nil {
		writeAdminSetupApplyFailure(w, preflightErr.status, "preflight_unavailable", preflightErr.message)
		return
	}
	if configured {
		writeAdminSetupApplyFailure(w, http.StatusConflict, "already_configured", "first-run setup is no longer available on this appliance")
		return
	}

	operation, created, err := s.operations.beginSetup(request.PlanFingerprint)
	if err != nil {
		if errors.Is(err, errSetupOperationBusy) || errors.Is(err, errSetupLockBusy) {
			current, currentErr := s.operations.currentSetup()
			if currentErr == nil && current != nil && current.PlanFingerprint == request.PlanFingerprint {
				writeJSON(w, http.StatusOK, adminSetupApplyResponse{OK: true, Created: false, Operation: current})
				return
			}
			writeAdminSetupApplyFailure(w, http.StatusConflict, "setup_busy", "another setup operation is already active")
			return
		}
		writeAdminSetupApplyFailure(w, http.StatusInternalServerError, "operation_create_failed", "setup operation could not be created")
		return
	}
	status := http.StatusAccepted
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, adminSetupApplyResponse{OK: true, Created: created, Operation: &operation})
}

func decodeAdminSetupApplyRequest(w http.ResponseWriter, r *http.Request, target *adminSetupApplyRequest) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, adminSetupApplyRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAdminSetupApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeAdminSetupApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return false
	}
	return true
}

func authoritativeSetupPlanForApply(parent context.Context, request adminSetupPlanRequest) (adminSetupPlanResponse, *setupApplyPreflightError) {
	var out adminSetupPlanResponse
	payload, err := json.Marshal(request)
	if err != nil {
		return out, &setupApplyPreflightError{status: http.StatusInternalServerError, message: "first-run setup request could not be prepared"}
	}
	ctx, cancel := context.WithTimeout(parent, adminSetupPlanTimeout)
	defer cancel()
	output, err := runAdminSetupPlanHelper(ctx, payload)
	if err != nil {
		return out, &setupApplyPreflightError{status: http.StatusServiceUnavailable, message: "first-run setup planning is unavailable"}
	}
	if err := decodeAdminSetupPlanResponse(output, &out); err != nil {
		return out, &setupApplyPreflightError{status: http.StatusInternalServerError, message: "first-run setup planner returned invalid data"}
	}
	if out.SchemaVersion != "v1" {
		return out, &setupApplyPreflightError{status: http.StatusInternalServerError, message: "first-run setup planner returned an unsupported schema"}
	}
	if out.Warnings == nil {
		out.Warnings = []adminSetupPlanWarning{}
	}
	if !out.OK {
		out.Error = boundedSetupPlanError(out.Error)
		return out, nil
	}
	if out.Normalized == nil || out.Requirements == nil || out.Normalized.Minecraft.VersionPolicy == "" || out.Normalized.Storage.Type == "" || out.Normalized.Backups.Type == "" {
		return out, &setupApplyPreflightError{status: http.StatusInternalServerError, message: "first-run setup planner returned incomplete data"}
	}
	fingerprint, err := adminSetupPlanFingerprint(out.SchemaVersion, out.Normalized, out.Requirements)
	if err != nil {
		return out, &setupApplyPreflightError{status: http.StatusInternalServerError, message: "first-run setup plan identity could not be created"}
	}
	out.PlanFingerprint = fingerprint
	return out, nil
}

func setupAlreadyConfiguredForApply(parent context.Context) (bool, *setupApplyPreflightError) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	output, err := runAdminDiscoveryHelper(ctx, "configuration")
	if err != nil {
		return false, &setupApplyPreflightError{status: http.StatusServiceUnavailable, message: "appliance configuration preflight is unavailable"}
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	var configuration adminConfigurationDiscovery
	if err := decoder.Decode(&configuration); err != nil {
		return false, &setupApplyPreflightError{status: http.StatusInternalServerError, message: "appliance configuration preflight returned invalid data"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return false, &setupApplyPreflightError{status: http.StatusInternalServerError, message: "appliance configuration preflight returned invalid data"}
	}
	return configuration.Configured, nil
}

func writeAdminSetupApplyFailure(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, adminSetupApplyResponse{OK: false, Code: code, Error: message, Created: false})
}
