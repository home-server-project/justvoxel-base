package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const adminSetupApplyRequestLimit = 24 * 1024

type adminSetupApplyRequest struct {
	PlanFingerprint string                `json:"plan_fingerprint"`
	Request         adminSetupPlanRequest `json:"request"`
	SMBPassword     string                `json:"smb_password,omitempty"`
}

type adminSetupApplyResponse struct {
	OK        bool              `json:"ok"`
	Code      string            `json:"code,omitempty"`
	Error     string            `json:"error,omitempty"`
	Created   bool              `json:"created"`
	Operation *operationJournal `json:"operation,omitempty"`
}

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

	plan, preflightErr := authoritativeAdminSetupPlan(r.Context(), request.Request)
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
	if plan.Requirements != nil && plan.Requirements.SMBPasswordRequired {
		if request.SMBPassword == "" || strings.ContainsAny(request.SMBPassword, "\r\n") || len(request.SMBPassword) > 4096 {
			writeAdminSetupApplyFailure(w, http.StatusBadRequest, "smb_password_required", "SMB password is required when setup is executed")
			return
		}
	} else if request.SMBPassword != "" {
		writeAdminSetupApplyFailure(w, http.StatusBadRequest, "unexpected_smb_password", "SMB password is not required for the reviewed setup")
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

func setupAlreadyConfiguredForApply(parent context.Context) (bool, *adminSetupPlanningError) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	output, err := runAdminDiscoveryHelper(ctx, "configuration")
	if err != nil {
		return false, &adminSetupPlanningError{status: http.StatusServiceUnavailable, message: "appliance configuration preflight is unavailable"}
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	var configuration adminConfigurationDiscovery
	if err := decoder.Decode(&configuration); err != nil {
		return false, &adminSetupPlanningError{status: http.StatusInternalServerError, message: "appliance configuration preflight returned invalid data"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return false, &adminSetupPlanningError{status: http.StatusInternalServerError, message: "appliance configuration preflight returned invalid data"}
	}
	return configuration.Configured, nil
}

func writeAdminSetupApplyFailure(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, adminSetupApplyResponse{OK: false, Code: code, Error: message, Created: false})
}
