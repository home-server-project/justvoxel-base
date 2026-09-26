package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

const adminMinecraftResetHelper = "/usr/libexec/justvoxel/mjust/admin-minecraft-reset-json"

var adminMinecraftResetPlanTimeout = 20 * time.Second
var adminMinecraftResetWorkerTimeout = 5 * time.Minute

var runAdminMinecraftResetHelper = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, adminMinecraftResetHelper, args...).CombinedOutput()
}

type adminMinecraftResetPlanResponse struct {
	OK                   bool     `json:"ok"`
	SchemaVersion        string   `json:"schema_version,omitempty"`
	Mode                 string   `json:"mode,omitempty"`
	PlanFingerprint      string   `json:"plan_fingerprint,omitempty"`
	Code                 string   `json:"code,omitempty"`
	Error                string   `json:"error,omitempty"`
	DataPath             string   `json:"data_path,omitempty"`
	DataScope            string   `json:"data_scope,omitempty"`
	DataAction           string   `json:"data_action,omitempty"`
	BackupPath           string   `json:"backup_path,omitempty"`
	BackupAction         string   `json:"backup_action,omitempty"`
	StorageLayoutAction  string   `json:"storage_layout_action,omitempty"`
	AuthenticationAction string   `json:"authentication_action,omitempty"`
	WebUIUsersAction     string   `json:"webui_users_action,omitempty"`
	PlayersOnline        int      `json:"players_online,omitempty"`
	Players              []string `json:"players"`
	Warnings             []string `json:"warnings"`
}

type adminMinecraftResetApplyRequest struct {
	PlanFingerprint string `json:"plan_fingerprint"`
	ConfirmPlayers  bool   `json:"confirm_players"`
}

type adminMinecraftResetApplyResponse struct {
	OK        bool              `json:"ok"`
	Code      string            `json:"code,omitempty"`
	Error     string            `json:"error,omitempty"`
	Created   bool              `json:"created"`
	Players   []string          `json:"players,omitempty"`
	Operation *operationJournal `json:"operation,omitempty"`
}

type adminMinecraftResetHelperApplyResponse struct {
	OK                   bool   `json:"ok"`
	SchemaVersion        string `json:"schema_version,omitempty"`
	Mode                 string `json:"mode,omitempty"`
	Code                 string `json:"code,omitempty"`
	Error                string `json:"error,omitempty"`
	DataPath             string `json:"data_path,omitempty"`
	DataScope            string `json:"data_scope,omitempty"`
	DataAction           string `json:"data_action,omitempty"`
	BackupPath           string `json:"backup_path,omitempty"`
	BackupAction         string `json:"backup_action,omitempty"`
	AuthenticationAction string `json:"authentication_action,omitempty"`
	WebUIUsersAction     string `json:"webui_users_action,omitempty"`
	Message              string `json:"message,omitempty"`
}

type minecraftResetFingerprintPayload struct {
	SchemaVersion        string `json:"schema_version"`
	Mode                 string `json:"mode"`
	DataPath             string `json:"data_path"`
	DataScope            string `json:"data_scope"`
	DataAction           string `json:"data_action"`
	BackupPath           string `json:"backup_path"`
	BackupAction         string `json:"backup_action"`
	StorageLayoutAction  string `json:"storage_layout_action"`
	AuthenticationAction string `json:"authentication_action"`
	WebUIUsersAction     string `json:"webui_users_action"`
}

func registerAdminMinecraftResetRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("POST /v1/admin/reset/minecraft/plan", s.adminMinecraftResetPlan)
	mux.HandleFunc("POST /v1/admin/reset/minecraft/apply", s.adminMinecraftResetApply)
}

func (s *server) adminMinecraftResetPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	plan, status := authoritativeAdminMinecraftResetPlan(r.Context())
	if status != 0 {
		writeJSON(w, status, plan)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *server) adminMinecraftResetApply(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	if s.operations == nil {
		writeAdminMinecraftResetApplyFailure(w, http.StatusServiceUnavailable, "operation_status_unavailable", "Minecraft reset operation tracking is unavailable", nil)
		return
	}

	var request adminMinecraftResetApplyRequest
	if !decodeAdminMinecraftResetApplyRequest(w, r, &request) {
		return
	}
	if !operationFingerprintPattern.MatchString(request.PlanFingerprint) {
		writeAdminMinecraftResetApplyFailure(w, http.StatusBadRequest, "invalid_plan_fingerprint", "invalid reviewed Minecraft reset fingerprint", nil)
		return
	}

	current, err := s.operations.currentMinecraftReset()
	if err != nil {
		writeAdminMinecraftResetApplyFailure(w, http.StatusInternalServerError, "operation_status_failed", "current Minecraft reset operation could not be read", nil)
		return
	}
	retrying := false
	if current != nil {
		if current.State == operationNeedsAttention {
			retrying = true
		} else if current.PlanFingerprint == request.PlanFingerprint {
			writeJSON(w, http.StatusOK, adminMinecraftResetApplyResponse{OK: true, Created: false, Operation: current})
			return
		} else {
			writeAdminMinecraftResetApplyFailure(w, http.StatusConflict, "reset_busy", "another Minecraft reset operation is already active", nil)
			return
		}
	}

	plan, status := authoritativeAdminMinecraftResetPlan(r.Context())
	if status != 0 {
		writeJSON(w, status, plan)
		return
	}
	if plan.PlanFingerprint != request.PlanFingerprint {
		writeAdminMinecraftResetApplyFailure(w, http.StatusConflict, "stale_plan", "the reviewed Minecraft reset plan has changed; review it again before resetting", nil)
		return
	}
	if plan.PlayersOnline > 0 && !request.ConfirmPlayers {
		writeAdminMinecraftResetApplyFailure(w, http.StatusConflict, "players_online", "players are online; explicit interruption confirmation is required", plan.Players)
		return
	}

	var operation operationJournal
	created := false
	if retrying {
		operation, err = s.operations.retryMinecraftReset(current.OperationID, plan.PlanFingerprint)
	} else {
		operation, created, err = s.operations.beginMinecraftReset(plan.PlanFingerprint)
	}
	if err != nil {
		if errors.Is(err, errMinecraftResetOperationBusy) ||
			errors.Is(err, errSetupLockBusy) ||
			errors.Is(err, errRestoreLockBusy) ||
			errors.Is(err, errDataMigrationLockBusy) ||
			errors.Is(err, errMigrationLockBusy) {
			writeAdminMinecraftResetApplyFailure(w, http.StatusConflict, "operation_busy", "another persistent appliance operation is active", nil)
			return
		}
		writeAdminMinecraftResetApplyFailure(w, http.StatusInternalServerError, "operation_create_failed", "Minecraft reset operation could not be created", nil)
		return
	}

	statusCode := http.StatusAccepted
	if !created {
		statusCode = http.StatusOK
	}
	writeJSON(w, statusCode, adminMinecraftResetApplyResponse{OK: true, Created: created, Operation: &operation})
	if created || retrying {
		startMinecraftResetWorker(s, operation.OperationID, plan.PlanFingerprint, actor)
	}
}

func authoritativeAdminMinecraftResetPlan(parent context.Context) (adminMinecraftResetPlanResponse, int) {
	ctx, cancel := context.WithTimeout(parent, adminMinecraftResetPlanTimeout)
	defer cancel()

	output, runErr := runAdminMinecraftResetHelper(ctx, "plan")
	var plan adminMinecraftResetPlanResponse
	if err := decodeAdminMinecraftResetPlanResponse(output, &plan); err != nil {
		return adminMinecraftResetPlanResponse{
			OK: false, Code: "plan_unavailable", Error: "Minecraft reset planning returned invalid data",
			Players: []string{}, Warnings: []string{},
		}, http.StatusServiceUnavailable
	}
	if plan.Players == nil {
		plan.Players = []string{}
	}
	if plan.Warnings == nil {
		plan.Warnings = []string{}
	}
	if runErr != nil || !plan.OK {
		status := http.StatusBadRequest
		if plan.Code == "not_configured" {
			status = http.StatusConflict
		}
		if plan.Error == "" {
			plan.Error = "Minecraft reset could not be planned"
		}
		return plan, status
	}
	if err := validateAdminMinecraftResetPlan(plan); err != nil {
		return adminMinecraftResetPlanResponse{
			OK: false, Code: "invalid_plan", Error: "Minecraft reset planning returned unsupported data",
			Players: []string{}, Warnings: []string{},
		}, http.StatusInternalServerError
	}
	fingerprint, err := minecraftResetPlanFingerprint(plan)
	if err != nil {
		return adminMinecraftResetPlanResponse{
			OK: false, Code: "fingerprint_failed", Error: "Minecraft reset plan fingerprint could not be created",
			Players: []string{}, Warnings: []string{},
		}, http.StatusInternalServerError
	}
	plan.PlanFingerprint = fingerprint
	return plan, 0
}

func validateAdminMinecraftResetPlan(plan adminMinecraftResetPlanResponse) error {
	if plan.SchemaVersion != "v1" || plan.Mode != "minecraft" {
		return errors.New("unsupported Minecraft reset plan")
	}
	switch plan.DataScope {
	case "internal":
		if plan.DataAction != "delete" {
			return errors.New("internal Minecraft data must be deleted")
		}
	case "network", "external", "unknown":
		if plan.DataAction != "preserve" {
			return errors.New("external Minecraft data must be preserved")
		}
	default:
		return errors.New("unsupported Minecraft data scope")
	}
	if strings.TrimSpace(plan.DataPath) == "" || strings.TrimSpace(plan.BackupPath) == "" {
		return errors.New("reset paths are required")
	}
	if plan.BackupAction != "preserve" ||
		plan.StorageLayoutAction != "preserve" ||
		plan.AuthenticationAction != "preserve" ||
		plan.WebUIUsersAction != "preserve" {
		return errors.New("Minecraft reset preservation contract changed")
	}
	if plan.PlayersOnline < 0 {
		return errors.New("invalid player count")
	}
	return nil
}

func minecraftResetPlanFingerprint(plan adminMinecraftResetPlanResponse) (string, error) {
	payload, err := json.Marshal(minecraftResetFingerprintPayload{
		SchemaVersion: plan.SchemaVersion,
		Mode: plan.Mode,
		DataPath: plan.DataPath,
		DataScope: plan.DataScope,
		DataAction: plan.DataAction,
		BackupPath: plan.BackupPath,
		BackupAction: plan.BackupAction,
		StorageLayoutAction: plan.StorageLayoutAction,
		AuthenticationAction: plan.AuthenticationAction,
		WebUIUsersAction: plan.WebUIUsersAction,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func decodeAdminMinecraftResetPlanResponse(data []byte, target *adminMinecraftResetPlanResponse) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("unexpected trailing JSON value")
	}
	return nil
}

func decodeAdminMinecraftResetApplyRequest(w http.ResponseWriter, r *http.Request, target *adminMinecraftResetApplyRequest) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAdminMinecraftResetApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid JSON request", nil)
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeAdminMinecraftResetApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid JSON request", nil)
		return false
	}
	return true
}

func writeAdminMinecraftResetApplyFailure(w http.ResponseWriter, status int, code, message string, players []string) {
	writeJSON(w, status, adminMinecraftResetApplyResponse{
		OK: false, Code: code, Error: message, Created: false, Players: players,
	})
}

var startMinecraftResetWorker = func(s *server, operationID, expectedFingerprint string, actor session) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), adminMinecraftResetWorkerTimeout)
		defer cancel()
		if err := executeMinecraftReset(ctx, s, operationID, expectedFingerprint, actor); err != nil {
			log.Printf("Minecraft reset operation %s finished with error: %v", operationID, err)
		}
	}()
}

func executeMinecraftReset(ctx context.Context, s *server, operationID, expectedFingerprint string, actor session) error {
	if _, err := s.operations.transition(operationID, operationValidating, "validating", "Revalidating Minecraft reset plan."); err != nil {
		return err
	}

	plan, status := authoritativeAdminMinecraftResetPlan(ctx)
	if status != 0 || !plan.OK || plan.PlanFingerprint != expectedFingerprint {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "plan_changed", "Minecraft reset stopped because the reviewed plan changed.")
		return errors.New("Minecraft reset plan changed before execution")
	}

	if _, err := s.operations.transition(operationID, operationRunning, "resetting", "Stopping Minecraft and removing appliance-owned Minecraft state."); err != nil {
		return err
	}

	output, runErr := runAdminMinecraftResetHelper(ctx, "apply", "--confirm-players")
	var applied adminMinecraftResetHelperApplyResponse
	if decodeErr := decodeAdminMinecraftResetApplyResponse(output, &applied); decodeErr != nil || runErr != nil || !applied.OK {
		status := "Minecraft reset did not complete. Review appliance state before retrying."
		if strings.TrimSpace(applied.Error) != "" {
			status = "Minecraft reset stopped: " + strings.TrimSpace(applied.Error)
			if len(status) > 512 {
				status = status[:512]
			}
		}
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "reset_failed", status)
		if decodeErr != nil {
			return decodeErr
		}
		if applied.Error != "" {
			return errors.New(applied.Error)
		}
		return runErr
	}

	if applied.BackupAction != "preserve" || applied.AuthenticationAction != "preserve" || applied.WebUIUsersAction != "preserve" {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "verification_failed", "Minecraft reset returned an invalid preservation result.")
		return errors.New("Minecraft reset preservation contract changed during Apply")
	}

	if _, err := s.operations.transition(operationID, operationVerifying, "verifying", "Verifying JustVoxel returned to first-setup mode."); err != nil {
		return err
	}
	configured, preflightErr := setupAlreadyConfiguredForApply(ctx)
	if preflightErr != nil || configured {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "verification_failed", "Minecraft reset finished with an unexpected appliance configuration state.")
		if preflightErr != nil {
			return errors.New(preflightErr.message)
		}
		return errors.New("appliance is still configured after Minecraft reset")
	}

	if _, err := s.operations.transition(operationID, operationSucceeded, "complete", "Minecraft reset completed. JustVoxel is ready for first setup."); err != nil {
		return err
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "reset_minecraft", "minecraft", true, "Minecraft configuration and appliance-owned internal data reset; backups and authentication preserved")
	}
	return nil
}

func decodeAdminMinecraftResetApplyResponse(data []byte, target *adminMinecraftResetHelperApplyResponse) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("unexpected trailing JSON value")
	}
	return nil
}
