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
	"regexp"
	"strings"
	"time"
)

const (
	adminMigrationExportPlanHelper       = "/usr/libexec/justvoxel/mjust/admin-migration-export-plan-json"
	adminMigrationExportPlanRequestLimit = 12 * 1024
)

var (
	adminMigrationExportPlanTimeout = 30 * time.Second
	migrationExportFilenamePattern  = regexp.MustCompile(`^justvoxel-migration-[A-Za-z0-9._-]+[.]tar[.]gz$`)
)

var runAdminMigrationExportPlanHelper = func(ctx context.Context, action string, request []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, adminMigrationExportPlanHelper, action)
	if len(request) > 0 {
		cmd.Stdin = bytes.NewReader(request)
	}
	return cmd.CombinedOutput()
}

type adminMigrationExportTargetRequest struct {
	Kind      string `json:"kind"`
	Path      string `json:"path,omitempty"`
	Device    string `json:"device,omitempty"`
	Removable bool   `json:"removable,omitempty"`
	Source    string `json:"source,omitempty"`
	Username  string `json:"username,omitempty"`
	Domain    string `json:"domain,omitempty"`
	Filename  string `json:"filename"`
}

type adminMigrationExportWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type adminMigrationExportDevice struct {
	Path       string `json:"path"`
	Parent     string `json:"parent,omitempty"`
	Filesystem string `json:"filesystem"`
	Mountpoint string `json:"mountpoint,omitempty"`
	Model      string `json:"model,omitempty"`
	Transport  string `json:"transport,omitempty"`
	SizeBytes  uint64 `json:"size_bytes"`
	Removable  bool   `json:"removable"`
}

type adminMigrationExportDiscoveryResponse struct {
	OK                        bool                         `json:"ok"`
	SchemaVersion             string                       `json:"schema_version"`
	Code                      string                       `json:"code,omitempty"`
	Error                     string                       `json:"error,omitempty"`
	SuggestedFilename         string                       `json:"suggested_filename"`
	ConfiguredBackupAvailable bool                         `json:"configured_backup_available"`
	Devices                   []adminMigrationExportDevice `json:"devices"`
	TargetKinds               []string                     `json:"target_kinds"`
}

type adminMigrationExportNormalized struct {
	Kind                 string `json:"kind"`
	Path                 string `json:"path"`
	Device               string `json:"device"`
	Removable            bool   `json:"removable"`
	Source               string `json:"source"`
	Username             string `json:"username"`
	Domain               string `json:"domain"`
	Filename             string `json:"filename"`
	TargetDisplay        string `json:"target_display"`
	TargetFilesystem     string `json:"target_filesystem"`
	DataPath             string `json:"data_path"`
	DataBytes            uint64 `json:"data_bytes"`
	TargetAvailableBytes uint64 `json:"target_available_bytes"`
}

type adminMigrationExportRequirements struct {
	ExportConfirmationRequired  bool     `json:"export_confirmation_required"`
	PlayersConfirmationRequired bool     `json:"players_confirmation_required"`
	MinecraftState              string   `json:"minecraft_state"`
	Online                      int      `json:"online"`
	Players                     []string `json:"players"`
	SMBPasswordRequired         bool     `json:"smb_password_required"`
	TargetValidationOnApply     bool     `json:"target_validation_on_apply"`
	IntegrityValidationRequired bool     `json:"integrity_validation_required"`
	RuntimeValidationRequired   bool     `json:"runtime_validation_required"`
}

type adminMigrationExportContext struct {
	ConfigIdentity string `json:"config_identity"`
	DataIdentity   string `json:"data_identity"`
	TargetIdentity string `json:"target_identity"`
}

type adminMigrationExportHelperResponse struct {
	OK            bool                              `json:"ok"`
	SchemaVersion string                            `json:"schema_version"`
	Code          string                            `json:"code,omitempty"`
	Error         string                            `json:"error,omitempty"`
	Normalized    *adminMigrationExportNormalized   `json:"normalized,omitempty"`
	Warnings      []adminMigrationExportWarning     `json:"warnings"`
	Requirements  *adminMigrationExportRequirements `json:"requirements,omitempty"`
	Context       *adminMigrationExportContext      `json:"context,omitempty"`
}

type adminMigrationExportPlanResponse struct {
	OK              bool                              `json:"ok"`
	SchemaVersion   string                            `json:"schema_version"`
	PlanFingerprint string                            `json:"plan_fingerprint,omitempty"`
	Code            string                            `json:"code,omitempty"`
	Error           string                            `json:"error,omitempty"`
	Normalized      *adminMigrationExportNormalized   `json:"normalized,omitempty"`
	Warnings        []adminMigrationExportWarning     `json:"warnings"`
	Requirements    *adminMigrationExportRequirements `json:"requirements,omitempty"`
	Context         *adminMigrationExportContext      `json:"-"`
}

type adminMigrationExportFingerprintIntent struct {
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	Device        string `json:"device"`
	Removable     bool   `json:"removable"`
	Source        string `json:"source"`
	Username      string `json:"username"`
	Domain        string `json:"domain"`
	Filename      string `json:"filename"`
	TargetDisplay string `json:"target_display"`
	DataPath      string `json:"data_path"`
}

type adminMigrationExportFingerprintSafety struct {
	ExportConfirmationRequired  bool `json:"export_confirmation_required"`
	SMBPasswordRequired         bool `json:"smb_password_required"`
	TargetValidationOnApply     bool `json:"target_validation_on_apply"`
	IntegrityValidationRequired bool `json:"integrity_validation_required"`
	RuntimeValidationRequired   bool `json:"runtime_validation_required"`
}

type adminMigrationExportFingerprintPayload struct {
	SchemaVersion string                                `json:"schema_version"`
	Intent        adminMigrationExportFingerprintIntent `json:"intent"`
	Safety        adminMigrationExportFingerprintSafety `json:"safety"`
	Context       adminMigrationExportContext           `json:"context"`
}

type adminMigrationExportPlanningError struct {
	status  int
	message string
}

func (s *server) adminMigrationExportDiscover(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), adminMigrationExportPlanTimeout)
	defer cancel()

	output, err := runAdminMigrationExportPlanHelper(ctx, "discover", nil)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "server migration export discovery is unavailable")
		return
	}
	var response adminMigrationExportDiscoveryResponse
	if err := decodeAdminMigrationExportJSON(output, &response); err != nil {
		writeError(w, http.StatusInternalServerError, "server migration export discovery returned invalid data")
		return
	}
	if !response.OK {
		writeJSON(w, http.StatusBadRequest, response)
		return
	}
	if response.SchemaVersion != "v1" ||
		!migrationExportFilenamePattern.MatchString(response.SuggestedFilename) ||
		response.Devices == nil ||
		response.TargetKinds == nil {
		writeError(w, http.StatusInternalServerError, "server migration export discovery contract changed")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) adminMigrationExportPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	var request adminMigrationExportTargetRequest
	if !decodeAdminMigrationExportRequest(w, r, &request) {
		return
	}
	plan, planningErr := authoritativeAdminMigrationExportPlan(r.Context(), request)
	if planningErr != nil {
		writeError(w, planningErr.status, planningErr.message)
		return
	}
	status := http.StatusOK
	if !plan.OK {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, plan)
}

func authoritativeAdminMigrationExportPlan(parent context.Context, request adminMigrationExportTargetRequest) (adminMigrationExportPlanResponse, *adminMigrationExportPlanningError) {
	if !validAdminMigrationExportTargetRequest(request) {
		return adminMigrationExportPlanResponse{}, &adminMigrationExportPlanningError{status: http.StatusBadRequest, message: "invalid server migration export request"}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return adminMigrationExportPlanResponse{}, &adminMigrationExportPlanningError{status: http.StatusInternalServerError, message: "server migration export request could not be encoded"}
	}

	ctx, cancel := context.WithTimeout(parent, adminMigrationExportPlanTimeout)
	defer cancel()
	output, err := runAdminMigrationExportPlanHelper(ctx, "plan", payload)
	if err != nil {
		return adminMigrationExportPlanResponse{}, &adminMigrationExportPlanningError{status: http.StatusServiceUnavailable, message: "server migration export planning is unavailable"}
	}

	var helper adminMigrationExportHelperResponse
	if err := decodeAdminMigrationExportJSON(output, &helper); err != nil {
		return adminMigrationExportPlanResponse{}, &adminMigrationExportPlanningError{status: http.StatusInternalServerError, message: "server migration export planning returned invalid data"}
	}
	public := adminMigrationExportPlanResponse{
		OK:            helper.OK,
		SchemaVersion: helper.SchemaVersion,
		Code:          helper.Code,
		Error:         boundedMigrationExportError(helper.Error),
		Normalized:    helper.Normalized,
		Warnings:      helper.Warnings,
		Requirements:  helper.Requirements,
	}
	if public.Warnings == nil {
		public.Warnings = []adminMigrationExportWarning{}
	}
	if !helper.OK {
		if helper.SchemaVersion != "v1" || public.Error == "" {
			return adminMigrationExportPlanResponse{}, &adminMigrationExportPlanningError{status: http.StatusInternalServerError, message: "server migration export rejection contract changed"}
		}
		return public, nil
	}
	if err := validateSuccessfulAdminMigrationExportPlan(request, &helper); err != nil {
		return adminMigrationExportPlanResponse{}, &adminMigrationExportPlanningError{status: http.StatusInternalServerError, message: "server migration export planning contract changed"}
	}
	fingerprint, err := adminMigrationExportPlanFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
	if err != nil {
		return adminMigrationExportPlanResponse{}, &adminMigrationExportPlanningError{status: http.StatusInternalServerError, message: "server migration export plan identity could not be created"}
	}
	public.PlanFingerprint = fingerprint
	public.Context = helper.Context
	return public, nil
}

func validateSuccessfulAdminMigrationExportPlan(request adminMigrationExportTargetRequest, helper *adminMigrationExportHelperResponse) error {
	if helper == nil || helper.SchemaVersion != "v1" || helper.Normalized == nil || helper.Requirements == nil || helper.Context == nil {
		return errors.New("missing export plan sections")
	}
	n, req, ctx := helper.Normalized, helper.Requirements, helper.Context
	if n.Kind != request.Kind ||
		n.Filename != request.Filename ||
		n.Device != request.Device ||
		n.Removable != request.Removable ||
		n.Source != request.Source ||
		n.Username != request.Username ||
		n.Domain != request.Domain {
		return errors.New("export plan request mismatch")
	}
	switch request.Kind {
	case "local":
		if n.Path != request.Path {
			return errors.New("local export path mismatch")
		}
	case "backup":
		if !strings.HasPrefix(n.Path, "/") {
			return errors.New("configured backup export path is invalid")
		}
	default:
		if n.Path != "" {
			return errors.New("unexpected export path")
		}
	}
	if !req.ExportConfirmationRequired ||
		!req.TargetValidationOnApply ||
		!req.IntegrityValidationRequired ||
		!req.RuntimeValidationRequired {
		return errors.New("export safety requirements missing")
	}
	if req.MinecraftState != "running" && req.MinecraftState != "stopped" {
		return errors.New("invalid Minecraft state")
	}
	if req.Online < 0 || req.Players == nil || (req.PlayersConfirmationRequired && req.Online == 0) {
		return errors.New("invalid player safety state")
	}
	if req.SMBPasswordRequired != (request.Kind == "smb") {
		return errors.New("invalid SMB password requirement")
	}
	if !strings.HasPrefix(n.DataPath, "/") ||
		n.DataBytes == 0 ||
		n.TargetDisplay == "" ||
		ctx.ConfigIdentity == "" ||
		ctx.DataIdentity == "" ||
		ctx.TargetIdentity == "" {
		return errors.New("export plan context incomplete")
	}
	return nil
}

func adminMigrationExportPlanFingerprint(schemaVersion string, normalized *adminMigrationExportNormalized, requirements *adminMigrationExportRequirements, context *adminMigrationExportContext) (string, error) {
	if schemaVersion == "" || normalized == nil || requirements == nil || context == nil {
		return "", errors.New("incomplete export plan")
	}
	payload, err := json.Marshal(adminMigrationExportFingerprintPayload{
		SchemaVersion: schemaVersion,
		Intent: adminMigrationExportFingerprintIntent{
			Kind:          normalized.Kind,
			Path:          normalized.Path,
			Device:        normalized.Device,
			Removable:     normalized.Removable,
			Source:        normalized.Source,
			Username:      normalized.Username,
			Domain:        normalized.Domain,
			Filename:      normalized.Filename,
			TargetDisplay: normalized.TargetDisplay,
			DataPath:      normalized.DataPath,
		},
		Safety: adminMigrationExportFingerprintSafety{
			ExportConfirmationRequired:  requirements.ExportConfirmationRequired,
			SMBPasswordRequired:         requirements.SMBPasswordRequired,
			TargetValidationOnApply:     requirements.TargetValidationOnApply,
			IntegrityValidationRequired: requirements.IntegrityValidationRequired,
			RuntimeValidationRequired:   requirements.RuntimeValidationRequired,
		},
		Context: *context,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validAdminMigrationExportTargetRequest(request adminMigrationExportTargetRequest) bool {
	if !migrationExportFilenamePattern.MatchString(request.Filename) || len(request.Filename) > 160 {
		return false
	}
	if strings.ContainsAny(request.Path+request.Device+request.Source+request.Username+request.Domain+request.Filename, "\r\n") {
		return false
	}
	switch request.Kind {
	case "local":
		return strings.HasPrefix(request.Path, "/") &&
			request.Device == "" && request.Source == "" && request.Username == "" && request.Domain == ""
	case "backup":
		return request.Path == "" && request.Device == "" && request.Source == "" && request.Username == "" && request.Domain == ""
	case "device":
		return strings.HasPrefix(request.Device, "/dev/") &&
			request.Path == "" && request.Source == "" && request.Username == "" && request.Domain == ""
	case "nfs":
		return request.Path == "" && request.Device == "" && request.Source != "" && request.Username == "" && request.Domain == ""
	case "smb":
		return request.Path == "" && request.Device == "" && request.Source != "" && request.Username != ""
	default:
		return false
	}
}

func decodeAdminMigrationExportRequest(w http.ResponseWriter, r *http.Request, target *adminMigrationExportTargetRequest) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, adminMigrationExportPlanRequestLimit))
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
	if !validAdminMigrationExportTargetRequest(*target) {
		writeError(w, http.StatusBadRequest, "invalid server migration export request")
		return false
	}
	return true
}

func decodeAdminMigrationExportJSON(output []byte, target any) error {
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

func boundedMigrationExportError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > 512 || strings.ContainsAny(value, "\r\n") {
		return "server migration export plan was rejected"
	}
	return value
}
