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
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

var adminMinecraftResetPlanTimeout = 20 * time.Second
var adminMinecraftResetWorkerTimeout = 5 * time.Minute

type adminMinecraftResetPlanResponse struct {
	InstanceID           string                    `json:"instance_id"`
	InstanceName         string                    `json:"-"`
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

type minecraftResetFingerprintPayload struct {
	InstanceID           string                    `json:"instance_id"`
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
	mux.HandleFunc("POST /v1/minecraft/instances/{id}/reset/plan", s.selectedResetHandler(s.adminMinecraftResetPlan))
	mux.HandleFunc("POST /v1/minecraft/instances/{id}/reset/apply", s.selectedResetHandler(s.adminMinecraftResetApply))
	mux.HandleFunc("POST /v1/minecraft/instances/{id}/reset/recover", s.selectedResetHandler(s.adminMinecraftResetApply))
	mux.HandleFunc("GET /v1/minecraft/instances/{id}/reset/current-operation", s.selectedResetHandler(s.selectedResetCurrent))
	mux.HandleFunc("GET /v1/minecraft/instances/{id}/reset/operations/{operation}", s.selectedResetHandler(s.selectedResetStatus))
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
		if current.InstanceID != pluginInstanceID(r.Context()) {
			writeError(w, 409, "Reset operation instance mismatch")
			return
		}
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
		operation, created, err = s.operations.beginMinecraftMaintenance(plan.PlanFingerprint, operationTypeMinecraftReset, plan.InstanceID)
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
	id := pluginInstanceID(parent)
	record, err := instanceRuntimes.readRecord(id)
	if err == nil {
		err = instanceRuntimes.verifyLive(record)
	}
	fail := func(err error) (adminMinecraftResetPlanResponse, int) {
		return adminMinecraftResetPlanResponse{OK: false, Code: "plan_unavailable", Error: err.Error(), Players: []string{}, Warnings: []string{}}, 409
	}
	if err != nil {
		return fail(err)
	}
	if validateInstanceRecordPlacement(record) != nil {
		return fail(errInstanceRecovery)
	}
	if err = instanceRuntimes.storage(parent, "validate", record); err != nil {
		return fail(err)
	}
	backupPath := ""
	if record.Backups != nil {
		backupPath = record.Backups.Path
	} else {
		// Legacy records retain the appliance backup destination.
		snapshot, _, _, snapshotErr := instanceRuntimes.snapshot(parent)
		if snapshotErr != nil {
			return fail(snapshotErr)
		}
		backupPath = snapshot.BackupPath
	}
	// Reuse the existing Reset preservation classification, in the host mount namespace.
	raw, err := instanceRuntimes.command(parent, "nsenter", "--mount=/proc/1/ns/mnt", "--", "bash", "-c", `source /usr/libexec/justvoxel/mjust/minecraft-reset-common.sh; jv_reset_data_scope "$1"`, "reset-data-scope", record.Storage.DataPath)
	if err != nil {
		return fail(err)
	}
	scope := strings.TrimSpace(string(raw))
	action := "preserve"
	if scope == "internal" {
		action = "delete"
	}
	if scope != "internal" && scope != "external" && scope != "network" && scope != "unknown" {
		return fail(errInstanceRecovery)
	}
	names, err := instanceRuntimes.identities().readNames()
	if err != nil || names[id].DisplayName == "" {
		return fail(errInstanceRecovery)
	}
	plan := adminMinecraftResetPlanResponse{OK: true, InstanceID: id, InstanceName: names[id].DisplayName, SchemaVersion: "v1", Mode: "minecraft", DataPath: record.Storage.DataPath, DataScope: scope, DataAction: action, BackupPath: backupPath, BackupAction: "preserve", StorageLayoutAction: "preserve", AuthenticationAction: "preserve", WebUIUsersAction: "preserve", Players: []string{}, Warnings: []string{}}
	plan.PlayersOnline, plan.Players, err = pluginInstallPlayers(parent, id)
	if err != nil {
		return fail(err)
	}
	if err = prepareMinecraftResetPlugins(&plan); err != nil {
		return fail(err)
	}
	plan.PlanFingerprint, err = minecraftResetPlanFingerprint(plan)
	if err != nil {
		return fail(err)
	}
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
		InstanceID:           plan.InstanceID,
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
	journal, err := s.operations.get(operationID)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		_, _ = s.operations.transition(operationID, operationNeedsAttention, "reset_recovery", "Selected Minecraft reset needs recovery: "+err.Error())
		return err
	}
	if !instanceIDPattern.MatchString(journal.InstanceID) || journal.OperationType != operationTypeMinecraftReset || journal.PlanFingerprint != expectedFingerprint {
		return fail(errInstanceRecovery)
	}
	ctx = withPluginInstance(withOperationTrace(ctx, s.operations, operationID), journal.InstanceID)

	plan, status := s.minecraftResetPlanWithRecovery(ctx)
	if status != 0 || !plan.OK || plan.InstanceID != journal.InstanceID || plan.PlanFingerprint != expectedFingerprint {
		return fail(errInstanceRecovery)
	}
	if _, err = s.operations.transition(operationID, operationValidating, "validating", "Revalidating selected Minecraft reset."); err != nil {
		return err
	}
	lock, err := acquirePluginRuntimeLock()
	if err != nil {
		return fail(err)
	}
	defer lock.Close()
	tx, err := s.prepareResetPluginTransaction(ctx, operationID, plan)
	if err != nil {
		return fail(err)
	}
	if _, err = s.operations.transition(operationID, operationRunning, "resetting", "Retiring the selected Minecraft server."); err != nil {
		return err
	}
	if !s.resetPluginHelperComplete(operationID) {
		if err = instanceRuntimes.retireSelectedInstance(ctx, plan.InstanceID, tx.InstanceName, plan.DataAction); err != nil {
			return fail(err)
		}
		if err = s.markResetPluginHelperComplete(*tx); err != nil {
			return fail(err)
		}
	}
	if _, err = s.operations.transition(operationID, operationVerifying, "verifying", "Verifying selected Minecraft retirement."); err != nil {
		return err
	}
	if plan.DataAction == "preserve" {
		if err = commitResetPluginCleanup(*tx); err != nil {
			return fail(err)
		}
	} else if err = verifyResetInternalPluginAbsence(plan.DataPath); err != nil {
		return fail(err)
	}
	if _, err = instanceRuntimes.readRecord(plan.InstanceID); !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errInstanceNotFound) {
		return fail(errInstanceRecovery)
	}
	if err = instanceRuntimes.removePrivate(instanceRuntimes.deletePath(plan.InstanceID)); err != nil {
		return fail(err)
	}
	if _, err = s.operations.transition(operationID, operationSucceeded, "complete", "Selected Minecraft server reset completed."); err != nil {
		return err
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "reset_minecraft", plan.InstanceID, true, "Selected Minecraft instance retired; backups and authentication preserved")
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
	InstanceID        string                          `json:"instance_id"`
	InstanceName      string                          `json:"instance_name"`
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
	if tx.Plan.DataAction == "preserve" {
		tx.Plan.PluginEvidence = &tx.Plugins
	}
	fingerprint, err := minecraftResetPlanFingerprint(tx.Plan)
	if err != nil || tx.SchemaVersion != "v1" || tx.OperationID != id || !instanceIDPattern.MatchString(tx.InstanceID) || tx.InstanceID != tx.Plan.InstanceID || tx.InstanceName == "" || (tx.Plan.DataAction == "preserve" && (tx.Plan.PluginsAction != "remove_managed_jars" || tx.Plan.DataPath != tx.Plugins.DataPath)) || fingerprint != tx.Plan.PlanFingerprint {
		return nil, errors.New("Invalid reset plugin evidence identity")
	}
	if err = validateAdminMinecraftResetPlan(tx.Plan); err != nil {
		return nil, err
	}
	if tx.Plan.DataAction == "delete" {
		return &tx, nil
	}
	var manifest pluginManifest
	if len(tx.Plugins.Manifest) == 0 {
		manifest = pluginManifest{SchemaVersion: "v1", Artifacts: []pluginManifestEntry{}}
	} else if json.Unmarshal(tx.Plugins.Manifest, &manifest) != nil {
		return nil, errors.New("Invalid original reset ownership")
	}
	if validatePluginManifest(manifest) != nil || len(manifest.Artifacts) != len(tx.Plugins.Entries) {
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
		if err != nil {
			return adminMinecraftResetPlanResponse{Code: "recovery_unavailable"}, 503
		}
		if current != nil {
			if current.InstanceID != pluginInstanceID(ctx) || !instanceIDPattern.MatchString(current.InstanceID) {
				return adminMinecraftResetPlanResponse{Code: "instance_mismatch"}, 409
			}
			tx, err := s.readResetPluginTransaction(current.OperationID)
			if err == nil {
				if tx.InstanceID != current.InstanceID || tx.Plan.PlanFingerprint != current.PlanFingerprint {
					return adminMinecraftResetPlanResponse{Code: "instance_mismatch"}, 409
				}
				// Refresh players only while the committed selected runtime still exists.
				// Once retirement begins, the pinned transaction is the recovery authority.
				if _, e := instanceRuntimes.deleteJournal(current.InstanceID); errors.Is(e, os.ErrNotExist) && !s.resetPluginHelperComplete(current.OperationID) {
					online, players, e := pluginInstallPlayers(ctx, current.InstanceID)
					if e != nil {
						return adminMinecraftResetPlanResponse{Code: "players_unavailable"}, 409
					}
					tx.Plan.PlayersOnline, tx.Plan.Players = online, players
				}
				return tx.Plan, 0
			}
			if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, unix.ENOENT) {
				return adminMinecraftResetPlanResponse{Code: "recovery_evidence_missing"}, 409
			}
			// No destructive boundary is reachable before transaction publication.
			// Recover an interrupted preflight only from the same live ID and exact plan,
			// and only when no retirement intent exists. Never reconstruct lost intent.
			if _, e := instanceRuntimes.deleteJournal(current.InstanceID); !errors.Is(e, os.ErrNotExist) {
				return adminMinecraftResetPlanResponse{Code: "recovery_evidence_missing"}, 409
			}
			fresh, status := authoritativeAdminMinecraftResetPlan(ctx)
			if status != 0 || fresh.PlanFingerprint != current.PlanFingerprint {
				return adminMinecraftResetPlanResponse{Code: "plan_changed"}, 409
			}
			return fresh, 0
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
		fresh, status := authoritativeAdminMinecraftResetPlan(ctx)
		if status != 0 || fresh.InstanceID != plan.InstanceID || fresh.PlanFingerprint != plan.PlanFingerprint {
			return nil, errInstanceRecovery
		}
		tx = &minecraftResetPluginTransaction{InstanceID: plan.InstanceID, InstanceName: fresh.InstanceName, SchemaVersion: "v1", OperationID: id, Plan: plan}
		if plan.PluginEvidence != nil {
			tx.Plugins = *plan.PluginEvidence
		}
		tx.OriginallyRunning, err = pluginServiceActive(ctx, plan.InstanceID)
		if err != nil {
			return nil, err
		}
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
	if tx.InstanceID != plan.InstanceID || tx.Plan.PlanFingerprint != plan.PlanFingerprint {
		return nil, errInstanceRecovery
	}
	if s.resetPluginHelperComplete(id) {
		return tx, nil
	}
	// A retirement journal pins runtime/storage evidence after the first reload.
	if _, err := instanceRuntimes.deleteJournal(plan.InstanceID); err == nil {
		return tx, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err = validatePluginInstance(tx.InstanceID, plan.DataPath); err != nil {
		return nil, err
	}
	state, err := instanceRuntimes.observe(ctx, tx.InstanceID)
	if err != nil {
		return nil, err
	}
	if state.State == "running" {
		err = instanceRuntimes.actionWithWarnings(ctx, tx.InstanceID, "stop", true)
	} else if state.State != "stopped" {
		err = errInstanceBusy
	}
	if err != nil {
		return nil, err
	}
	if plan.DataAction == "preserve" {
		if err = checkResetPluginIdentity(tx.Plugins); err == nil {
			err = stageResetPluginJars(*tx)
		}
		if err != nil {
			return nil, errors.Join(err, restoreResetPluginJars(*tx))
		}
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
		InstanceID  string `json:"instance_id"`
	}
	return readPrivatePluginJSON(dir, "helper-complete.json", &marker, pluginManifestLimit) == nil && marker.OperationID == id && marker.InstanceID == pluginInstanceIDForResetEvidence(s, id)
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
		InstanceID  string `json:"instance_id"`
	}{tx.OperationID, tx.InstanceID})
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
var verifyInterruptedMinecraftResetRuntime = func(context.Context) error { return errInstanceRecovery }

func checkPinnedResetPluginDirectories(tx minecraftResetPluginTransaction, active, disabled, staging *os.File) error {
	return checkPinnedManagedPluginDirectories(pluginManageTransaction{OperationID: tx.OperationID, DataPath: tx.Plugins.DataPath, DataDevice: tx.Plugins.Device, DataInode: tx.Plugins.Inode}, active, disabled, staging)
}

func (s *server) selectedResetHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.requireAdministrator(w, r); !ok {
			return
		}
		id, ok := instanceRequestID(w, r)
		if !ok {
			return
		}
		if r.PathValue("operation") != "" {
			next(w, r.WithContext(withPluginInstance(r.Context(), id)))
			return
		}
		if s.operations != nil {
			current, err := s.operations.currentMinecraftReset()
			if err != nil {
				writeError(w, 503, "Reset tracking unavailable")
				return
			}
			if current != nil && current.InstanceID != id {
				writeError(w, 409, "Reset operation instance mismatch")
				return
			}
			// Recovery remains available after the selected identity has been retired.
			if current != nil {
				next(w, r.WithContext(withPluginInstance(r.Context(), id)))
				return
			}
		}
		if err := validatePluginInstance(id, ""); err != nil {
			instanceRuntimeError(w, err, id)
			return
		}
		next(w, r.WithContext(withPluginInstance(r.Context(), id)))
	}
}
func (s *server) selectedResetCurrent(w http.ResponseWriter, r *http.Request) {
	if s.operations == nil {
		writeError(w, 503, "Reset tracking unavailable")
		return
	}
	journal, err := s.operations.currentMinecraftReset()
	if err != nil || (journal != nil && journal.InstanceID != pluginInstanceID(r.Context())) {
		writeError(w, 409, "Reset operation instance mismatch")
		return
	}
	writeJSON(w, 200, adminOperationResponse{Operation: journal})
}
func (s *server) selectedResetStatus(w http.ResponseWriter, r *http.Request) {
	if s.operations == nil {
		writeError(w, 503, "Reset tracking unavailable")
		return
	}
	journal, err := s.operations.get(r.PathValue("operation"))
	if err != nil || journal.OperationType != operationTypeMinecraftReset || journal.InstanceID != pluginInstanceID(r.Context()) {
		writeError(w, 409, "Reset operation instance mismatch")
		return
	}
	writeJSON(w, 200, adminOperationResponse{Operation: &journal})
}

func pluginInstanceIDForResetEvidence(s *server, id string) string {
	tx, err := s.readResetPluginTransaction(id)
	if err != nil {
		return ""
	}
	return tx.InstanceID
}
