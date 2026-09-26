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

	"github.com/home-server-project/justvoxel/management/internal/systemauth"
)

const adminFactoryResetHelper = "/usr/libexec/justvoxel/mjust/admin-factory-reset-json"

var adminFactoryResetPlanTimeout = 20 * time.Second
var adminFactoryResetWorkerTimeout = 5 * time.Minute

var runAdminFactoryResetHelper = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, adminFactoryResetHelper, args...).CombinedOutput()
}

var resetFactoryAuthenticationState = resetAuthenticationStateForFactoryReset

var expireSystemAdministratorPassword = func(ctx context.Context) error {
	return exec.CommandContext(ctx, "/usr/bin/chage", "-d", "0", systemAdminUsername).Run()
}

type adminFactoryResetPlanResponse struct {
	OK                   bool     `json:"ok"`
	SchemaVersion        string   `json:"schema_version,omitempty"`
	Mode                 string   `json:"mode,omitempty"`
	PlanFingerprint      string   `json:"plan_fingerprint,omitempty"`
	Code                 string   `json:"code,omitempty"`
	Error                string   `json:"error,omitempty"`
	MinecraftConfigured  bool     `json:"minecraft_configured"`
	DataPath             string   `json:"data_path,omitempty"`
	DataScope            string   `json:"data_scope,omitempty"`
	DataAction           string   `json:"data_action,omitempty"`
	BackupPath           string   `json:"backup_path,omitempty"`
	BackupScope          string   `json:"backup_scope,omitempty"`
	BackupAction         string   `json:"backup_action,omitempty"`
	ConfigBackupsAction  string   `json:"config_backups_action,omitempty"`
	StorageLayoutAction  string   `json:"storage_layout_action,omitempty"`
	ExternalStorageAction string  `json:"external_storage_action,omitempty"`
	NetworkStorageAction string   `json:"network_storage_action,omitempty"`
	AuthenticationAction string   `json:"authentication_action,omitempty"`
	WebUIUsersAction     string   `json:"webui_users_action,omitempty"`
	PasswordAction       string   `json:"password_action,omitempty"`
	SessionsAction       string   `json:"sessions_action,omitempty"`
	PlayersOnline        int      `json:"players_online,omitempty"`
	Players              []string `json:"players"`
	Warnings             []string `json:"warnings"`
}

type adminFactoryResetApplyRequest struct {
	PlanFingerprint string `json:"plan_fingerprint"`
	ConfirmPlayers  bool   `json:"confirm_players"`
	SystemPassword  string `json:"system_password"`
}

type adminFactoryResetApplyResponse struct {
	OK        bool              `json:"ok"`
	Code      string            `json:"code,omitempty"`
	Error     string            `json:"error,omitempty"`
	Created   bool              `json:"created"`
	Players   []string          `json:"players,omitempty"`
	Operation *operationJournal `json:"operation,omitempty"`
}

type adminFactoryResetHelperApplyResponse struct {
	OK                    bool   `json:"ok"`
	SchemaVersion         string `json:"schema_version,omitempty"`
	Mode                  string `json:"mode,omitempty"`
	Code                  string `json:"code,omitempty"`
	Error                 string `json:"error,omitempty"`
	MinecraftWasConfigured bool  `json:"minecraft_was_configured"`
	DataPath              string `json:"data_path,omitempty"`
	DataScope             string `json:"data_scope,omitempty"`
	DataAction            string `json:"data_action,omitempty"`
	BackupPath            string `json:"backup_path,omitempty"`
	BackupScope           string `json:"backup_scope,omitempty"`
	BackupAction          string `json:"backup_action,omitempty"`
	ConfigBackupsAction   string `json:"config_backups_action,omitempty"`
	StorageLayoutAction   string `json:"storage_layout_action,omitempty"`
	ExternalStorageAction string `json:"external_storage_action,omitempty"`
	NetworkStorageAction  string `json:"network_storage_action,omitempty"`
	Message               string `json:"message,omitempty"`
}

type factoryResetFingerprintPayload struct {
	SchemaVersion         string `json:"schema_version"`
	Mode                  string `json:"mode"`
	MinecraftConfigured   bool   `json:"minecraft_configured"`
	DataPath              string `json:"data_path"`
	DataScope             string `json:"data_scope"`
	DataAction            string `json:"data_action"`
	BackupPath            string `json:"backup_path"`
	BackupScope           string `json:"backup_scope"`
	BackupAction          string `json:"backup_action"`
	ConfigBackupsAction   string `json:"config_backups_action"`
	StorageLayoutAction   string `json:"storage_layout_action"`
	ExternalStorageAction string `json:"external_storage_action"`
	NetworkStorageAction  string `json:"network_storage_action"`
	AuthenticationAction string `json:"authentication_action"`
	WebUIUsersAction      string `json:"webui_users_action"`
	PasswordAction        string `json:"password_action"`
	SessionsAction        string `json:"sessions_action"`
}

func registerAdminFactoryResetRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("POST /v1/admin/reset/factory/plan", s.adminFactoryResetPlan)
	mux.HandleFunc("POST /v1/admin/reset/factory/apply", s.adminFactoryResetApply)
}

func (s *server) adminFactoryResetPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	plan, status := authoritativeAdminFactoryResetPlan(r.Context())
	if status != 0 {
		writeJSON(w, status, plan)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *server) adminFactoryResetApply(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeAdminFactoryResetApplyFailure(w, http.StatusServiceUnavailable, "operation_status_unavailable", "factory reset operation tracking is unavailable", nil)
		return
	}

	var request adminFactoryResetApplyRequest
	if !decodeAdminFactoryResetApplyRequest(w, r, &request) {
		return
	}
	if !operationFingerprintPattern.MatchString(request.PlanFingerprint) {
		writeAdminFactoryResetApplyFailure(w, http.StatusBadRequest, "invalid_plan_fingerprint", "invalid reviewed factory reset fingerprint", nil)
		return
	}

	current, err := s.operations.currentFactoryReset()
	if err != nil {
		writeAdminFactoryResetApplyFailure(w, http.StatusInternalServerError, "operation_status_failed", "current factory reset operation could not be read", nil)
		return
	}
	retrying := false
	if current != nil {
		if current.State == operationNeedsAttention {
			retrying = true
		} else if current.PlanFingerprint == request.PlanFingerprint {
			writeJSON(w, http.StatusOK, adminFactoryResetApplyResponse{OK: true, Created: false, Operation: current})
			return
		} else {
			writeAdminFactoryResetApplyFailure(w, http.StatusConflict, "reset_busy", "another full factory reset operation is already active", nil)
			return
		}
	}

	authMode, err := readAuthMode()
	if err != nil {
		writeAdminFactoryResetApplyFailure(w, http.StatusServiceUnavailable, "auth_mode_unavailable", "authentication mode is unavailable", nil)
		return
	}
	if authMode == authModeSeparate {
		if strings.TrimSpace(request.SystemPassword) == "" {
			writeAdminFactoryResetApplyFailure(w, http.StatusBadRequest, "system_password_required", "current voxel system password is required while WebUI uses a separate password", nil)
			return
		}
		if _, err := systemAuthenticate(systemAdminUsername, request.SystemPassword); err != nil {
			request.SystemPassword = ""
			if errors.Is(err, systemauth.ErrInvalidCredentials) {
				writeAdminFactoryResetApplyFailure(w, http.StatusUnauthorized, "system_password_incorrect", "current voxel system password is incorrect", nil)
				return
			}
			writeAdminFactoryResetApplyFailure(w, http.StatusForbidden, "system_account_unavailable", "voxel system account authentication failed", nil)
			return
		}
	}
	request.SystemPassword = ""

	plan, status := authoritativeAdminFactoryResetPlan(r.Context())
	if status != 0 {
		writeJSON(w, status, plan)
		return
	}
	if plan.PlanFingerprint != request.PlanFingerprint {
		writeAdminFactoryResetApplyFailure(w, http.StatusConflict, "stale_plan", "the reviewed factory reset plan has changed; review it again before resetting", nil)
		return
	}
	if plan.PlayersOnline > 0 && !request.ConfirmPlayers {
		writeAdminFactoryResetApplyFailure(w, http.StatusConflict, "players_online", "players are online; explicit interruption confirmation is required", plan.Players)
		return
	}

	var operation operationJournal
	created := false
	if retrying {
		operation, err = s.operations.retryFactoryReset(current.OperationID, plan.PlanFingerprint)
	} else {
		operation, created, err = s.operations.beginFactoryReset(plan.PlanFingerprint)
	}
	if err != nil {
		if errors.Is(err, errFactoryResetOperationBusy) ||
			errors.Is(err, errMinecraftResetOperationBusy) ||
			errors.Is(err, errSetupLockBusy) ||
			errors.Is(err, errRestoreLockBusy) ||
			errors.Is(err, errDataMigrationLockBusy) ||
			errors.Is(err, errMigrationLockBusy) {
			writeAdminFactoryResetApplyFailure(w, http.StatusConflict, "operation_busy", "another persistent appliance operation is active", nil)
			return
		}
		writeAdminFactoryResetApplyFailure(w, http.StatusInternalServerError, "operation_create_failed", "full factory reset operation could not be created", nil)
		return
	}

	statusCode := http.StatusAccepted
	if !created {
		statusCode = http.StatusOK
	}
	writeJSON(w, statusCode, adminFactoryResetApplyResponse{OK: true, Created: created, Operation: &operation})
	if created || retrying {
		startFactoryResetWorker(s, operation.OperationID, plan.PlanFingerprint)
	}
}

func authoritativeAdminFactoryResetPlan(parent context.Context) (adminFactoryResetPlanResponse, int) {
	ctx, cancel := context.WithTimeout(parent, adminFactoryResetPlanTimeout)
	defer cancel()

	output, runErr := runAdminFactoryResetHelper(ctx, "plan")
	var plan adminFactoryResetPlanResponse
	if err := decodeAdminFactoryResetPlanResponse(output, &plan); err != nil {
		return adminFactoryResetPlanResponse{
			OK: false, Code: "plan_unavailable", Error: "factory reset planning returned invalid data",
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
		if plan.Error == "" {
			plan.Error = "full factory reset could not be planned"
		}
		return plan, http.StatusBadRequest
	}
	if err := validateAdminFactoryResetPlan(plan); err != nil {
		return adminFactoryResetPlanResponse{
			OK: false, Code: "invalid_plan", Error: "factory reset planning returned unsupported data",
			Players: []string{}, Warnings: []string{},
		}, http.StatusInternalServerError
	}
	fingerprint, err := factoryResetPlanFingerprint(plan)
	if err != nil {
		return adminFactoryResetPlanResponse{
			OK: false, Code: "fingerprint_failed", Error: "factory reset plan fingerprint could not be created",
			Players: []string{}, Warnings: []string{},
		}, http.StatusInternalServerError
	}
	plan.PlanFingerprint = fingerprint
	return plan, 0
}

func validateAdminFactoryResetPlan(plan adminFactoryResetPlanResponse) error {
	if plan.SchemaVersion != "v1" || plan.Mode != "factory" {
		return errors.New("unsupported factory reset plan")
	}
	if plan.MinecraftConfigured {
		if strings.TrimSpace(plan.DataPath) == "" || strings.TrimSpace(plan.BackupPath) == "" {
			return errors.New("configured reset paths are required")
		}
		if !validFactoryResetStorageAction(plan.DataScope, plan.DataAction) {
			return errors.New("unsupported Minecraft data reset scope")
		}
		if !validFactoryResetStorageAction(plan.BackupScope, plan.BackupAction) {
			return errors.New("unsupported backup reset scope")
		}
	} else {
		if plan.DataScope != "none" || plan.DataAction != "none" ||
			plan.BackupScope != "none" || plan.BackupAction != "none" {
			return errors.New("unconfigured appliance returned storage reset actions")
		}
	}
	if plan.ConfigBackupsAction != "delete" ||
		plan.StorageLayoutAction != "preserve" ||
		plan.ExternalStorageAction != "preserve" ||
		plan.NetworkStorageAction != "preserve" ||
		plan.AuthenticationAction != "reset_to_system" ||
		plan.WebUIUsersAction != "delete" ||
		plan.PasswordAction != "expire" ||
		plan.SessionsAction != "invalidate" {
		return errors.New("factory reset preservation contract changed")
	}
	if plan.PlayersOnline < 0 {
		return errors.New("invalid player count")
	}
	return nil
}

func validFactoryResetStorageAction(scope, action string) bool {
	switch scope {
	case "internal":
		return action == "delete"
	case "network", "external", "unknown":
		return action == "preserve"
	default:
		return false
	}
}

func factoryResetPlanFingerprint(plan adminFactoryResetPlanResponse) (string, error) {
	payload, err := json.Marshal(factoryResetFingerprintPayload{
		SchemaVersion: plan.SchemaVersion,
		Mode: plan.Mode,
		MinecraftConfigured: plan.MinecraftConfigured,
		DataPath: plan.DataPath,
		DataScope: plan.DataScope,
		DataAction: plan.DataAction,
		BackupPath: plan.BackupPath,
		BackupScope: plan.BackupScope,
		BackupAction: plan.BackupAction,
		ConfigBackupsAction: plan.ConfigBackupsAction,
		StorageLayoutAction: plan.StorageLayoutAction,
		ExternalStorageAction: plan.ExternalStorageAction,
		NetworkStorageAction: plan.NetworkStorageAction,
		AuthenticationAction: plan.AuthenticationAction,
		WebUIUsersAction: plan.WebUIUsersAction,
		PasswordAction: plan.PasswordAction,
		SessionsAction: plan.SessionsAction,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func decodeAdminFactoryResetPlanResponse(data []byte, target *adminFactoryResetPlanResponse) error {
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

func decodeAdminFactoryResetApplyRequest(w http.ResponseWriter, r *http.Request, target *adminFactoryResetApplyRequest) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAdminFactoryResetApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid JSON request", nil)
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeAdminFactoryResetApplyFailure(w, http.StatusBadRequest, "invalid_request", "invalid JSON request", nil)
		return false
	}
	return true
}

func writeAdminFactoryResetApplyFailure(w http.ResponseWriter, status int, code, message string, players []string) {
	writeJSON(w, status, adminFactoryResetApplyResponse{
		OK: false, Code: code, Error: message, Created: false, Players: players,
	})
}

var startFactoryResetWorker = func(s *server, operationID, expectedFingerprint string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), adminFactoryResetWorkerTimeout)
		defer cancel()
		if err := executeFactoryReset(ctx, s, operationID, expectedFingerprint); err != nil {
			log.Printf("full factory reset operation %s finished with error: %v", operationID, err)
		}
	}()
}

func executeFactoryReset(ctx context.Context, s *server, operationID, expectedFingerprint string) error {
	if _, err := s.operations.transition(operationID, operationValidating, "validating", "Revalidating full factory reset plan."); err != nil {
		return err
	}

	plan, status := authoritativeAdminFactoryResetPlan(ctx)
	if status != 0 || !plan.OK || plan.PlanFingerprint != expectedFingerprint {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "plan_changed", "Full factory reset stopped because the reviewed plan changed.")
		return errors.New("factory reset plan changed before execution")
	}

	if _, err := s.operations.transition(operationID, operationRunning, "resetting_runtime", "Removing appliance-owned Minecraft and local backup state."); err != nil {
		return err
	}

	output, runErr := runAdminFactoryResetHelper(ctx, "apply", "--confirm-players")
	var applied adminFactoryResetHelperApplyResponse
	if decodeErr := decodeAdminFactoryResetApplyResponse(output, &applied); decodeErr != nil || runErr != nil || !applied.OK {
		status := "Full factory reset did not complete local runtime cleanup. Review appliance state before retrying."
		if strings.TrimSpace(applied.Error) != "" {
			status = "Factory reset stopped: " + strings.TrimSpace(applied.Error)
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
	if err := validateFactoryResetApplyResult(plan, applied); err != nil {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "verification_failed", "Full factory reset returned an invalid storage preservation result.")
		return err
	}

	if _, err := s.operations.updateProgress(operationID, operationRunning, "resetting_identity", "Resetting WebUI users and authentication state."); err != nil {
		return err
	}
	if s.store == nil {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "identity_failed", "WebUI identity storage is unavailable after local runtime cleanup.")
		return errors.New("WebUI identity store is unavailable")
	}
	if err := s.store.resetFactoryState(); err != nil {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "identity_failed", "WebUI identity state could not be reset completely.")
		return err
	}
	if err := resetFactoryAuthenticationState(); err != nil {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "identity_failed", "Authentication state could not be returned to System mode.")
		return err
	}
	if err := expireSystemAdministratorPassword(ctx); err != nil {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "password_expire_failed", "System administrator password could not be marked for mandatory change.")
		return err
	}

	if _, err := s.operations.transition(operationID, operationVerifying, "verifying", "Verifying fresh JustVoxel first-use state."); err != nil {
		return err
	}
	configured, preflightErr := setupAlreadyConfiguredForApply(ctx)
	if preflightErr != nil || configured {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "verification_failed", "Factory reset finished with an unexpected appliance configuration state.")
		if preflightErr != nil {
			return errors.New(preflightErr.message)
		}
		return errors.New("appliance is still configured after full factory reset")
	}
	mode, err := readAuthMode()
	if err != nil || mode != authModeSystem {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "verification_failed", "Factory reset did not restore System authentication mode.")
		if err != nil {
			return err
		}
		return errors.New("factory reset authentication mode is not system")
	}
	users, err := s.store.listWebUsers()
	if err != nil || len(users) != 0 {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "verification_failed", "Factory reset did not remove WebUI users.")
		if err != nil {
			return err
		}
		return errors.New("WebUI users remain after full factory reset")
	}

	if _, err := s.operations.transition(operationID, operationSucceeded, "complete", "Full factory reset completed. Sign in with the voxel system account to set a new password."); err != nil {
		return err
	}
	s.invalidateSessions()
	s.clearFailures()
	return nil
}

func validateFactoryResetApplyResult(plan adminFactoryResetPlanResponse, applied adminFactoryResetHelperApplyResponse) error {
	if applied.SchemaVersion != "v1" || applied.Mode != "factory" {
		return errors.New("factory reset Apply returned unsupported data")
	}
	if applied.StorageLayoutAction != "preserve" ||
		applied.ExternalStorageAction != "preserve" ||
		applied.NetworkStorageAction != "preserve" ||
		applied.ConfigBackupsAction != "delete" {
		return errors.New("factory reset storage preservation contract changed during Apply")
	}
	if applied.DataScope != plan.DataScope || applied.DataAction != plan.DataAction ||
		applied.BackupScope != plan.BackupScope || applied.BackupAction != plan.BackupAction {
		return errors.New("factory reset storage actions changed during Apply")
	}
	return nil
}

func decodeAdminFactoryResetApplyResponse(data []byte, target *adminFactoryResetHelperApplyResponse) error {
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
