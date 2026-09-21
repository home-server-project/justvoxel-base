package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"time"
)

const adminSetupRuntimeTransactionHelper = "/usr/libexec/justvoxel/mjust/admin-setup-runtime-transaction-json"

var (
	setupRuntimeValidateTimeout = 20 * time.Second
	setupRuntimeApplyTimeout    = 60 * time.Second
	setupRuntimeVerifyTimeout   = 20 * time.Minute
	setupRuntimeRollbackTimeout = 90 * time.Second
	setupRuntimeCommitTimeout   = 15 * time.Second
	setupWorkerTimeout          = 30 * time.Minute
)

var runAdminSetupRuntimeTransactionHelper = func(ctx context.Context, action string, request []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, adminSetupRuntimeTransactionHelper, action)
	cmd.Stdin = bytes.NewReader(request)
	return cmd.CombinedOutput()
}

type setupRuntimeServer struct {
	MOTD           string `json:"motd"`
	MaxPlayers     int    `json:"max_players"`
	BedrockEnabled bool   `json:"bedrock_enabled"`
	Timezone       string `json:"timezone"`
}

type setupRuntimeMinecraft struct {
	JavaMemory      string `json:"java_memory"`
	ContainerMemory string `json:"container_memory"`
	JavaPort        int    `json:"java_port"`
	BedrockPort     int    `json:"bedrock_port"`
	ImageTag        string `json:"image_tag"`
	VersionPolicy   string `json:"version_policy"`
	Version         string `json:"version"`
	MinecraftUID    uint32 `json:"minecraft_uid"`
	MinecraftGID    uint32 `json:"minecraft_gid"`
}

type setupRuntimeStorage struct {
	Type           string `json:"type"`
	Path           string `json:"path"`
	MountPoint     string `json:"mount_point,omitempty"`
	ExpectedUUID   string `json:"expected_uuid,omitempty"`
	ExpectedSource string `json:"expected_source,omitempty"`
}

type setupRuntimeBackups struct {
	Type           string `json:"type"`
	Path           string `json:"path"`
	MountPoint     string `json:"mount_point,omitempty"`
	ExpectedUUID   string `json:"expected_uuid,omitempty"`
	ExpectedSource string `json:"expected_source,omitempty"`
	Automatic      bool   `json:"automatic"`
	Schedule       string `json:"schedule"`
	Keep           int    `json:"keep"`
}

type setupRuntimeTransactionRequest struct {
	SchemaVersion   string                         `json:"schema_version"`
	OperationID     string                         `json:"operation_id"`
	PlanFingerprint string                         `json:"plan_fingerprint"`
	Server          setupRuntimeServer             `json:"server"`
	Minecraft       setupRuntimeMinecraft          `json:"minecraft"`
	Storage         setupRuntimeStorage            `json:"storage"`
	Backups         setupRuntimeBackups            `json:"backups"`
}

type setupRuntimeTransactionResponse struct {
	OK      bool   `json:"ok"`
	Applied bool   `json:"applied"`
	Phase   string `json:"phase"`
	Error   string `json:"error,omitempty"`
}

func setupRuntimeRequestForOperation(operation operationJournal, plan *adminSetupNormalizedPlan) (setupRuntimeTransactionRequest, error) {
	if plan == nil {
		return setupRuntimeTransactionRequest{}, errors.New("normalized setup plan is required")
	}
	if operation.OperationType != operationTypeSetup || !validOperationID(operation.OperationID) || !operationFingerprintPattern.MatchString(operation.PlanFingerprint) {
		return setupRuntimeTransactionRequest{}, errors.New("invalid setup operation identity")
	}
	if plan.Minecraft.MinecraftUID == 0 || plan.Minecraft.MinecraftGID == 0 {
		return setupRuntimeTransactionRequest{}, errors.New("invalid Minecraft runtime identity")
	}
	return setupRuntimeTransactionRequest{
		SchemaVersion: operationSchemaVersion, OperationID: operation.OperationID, PlanFingerprint: operation.PlanFingerprint,
		Server: setupRuntimeServer{MOTD: plan.Server.MOTD, MaxPlayers: plan.Server.MaxPlayers, BedrockEnabled: plan.Server.BedrockEnabled, Timezone: plan.Server.Timezone},
		Minecraft: setupRuntimeMinecraft{JavaMemory: plan.Minecraft.JavaMemory, ContainerMemory: plan.Minecraft.ContainerMemory, JavaPort: plan.Minecraft.JavaPort, BedrockPort: plan.Minecraft.BedrockPort, ImageTag: plan.Minecraft.ImageTag, VersionPolicy: plan.Minecraft.VersionPolicy, Version: plan.Minecraft.Version, MinecraftUID: plan.Minecraft.MinecraftUID, MinecraftGID: plan.Minecraft.MinecraftGID},
		Storage: setupRuntimeStorage{Type: plan.Storage.Type, Path: plan.Storage.Path, MountPoint: plan.Storage.MountPoint, ExpectedUUID: plan.Storage.ExpectedUUID, ExpectedSource: plan.Storage.ExpectedSource},
		Backups: setupRuntimeBackups{Type: plan.Backups.Type, Path: plan.Backups.Path, MountPoint: plan.Backups.MountPoint, ExpectedUUID: plan.Backups.ExpectedUUID, ExpectedSource: plan.Backups.ExpectedSource, Automatic: plan.Backups.Automatic, Schedule: plan.Backups.Schedule, Keep: plan.Backups.Keep},
	}, nil
}

func runSetupRuntimeTransactionAction(parent context.Context, action string, timeout time.Duration, request setupRuntimeTransactionRequest) (setupRuntimeTransactionResponse, error) {
	var response setupRuntimeTransactionResponse
	switch action {
	case "validate", "apply", "verify", "rollback", "commit":
	default:
		return response, errors.New("invalid setup runtime transaction action")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return response, err
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	output, err := runAdminSetupRuntimeTransactionHelper(ctx, action, payload)
	if err != nil {
		return response, newSetupHelperExecutionError("runtime", action, err, output)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return response, errors.New("setup runtime transaction helper returned invalid data")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return response, errors.New("setup runtime transaction helper returned invalid data")
	}
	if response.Phase == "" {
		return response, errors.New("setup runtime transaction helper returned incomplete data")
	}
	if response.Error != "" && (len(response.Error) > 512 || bytes.ContainsAny([]byte(response.Error), "\r\n")) {
		response.Error = "setup runtime transaction failed"
	}
	return response, nil
}

func executeSetupTransaction(parent context.Context, store *operationStore, operationID string, plan *adminSetupNormalizedPlan, smbPassword []byte) error {
	if store == nil {
		return errors.New("operation store is unavailable")
	}
	if err := executeSetupStorage(parent, store, operationID, plan, string(smbPassword)); err != nil {
		zeroBytes(smbPassword)
		return err
	}
	zeroBytes(smbPassword)

	operation, err := store.get(operationID)
	if err != nil {
		return err
	}
	request, err := setupRuntimeRequestForOperation(operation, plan)
	if err != nil {
		return failSetupAfterStorage(parent, store, operationID, plan, setupRuntimeTransactionRequest{}, err)
	}
	if _, err := store.updateProgress(operationID, operationRunning, "runtime_preflight", "Validating first-run Minecraft runtime activation."); err != nil {
		return err
	}
	store.appendSetupRuntimeEvidenceBestEffort(operationID, "preflight", plan)
	validated, err := runSetupRuntimeTransactionAction(parent, "validate", setupRuntimeValidateTimeout, request)
	if err != nil {
		store.appendSetupHelperFailureBestEffort(operationID, "runtime", "validate", err)
	}
	if err != nil || !validated.OK {
		if err == nil {
			err = errors.New(setupRuntimeFirstNonEmpty(validated.Error, "runtime preflight failed"))
		}
		store.appendSetupRuntimeFailureBundleBestEffort(operationID, "validate", plan, err)
		return failSetupAfterStorage(parent, store, operationID, plan, request, err)
	}
	if _, err := store.updateProgress(operationID, operationRunning, "runtime_config", "Writing transactional Minecraft runtime configuration."); err != nil {
		return err
	}
	applied, err := runSetupRuntimeTransactionAction(parent, "apply", setupRuntimeApplyTimeout, request)
	if err != nil {
		store.appendSetupHelperFailureBestEffort(operationID, "runtime", "apply", err)
	}
	if err != nil || !applied.OK || !applied.Applied {
		if err == nil {
			err = errors.New(setupRuntimeFirstNonEmpty(applied.Error, "runtime activation failed"))
		}
		store.appendSetupRuntimeFailureBundleBestEffort(operationID, "apply", plan, err)
		return failSetupAfterStorage(parent, store, operationID, plan, request, err)
	}
	store.appendSetupRuntimeEvidenceBestEffort(operationID, "configured", plan)

	if _, err := store.transition(operationID, operationVerifying, "minecraft_verify", "Starting Minecraft and verifying runtime readiness."); err != nil {
		return err
	}
	verifyStarted := time.Now()
	verified, err := runSetupRuntimeTransactionAction(parent, "verify", setupRuntimeVerifyTimeout, request)
	verifyElapsed := time.Since(verifyStarted)
	if err != nil {
		store.appendSetupHelperFailureBestEffort(operationID, "runtime", "verify", err)
	}
	if err != nil || !verified.OK {
		if err == nil {
			err = errors.New(setupRuntimeFirstNonEmpty(verified.Error, "Minecraft runtime verification failed"))
		}
		store.appendSetupRuntimeFailureBundleBestEffort(operationID, "verify", plan, err)
		return failSetupAfterStorage(parent, store, operationID, plan, request, err)
	}
	store.appendSetupRuntimeVerificationBestEffort(operationID, plan, verifyElapsed)
	if _, err := store.updateProgress(operationID, operationVerifying, "final_validation", "Final JustVoxel validation passed; committing configured state."); err != nil {
		return err
	}
	committed, err := runSetupRuntimeTransactionAction(parent, "commit", setupRuntimeCommitTimeout, request)
	if err != nil {
		store.appendSetupHelperFailureBestEffort(operationID, "runtime", "commit", err)
	}
	if err != nil || !committed.OK {
		if err == nil {
			err = errors.New(setupRuntimeFirstNonEmpty(committed.Error, "configured state could not be committed"))
		}
		store.appendSetupRuntimeFailureBundleBestEffort(operationID, "commit", plan, err)
		return failSetupAfterStorage(parent, store, operationID, plan, request, err)
	}
	store.appendSetupRuntimeEvidenceBestEffort(operationID, "committed", plan)
	_, err = store.transition(operationID, operationSucceeded, "completed", "JustVoxel first-run setup completed successfully.")
	return err
}

func failSetupAfterStorage(parent context.Context, store *operationStore, operationID string, plan *adminSetupNormalizedPlan, runtimeRequest setupRuntimeTransactionRequest, cause error) error {
	current, currentErr := store.get(operationID)
	if currentErr != nil {
		return cause
	}
	if current.State != operationFailed && current.State != operationRollingBack {
		if _, err := store.transition(operationID, operationFailed, "setup_failed", "First-run setup failed and rollback is starting."); err != nil {
			return cause
		}
	}
	if _, err := store.transition(operationID, operationRollingBack, "runtime_rollback", "Rolling back Minecraft runtime changes before storage."); err != nil {
		return cause
	}
	if runtimeRequest.OperationID != "" {
		rolledRuntime, err := runSetupRuntimeTransactionAction(parent, "rollback", setupRuntimeRollbackTimeout, runtimeRequest)
	if err != nil {
		store.appendSetupHelperFailureBestEffort(operationID, "runtime", "rollback", err)
	}
		if err != nil || !rolledRuntime.OK {
			if err == nil {
				err = errors.New(setupRuntimeFirstNonEmpty(rolledRuntime.Error, "runtime rollback could not be confirmed"))
			}
			store.appendSetupRuntimeFailureBundleBestEffort(operationID, "rollback", plan, err)
			_, _ = store.transition(operationID, operationNeedsAttention, "runtime_rollback", "Runtime rollback could not be confirmed; manual attention is required.")
			return cause
		}
		store.appendSetupRuntimeEvidenceBestEffort(operationID, "runtime_rolled_back", plan)
	}
	rolledStorage, err := rollbackSetupStorage(parent, store, operationID, plan)
	if err != nil || !rolledStorage.OK || rolledStorage.RollbackState != "succeeded" {
		_, _ = store.transition(operationID, operationNeedsAttention, "storage_rollback", "Storage rollback could not be confirmed; manual attention is required.")
		return cause
	}
	store.appendSetupRuntimeEvidenceBestEffort(operationID, "rollback_complete", plan)
	_, _ = store.transition(operationID, operationRolledBack, "setup_rolled_back", "First-run changes were rolled back.")
	return cause
}

func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func setupRuntimeFirstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
