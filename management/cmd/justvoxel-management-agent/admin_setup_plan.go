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

const adminSetupPlanHelper = "/usr/libexec/justvoxel/mjust/admin-setup-plan-json"

var adminSetupPlanTimeout = 25 * time.Second

var runAdminSetupPlanHelper = func(ctx context.Context, request []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, adminSetupPlanHelper)
	cmd.Stdin = bytes.NewReader(request)
	return cmd.CombinedOutput()
}

type adminSetupPlanServerRequest struct {
	DisplayName    string `json:"display_name,omitempty"`
	MOTD           string `json:"motd"`
	MOTDAutomatic  bool   `json:"motd_automatic,omitempty"`
	MaxPlayers     int    `json:"max_players"`
	BedrockEnabled bool   `json:"bedrock_enabled"`
	Timezone       string `json:"timezone,omitempty"`
}

type adminSetupPlanMinecraftRequest struct {
	ServerType      string `json:"server_type,omitempty"`
	LoaderVersion   string `json:"loader_version,omitempty"`
	GameMode        string `json:"game_mode"`
	JavaMemory      string `json:"java_memory"`
	ContainerMemory string `json:"container_memory"`
	JavaPort        int    `json:"java_port"`
	BedrockPort     int    `json:"bedrock_port"`
	BedrockTCPPort  int    `json:"bedrock_tcp_port,omitempty"`
	BedrockUDPStart int    `json:"bedrock_udp_start,omitempty"`
	BedrockUDPEnd   int    `json:"bedrock_udp_end,omitempty"`
	ImageTag        string `json:"image_tag"`
	VersionPolicy   string `json:"version_policy"`
	Version         string `json:"version"`
}

type adminSetupPlanStorageRequest struct {
	Type       string `json:"type"`
	Path       string `json:"path"`
	Device     string `json:"device"`
	MountPoint string `json:"mount_point"`
}

type adminSetupPlanBackupsRequest struct {
	Automatic  bool   `json:"automatic"`
	DailyTime  string `json:"daily_time"`
	Keep       int    `json:"keep"`
	Type       string `json:"type"`
	Path       string `json:"path"`
	Device     string `json:"device"`
	MountPoint string `json:"mount_point"`
	Source     string `json:"source"`
	Username   string `json:"username"`
	Domain     string `json:"domain"`
}

type adminSetupPlanRequest struct {
	AddServer           bool                           `json:"add_server,omitempty"`
	StartAfterSetup     bool                           `json:"start_after_setup,omitempty"`
	StartOnBoot         bool                           `json:"start_on_boot,omitempty"`
	MountedOnly         bool                           `json:"mounted_only,omitempty"`
	DiagnosticSessionID string                         `json:"diagnostic_session_id,omitempty"`
	Server              adminSetupPlanServerRequest    `json:"server"`
	Minecraft           adminSetupPlanMinecraftRequest `json:"minecraft"`
	Storage             adminSetupPlanStorageRequest   `json:"storage"`
	Backups             adminSetupPlanBackupsRequest   `json:"backups"`
}

type adminSetupPlanWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type adminSetupPlanServer struct {
	DisplayName    string `json:"display_name,omitempty"`
	MOTD           string `json:"motd"`
	MaxPlayers     int    `json:"max_players"`
	BedrockEnabled bool   `json:"bedrock_enabled"`
	Timezone       string `json:"timezone,omitempty"`
}

type adminSetupPlanMinecraft struct {
	ServerType             string `json:"server_type"`
	LoaderVersion          string `json:"loader_version,omitempty"`
	GameMode               string `json:"game_mode"`
	JavaMemory             string `json:"java_memory"`
	ContainerMemory        string `json:"container_memory"`
	JavaPort               int    `json:"java_port"`
	BedrockPort            int    `json:"bedrock_port"`
	BedrockTCPPort         int    `json:"bedrock_tcp_port,omitempty"`
	BedrockUDPStart        int    `json:"bedrock_udp_start,omitempty"`
	BedrockUDPEnd          int    `json:"bedrock_udp_end,omitempty"`
	ImageTag               string `json:"image_tag"`
	RequestedVersionPolicy string `json:"requested_version_policy"`
	VersionPolicy          string `json:"version_policy"`
	Version                string `json:"version"`
	SystemMemoryMiB        int    `json:"system_memory_mib"`
	SystemReserveMiB       int    `json:"system_reserve_mib"`
	MinecraftUID           uint32 `json:"minecraft_uid"`
	MinecraftGID           uint32 `json:"minecraft_gid"`
}

type adminSetupPlanStorage struct {
	Type           string `json:"type"`
	Path           string `json:"path"`
	Device         string `json:"device,omitempty"`
	ParentDisk     string `json:"parent_disk,omitempty"`
	Model          string `json:"model,omitempty"`
	Transport      string `json:"transport,omitempty"`
	SizeBytes      uint64 `json:"size_bytes,omitempty"`
	Filesystem     string `json:"filesystem,omitempty"`
	UUID           string `json:"uuid,omitempty"`
	MountPoint     string `json:"mount_point,omitempty"`
	ExpectedUUID   string `json:"expected_uuid,omitempty"`
	ExpectedSource string `json:"expected_source,omitempty"`
	MountedAt      string `json:"mounted_at,omitempty"`
	Source         string `json:"source,omitempty"`
	SystemDisk     bool   `json:"system_disk"`
	Purpose        string `json:"purpose,omitempty"`
}

type adminSetupPlanBackups struct {
	Type                string `json:"type"`
	Path                string `json:"path"`
	Device              string `json:"device,omitempty"`
	ParentDisk          string `json:"parent_disk,omitempty"`
	Model               string `json:"model,omitempty"`
	Transport           string `json:"transport,omitempty"`
	SizeBytes           uint64 `json:"size_bytes,omitempty"`
	Filesystem          string `json:"filesystem,omitempty"`
	UUID                string `json:"uuid,omitempty"`
	MountPoint          string `json:"mount_point,omitempty"`
	ExpectedUUID        string `json:"expected_uuid,omitempty"`
	ExpectedSource      string `json:"expected_source,omitempty"`
	MountedAt           string `json:"mounted_at,omitempty"`
	Source              string `json:"source,omitempty"`
	SystemDisk          bool   `json:"system_disk"`
	Purpose             string `json:"purpose,omitempty"`
	Username            string `json:"username,omitempty"`
	Domain              string `json:"domain,omitempty"`
	CredentialsRequired bool   `json:"credentials_required"`
	Automatic           bool   `json:"automatic"`
	DailyTime           string `json:"daily_time"`
	Schedule            string `json:"schedule"`
	Keep                int    `json:"keep"`
}

type adminSetupNormalizedPlan struct {
	AddServer       bool                    `json:"add_server,omitempty"`
	StartAfterSetup bool                    `json:"start_after_setup,omitempty"`
	StartOnBoot     bool                    `json:"start_on_boot,omitempty"`
	MountedOnly     bool                    `json:"mounted_only,omitempty"`
	Server          adminSetupPlanServer    `json:"server"`
	Minecraft       adminSetupPlanMinecraft `json:"minecraft"`
	Storage         adminSetupPlanStorage   `json:"storage"`
	Backups         adminSetupPlanBackups   `json:"backups"`
}

type adminSetupPlanRequirements struct {
	SMBPasswordRequired            bool `json:"smb_password_required"`
	NetworkBackupValidationOnApply bool `json:"network_backup_validation_on_apply"`
}

type adminSetupPlanResponse struct {
	OK              bool                        `json:"ok"`
	SchemaVersion   string                      `json:"schema_version"`
	PlanFingerprint string                      `json:"plan_fingerprint,omitempty"`
	Code            string                      `json:"code,omitempty"`
	Error           string                      `json:"error,omitempty"`
	Normalized      *adminSetupNormalizedPlan   `json:"normalized,omitempty"`
	Warnings        []adminSetupPlanWarning     `json:"warnings"`
	Requirements    *adminSetupPlanRequirements `json:"requirements,omitempty"`
}

type adminSetupFingerprintMinecraft struct {
	ServerType             string `json:"server_type"`
	LoaderVersion          string `json:"loader_version,omitempty"`
	GameMode               string `json:"game_mode"`
	JavaMemory             string `json:"java_memory"`
	ContainerMemory        string `json:"container_memory"`
	JavaPort               int    `json:"java_port"`
	BedrockPort            int    `json:"bedrock_port"`
	BedrockTCPPort         int    `json:"bedrock_tcp_port,omitempty"`
	BedrockUDPStart        int    `json:"bedrock_udp_start,omitempty"`
	BedrockUDPEnd          int    `json:"bedrock_udp_end,omitempty"`
	ImageTag               string `json:"image_tag"`
	RequestedVersionPolicy string `json:"requested_version_policy"`
	VersionPolicy          string `json:"version_policy"`
	Version                string `json:"version"`
	MinecraftUID           uint32 `json:"minecraft_uid"`
	MinecraftGID           uint32 `json:"minecraft_gid"`
}

type adminSetupFingerprintStorage struct {
	Type           string `json:"type"`
	Path           string `json:"path"`
	Device         string `json:"device"`
	ParentDisk     string `json:"parent_disk"`
	Filesystem     string `json:"filesystem"`
	UUID           string `json:"uuid"`
	MountPoint     string `json:"mount_point"`
	ExpectedUUID   string `json:"expected_uuid"`
	ExpectedSource string `json:"expected_source"`
	Source         string `json:"source"`
	SystemDisk     bool   `json:"system_disk"`
	Purpose        string `json:"purpose"`
}

type adminSetupFingerprintBackups struct {
	Type                string `json:"type"`
	Path                string `json:"path"`
	Device              string `json:"device"`
	ParentDisk          string `json:"parent_disk"`
	Filesystem          string `json:"filesystem"`
	UUID                string `json:"uuid"`
	MountPoint          string `json:"mount_point"`
	ExpectedUUID        string `json:"expected_uuid"`
	ExpectedSource      string `json:"expected_source"`
	Source              string `json:"source"`
	SystemDisk          bool   `json:"system_disk"`
	Purpose             string `json:"purpose"`
	Username            string `json:"username"`
	Domain              string `json:"domain"`
	CredentialsRequired bool   `json:"credentials_required"`
	Automatic           bool   `json:"automatic"`
	DailyTime           string `json:"daily_time"`
	Schedule            string `json:"schedule"`
	Keep                int    `json:"keep"`
}

type adminSetupFingerprintNormalized struct {
	Server    adminSetupPlanServer           `json:"server"`
	Minecraft adminSetupFingerprintMinecraft `json:"minecraft"`
	Storage   adminSetupFingerprintStorage   `json:"storage"`
	Backups   adminSetupFingerprintBackups   `json:"backups"`
}

type adminSetupFingerprintPayload struct {
	AddServer       bool                            `json:"add_server,omitempty"`
	StartAfterSetup bool                            `json:"start_after_setup,omitempty"`
	StartOnBoot     bool                            `json:"start_on_boot,omitempty"`
	MountedOnly     bool                            `json:"mounted_only,omitempty"`
	SchemaVersion   string                          `json:"schema_version"`
	Normalized      adminSetupFingerprintNormalized `json:"normalized"`
	Requirements    adminSetupPlanRequirements      `json:"requirements"`
}

func registerAdminSetupPlanRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("POST /v1/admin/setup/plan", s.adminSetupPlan)
	mux.HandleFunc("GET /v1/admin/setup/recommended-storage", s.adminRecommendedSetupStorage)
}

type adminSetupPlanningError struct {
	status  int
	message string
	detail  string
}

func (e *adminSetupPlanningError) Error() string { return e.message }

func (s *server) adminSetupPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}

	var request adminSetupPlanRequest
	if !decodeAdminSetupPlanRequest(w, r, &request) {
		return
	}
	diagnosticID := strings.TrimSpace(request.DiagnosticSessionID)
	if s.operations != nil && validOperationID(diagnosticID) {
		_ = s.operations.appendSetupDiagnostic(diagnosticID, "PLAN", "setup planning requested", setupDiagnosticPlanRequestValues(request))
	}
	out, planningErr := authoritativeAdminSetupPlan(r.Context(), request)
	if planningErr != nil {
		if s.operations != nil && validOperationID(diagnosticID) {
			_ = s.operations.appendSetupDiagnostic(diagnosticID, "PLAN", "setup planning unavailable", setupDiagnosticPlanningErrorValues(planningErr))
		}
		writeError(w, planningErr.status, planningErr.message)
		return
	}
	if s.operations != nil && validOperationID(diagnosticID) {
		values := setupDiagnosticNormalizedPlanValues(out.Normalized)
		if values == nil {
			values = map[string]string{}
		}
		values["plan_fingerprint"] = out.PlanFingerprint
		values["result_code"] = out.Code
		values["result_error"] = out.Error
		_ = s.operations.appendSetupDiagnostic(diagnosticID, "PLAN", "authoritative setup plan completed", values)
	}
	if !out.OK {
		writeJSON(w, http.StatusBadRequest, out)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func authoritativeAdminSetupPlan(parent context.Context, request adminSetupPlanRequest) (adminSetupPlanResponse, *adminSetupPlanningError) {
	var out adminSetupPlanResponse
	if request.Minecraft.ServerType == "" {
		request.Minecraft.ServerType = "paper"
	}
	if validateJavaServerSelection(request.Minecraft.ServerType, request.Minecraft.VersionPolicy, request.Minecraft.Version, request.Minecraft.LoaderVersion, request.Minecraft.ImageTag, request.Server.BedrockEnabled) != nil {
		return out, &adminSetupPlanningError{status: http.StatusBadRequest, message: "invalid server software or Bedrock choice"}
	}
	request.Server.Timezone = "" // Legacy wire field is accepted but never selected or persisted.
	if request.Minecraft.ServerType == "bedrock" && request.Minecraft.GameMode == "spectator" {
		return out, &adminSetupPlanningError{status: 400, message: "Bedrock does not support Spectator as an initial game mode"}
	}
	// Friendly names belong to the Go instance registry, not the shell planner.
	displayName, nameErr := validateInstanceName(request.Server.DisplayName)
	if nameErr != nil || (request.Server.DisplayName != "" && displayName == "") {
		return out, &adminSetupPlanningError{status: http.StatusBadRequest, message: "invalid server name"}
	}
	if displayName != "" {
		instances, err := instanceRuntimes.identities().listInstances("")
		if err != nil {
			return out, &adminSetupPlanningError{status: http.StatusServiceUnavailable, message: "server names could not be checked"}
		}
		if instanceNameUsed(instances, "", displayName) {
			return out, &adminSetupPlanningError{status: http.StatusBadRequest, message: "another server already uses this name"}
		}
	}
	helperRequest := request
	helperRequest.Server.DisplayName = ""
	helperRequest.DiagnosticSessionID = ""
	helperRequest.AddServer, helperRequest.StartOnBoot, helperRequest.MountedOnly, helperRequest.StartAfterSetup = false, false, false, false
	// The shell helper keeps its existing strict contract. The agent generates
	// this WebUI default only after the helper resolves the final version.
	helperRequest.Server.MOTDAutomatic = false
	payload, err := json.Marshal(helperRequest)
	if err != nil {
		return out, &adminSetupPlanningError{status: http.StatusInternalServerError, message: "first-run setup request could not be prepared"}
	}

	ctx, cancel := context.WithTimeout(parent, adminSetupPlanTimeout)
	defer cancel()
	output, err := runAdminSetupPlanHelper(ctx, payload)
	if err != nil {
		return out, &adminSetupPlanningError{status: http.StatusServiceUnavailable, message: "first-run setup planning is unavailable", detail: boundedSetupDiagnosticHelperOutput(output)}
	}
	if err := decodeAdminSetupPlanResponse(output, &out); err != nil {
		return out, &adminSetupPlanningError{status: http.StatusInternalServerError, message: "first-run setup planner returned invalid data", detail: boundedSetupDiagnosticHelperOutput(output)}
	}
	if out.SchemaVersion != "v1" {
		return out, &adminSetupPlanningError{status: http.StatusInternalServerError, message: "first-run setup planner returned an unsupported schema"}
	}
	if out.Warnings == nil {
		out.Warnings = []adminSetupPlanWarning{}
	}
	if !out.OK {
		out.Error = boundedSetupPlanError(out.Error)
		return out, nil
	}
	if out.Normalized != nil && out.Normalized.Minecraft.ServerType == "" {
		out.Normalized.Minecraft.ServerType = "paper"
	}
	if out.Normalized == nil || !validSetupServerType(out.Normalized.Minecraft.ServerType) || validateJavaServerSelection(out.Normalized.Minecraft.ServerType, out.Normalized.Minecraft.VersionPolicy, out.Normalized.Minecraft.Version, out.Normalized.Minecraft.LoaderVersion, out.Normalized.Minecraft.ImageTag, out.Normalized.Server.BedrockEnabled) != nil || out.Requirements == nil || out.Normalized.Minecraft.VersionPolicy == "" || !validSetupGameMode(out.Normalized.Minecraft.GameMode) || out.Normalized.Minecraft.MinecraftUID == 0 || out.Normalized.Minecraft.MinecraftGID == 0 || out.Normalized.Storage.Type == "" || out.Normalized.Backups.Type == "" {
		return out, &adminSetupPlanningError{status: http.StatusInternalServerError, message: "first-run setup planner returned incomplete data"}
	}
	if software := request.Minecraft.ServerType; software != "paper" && software != "purpur" && software != "vanilla" {
		selected, resolved := request.Minecraft, out.Normalized.Minecraft
		if resolved.ServerType != selected.ServerType || resolved.VersionPolicy != selected.VersionPolicy || resolved.Version != selected.Version || resolved.LoaderVersion != selected.LoaderVersion || resolved.ImageTag != selected.ImageTag {
			return out, &adminSetupPlanningError{status: http.StatusInternalServerError, message: "setup planner changed an explicit Java runtime selection"}
		}
	}
	if request.Server.MOTDAutomatic {
		version := out.Normalized.Minecraft.Version
		if version == "" || version == "LATEST" || version == "recommended" || version == "latest" || version == "pinned" {
			return out, &adminSetupPlanningError{status: http.StatusInternalServerError, message: "first-run setup planner did not resolve a Minecraft version"}
		}
		capabilities, _ := javaServerSoftware(out.Normalized.Minecraft.ServerType)
		software := capabilities.Name
		if out.Normalized.Minecraft.ServerType == "bedrock" {
			software = "Bedrock"
		}
		out.Normalized.Server.MOTD = "JustVoxel " + software + " Minecraft " + version + " Server"
	}
	out.Normalized.Server.Timezone = ""
	if request.Minecraft.ServerType == "bedrock" {
		out.Normalized.Minecraft.BedrockTCPPort = request.Minecraft.BedrockTCPPort
		out.Normalized.Minecraft.BedrockUDPStart = request.Minecraft.BedrockUDPStart
		out.Normalized.Minecraft.BedrockUDPEnd = request.Minecraft.BedrockUDPEnd
		if err := instanceRuntimes.planNativeBedrock(parent, &out.Normalized.Minecraft, request.Server.MaxPlayers); err != nil {
			return out, &adminSetupPlanningError{status: 409, message: err.Error()}
		}
	}
	out.Normalized.Server.DisplayName = displayName
	out.Normalized.StartAfterSetup = request.AddServer && request.StartAfterSetup
	out.Normalized.AddServer = request.AddServer
	out.Normalized.StartOnBoot = !request.AddServer || request.StartOnBoot
	out.Normalized.MountedOnly = request.MountedOnly
	if request.MountedOnly {
		if err := validateRecommendedPlan(parent, out.Normalized); err != nil {
			return out, &adminSetupPlanningError{status: http.StatusConflict, message: "Recommended storage is no longer available; return to storage selection"}
		}
	}
	if request.AddServer {
		snapshot, _, _, err := instanceRuntimes.snapshot(parent)
		if err != nil {
			return out, &adminSetupPlanningError{status: http.StatusConflict, message: "Minecraft memory capacity could not be verified."}
		}
		records, err := instanceRuntimes.records()
		if err != nil {
			return out, &adminSetupPlanningError{status: http.StatusConflict, message: "Existing server settings could not be verified."}
		}
		candidate := minecraftInstanceRuntimeConfig{ContainerMemory: out.Normalized.Minecraft.ContainerMemory}
		for _, boot := range []bool{false, true} {
			if (!boot && request.StartAfterSetup) || (boot && request.StartOnBoot) {
				if err := instanceRuntimes.admitMemory(parent, snapshot.TotalMemoryMiB, records, candidate, boot); err != nil {
					return out, &adminSetupPlanningError{status: http.StatusConflict, message: err.Error() + " Return to Server to disable the selected startup option, or reduce memory."}
				}
			}
		}
	}
	fingerprint, err := adminSetupPlanFingerprint(out.SchemaVersion, out.Normalized, out.Requirements)
	if err != nil {
		return out, &adminSetupPlanningError{status: http.StatusInternalServerError, message: "first-run setup plan identity could not be created"}
	}
	out.PlanFingerprint = fingerprint
	return out, nil
}

func adminSetupPlanFingerprint(schemaVersion string, normalized *adminSetupNormalizedPlan, requirements *adminSetupPlanRequirements) (string, error) {
	if schemaVersion == "" || normalized == nil || requirements == nil {
		return "", errors.New("incomplete setup plan")
	}
	serverType := normalized.Minecraft.ServerType
	if serverType == "" {
		serverType = "paper"
	}
	if !validSetupServerType(serverType) {
		return "", errors.New("invalid server type")
	}
	canonical := adminSetupFingerprintPayload{
		SchemaVersion: schemaVersion, AddServer: normalized.AddServer, StartOnBoot: normalized.StartOnBoot, StartAfterSetup: normalized.StartAfterSetup, MountedOnly: normalized.MountedOnly,
		Normalized: adminSetupFingerprintNormalized{
			Server: normalized.Server,
			Minecraft: adminSetupFingerprintMinecraft{
				ServerType: serverType, LoaderVersion: normalized.Minecraft.LoaderVersion,
				GameMode:   normalized.Minecraft.GameMode,
				JavaMemory: normalized.Minecraft.JavaMemory, ContainerMemory: normalized.Minecraft.ContainerMemory,
				JavaPort: normalized.Minecraft.JavaPort, BedrockPort: normalized.Minecraft.BedrockPort, BedrockTCPPort: normalized.Minecraft.BedrockTCPPort, BedrockUDPStart: normalized.Minecraft.BedrockUDPStart, BedrockUDPEnd: normalized.Minecraft.BedrockUDPEnd,
				ImageTag: normalized.Minecraft.ImageTag, RequestedVersionPolicy: normalized.Minecraft.RequestedVersionPolicy,
				VersionPolicy: normalized.Minecraft.VersionPolicy, Version: normalized.Minecraft.Version,
				MinecraftUID: normalized.Minecraft.MinecraftUID, MinecraftGID: normalized.Minecraft.MinecraftGID,
			},
			Storage: adminSetupFingerprintStorage{
				Type: normalized.Storage.Type, Path: normalized.Storage.Path, Device: normalized.Storage.Device,
				ParentDisk: normalized.Storage.ParentDisk, Filesystem: normalized.Storage.Filesystem, UUID: normalized.Storage.UUID,
				MountPoint: normalized.Storage.MountPoint, ExpectedUUID: normalized.Storage.ExpectedUUID,
				ExpectedSource: normalized.Storage.ExpectedSource, Source: normalized.Storage.Source,
				SystemDisk: normalized.Storage.SystemDisk, Purpose: normalized.Storage.Purpose,
			},
			Backups: adminSetupFingerprintBackups{
				Type: normalized.Backups.Type, Path: normalized.Backups.Path, Device: normalized.Backups.Device,
				ParentDisk: normalized.Backups.ParentDisk, Filesystem: normalized.Backups.Filesystem, UUID: normalized.Backups.UUID,
				MountPoint: normalized.Backups.MountPoint, ExpectedUUID: normalized.Backups.ExpectedUUID,
				ExpectedSource: normalized.Backups.ExpectedSource, Source: normalized.Backups.Source,
				SystemDisk: normalized.Backups.SystemDisk, Purpose: normalized.Backups.Purpose,
				Username: normalized.Backups.Username, Domain: normalized.Backups.Domain,
				CredentialsRequired: normalized.Backups.CredentialsRequired, Automatic: normalized.Backups.Automatic,
				DailyTime: normalized.Backups.DailyTime, Schedule: normalized.Backups.Schedule, Keep: normalized.Backups.Keep,
			},
		},
		Requirements: *requirements,
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validSetupServerType(value string) bool {
	_, ok := javaServerSoftware(value)
	return ok || value == "bedrock"
}

func validSetupGameMode(mode string) bool {
	switch mode {
	case "survival", "creative", "adventure", "spectator":
		return true
	}
	return false
}

func decodeAdminSetupPlanRequest(w http.ResponseWriter, r *http.Request, target *adminSetupPlanRequest) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
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

func decodeAdminSetupPlanResponse(data []byte, target *adminSetupPlanResponse) error {
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

func boundedSetupPlanError(message string) string {
	const fallback = "first-run setup could not be validated"
	if message == "" || len(message) > 512 || strings.ContainsAny(message, "\r\n") {
		return fallback
	}
	return message
}
