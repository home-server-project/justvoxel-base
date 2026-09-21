package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

const adminSetupStorageTransactionHelper = "/usr/libexec/justvoxel/mjust/admin-setup-storage-transaction-json"

var (
	setupStorageValidateTimeout = 20 * time.Second
	setupStorageApplyTimeout    = 90 * time.Second
	setupStorageRollbackTimeout = 45 * time.Second
)

var runAdminSetupStorageTransactionHelper = func(ctx context.Context, action string, request []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, adminSetupStorageTransactionHelper, action)
	cmd.Stdin = bytes.NewReader(request)
	return cmd.CombinedOutput()
}

type setupStorageTransactionTarget struct {
	Type                string `json:"type"`
	Path                string `json:"path"`
	Device              string `json:"device,omitempty"`
	ParentDisk          string `json:"parent_disk,omitempty"`
	Filesystem          string `json:"filesystem,omitempty"`
	UUID                string `json:"uuid,omitempty"`
	MountPoint          string `json:"mount_point,omitempty"`
	ExpectedUUID        string `json:"expected_uuid,omitempty"`
	ExpectedSource      string `json:"expected_source,omitempty"`
	Source              string `json:"source,omitempty"`
	Username            string `json:"username,omitempty"`
	Domain              string `json:"domain,omitempty"`
	CredentialsRequired bool   `json:"credentials_required,omitempty"`
}

type setupStorageTransactionRequest struct {
	SchemaVersion   string                        `json:"schema_version"`
	OperationID     string                        `json:"operation_id"`
	PlanFingerprint string                        `json:"plan_fingerprint"`
	Storage         setupStorageTransactionTarget `json:"storage"`
	Backups         setupStorageTransactionTarget `json:"backups"`
	SMBPassword     string                        `json:"smb_password,omitempty"`
}

type setupStorageTransactionResponse struct {
	OK             bool   `json:"ok"`
	Applied        bool   `json:"applied"`
	Phase          string `json:"phase"`
	RollbackState  string `json:"rollback_state"`
	RollbackResult string `json:"rollback_result"`
	Error          string `json:"error,omitempty"`
}

var errSetupStorageTypeUnsupported = errors.New("unsupported setup storage type")

func setupStorageTargetFromPlan(targetType, path, device, parentDisk, filesystem, uuid, mountPoint, expectedUUID, expectedSource, source, username, domain string, credentialsRequired, allowNetwork bool) (setupStorageTransactionTarget, error) {
	if targetType != "system" && targetType != "partition" && !(allowNetwork && (targetType == "nfs" || targetType == "smb")) {
		return setupStorageTransactionTarget{}, errSetupStorageTypeUnsupported
	}
	return setupStorageTransactionTarget{
		Type: targetType, Path: path, Device: device, ParentDisk: parentDisk,
		Filesystem: filesystem, UUID: uuid, MountPoint: mountPoint,
		ExpectedUUID: expectedUUID, ExpectedSource: expectedSource,
		Source: source, Username: username, Domain: domain, CredentialsRequired: credentialsRequired,
	}, nil
}

func setupStorageTransactionRequestForOperation(operation operationJournal, plan *adminSetupNormalizedPlan, smbPassword string) (setupStorageTransactionRequest, error) {
	if plan == nil {
		return setupStorageTransactionRequest{}, errors.New("normalized setup plan is required")
	}
	if operation.OperationType != operationTypeSetup || !validOperationID(operation.OperationID) || !operationFingerprintPattern.MatchString(operation.PlanFingerprint) {
		return setupStorageTransactionRequest{}, errors.New("invalid setup operation identity")
	}
	storage, err := setupStorageTargetFromPlan(
		plan.Storage.Type, plan.Storage.Path, plan.Storage.Device, plan.Storage.ParentDisk,
		plan.Storage.Filesystem, plan.Storage.UUID, plan.Storage.MountPoint,
		plan.Storage.ExpectedUUID, plan.Storage.ExpectedSource,
		"", "", "", false, false,
	)
	if err != nil {
		return setupStorageTransactionRequest{}, err
	}
	backups, err := setupStorageTargetFromPlan(
		plan.Backups.Type, plan.Backups.Path, plan.Backups.Device, plan.Backups.ParentDisk,
		plan.Backups.Filesystem, plan.Backups.UUID, plan.Backups.MountPoint,
		plan.Backups.ExpectedUUID, plan.Backups.ExpectedSource,
		plan.Backups.Source, plan.Backups.Username, plan.Backups.Domain, plan.Backups.CredentialsRequired, true,
	)
	if err != nil {
		return setupStorageTransactionRequest{}, err
	}
	return setupStorageTransactionRequest{
		SchemaVersion:   operationSchemaVersion,
		OperationID:     operation.OperationID,
		PlanFingerprint: operation.PlanFingerprint,
		Storage:         storage,
		Backups:         backups,
		SMBPassword:     smbPassword,
	}, nil
}

func runSetupStorageTransactionAction(parent context.Context, action string, timeout time.Duration, request setupStorageTransactionRequest) (setupStorageTransactionResponse, error) {
	var response setupStorageTransactionResponse
	if action != "validate" && action != "apply" && action != "rollback" {
		return response, errors.New("invalid setup storage transaction action")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return response, err
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	output, err := runAdminSetupStorageTransactionHelper(ctx, action, payload)
	if err != nil {
		return response, newSetupHelperExecutionError("storage", action, err, output)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return response, errors.New("setup storage transaction helper returned invalid data")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return response, errors.New("setup storage transaction helper returned invalid data")
	}
	if response.Phase == "" || response.RollbackState == "" {
		return response, errors.New("setup storage transaction helper returned incomplete data")
	}
	if response.Error != "" && (len(response.Error) > 512 || bytes.ContainsAny([]byte(response.Error), "\r\n")) {
		response.Error = "setup storage transaction failed"
	}
	return response, nil
}

func executeSetupStorage(parent context.Context, store *operationStore, operationID string, plan *adminSetupNormalizedPlan, smbPassword string) error {
	if store == nil {
		return errors.New("operation store is unavailable")
	}
	operation, err := store.get(operationID)
	if err != nil {
		return err
	}
	if operation.State != operationQueued {
		return fmt.Errorf("setup storage execution requires queued operation, got %s", operation.State)
	}
	request, err := setupStorageTransactionRequestForOperation(operation, plan, smbPassword)
	if err != nil {
		return err
	}

	if _, err := store.transition(operationID, operationValidating, "storage_preflight", "Revalidating reviewed storage."); err != nil {
		return err
	}
	store.appendSetupStorageEvidenceBestEffort(operationID, "preflight", plan, "not_run")
	validation, err := runSetupStorageTransactionAction(parent, "validate", setupStorageValidateTimeout, request)
	if err != nil {
		store.appendSetupHelperFailureBestEffort(operationID, "storage", "validate", err)
	}
	if err != nil || !validation.OK {
		cause := err
		if cause == nil {
			message := validation.Error
			if message == "" {
				message = "storage preflight failed"
			}
			cause = errors.New(message)
		}
		store.appendSetupStorageFailureEvidenceBestEffort(operationID, "validate", plan, cause)
		return finishSetupStorageWithoutMutation(store, operationID, validation, err)
	}

	if _, err := store.transition(operationID, operationRunning, "storage_snapshot", "Preparing reversible storage transaction."); err != nil {
		return err
	}
	applied, err := runSetupStorageTransactionAction(parent, "apply", setupStorageApplyTimeout, request)
	if err != nil {
		store.appendSetupHelperFailureBestEffort(operationID, "storage", "apply", err)
	}
	if err != nil {
		store.appendSetupStorageFailureEvidenceBestEffort(operationID, "apply", plan, err)
		_, _ = store.transition(operationID, operationNeedsAttention, "storage_unknown", "Storage execution stopped without a trustworthy rollback result.")
		return err
	}
	if !applied.OK || !applied.Applied {
		message := applied.Error
		if message == "" {
			message = "storage transaction failed"
		}
		store.appendSetupStorageFailureEvidenceBestEffort(operationID, "apply", plan, errors.New(message))
		return finishSetupStorageAfterApplyFailure(store, operationID, applied)
	}
	if applied.Phase != "storage_verified" {
		err := errors.New("unexpected setup storage completion phase")
		store.appendSetupStorageFailureEvidenceBestEffort(operationID, "apply", plan, err)
		_, _ = store.transition(operationID, operationNeedsAttention, "storage_unknown", "Storage helper returned an unexpected completion phase.")
		return err
	}
	store.appendSetupStorageEvidenceBestEffort(operationID, "verified", plan, "passed")
	_, err = store.updateProgress(operationID, operationRunning, "storage_verified", "Storage transaction completed and was verified.")
	return err
}

func finishSetupStorageWithoutMutation(store *operationStore, operationID string, response setupStorageTransactionResponse, helperErr error) error {
	message := "Reviewed storage could not be revalidated."
	if response.Error != "" {
		message = response.Error
	}
	if _, err := store.transition(operationID, operationFailed, "storage_preflight", message); err != nil {
		return err
	}
	if _, err := store.transition(operationID, operationRollingBack, "storage_rollback", "No storage mutation occurred; closing the failed transaction safely."); err != nil {
		return err
	}
	if _, err := store.transition(operationID, operationRolledBack, "storage_rolled_back", "No storage changes were made."); err != nil {
		return err
	}
	if helperErr != nil {
		return helperErr
	}
	return errors.New(message)
}

func finishSetupStorageAfterApplyFailure(store *operationStore, operationID string, response setupStorageTransactionResponse) error {
	message := response.Error
	if message == "" {
		message = "Storage transaction failed."
	}
	if _, err := store.transition(operationID, operationFailed, "storage_failed", message); err != nil {
		return err
	}
	if response.RollbackState == "succeeded" {
		if _, err := store.transition(operationID, operationRollingBack, "storage_rollback", "Storage rollback completed; finalizing operation state."); err != nil {
			return err
		}
		if _, err := store.transition(operationID, operationRolledBack, "storage_rolled_back", "Storage changes were rolled back."); err != nil {
			return err
		}
		return errors.New(message)
	}
	if _, err := store.transition(operationID, operationNeedsAttention, "storage_rollback", "Storage rollback could not be confirmed; manual attention is required."); err != nil {
		return err
	}
	return errors.New(message)
}

func rollbackSetupStorage(parent context.Context, store *operationStore, operationID string, plan *adminSetupNormalizedPlan) (setupStorageTransactionResponse, error) {
	var response setupStorageTransactionResponse
	if store == nil {
		return response, errors.New("operation store is unavailable")
	}
	operation, err := store.get(operationID)
	if err != nil {
		return response, err
	}
	request, err := setupStorageTransactionRequestForOperation(operation, plan, "")
	if err != nil {
		return response, err
	}
	response, err = runSetupStorageTransactionAction(parent, "rollback", setupStorageRollbackTimeout, request)
	if err != nil {
		store.appendSetupHelperFailureBestEffort(operationID, "storage", "rollback", err)
		store.appendSetupStorageFailureEvidenceBestEffort(operationID, "rollback", plan, err)
		return response, err
	}
	if !response.OK || response.RollbackState != "succeeded" {
		message := response.Error
		if message == "" {
			message = "storage rollback could not be confirmed"
		}
		store.appendSetupStorageFailureEvidenceBestEffort(operationID, "rollback", plan, errors.New(message))
		return response, nil
	}
	store.appendSetupStorageEvidenceBestEffort(operationID, "rolled_back", plan, "rollback_verified")
	return response, nil
}

func executeSetupLocalStorage(parent context.Context, store *operationStore, operationID string, plan *adminSetupNormalizedPlan) error {
	return executeSetupStorage(parent, store, operationID, plan, "")
}

func rollbackSetupLocalStorage(parent context.Context, store *operationStore, operationID string, plan *adminSetupNormalizedPlan) (setupStorageTransactionResponse, error) {
	return rollbackSetupStorage(parent, store, operationID, plan)
}
