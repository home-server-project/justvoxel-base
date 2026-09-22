package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

const (
	adminRestorePlanHelper       = "/usr/libexec/justvoxel/mjust/admin-restore-plan-json"
	adminRestorePlanRequestLimit = 4 * 1024
)

var adminRestorePlanTimeout = 20 * time.Second

var runAdminRestorePlanHelper = func(ctx context.Context, request []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, adminRestorePlanHelper)
	cmd.Stdin = bytes.NewReader(request)
	return cmd.CombinedOutput()
}

type adminRestorePlanRequest struct {
	BackupID string `json:"backup_id"`
	Mode     string `json:"mode"`
}

type adminRestorePlanWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type adminRestorePlanCurrent struct {
	VersionMode    string `json:"version_mode"`
	Version        string `json:"version"`
	BedrockEnabled bool   `json:"bedrock_enabled"`
}

type adminRestorePlanValidation struct {
	ArchiveIntegrity string `json:"archive_integrity"`
	ArchiveSafety    string `json:"archive_safety"`
	StagingSpace     string `json:"staging_space"`
}

type adminRestorePlanNormalized struct {
	BackupID       string                      `json:"backup_id"`
	Mode           string                      `json:"mode"`
	CreatedAt      string                      `json:"created_at"`
	SizeBytes      uint64                      `json:"size_bytes"`
	MetadataStatus string                      `json:"metadata_status"`
	Metadata       *adminRestoreBackupMetadata `json:"metadata,omitempty"`
	Current        adminRestorePlanCurrent     `json:"current"`
	VersionRelation string                     `json:"version_relation"`
	Validation     adminRestorePlanValidation  `json:"validation"`
}

type adminRestorePlanRequirements struct {
	DestructiveConfirmationRequired   bool     `json:"destructive_confirmation_required"`
	PlayersConfirmationRequired       bool     `json:"players_confirmation_required"`
	MinecraftState                    string   `json:"minecraft_state"`
	Online                            int      `json:"online"`
	Players                           []string `json:"players"`
	ArchiveIntegrityValidationOnApply bool     `json:"archive_integrity_validation_on_apply"`
	ArchiveSafetyValidationOnApply    bool     `json:"archive_safety_validation_on_apply"`
	StagingSpaceValidationOnApply     bool     `json:"staging_space_validation_on_apply"`
}

type adminRestorePlanContext struct {
	ArchiveIdentity       string `json:"archive_identity"`
	DataPath              string `json:"data_path"`
	BackupPath            string `json:"backup_path"`
	MinecraftUID          string `json:"minecraft_uid"`
	MinecraftGID          string `json:"minecraft_gid"`
	BackupType            string `json:"backup_type"`
	BackupMountPoint      string `json:"backup_mount_point"`
	BackupExpectedUUID    string `json:"backup_expected_uuid"`
	BackupExpectedSource  string `json:"backup_expected_source"`
}

type adminRestorePlanHelperResponse struct {
	OK           bool                          `json:"ok"`
	SchemaVersion string                       `json:"schema_version"`
	Code         string                        `json:"code,omitempty"`
	Error        string                        `json:"error,omitempty"`
	Normalized   *adminRestorePlanNormalized   `json:"normalized,omitempty"`
	Warnings     []adminRestorePlanWarning     `json:"warnings"`
	Requirements *adminRestorePlanRequirements `json:"requirements,omitempty"`
	Context      *adminRestorePlanContext      `json:"context,omitempty"`
}

type adminRestorePlanResponse struct {
	OK              bool                          `json:"ok"`
	SchemaVersion   string                        `json:"schema_version"`
	PlanFingerprint string                        `json:"plan_fingerprint,omitempty"`
	Code            string                        `json:"code,omitempty"`
	Error           string                        `json:"error,omitempty"`
	Normalized      *adminRestorePlanNormalized   `json:"normalized,omitempty"`
	Warnings        []adminRestorePlanWarning     `json:"warnings"`
	Requirements    *adminRestorePlanRequirements `json:"requirements,omitempty"`
	Context         *adminRestorePlanContext      `json:"-"`
}

type adminRestoreFingerprintPayload struct {
	SchemaVersion string                       `json:"schema_version"`
	Normalized    adminRestorePlanNormalized   `json:"normalized"`
	Requirements  adminRestorePlanRequirements `json:"requirements"`
	Context       adminRestorePlanContext      `json:"context"`
}

type adminRestorePlanningError struct {
	status  int
	message string
}

func (e *adminRestorePlanningError) Error() string { return e.message }

func (s *server) adminRestorePlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}

	var request adminRestorePlanRequest
	if !decodeAdminRestorePlanRequest(w, r, &request) {
		return
	}
	if !adminRestoreBackupIDPattern.MatchString(request.BackupID) {
		writeError(w, http.StatusBadRequest, "invalid restore backup id")
		return
	}
	if request.Mode != "world" && request.Mode != "full" {
		writeError(w, http.StatusBadRequest, "restore mode must be world or full")
		return
	}

	out, planningErr := authoritativeAdminRestorePlan(r.Context(), request)
	if planningErr != nil {
		writeError(w, planningErr.status, planningErr.message)
		return
	}
	if !out.OK {
		writeJSON(w, http.StatusBadRequest, out)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func authoritativeAdminRestorePlan(parent context.Context, request adminRestorePlanRequest) (adminRestorePlanResponse, *adminRestorePlanningError) {
	var public adminRestorePlanResponse
	payload, err := json.Marshal(request)
	if err != nil {
		return public, &adminRestorePlanningError{status: http.StatusInternalServerError, message: "restore planning request could not be prepared"}
	}

	ctx, cancel := context.WithTimeout(parent, adminRestorePlanTimeout)
	defer cancel()
	output, err := runAdminRestorePlanHelper(ctx, payload)
	if err != nil {
		return public, &adminRestorePlanningError{status: http.StatusServiceUnavailable, message: "restore planning is unavailable"}
	}

	var helper adminRestorePlanHelperResponse
	if err := decodeAdminRestorePlanHelperResponse(output, &helper); err != nil {
		return public, &adminRestorePlanningError{status: http.StatusInternalServerError, message: "restore planner returned invalid data"}
	}
	if helper.SchemaVersion != "v1" {
		return public, &adminRestorePlanningError{status: http.StatusInternalServerError, message: "restore planner returned an unsupported schema"}
	}
	if helper.Warnings == nil {
		helper.Warnings = []adminRestorePlanWarning{}
	}
	public = adminRestorePlanResponse{
		OK: helper.OK, SchemaVersion: helper.SchemaVersion, Code: helper.Code, Error: boundedRestorePlanError(helper.Error),
		Normalized: helper.Normalized, Warnings: helper.Warnings, Requirements: helper.Requirements,
	}
	if !helper.OK {
		if helper.Code == "" || helper.Error == "" || helper.Context != nil {
			return adminRestorePlanResponse{}, &adminRestorePlanningError{status: http.StatusInternalServerError, message: "restore planner returned incomplete rejection data"}
		}
		return public, nil
	}
	if err := validateSuccessfulAdminRestorePlan(request, &helper); err != nil {
		return adminRestorePlanResponse{}, &adminRestorePlanningError{status: http.StatusInternalServerError, message: "restore planner returned incomplete data"}
	}
	fingerprint, err := adminRestorePlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		return adminRestorePlanResponse{}, &adminRestorePlanningError{status: http.StatusInternalServerError, message: "restore plan identity could not be created"}
	}
	public.PlanFingerprint = fingerprint
	public.Context = helper.Context
	return public, nil
}

func validateSuccessfulAdminRestorePlan(request adminRestorePlanRequest, helper *adminRestorePlanHelperResponse) error {
	if helper == nil || helper.Normalized == nil || helper.Requirements == nil || helper.Context == nil {
		return errors.New("missing restore plan sections")
	}
	n := helper.Normalized
	r := helper.Requirements
	c := helper.Context
	if n.BackupID != request.BackupID || n.Mode != request.Mode {
		return errors.New("restore plan request mismatch")
	}
	if _, err := time.Parse(time.RFC3339, n.CreatedAt); err != nil {
		return errors.New("invalid restore plan timestamp")
	}
	switch n.MetadataStatus {
	case "valid":
		if n.Metadata == nil {
			return errors.New("valid metadata missing")
		}
	case "missing", "invalid":
		if n.Metadata != nil {
			return errors.New("unexpected metadata")
		}
	default:
		return errors.New("invalid metadata status")
	}
	switch n.VersionRelation {
	case "same", "backup_older", "unknown":
	default:
		return errors.New("unsafe or invalid version relation in successful plan")
	}
	if n.Validation.ArchiveIntegrity != "pending_apply" || n.Validation.ArchiveSafety != "pending_apply" || n.Validation.StagingSpace != "pending_apply" {
		return errors.New("restore plan validation contract changed")
	}
	if !r.DestructiveConfirmationRequired || !r.ArchiveIntegrityValidationOnApply || !r.ArchiveSafetyValidationOnApply || !r.StagingSpaceValidationOnApply {
		return errors.New("restore apply safety requirements missing")
	}
	if r.MinecraftState != "running" && r.MinecraftState != "stopped" {
		return errors.New("invalid Minecraft state")
	}
	if r.Online < 0 || r.Players == nil || (r.PlayersConfirmationRequired && r.Online == 0) {
		return errors.New("invalid player safety state")
	}
	if c.ArchiveIdentity == "" || !strings.HasPrefix(c.DataPath, "/") || !strings.HasPrefix(c.BackupPath, "/") || c.MinecraftUID == "" || c.MinecraftGID == "" {
		return errors.New("restore fingerprint context incomplete")
	}
	return nil
}

func adminRestorePlanFingerprint(schemaVersion string, normalized *adminRestorePlanNormalized, requirements *adminRestorePlanRequirements, context *adminRestorePlanContext) (string, error) {
	if schemaVersion == "" || normalized == nil || requirements == nil || context == nil {
		return "", errors.New("incomplete restore plan")
	}
	payload, err := json.Marshal(adminRestoreFingerprintPayload{
		SchemaVersion: schemaVersion,
		Normalized: *normalized,
		Requirements: *requirements,
		Context: *context,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func decodeAdminRestorePlanRequest(w http.ResponseWriter, r *http.Request, target *adminRestorePlanRequest) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, adminRestorePlanRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return false
	}
	return true
}

func decodeAdminRestorePlanHelperResponse(output []byte, target *adminRestorePlanHelperResponse) error {
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func boundedRestorePlanError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > 512 || strings.ContainsAny(value, "\r\n") {
		return "restore plan was rejected"
	}
	return value
}
