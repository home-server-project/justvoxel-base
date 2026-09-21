package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var setupStorageEvidenceTransactionRoot = "/var/lib/justvoxel/management/transactions"

type setupStorageDiagnosticManifest struct {
	Phase              string            `json:"phase"`
	FSTabChanged       bool              `json:"fstab_changed"`
	CredentialsChanged bool              `json:"credentials_changed"`
	Mounts             []json.RawMessage `json:"mounts_by_transaction"`
	Directories        []string          `json:"directories_created"`
	Rollback           struct {
		State  string `json:"state"`
		Result string `json:"result"`
	} `json:"rollback"`
}

func (s *operationStore) appendSetupStorageHelperEvidenceBestEffort(operationID, action string, evidence map[string]string) {
	if s == nil || !validOperationID(operationID) || len(evidence) == 0 {
		return
	}
	values := make(map[string]string, len(evidence)+1)
	values["action"] = action
	for key, value := range evidence {
		values[key] = value
	}
	_ = s.appendSetupDiagnostic(operationID, "STORAGE", "storage helper evidence", values)
}

func (s *operationStore) appendSetupStorageEvidenceBestEffort(operationID, phase string, plan *adminSetupNormalizedPlan, writeProbe string) {
	if s == nil || !validOperationID(operationID) {
		return
	}
	values := collectSetupStorageEvidence(operationID, plan)
	values["phase"] = phase
	values["write_probe"] = writeProbe
	_ = s.appendSetupDiagnostic(operationID, "STORAGE", "storage dependency evidence", values)
}

func (s *operationStore) appendSetupStorageFailureEvidenceBestEffort(operationID, action string, plan *adminSetupNormalizedPlan, cause error) {
	if s == nil || !validOperationID(operationID) {
		return
	}
	values := collectSetupStorageEvidence(operationID, plan)
	values["action"] = action
	if cause != nil {
		values["error"] = cause.Error()
	}
	_ = s.appendSetupDiagnostic(operationID, "FAILURE", "storage failure evidence", values)
}

func collectSetupStorageEvidence(operationID string, plan *adminSetupNormalizedPlan) map[string]string {
	values := setupStorageManifestEvidence(operationID)
	if plan == nil {
		return values
	}
	for key, value := range setupStorageTargetEvidence("data", plan.Storage.Path, plan.Storage.MountPoint, plan.Storage.ExpectedUUID, plan.Storage.ExpectedSource) {
		values[key] = value
	}
	for key, value := range setupStorageTargetEvidence("backup", plan.Backups.Path, plan.Backups.MountPoint, plan.Backups.ExpectedUUID, plan.Backups.ExpectedSource) {
		values[key] = value
	}
	values["data_type"] = plan.Storage.Type
	values["backup_type"] = plan.Backups.Type
	return values
}

func setupStorageTargetEvidence(prefix, path, mountPoint, expectedUUID, expectedSource string) map[string]string {
	target := strings.TrimSpace(mountPoint)
	if target == "" {
		target = strings.TrimSpace(path)
	}
	values := map[string]string{
		prefix + "_mount_present": "unknown",
		prefix + "_filesystem":    "<unavailable>",
		prefix + "_size_mib":      "<unavailable>",
		prefix + "_available_mib": "<unavailable>",
		prefix + "_permissions":   "<unavailable>",
		prefix + "_selinux_type":  "<unavailable>",
		prefix + "_uuid_match":    "not_applicable",
		prefix + "_source_match":  "not_applicable",
	}
	if target == "" {
		return values
	}

	if output, err := setupDiagnosticRunCommand(4*time.Second, "findmnt", "-n", "-o", "FSTYPE", "--target", target); err == nil {
		values[prefix+"_mount_present"] = "yes"
		values[prefix+"_filesystem"] = setupDiagnosticFirstLine(output)
	} else {
		values[prefix+"_mount_present"] = "no"
	}

	if output, err := setupDiagnosticRunCommand(4*time.Second, "df", "-Pm", "--output=size,avail", "--", target); err == nil {
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[len(lines)-1])
			if len(fields) >= 2 {
				values[prefix+"_size_mib"] = fields[0]
				values[prefix+"_available_mib"] = fields[1]
			}
		}
	}

	if output, err := setupDiagnosticRunCommand(4*time.Second, "stat", "-c", "%a:%u:%g", "--", path); err == nil {
		values[prefix+"_permissions"] = setupDiagnosticFirstLine(output)
	}
	values[prefix+"_selinux_type"] = setupDiagnosticSELinuxType(path)

	if expectedUUID != "" {
		values[prefix+"_uuid_match"] = "unavailable"
		if output, err := setupDiagnosticRunCommand(4*time.Second, "findmnt", "-n", "-o", "UUID", "--target", target); err == nil {
			if strings.TrimSpace(string(output)) == expectedUUID {
				values[prefix+"_uuid_match"] = "yes"
			} else {
				values[prefix+"_uuid_match"] = "no"
			}
		}
	}

	if expectedSource != "" {
		values[prefix+"_source_match"] = "unavailable"
		if output, err := setupDiagnosticRunCommand(4*time.Second, "findmnt", "-n", "-o", "SOURCE", "--target", target); err == nil {
			if strings.TrimSpace(string(output)) == expectedSource {
				values[prefix+"_source_match"] = "yes"
			} else {
				values[prefix+"_source_match"] = "no"
			}
		}
	}
	return values
}

func setupStorageManifestEvidence(operationID string) map[string]string {
	values := map[string]string{
		"transaction_manifest":    "absent",
		"transaction_phase":       "<unavailable>",
		"fstab_changed":           "<unavailable>",
		"credentials_changed":     "<unavailable>",
		"transaction_mount_count": "<unavailable>",
		"created_directory_count": "<unavailable>",
		"rollback_state":          "<unavailable>",
		"rollback_result":         "<unavailable>",
	}
	if !validOperationID(operationID) {
		return values
	}

	path := filepath.Join(setupStorageEvidenceTransactionRoot, operationID, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return values
	}

	var manifest setupStorageDiagnosticManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		values["transaction_manifest"] = "invalid"
		return values
	}

	values["transaction_manifest"] = "present"
	values["transaction_phase"] = manifest.Phase
	values["fstab_changed"] = strconv.FormatBool(manifest.FSTabChanged)
	values["credentials_changed"] = strconv.FormatBool(manifest.CredentialsChanged)
	values["transaction_mount_count"] = strconv.Itoa(len(manifest.Mounts))
	values["created_directory_count"] = strconv.Itoa(len(manifest.Directories))
	values["rollback_state"] = manifest.Rollback.State
	values["rollback_result"] = manifest.Rollback.Result
	return values
}
