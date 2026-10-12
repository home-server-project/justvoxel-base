package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const adminMinecraftResetHelper = "/usr/libexec/justvoxel/mjust/admin-minecraft-reset-json"

var adminMinecraftResetPlanTimeout = 20 * time.Second
var adminMinecraftResetWorkerTimeout = 5 * time.Minute

var runAdminMinecraftResetHelper = func(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, adminMinecraftResetHelper, args...)
	finish, err := attachOperationTrace(ctx, cmd)
	if err != nil {
		return nil, err
	}
	output, err := cmd.CombinedOutput()
	finish(err)
	return output, err
}

type adminMinecraftResetPlanResponse struct {
	OK                   bool                      `json:"ok"`
	SchemaVersion        string                    `json:"schema_version,omitempty"`
	Mode                 string                    `json:"mode,omitempty"`
	PlanFingerprint      string                    `json:"plan_fingerprint,omitempty"`
	Code                 string                    `json:"code,omitempty"`
	Error                string                    `json:"error,omitempty"`
	DataPath             string                    `json:"data_path,omitempty"`
	DataScope            string                    `json:"data_scope,omitempty"`
	DataAction           string                    `json:"data_action,omitempty"`
	PluginsAction        string                    `json:"plugins_action,omitempty"`
	PluginEvidence       *minecraftResetPluginPlan `json:"-"`
	BackupPath           string                    `json:"backup_path,omitempty"`
	BackupAction         string                    `json:"backup_action,omitempty"`
	StorageLayoutAction  string                    `json:"storage_layout_action,omitempty"`
	AuthenticationAction string                    `json:"authentication_action,omitempty"`
	WebUIUsersAction     string                    `json:"webui_users_action,omitempty"`
	PlayersOnline        int                       `json:"players_online,omitempty"`
	Players              []string                  `json:"players"`
	Warnings             []string                  `json:"warnings"`
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
	SchemaVersion        string                    `json:"schema_version"`
	Mode                 string                    `json:"mode"`
	DataPath             string                    `json:"data_path"`
	DataScope            string                    `json:"data_scope"`
	DataAction           string                    `json:"data_action"`
	PluginsAction        string                    `json:"plugins_action"`
	Plugins              *minecraftResetPluginPlan `json:"plugins,omitempty"`
	BackupPath           string                    `json:"backup_path"`
	BackupAction         string                    `json:"backup_action"`
	StorageLayoutAction  string                    `json:"storage_layout_action"`
	AuthenticationAction string                    `json:"authentication_action"`
	WebUIUsersAction     string                    `json:"webui_users_action"`
}

func registerAdminMinecraftResetRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("POST /v1/admin/reset/minecraft/plan", s.adminMinecraftResetPlan)
	mux.HandleFunc("POST /v1/admin/reset/minecraft/apply", s.adminMinecraftResetApply)
}

func (s *server) adminMinecraftResetPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	plan, status := s.minecraftResetPlanWithRecovery(r.Context())
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

	plan, status := s.minecraftResetPlanWithRecovery(r.Context())
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
	if err := prepareMinecraftResetPlugins(&plan); err != nil {
		return adminMinecraftResetPlanResponse{OK: false, Code: "plugins_unsafe", Error: "Managed plugin cleanup cannot be safely planned: " + err.Error(), Players: []string{}, Warnings: []string{}}, http.StatusConflict
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
	if plan.PluginsAction != "" && (plan.DataAction == "delete" && plan.PluginsAction != "delete_with_internal_data" || plan.DataAction == "preserve" && plan.PluginsAction != "remove_managed_jars") {
		return errors.New("Unsupported managed plugin reset action")
	}
	if plan.PlayersOnline < 0 {
		return errors.New("invalid player count")
	}
	return nil
}

func minecraftResetPlanFingerprint(plan adminMinecraftResetPlanResponse) (string, error) {
	if plan.PluginsAction == "" {
		if plan.DataAction == "delete" {
			plan.PluginsAction = "delete_with_internal_data"
		} else {
			plan.PluginsAction = "remove_managed_jars"
		}
	}
	payload, err := json.Marshal(minecraftResetFingerprintPayload{
		SchemaVersion:        plan.SchemaVersion,
		Mode:                 plan.Mode,
		DataPath:             plan.DataPath,
		DataScope:            plan.DataScope,
		DataAction:           plan.DataAction,
		PluginsAction:        plan.PluginsAction,
		Plugins:              plan.PluginEvidence,
		BackupPath:           plan.BackupPath,
		BackupAction:         plan.BackupAction,
		StorageLayoutAction:  plan.StorageLayoutAction,
		AuthenticationAction: plan.AuthenticationAction,
		WebUIUsersAction:     plan.WebUIUsersAction,
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
	ctx = withOperationTrace(ctx, s.operations, operationID)
	traceStage(ctx, "Validation")
	if _, err := s.operations.transition(operationID, operationValidating, "validating", "Revalidating Minecraft reset plan."); err != nil {
		return err
	}

	plan, status := s.minecraftResetPlanWithRecovery(ctx)
	if status != 0 || !plan.OK || plan.PlanFingerprint != expectedFingerprint {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "plan_changed", "Minecraft reset stopped because the reviewed plan changed.")
		return errors.New("Minecraft reset plan changed before execution")
	}

	var pluginTx *minecraftResetPluginTransaction
	if plan.DataAction == "preserve" {
		lock, err := acquirePluginRuntimeLock()
		if err != nil {
			_, _ = s.operations.transition(operationID, operationNeedsAttention, "plugins_cleanup", "Minecraft maintenance is busy; reset can be retried safely.")
			return err
		}
		defer lock.Close()
		pluginTx, err = s.prepareResetPluginTransaction(ctx, operationID, plan)
		if err != nil {
			_, _ = s.operations.transition(operationID, operationNeedsAttention, "plugins_cleanup", "Managed plugin staging requires recovery; durable evidence was retained.")
			return err
		}
	}

	if _, err := s.operations.transition(operationID, operationRunning, "resetting", "Stopping Minecraft and removing appliance-owned Minecraft state."); err != nil {
		return err
	}

	var output []byte
	var runErr error
	if pluginTx != nil && s.resetPluginHelperComplete(operationID) {
		output, _ = json.Marshal(adminMinecraftResetHelperApplyResponse{OK: true, SchemaVersion: "v1", Mode: "minecraft", DataPath: plan.DataPath, DataScope: plan.DataScope, DataAction: plan.DataAction, BackupAction: "preserve", AuthenticationAction: "preserve", WebUIUsersAction: "preserve"})
	} else {
		output, runErr = runAdminMinecraftResetHelper(ctx, "apply", "--confirm-players")
	}
	var applied adminMinecraftResetHelperApplyResponse
	if decodeErr := decodeAdminMinecraftResetApplyResponse(output, &applied); decodeErr != nil || runErr != nil || !applied.OK {
		if pluginTx != nil {
			if err := restoreResetPluginJars(*pluginTx); err != nil {
				_, _ = s.operations.transition(operationID, operationNeedsAttention, "plugins_recovery", "Minecraft reset failed and exact managed plugin recovery remains unverified. Recovery evidence is retained.")
				return err
			}
		}
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

	if applied.BackupAction != "preserve" || applied.AuthenticationAction != "preserve" || applied.WebUIUsersAction != "preserve" || (pluginTx != nil && (applied.SchemaVersion != "v1" || applied.Mode != "minecraft" || applied.DataPath != plan.DataPath || applied.DataAction != plan.DataAction || applied.DataScope != plan.DataScope)) {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "verification_failed", "Minecraft reset returned an invalid preservation result.")
		return errors.New("Minecraft reset preservation contract changed during Apply")
	}

	if _, err := s.operations.transition(operationID, operationVerifying, "verifying", "Verifying JustVoxel returned to first-setup mode."); err != nil {
		return err
	}
	traceStage(ctx, "Verification")
	configured, preflightErr := setupAlreadyConfiguredForApply(ctx)
	if preflightErr != nil || configured {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "verification_failed", "Minecraft reset finished with an unexpected appliance configuration state.")
		if preflightErr != nil {
			return errors.New(preflightErr.message)
		}
		return errors.New("appliance is still configured after Minecraft reset")
	}

	if pluginTx != nil {
		if err := s.markResetPluginHelperComplete(*pluginTx); err != nil {
			_, _ = s.operations.transition(operationID, operationNeedsAttention, "plugins_cleanup", "Reset helper completed; durable plugin cleanup recovery is retained.")
			return err
		}
		if err := commitResetPluginCleanup(*pluginTx); err != nil {
			_, _ = s.operations.transition(operationID, operationNeedsAttention, "plugins_cleanup", "Reset helper completed; exact managed plugin ownership cleanup requires recovery.")
			return err
		}
	} else if plan.DataAction == "delete" {
		if err := verifyResetInternalPluginAbsence(plan.DataPath); err != nil {
			_, _ = s.operations.transition(operationID, operationNeedsAttention, "plugins_cleanup", "Internal Minecraft data deletion left unexpected plugin ownership state.")
			return err
		}
	}

	if err := minecraftInstances.retire(); err != nil {
		failure := fmt.Errorf("Minecraft Instance ID retirement failed: %w", err)
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "instance_retirement_failed", failure.Error())
		return failure
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

// The cleanup plan is private fingerprint evidence, never an ownership schema
// change. Staging remains on the data filesystem so rename is truly atomic.
type minecraftResetPluginPlan struct {
	DataPath string                   `json:"data_path"`
	Device   uint64                   `json:"device"`
	Inode    uint64                   `json:"inode"`
	Manifest []byte                   `json:"manifest"`
	Entries  []pluginLocationEvidence `json:"entries"`
}

// Allow the 1 MiB original manifest's base64 encoding, up to sixfold JSON
// escaping in repeated entry evidence, and transaction/plan framing.
const resetPluginTransactionLimit = 8 << 20

type minecraftResetPluginTransaction struct {
	SchemaVersion     string                          `json:"schema_version"`
	OperationID       string                          `json:"operation_id"`
	Plan              adminMinecraftResetPlanResponse `json:"plan"`
	Plugins           minecraftResetPluginPlan        `json:"plugins"`
	OriginallyRunning bool                            `json:"originally_running"`
}

func prepareMinecraftResetPlugins(plan *adminMinecraftResetPlanResponse) error {
	if plan.DataAction == "delete" {
		plan.PluginsAction = "delete_with_internal_data"
		return nil
	}
	plan.PluginsAction = "remove_managed_jars"
	data, err := openPluginDataDirectory(plan.DataPath)
	if err != nil {
		return errors.New("Minecraft data directory is unavailable or unsafe")
	}
	defer data.Close()
	var st unix.Stat_t
	if unix.Fstat(int(data.Fd()), &st) != nil {
		return errors.New("Data identity unavailable")
	}
	manifest, raw, err := readPluginManifest(plan.DataPath)
	if err != nil {
		return err
	}
	evidence := minecraftResetPluginPlan{plan.DataPath, uint64(st.Dev), st.Ino, raw, []pluginLocationEvidence{}}
	for _, entry := range manifest.Artifacts {
		state, err := resolveManagedPluginLocation(plan.DataPath, entry)
		if err != nil {
			return err
		}
		evidence.Entries = append(evidence.Entries, pluginLocationEvidence{entry, state})
	}
	disabled, err := openDisabledPluginDirectory(plan.DataPath, false)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	if disabled != nil {
		defer disabled.Close()
		names, err := disabled.Readdirnames(-1)
		if err != nil {
			return err
		}
		for _, name := range names {
			if !slices.ContainsFunc(evidence.Entries, func(e pluginLocationEvidence) bool { return e.State == "disabled" && e.Entry.Filename == name }) {
				return errors.New("Disabled plugin store contains an unowned or ambiguous file")
			}
		}
	}
	plan.PluginEvidence = &evidence
	return nil
}
func readPrivatePluginJSON(dir *os.File, name string, out any, limit int64) error {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Mode&0777 != 0600 || st.Size > limit {
		return errors.New("Unsafe private plugin evidence")
	}
	d := json.NewDecoder(io.LimitReader(f, limit+1))
	d.DisallowUnknownFields()
	if err = d.Decode(out); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("Trailing transaction evidence")
	}
	return nil
}

// Keep reset evidence publication pinned, synced, and non-replacing, with the
// same bound used by its recovery reader.
func publishResetPluginTransaction(directory *os.File, tx *minecraftResetPluginTransaction) error {
	const name = "transaction.json"
	raw, err := json.Marshal(tx)
	if err != nil {
		return err
	}
	if len(raw) > resetPluginTransactionLimit {
		return errors.New("Transaction evidence too large")
	}
	unique, err := newOperationID()
	if err != nil {
		return err
	}
	temporary := "." + name + "-" + unique + ".tmp"
	fd, err := unix.Openat(int(directory.Fd()), temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(int(directory.Fd()), temporary, 0)
	f := os.NewFile(uintptr(fd), temporary)
	n, err := f.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = pluginEvidenceSync(f)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := unix.Renameat2(int(directory.Fd()), temporary, int(directory.Fd()), name, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	return pluginEvidenceSync(directory)
}

func (s *server) resetPluginEvidenceDirectory(id string, create bool) (*os.File, error) {
	if !validOperationID(id) {
		return nil, errors.New("Invalid reset operation")
	}
	path := filepath.Join(s.operations.baseDir, "reset-plugins")
	if create {
		if err := ensurePrivateDirectory(path); err != nil {
			return nil, err
		}
	}
	parent, err := openPluginEvidenceDirectory(path)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	dir, err := openPrivatePluginChild(parent, id, create)
	if err != nil {
		return nil, err
	}
	if create {
		base, err := openPluginEvidenceDirectory(s.operations.baseDir)
		if err != nil {
			dir.Close()
			return nil, err
		}
		err = base.Sync()
		base.Close()
		if err != nil {
			dir.Close()
			return nil, err
		}
	}
	return dir, nil
}
func (s *server) readResetPluginTransaction(id string) (*minecraftResetPluginTransaction, error) {
	dir, err := s.resetPluginEvidenceDirectory(id, false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	var tx minecraftResetPluginTransaction
	if err = readPrivatePluginJSON(dir, "transaction.json", &tx, resetPluginTransactionLimit); err != nil {
		return nil, err
	}
	tx.Plan.PluginEvidence = &tx.Plugins
	fingerprint, err := minecraftResetPlanFingerprint(tx.Plan)
	if err != nil || tx.SchemaVersion != "v1" || tx.OperationID != id || tx.Plan.DataAction != "preserve" || tx.Plan.PluginsAction != "remove_managed_jars" || tx.Plan.DataPath != tx.Plugins.DataPath || fingerprint != tx.Plan.PlanFingerprint {
		return nil, errors.New("Invalid reset plugin evidence identity")
	}
	if err = validateAdminMinecraftResetPlan(tx.Plan); err != nil {
		return nil, err
	}
	var manifest pluginManifest
	if len(tx.Plugins.Manifest) == 0 {
		manifest = pluginManifest{SchemaVersion: "v1", Artifacts: []pluginManifestEntry{}}
	} else if json.Unmarshal(tx.Plugins.Manifest, &manifest) != nil {
		return nil, errors.New("Invalid original reset ownership")
	}
	if manifest.SchemaVersion != "v1" || len(manifest.Artifacts) != len(tx.Plugins.Entries) {
		return nil, errors.New("Incomplete reset plugin evidence")
	}
	for _, e := range tx.Plugins.Entries {
		if !slices.Contains([]string{"enabled", "disabled"}, e.State) || !slices.ContainsFunc(manifest.Artifacts, func(m pluginManifestEntry) bool { return reflect.DeepEqual(m, e.Entry) }) || !safePluginFilename(e.Entry.Filename) || !pluginHashPattern.MatchString(e.Entry.SHA512) || !pluginMainPattern.MatchString(e.Entry.Main) || !pluginNamePattern.MatchString(e.Entry.PluginName) || managedPluginDescriptor(e.Entry.Filename, e.Entry.PluginName, e.Entry.Main) != "" {
			return nil, errors.New("Unsafe reset plugin identity")
		}
	}
	return &tx, nil
}
func (s *server) minecraftResetPlanWithRecovery(ctx context.Context) (adminMinecraftResetPlanResponse, int) {
	if s.operations != nil {
		current, err := s.operations.currentMinecraftReset()
		if err == nil && current != nil {
			tx, err := s.readResetPluginTransaction(current.OperationID)
			if err == nil {
				if current.PlanFingerprint != tx.Plan.PlanFingerprint {
					return adminMinecraftResetPlanResponse{OK: false, Code: "plugins_recovery", Error: "Reset plugin recovery identity changed", Players: []string{}, Warnings: []string{}}, 409
				}
				// Player snapshots are not fingerprint fields, matching the
				// existing reset contract. Refresh them even while JARs are
				// staged, without re-planning or changing ownership evidence.
				output, runErr := runAdminMinecraftResetHelper(ctx, "plan")
				var fresh adminMinecraftResetPlanResponse
				if decodeAdminMinecraftResetPlanResponse(output, &fresh) == nil && fresh.OK && runErr == nil {
					if validateAdminMinecraftResetPlan(fresh) != nil || fresh.DataPath != tx.Plan.DataPath || fresh.DataAction != tx.Plan.DataAction || fresh.DataScope != tx.Plan.DataScope {
						return adminMinecraftResetPlanResponse{OK: false, Code: "plan_changed", Error: "Reset configuration changed; preserved plugin recovery remains reserved", Players: []string{}, Warnings: []string{}}, 409
					}
					tx.Plan.PlayersOnline = fresh.PlayersOnline
					tx.Plan.Players = fresh.Players
				} else if fresh.Code != "not_configured" {
					return adminMinecraftResetPlanResponse{OK: false, Code: "plugins_recovery", Error: "Reset recovery player/configuration status unavailable", Players: []string{}, Warnings: []string{}}, 503
				}
				return tx.Plan, 0
			}
			if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, unix.ENOENT) {
				return adminMinecraftResetPlanResponse{OK: false, Code: "plugins_recovery", Error: "Reset plugin recovery evidence cannot be safely read", Players: []string{}, Warnings: []string{}}, 409
			}
		}
	}
	return authoritativeAdminMinecraftResetPlan(ctx)
}
func (s *server) prepareResetPluginTransaction(ctx context.Context, id string, plan adminMinecraftResetPlanResponse) (*minecraftResetPluginTransaction, error) {
	tx, err := s.readResetPluginTransaction(id)
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, unix.ENOENT) {
		return nil, err
	}
	if tx == nil {
		if plan.PluginEvidence == nil {
			return nil, errors.New("Missing managed plugin cleanup plan")
		}
		fresh := plan
		if err = prepareMinecraftResetPlugins(&fresh); err != nil {
			return nil, err
		}
		fingerprint, _ := minecraftResetPlanFingerprint(fresh)
		if fingerprint != plan.PlanFingerprint {
			return nil, errors.New("Managed plugin cleanup plan changed")
		}
		running, err := pluginServiceActive(ctx)
		if err != nil {
			return nil, err
		}
		tx = &minecraftResetPluginTransaction{"v1", id, plan, *plan.PluginEvidence, running}
		dir, err := s.resetPluginEvidenceDirectory(id, true)
		if err != nil {
			return nil, err
		}
		err = publishResetPluginTransaction(dir, tx)
		dir.Close()
		if err != nil {
			return nil, err
		}
	}
	if s.resetPluginHelperComplete(id) {
		return tx, nil
	}
	configured, preflightErr := setupAlreadyConfiguredForApply(ctx)
	if preflightErr != nil {
		return nil, errors.New("Reset configuration state unavailable")
	}
	if !configured {
		if err := verifyInterruptedMinecraftResetRuntime(ctx); err != nil {
			return nil, err
		}
		// First prove every JAR is still either original or private staging. If an
		// interrupted helper completed, finish staging before ownership cleanup.
		if err := stageResetPluginJars(*tx); err != nil {
			return nil, err
		}
		if err := s.markResetPluginHelperComplete(*tx); err != nil {
			return nil, err
		}
		return tx, nil
	}
	if online, _, err := pluginInstallPlayers(ctx); err != nil || online > 0 && plan.PlayersOnline == 0 {
		return nil, errors.New("Player state changed; refresh the reset review before retrying")
	}

	if err = checkResetPluginIdentity(tx.Plugins); err != nil {
		return nil, err
	}
	if err = pluginService(ctx, "stop"); err != nil {
		return nil, err
	}
	if active, err := pluginServiceActive(ctx); err != nil || active {
		return nil, errors.New("Minecraft must be stopped before managed plugin staging")
	}
	if err = stageResetPluginJars(*tx); err != nil {
		restoreErr := restoreResetPluginJars(*tx)
		return nil, errors.Join(err, restoreErr)
	}
	return tx, nil
}
func checkResetPluginIdentity(e minecraftResetPluginPlan) error {
	data, err := openPluginDataDirectory(e.DataPath)
	if err != nil {
		return err
	}
	defer data.Close()
	var st unix.Stat_t
	if unix.Fstat(int(data.Fd()), &st) != nil || uint64(st.Dev) != e.Device || st.Ino != e.Inode {
		return errors.New("Reset Minecraft data identity changed")
	}
	return nil
}
func resetPluginRoots(tx minecraftResetPluginTransaction) (*os.File, *os.File, *os.File, error) {
	if err := checkResetPluginIdentity(tx.Plugins); err != nil {
		return nil, nil, nil, err
	}
	active, err := openPluginDirectory(tx.Plugins.DataPath)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return nil, nil, nil, err
	}
	disabled, err := openDisabledPluginDirectory(tx.Plugins.DataPath, true)
	if err != nil {
		if active != nil {
			active.Close()
		}
		return nil, nil, nil, err
	}
	staging, err := openPluginOperationStaging(tx.Plugins.DataPath, tx.OperationID, true)
	if err != nil {
		if active != nil {
			active.Close()
		}
		disabled.Close()
		return nil, nil, nil, err
	}
	return active, disabled, staging, nil
}
func closeResetPluginRoots(roots ...*os.File) {
	for _, root := range roots {
		if root != nil {
			root.Close()
		}
	}
}
func stageResetPluginJars(tx minecraftResetPluginTransaction) error {
	_, raw, err := readPluginManifest(tx.Plugins.DataPath)
	if err != nil || !bytes.Equal(raw, tx.Plugins.Manifest) {
		return errors.New("Reset plugin manifest changed")
	}
	active, disabled, staging, err := resetPluginRoots(tx)
	if err != nil {
		return err
	}
	defer closeResetPluginRoots(active, disabled, staging)
	for _, e := range tx.Plugins.Entries {
		source := active
		if e.State == "disabled" {
			source = disabled
		}
		count := 0
		var found *os.File
		for _, root := range []*os.File{active, disabled, staging} {
			exists, err := pluginFilePresent(root, e.Entry.Filename)
			if err != nil {
				return err
			}
			if exists {
				count++
				found = root
			}
		}
		if count != 1 || found != source && found != staging {
			return errors.New("Reset plugin state is ambiguous")
		}
		if err = verifyManagedPluginAt(found, e.Entry); err != nil {
			return err
		}
		if found == source {
			if err = checkPinnedResetPluginDirectories(tx, active, disabled, staging); err != nil {
				return err
			}
			if _, err = checkedPluginMove(source, staging, e.Entry); err != nil {
				return err
			}
		}
	}
	return nil
}
func restoreResetPluginJars(tx minecraftResetPluginTransaction) error {
	_, raw, err := readPluginManifest(tx.Plugins.DataPath)
	if err != nil || !bytes.Equal(raw, tx.Plugins.Manifest) {
		return errors.New("Reset rollback ownership differs from durable evidence")
	}
	active, disabled, staging, err := resetPluginRoots(tx)
	if err != nil {
		return err
	}
	defer closeResetPluginRoots(active, disabled, staging)
	for _, e := range tx.Plugins.Entries {
		target := active
		if e.State == "disabled" {
			target = disabled
		}
		if target == nil {
			return errors.New("Original reset plugin location is unavailable")
		}
		count := 0
		var found *os.File
		for _, root := range []*os.File{active, disabled, staging} {
			exists, err := pluginFilePresent(root, e.Entry.Filename)
			if err != nil {
				return err
			}
			if exists {
				count++
				found = root
			}
		}
		if count != 1 || found != target && found != staging {
			return errors.New("Reset rollback JAR is ambiguous or missing")
		}
		if err = verifyManagedPluginAt(found, e.Entry); err != nil {
			return err
		}
		if found == staging {
			if err = checkPinnedResetPluginDirectories(tx, active, disabled, staging); err != nil {
				return err
			}
			if _, err = checkedPluginMove(staging, target, e.Entry); err != nil {
				return err
			}
		}
		state, err := resolveManagedPluginLocation(tx.Plugins.DataPath, e.Entry)
		if err != nil || state != e.State {
			return errors.New("Reset rollback location cannot be proven")
		}
	}
	return nil
}
func (s *server) resetPluginHelperComplete(id string) bool {
	dir, err := s.resetPluginEvidenceDirectory(id, false)
	if err != nil {
		return false
	}
	defer dir.Close()
	var marker struct {
		OperationID string `json:"operation_id"`
	}
	return readPrivatePluginJSON(dir, "helper-complete.json", &marker, pluginManifestLimit) == nil && marker.OperationID == id
}
func (s *server) markResetPluginHelperComplete(tx minecraftResetPluginTransaction) error {
	if s.resetPluginHelperComplete(tx.OperationID) {
		return nil
	}
	dir, err := s.resetPluginEvidenceDirectory(tx.OperationID, false)
	if err != nil {
		return err
	}
	defer dir.Close()
	return publishPluginEvidence(dir, "helper-complete.json", struct {
		OperationID string `json:"operation_id"`
	}{tx.OperationID})
}
func commitResetPluginCleanup(tx minecraftResetPluginTransaction) error {
	if err := checkResetPluginIdentity(tx.Plugins); err != nil {
		return err
	}
	active, disabled, staging, err := resetPluginRoots(tx)
	if err != nil {
		return err
	}
	defer closeResetPluginRoots(active, disabled, staging)
	// A previous completed cleanup is idempotent. Never delete a file that has
	// reappeared in an active/disabled location or replace a new manifest.
	_, raw, err := readPluginManifest(tx.Plugins.DataPath)
	if err != nil {
		return err
	}
	if raw != nil && !bytes.Equal(raw, tx.Plugins.Manifest) {
		return errors.New("Reset ownership manifest changed")
	}
	for _, e := range tx.Plugins.Entries {
		for _, root := range []*os.File{active, disabled} {
			exists, err := pluginFilePresent(root, e.Entry.Filename)
			if err != nil || exists {
				return errors.New("Managed JAR reappeared outside reset staging")
			}
		}
		exists, err := pluginFilePresent(staging, e.Entry.Filename)
		if err != nil {
			return err
		}
		if exists {
			if err = verifyManagedPluginAt(staging, e.Entry); err != nil {
				return err
			}
		} else if raw != nil {
			return errors.New("Reset rollback copy disappeared before ownership commit")
		}
	}
	if err = checkPinnedResetPluginDirectories(tx, active, disabled, staging); err != nil {
		return err
	}
	data, err := openPluginDataDirectory(tx.Plugins.DataPath)
	if err != nil {
		return err
	}
	defer data.Close()
	owned, err := openPrivatePluginChild(data, ".justvoxel", false)
	if err != nil {
		return err
	}
	defer owned.Close()
	if raw != nil {
		if err = unix.Unlinkat(int(owned.Fd()), "plugins.json", 0); err != nil {
			return err
		}
		if err = owned.Sync(); err != nil {
			return err
		}
	}
	// Remove only the empty verified private disabled store.
	if err = unix.Unlinkat(int(owned.Fd()), "disabled-plugins", unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	if err = owned.Sync(); err != nil {
		return err
	}
	for _, e := range tx.Plugins.Entries {
		exists, err := pluginFilePresent(staging, e.Entry.Filename)
		if err != nil {
			return err
		}
		if exists {
			if err = verifyManagedPluginAt(staging, e.Entry); err != nil {
				return err
			}
			if err = unix.Unlinkat(int(staging.Fd()), e.Entry.Filename, 0); err != nil {
				return err
			}
		}
	}
	return staging.Sync()
}
func verifyResetInternalPluginAbsence(path string) error {
	data, err := openPluginDataDirectory(path)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer data.Close()
	owned, err := openPrivatePluginChild(data, ".justvoxel", false)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer owned.Close()
	for _, name := range []string{"plugins.json", "disabled-plugins"} {
		var st unix.Stat_t
		if err = unix.Fstatat(int(owned.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) {
			return errors.New("Plugin ownership state survived internal Minecraft data deletion")
		}
	}
	return nil
}

// Prove a completed destructive helper after an interruption between its return
// and the completion marker. Missing configuration alone is insufficient.
var verifyInterruptedMinecraftResetRuntime = func(ctx context.Context) error {
	for _, path := range []string{"/etc/justvoxel/justvoxel.conf", "/etc/justvoxel/minecraft.env", "/etc/justvoxel/minecraft-backup.env", "/etc/containers/systemd/minecraft.container", "/etc/systemd/system/minecraft-backup.service", "/etc/systemd/system/minecraft-backup.timer", "/var/lib/justvoxel/state/minecraft-previous-image-id"} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("Interrupted reset runtime cleanup cannot be proven")
		}
	}
	_, err := pluginInstallCommand(ctx, "podman", "container", "exists", "minecraft")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return errors.New("Interrupted reset container absence cannot be proven")
	}
	// The helper's final reload is bounded and repeatable after files are proven
	// absent, so a crash after configuration removal cannot strand recovery.
	if _, err = pluginInstallCommand(ctx, "systemctl", "daemon-reload"); err != nil {
		return errors.New("Interrupted reset runtime reload failed")
	}
	out, err := pluginInstallCommand(ctx, "systemctl", "show", "--property=ActiveState", "--value", "minecraft.service")
	if err != nil || strings.TrimSpace(string(out)) != "inactive" {
		return errors.New("Interrupted reset stopped runtime cannot be proven")
	}
	return nil
}

func checkPinnedResetPluginDirectories(tx minecraftResetPluginTransaction, active, disabled, staging *os.File) error {
	return checkPinnedManagedPluginDirectories(pluginManageTransaction{OperationID: tx.OperationID, DataPath: tx.Plugins.DataPath, DataDevice: tx.Plugins.Device, DataInode: tx.Plugins.Inode}, active, disabled, staging)
}
