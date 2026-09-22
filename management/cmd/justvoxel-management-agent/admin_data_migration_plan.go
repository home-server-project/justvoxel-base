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
	adminDataMigrationPlanHelper       = "/usr/libexec/justvoxel/mjust/admin-data-migration-plan-json"
	adminDataMigrationPlanRequestLimit = 8 * 1024
)

var adminDataMigrationPlanTimeout = 20 * time.Second

var runAdminDataMigrationPlanHelper = func(ctx context.Context, action string, request []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, adminDataMigrationPlanHelper, action)
	if len(request) > 0 {
		cmd.Stdin = bytes.NewReader(request)
	}
	return cmd.CombinedOutput()
}

type adminDataMigrationRequest struct {
	Operation  string `json:"operation"`
	Device     string `json:"device"`
	MountPoint string `json:"mount_point"`
	Path       string `json:"path"`
	SizeGiB    string `json:"size_gib,omitempty"`
}

type adminDataMigrationCandidate struct {
	Path       string `json:"path"`
	Parent     string `json:"parent,omitempty"`
	Model      string `json:"model,omitempty"`
	Transport  string `json:"transport,omitempty"`
	Filesystem string `json:"filesystem,omitempty"`
	Mountpoint string `json:"mountpoint,omitempty"`
	SizeBytes  uint64 `json:"size_bytes"`
	SystemDisk bool   `json:"system_disk,omitempty"`
}

type adminDataMigrationWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type adminDataMigrationDiscovery struct {
	OK              bool                          `json:"ok"`
	SchemaVersion   string                        `json:"schema_version"`
	Code            string                        `json:"code,omitempty"`
	Error           string                        `json:"error,omitempty"`
	Warnings        []adminDataMigrationWarning   `json:"warnings"`
	WholeDisks      []adminDataMigrationCandidate `json:"whole_disks"`
	Partitions      []adminDataMigrationCandidate `json:"partitions"`
	BlankPartitions []adminDataMigrationCandidate `json:"blank_partitions"`
	FreeSpaceDisks  []adminDataMigrationCandidate `json:"free_space_disks"`
}

type adminDataMigrationNormalized struct {
	Operation           string `json:"operation"`
	Device              string `json:"device"`
	Model               string `json:"model,omitempty"`
	Transport           string `json:"transport,omitempty"`
	Filesystem          string `json:"filesystem,omitempty"`
	MountPoint          string `json:"mount_point"`
	Path                string `json:"path"`
	SizeGiB             string `json:"size_gib,omitempty"`
	SizeBytes           uint64 `json:"size_bytes"`
	DataBytes           uint64 `json:"data_bytes"`
	TargetCapacityBytes uint64 `json:"target_capacity_bytes"`
	CurrentDataPath     string `json:"current_data_path"`
	FreeStart           string `json:"free_start,omitempty"`
	FreeEnd             string `json:"free_end,omitempty"`
	PlannedEnd          string `json:"planned_end,omitempty"`
	FreeMiB             uint64 `json:"free_mib,omitempty"`
	PlannedMiB          uint64 `json:"planned_mib,omitempty"`
}

type adminDataMigrationRequirements struct {
	MigrationConfirmationRequired   bool     `json:"migration_confirmation_required"`
	DestructiveConfirmationRequired bool     `json:"destructive_confirmation_required"`
	ConfirmationPhrase              string   `json:"confirmation_phrase"`
	PlayersConfirmationRequired     bool     `json:"players_confirmation_required"`
	MinecraftState                  string   `json:"minecraft_state"`
	Online                          int      `json:"online"`
	Players                         []string `json:"players"`
	ExactSpaceValidationOnApply     bool     `json:"exact_space_validation_on_apply"`
	ColdBackupRequired              bool     `json:"cold_backup_required"`
	CopyVerificationRequired        bool     `json:"copy_verification_required"`
	RuntimeValidationRequired       bool     `json:"runtime_validation_required"`
}

type adminDataMigrationContext struct {
	ConfigIdentity        string `json:"config_identity"`
	TargetIdentity        string `json:"target_identity"`
	CurrentDataIdentity   string `json:"current_data_identity"`
	CurrentDataPath       string `json:"current_data_path"`
	CurrentMountPoint     string `json:"current_mount_point"`
	CurrentExpectedUUID   string `json:"current_expected_uuid"`
	CurrentExpectedSource string `json:"current_expected_source"`
	MinecraftUID          string `json:"minecraft_uid"`
	MinecraftGID          string `json:"minecraft_gid"`
	BackupPath            string `json:"backup_path"`
	BackupMountPoint      string `json:"backup_mount_point"`
	BackupExpectedUUID    string `json:"backup_expected_uuid"`
	BackupExpectedSource  string `json:"backup_expected_source"`
}

type adminDataMigrationHelperResponse struct {
	OK            bool                            `json:"ok"`
	SchemaVersion string                          `json:"schema_version"`
	Code          string                          `json:"code,omitempty"`
	Error         string                          `json:"error,omitempty"`
	Normalized    *adminDataMigrationNormalized   `json:"normalized,omitempty"`
	Warnings      []adminDataMigrationWarning     `json:"warnings"`
	Requirements  *adminDataMigrationRequirements `json:"requirements,omitempty"`
	Context       *adminDataMigrationContext      `json:"context,omitempty"`
}

type adminDataMigrationPlanResponse struct {
	OK              bool                            `json:"ok"`
	SchemaVersion   string                          `json:"schema_version"`
	PlanFingerprint string                          `json:"plan_fingerprint,omitempty"`
	Code            string                          `json:"code,omitempty"`
	Error           string                          `json:"error,omitempty"`
	Normalized      *adminDataMigrationNormalized   `json:"normalized,omitempty"`
	Warnings        []adminDataMigrationWarning     `json:"warnings"`
	Requirements    *adminDataMigrationRequirements `json:"requirements,omitempty"`
	Context         *adminDataMigrationContext      `json:"-"`
}

type adminDataMigrationFingerprintPayload struct {
	SchemaVersion string                         `json:"schema_version"`
	Normalized    adminDataMigrationNormalized   `json:"normalized"`
	Requirements  adminDataMigrationRequirements `json:"requirements"`
	Context       adminDataMigrationContext      `json:"context"`
}

type adminDataMigrationPlanningError struct {
	status  int
	message string
}

func (e *adminDataMigrationPlanningError) Error() string {
	return e.message
}

func (s *server) adminDataMigrationDiscover(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), adminDataMigrationPlanTimeout)
	defer cancel()

	output, err := runAdminDataMigrationPlanHelper(ctx, "discover", nil)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Minecraft data migration discovery is unavailable")
		return
	}

	var out adminDataMigrationDiscovery
	if err := decodeAdminDataMigrationJSON(output, &out); err != nil || out.SchemaVersion != "v1" {
		writeError(w, http.StatusInternalServerError, "Minecraft data migration discovery returned invalid data")
		return
	}
	if out.Warnings == nil {
		out.Warnings = []adminDataMigrationWarning{}
	}
	if out.WholeDisks == nil {
		out.WholeDisks = []adminDataMigrationCandidate{}
	}
	if out.Partitions == nil {
		out.Partitions = []adminDataMigrationCandidate{}
	}
	if out.BlankPartitions == nil {
		out.BlankPartitions = []adminDataMigrationCandidate{}
	}
	if out.FreeSpaceDisks == nil {
		out.FreeSpaceDisks = []adminDataMigrationCandidate{}
	}
	if !out.OK {
		writeJSON(w, http.StatusBadRequest, out)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) adminDataMigrationPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}

	var request adminDataMigrationRequest
	if !decodeAdminDataMigrationRequest(w, r, &request) {
		return
	}

	out, planningErr := authoritativeAdminDataMigrationPlan(r.Context(), request)
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

func authoritativeAdminDataMigrationPlan(parent context.Context, request adminDataMigrationRequest) (adminDataMigrationPlanResponse, *adminDataMigrationPlanningError) {
	var public adminDataMigrationPlanResponse
	if !validAdminDataMigrationRequest(request) {
		return public, &adminDataMigrationPlanningError{status: http.StatusBadRequest, message: "invalid Minecraft data migration request"}
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return public, &adminDataMigrationPlanningError{status: http.StatusInternalServerError, message: "Minecraft data migration request could not be prepared"}
	}

	ctx, cancel := context.WithTimeout(parent, adminDataMigrationPlanTimeout)
	defer cancel()
	output, err := runAdminDataMigrationPlanHelper(ctx, "plan", payload)
	if err != nil {
		return public, &adminDataMigrationPlanningError{status: http.StatusServiceUnavailable, message: "Minecraft data migration planning is unavailable"}
	}

	var helper adminDataMigrationHelperResponse
	if err := decodeAdminDataMigrationJSON(output, &helper); err != nil {
		return public, &adminDataMigrationPlanningError{status: http.StatusInternalServerError, message: "Minecraft data migration planner returned invalid data"}
	}
	if helper.SchemaVersion != "v1" {
		return public, &adminDataMigrationPlanningError{status: http.StatusInternalServerError, message: "Minecraft data migration planner returned an unsupported schema"}
	}
	if helper.Warnings == nil {
		helper.Warnings = []adminDataMigrationWarning{}
	}

	public = adminDataMigrationPlanResponse{
		OK:            helper.OK,
		SchemaVersion: helper.SchemaVersion,
		Code:          helper.Code,
		Error:         boundedDataMigrationError(helper.Error),
		Normalized:    helper.Normalized,
		Warnings:      helper.Warnings,
		Requirements:  helper.Requirements,
	}
	if !helper.OK {
		if helper.Code == "" || helper.Error == "" || helper.Context != nil {
			return adminDataMigrationPlanResponse{}, &adminDataMigrationPlanningError{status: http.StatusInternalServerError, message: "Minecraft data migration planner returned incomplete rejection data"}
		}
		return public, nil
	}

	if err := validateSuccessfulAdminDataMigrationPlan(request, &helper); err != nil {
		return adminDataMigrationPlanResponse{}, &adminDataMigrationPlanningError{status: http.StatusInternalServerError, message: "Minecraft data migration planner returned incomplete data"}
	}

	fingerprint, err := adminDataMigrationPlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		return adminDataMigrationPlanResponse{}, &adminDataMigrationPlanningError{status: http.StatusInternalServerError, message: "Minecraft data migration plan identity could not be created"}
	}
	public.PlanFingerprint = fingerprint
	public.Context = helper.Context
	return public, nil
}

func validateSuccessfulAdminDataMigrationPlan(request adminDataMigrationRequest, helper *adminDataMigrationHelperResponse) error {
	if helper == nil || helper.Normalized == nil || helper.Requirements == nil || helper.Context == nil {
		return errors.New("missing data migration plan sections")
	}

	n := helper.Normalized
	req := helper.Requirements
	ctx := helper.Context
	if n.Operation != request.Operation ||
		n.Device != request.Device ||
		n.MountPoint != request.MountPoint ||
		n.Path != request.Path {
		return errors.New("data migration plan request mismatch")
	}
	if request.SizeGiB != "" && n.SizeGiB != request.SizeGiB {
		return errors.New("data migration size request mismatch")
	}
	if !strings.HasPrefix(n.Device, "/dev/") ||
		!strings.HasPrefix(n.MountPoint, "/") ||
		!strings.HasPrefix(n.Path, "/") ||
		!strings.HasPrefix(n.CurrentDataPath, "/") {
		return errors.New("invalid data migration paths")
	}

	switch n.Operation {
	case "erase_disk", "use_partition", "format_partition", "create_partition":
	default:
		return errors.New("invalid data migration operation")
	}

	if !req.MigrationConfirmationRequired ||
		!req.ExactSpaceValidationOnApply ||
		!req.ColdBackupRequired ||
		!req.CopyVerificationRequired ||
		!req.RuntimeValidationRequired {
		return errors.New("data migration safety requirements missing")
	}
	if req.DestructiveConfirmationRequired && req.ConfirmationPhrase == "" {
		return errors.New("destructive confirmation phrase missing")
	}
	if !req.DestructiveConfirmationRequired && req.ConfirmationPhrase != "" {
		return errors.New("unexpected destructive confirmation phrase")
	}
	if req.MinecraftState != "running" && req.MinecraftState != "stopped" {
		return errors.New("invalid Minecraft state")
	}
	if req.Online < 0 || req.Players == nil || (req.PlayersConfirmationRequired && req.Online == 0) {
		return errors.New("invalid player safety state")
	}
	if ctx.ConfigIdentity == "" ||
		ctx.TargetIdentity == "" ||
		ctx.CurrentDataIdentity == "" ||
		!strings.HasPrefix(ctx.CurrentDataPath, "/") ||
		ctx.MinecraftUID == "" ||
		ctx.MinecraftGID == "" {
		return errors.New("data migration fingerprint context incomplete")
	}
	return nil
}

func adminDataMigrationPlanFingerprint(schemaVersion string, normalized *adminDataMigrationNormalized, requirements *adminDataMigrationRequirements, context *adminDataMigrationContext) (string, error) {
	if schemaVersion == "" || normalized == nil || requirements == nil || context == nil {
		return "", errors.New("incomplete data migration plan")
	}
	payload, err := json.Marshal(adminDataMigrationFingerprintPayload{
		SchemaVersion: schemaVersion,
		Normalized:    *normalized,
		Requirements:  *requirements,
		Context:       *context,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func decodeAdminDataMigrationRequest(w http.ResponseWriter, r *http.Request, target *adminDataMigrationRequest) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, adminDataMigrationPlanRequestLimit))
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
	if target.SizeGiB == "" {
		target.SizeGiB = "all"
	}
	if !validAdminDataMigrationRequest(*target) {
		writeError(w, http.StatusBadRequest, "invalid Minecraft data migration request")
		return false
	}
	return true
}

func validAdminDataMigrationRequest(request adminDataMigrationRequest) bool {
	switch request.Operation {
	case "erase_disk", "use_partition", "format_partition", "create_partition":
	default:
		return false
	}
	return strings.HasPrefix(request.Device, "/dev/") &&
		strings.HasPrefix(request.MountPoint, "/") &&
		strings.HasPrefix(request.Path, "/") &&
		request.SizeGiB != ""
}

func decodeAdminDataMigrationJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
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

func boundedDataMigrationError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > 512 || strings.ContainsAny(value, "\r\n") {
		return "Minecraft data migration plan was rejected"
	}
	return value
}
