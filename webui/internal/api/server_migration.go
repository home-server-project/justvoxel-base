package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	adminMigrationExportPath        = "/v1/admin/migration/export"
	adminMigrationExportPlanPath    = "/v1/admin/migration/export/plan"
	adminMigrationExportApplyPath   = "/v1/admin/migration/export/apply"
	adminMigrationImportPath        = "/v1/admin/migration/import"
	adminMigrationImportPlanPath    = "/v1/admin/migration/import/plan"
	adminMigrationImportApplyPath   = "/v1/admin/migration/import/apply"
	adminMigrationRecoveryPath      = "/v1/admin/migration/recovery"
	adminMigrationRecoveryPlanPath  = "/v1/admin/migration/recovery/plan"
	adminMigrationRecoveryApplyPath = "/v1/admin/migration/recovery/apply"
)

var (
	adminMigrationDiscoveryTimeout    = 35 * time.Second
	adminMigrationPlanTimeout         = 10 * time.Minute
	adminMigrationApplyTimeout        = 35 * time.Second
	serverMigrationFingerprintPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type AdminMigrationWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type AdminMigrationExportTargetRequest struct {
	Kind      string `json:"kind"`
	Path      string `json:"path,omitempty"`
	Device    string `json:"device,omitempty"`
	Removable bool   `json:"removable,omitempty"`
	Source    string `json:"source,omitempty"`
	Username  string `json:"username,omitempty"`
	Domain    string `json:"domain,omitempty"`
	Filename  string `json:"filename"`
}

type AdminMigrationExportDevice struct {
	Path       string `json:"path"`
	Parent     string `json:"parent,omitempty"`
	Filesystem string `json:"filesystem"`
	Mountpoint string `json:"mountpoint,omitempty"`
	Model      string `json:"model,omitempty"`
	Transport  string `json:"transport,omitempty"`
	SizeBytes  uint64 `json:"size_bytes"`
	Removable  bool   `json:"removable"`
}

type AdminMigrationExportDiscoveryResponse struct {
	OK                        bool                         `json:"ok"`
	SchemaVersion             string                       `json:"schema_version"`
	Code                      string                       `json:"code,omitempty"`
	Error                     string                       `json:"error,omitempty"`
	SuggestedFilename         string                       `json:"suggested_filename"`
	ConfiguredBackupAvailable bool                         `json:"configured_backup_available"`
	Devices                   []AdminMigrationExportDevice `json:"devices"`
	TargetKinds               []string                     `json:"target_kinds"`
}

type AdminMigrationExportNormalized struct {
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

type AdminMigrationExportRequirements struct {
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

type AdminMigrationExportPlanResponse struct {
	OK              bool                              `json:"ok"`
	SchemaVersion   string                            `json:"schema_version"`
	PlanFingerprint string                            `json:"plan_fingerprint,omitempty"`
	Code            string                            `json:"code,omitempty"`
	Error           string                            `json:"error,omitempty"`
	Normalized      *AdminMigrationExportNormalized   `json:"normalized,omitempty"`
	Warnings        []AdminMigrationWarning           `json:"warnings"`
	Requirements    *AdminMigrationExportRequirements `json:"requirements,omitempty"`
}

type AdminMigrationExportApplyRequest struct {
	PlanFingerprint  string                            `json:"plan_fingerprint"`
	Request          AdminMigrationExportTargetRequest `json:"request"`
	ExportConfirmed  bool                              `json:"export_confirmed"`
	PlayersConfirmed bool                              `json:"players_confirmed"`
	SMBPassword      string                            `json:"smb_password,omitempty"`
}

type AdminMigrationImportSourceRequest struct {
	Kind          string `json:"kind,omitempty"`
	Path          string `json:"path"`
	Device        string `json:"device,omitempty"`
	Removable     bool   `json:"removable,omitempty"`
	Source        string `json:"source,omitempty"`
	Username      string `json:"username,omitempty"`
	Domain        string `json:"domain,omitempty"`
	SMBPassword   string `json:"smb_password,omitempty"`
	SelectedRoot  string `json:"selected_root,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`
}

type AdminMigrationImportDestinationRequest struct {
	JavaPort        int                          `json:"java_port"`
	BedrockPort     int                          `json:"bedrock_port"`
	JavaMemory      string                       `json:"java_memory,omitempty"`
	ContainerMemory string                       `json:"container_memory,omitempty"`
	Timezone        string                       `json:"timezone,omitempty"`
	BackupKeep      int                          `json:"backup_keep"`
	BackupDailyTime string                       `json:"backup_daily_time,omitempty"`
	BackupAutomatic bool                         `json:"backup_automatic"`
	Storage         AdminSetupPlanStorageRequest `json:"storage"`
	Backups         AdminSetupPlanBackupsRequest `json:"backups"`
}

type AdminMigrationImportRequest struct {
	Source      AdminMigrationImportSourceRequest      `json:"source"`
	Destination AdminMigrationImportDestinationRequest `json:"destination"`
}

type AdminMigrationImportDefaults struct {
	DataPath        string `json:"data_path"`
	BackupPath      string `json:"backup_path"`
	JavaMemory      string `json:"java_memory"`
	ContainerMemory string `json:"container_memory"`
	Timezone        string `json:"timezone"`
	JavaPort        int    `json:"java_port"`
	BedrockPort     int    `json:"bedrock_port"`
	BackupKeep      int    `json:"backup_keep"`
	BackupDailyTime string `json:"backup_daily_time"`
	BackupAutomatic bool   `json:"backup_automatic"`
}

type AdminMigrationImportDiscoveryResponse struct {
	OK            bool                         `json:"ok"`
	SchemaVersion string                       `json:"schema_version"`
	Configured    bool                         `json:"configured"`
	SourceKinds   []string                     `json:"source_kinds"`
	Defaults      AdminMigrationImportDefaults `json:"defaults"`
}

type AdminMigrationImportSourceEntry struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type AdminMigrationImportCandidate struct {
	Root               string   `json:"root"`
	RootRelative       string   `json:"root_relative"`
	SourceType         string   `json:"sourceType"`
	Supported          bool     `json:"supported"`
	LevelName          string   `json:"levelName,omitempty"`
	MinecraftVersion   string   `json:"minecraftVersion,omitempty"`
	OnlineMode         *bool    `json:"onlineMode,omitempty"`
	GameMode           string   `json:"gameMode,omitempty"`
	Difficulty         string   `json:"difficulty,omitempty"`
	WhitelistEnabled   *bool    `json:"whitelistEnabled,omitempty"`
	EnforceWhitelist   *bool    `json:"enforceWhitelist,omitempty"`
	MaxPlayers         string   `json:"maxPlayers,omitempty"`
	MOTD               string   `json:"motd,omitempty"`
	JavaPortHint       string   `json:"javaPortHint,omitempty"`
	PluginJarCount     int      `json:"pluginJarCount,omitempty"`
	PluginJars         []string `json:"pluginJars,omitempty"`
	GeyserEnabled      bool     `json:"geyserEnabled,omitempty"`
	GeyserAuthType     string   `json:"geyserAuthType,omitempty"`
	BedrockPortHint    int      `json:"bedrockPortHint,omitempty"`
	FloodgateEnabled   bool     `json:"floodgateEnabled,omitempty"`
	FloodgateKeySHA256 string   `json:"floodgateKeySha256,omitempty"`
}

type AdminMigrationImportSourceNormalized struct {
	Path                  string `json:"path"`
	SelectedRoot          string `json:"selected_root"`
	SourceClass           string `json:"source_class"`
	CandidateType         string `json:"candidate_type"`
	MinecraftVersion      string `json:"minecraft_version"`
	OnlineMode            string `json:"online_mode"`
	GameMode              string `json:"game_mode"`
	Difficulty            string `json:"difficulty"`
	WhitelistEnabled      bool   `json:"whitelist_enabled"`
	EnforceWhitelist      bool   `json:"enforce_whitelist"`
	MaxPlayers            int    `json:"max_players"`
	MOTD                  string `json:"motd"`
	PluginCount           int    `json:"plugin_count"`
	BedrockEnabled        bool   `json:"bedrock_enabled"`
	BedrockManagedPlugins bool   `json:"bedrock_managed_plugins"`
	JavaPortHint          int    `json:"java_port_hint"`
	BedrockPortHint       int    `json:"bedrock_port_hint"`
	ExpandedBytes         uint64 `json:"expanded_bytes"`
}

type AdminMigrationImportDestinationNormalized struct {
	Mode            string                 `json:"mode"`
	DataPath        string                 `json:"data_path"`
	BackupPath      string                 `json:"backup_path"`
	JavaPort        int                    `json:"java_port"`
	BedrockPort     int                    `json:"bedrock_port"`
	JavaMemory      string                 `json:"java_memory"`
	ContainerMemory string                 `json:"container_memory"`
	Timezone        string                 `json:"timezone"`
	ImageTag        string                 `json:"image_tag"`
	MinecraftUID    uint32                 `json:"minecraft_uid"`
	MinecraftGID    uint32                 `json:"minecraft_gid"`
	BackupKeep      int                    `json:"backup_keep"`
	BackupSchedule  string                 `json:"backup_schedule"`
	BackupAutomatic bool                   `json:"backup_automatic"`
	Storage         *AdminSetupPlanStorage `json:"storage,omitempty"`
	Backups         *AdminSetupPlanBackups `json:"backups,omitempty"`
}

type AdminMigrationImportNormalized struct {
	Source      AdminMigrationImportSourceNormalized      `json:"source"`
	Destination AdminMigrationImportDestinationNormalized `json:"destination"`
}

type AdminMigrationImportRequirements struct {
	ImportConfirmationRequired     bool     `json:"import_confirmation_required"`
	PlayersConfirmationRequired    bool     `json:"players_confirmation_required"`
	MinecraftState                 string   `json:"minecraft_state"`
	Online                         int      `json:"online"`
	Players                        []string `json:"players"`
	EULAAcceptanceRequired         bool     `json:"eula_acceptance_required"`
	VanillaConfirmationRequired    bool     `json:"vanilla_confirmation_required"`
	PluginsConfirmationRequired    bool     `json:"plugins_confirmation_required"`
	OnlineModeConfirmationRequired bool     `json:"online_mode_confirmation_required"`
	SourceRevalidationOnApply      bool     `json:"source_revalidation_on_apply"`
	RuntimeValidationRequired      bool     `json:"runtime_validation_required"`
	RollbackRequired               bool     `json:"rollback_required"`
	BackupSMBPasswordRequired      bool     `json:"backup_smb_password_required"`
}

type AdminMigrationImportPlanResponse struct {
	OK              bool                              `json:"ok"`
	SchemaVersion   string                            `json:"schema_version"`
	PlanFingerprint string                            `json:"plan_fingerprint,omitempty"`
	Code            string                            `json:"code,omitempty"`
	Error           string                            `json:"error,omitempty"`
	Normalized      *AdminMigrationImportNormalized   `json:"normalized,omitempty"`
	Warnings        []AdminMigrationWarning           `json:"warnings"`
	Requirements    *AdminMigrationImportRequirements `json:"requirements,omitempty"`
	Candidates      []AdminMigrationImportCandidate   `json:"candidates"`
	SourceEntries   []AdminMigrationImportSourceEntry `json:"source_entries"`
}

type AdminMigrationImportApplyRequest struct {
	PlanFingerprint     string                      `json:"plan_fingerprint"`
	Request             AdminMigrationImportRequest `json:"request"`
	ImportConfirmed     bool                        `json:"import_confirmed"`
	PlayersConfirmed    bool                        `json:"players_confirmed"`
	EULAAccepted        bool                        `json:"eula_accepted"`
	VanillaConfirmed    bool                        `json:"vanilla_confirmed"`
	PluginsConfirmed    bool                        `json:"plugins_confirmed"`
	OnlineModeConfirmed bool                        `json:"online_mode_confirmed"`
	BackupSMBPassword   string                      `json:"backup_smb_password,omitempty"`
}

type AdminMigrationRecoverySummary struct {
	Transaction string `json:"transaction"`
	Phase       string `json:"phase"`
	SourceType  string `json:"source_type"`
	UpdatedAt   string `json:"updated_at"`
	Mode        string `json:"mode"`
	Finalizable bool   `json:"finalizable"`
}

type AdminMigrationRecoveryDiscoveryResponse struct {
	OK            bool                            `json:"ok"`
	SchemaVersion string                          `json:"schema_version"`
	Configured    bool                            `json:"configured"`
	Transactions  []AdminMigrationRecoverySummary `json:"transactions"`
}

type AdminMigrationRecoveryPlanRequest struct {
	Transaction string `json:"transaction"`
}

type AdminMigrationRecoveryRequirements struct {
	FinalizeConfirmationRequired   bool `json:"finalize_confirmation_required"`
	RuntimeValidationRequired      bool `json:"runtime_validation_required"`
	UnconfiguredValidationRequired bool `json:"unconfigured_validation_required"`
}

type AdminMigrationRecoveryPlanResponse struct {
	OK              bool                                `json:"ok"`
	SchemaVersion   string                              `json:"schema_version"`
	PlanFingerprint string                              `json:"plan_fingerprint,omitempty"`
	Code            string                              `json:"code,omitempty"`
	Error           string                              `json:"error,omitempty"`
	Normalized      *AdminMigrationRecoverySummary      `json:"normalized,omitempty"`
	Warnings        []AdminMigrationWarning             `json:"warnings"`
	Requirements    *AdminMigrationRecoveryRequirements `json:"requirements,omitempty"`
}

type AdminMigrationRecoveryApplyRequest struct {
	PlanFingerprint   string `json:"plan_fingerprint"`
	Transaction       string `json:"transaction"`
	FinalizeConfirmed bool   `json:"finalize_confirmed"`
}

type AdminMigrationApplyResponse struct {
	OK        bool                 `json:"ok"`
	Code      string               `json:"code,omitempty"`
	Error     string               `json:"error,omitempty"`
	Created   bool                 `json:"created"`
	Operation *PersistentOperation `json:"operation,omitempty"`
}

func (c *Client) AdminMigrationExportDiscovery(ctx context.Context, session string) (AdminMigrationExportDiscoveryResponse, error) {
	var out AdminMigrationExportDiscoveryResponse
	status, err := c.serverMigrationJSON(ctx, http.MethodGet, adminMigrationExportPath, session, nil, &out, adminMigrationDiscoveryTimeout, http.StatusOK)
	if err != nil {
		return out, err
	}
	if status != http.StatusOK || out.SchemaVersion != "v1" || out.Devices == nil || out.TargetKinds == nil || out.SuggestedFilename == "" {
		return out, errors.New("management API returned invalid server migration export discovery response")
	}
	return out, nil
}

func (c *Client) AdminMigrationExportPlan(ctx context.Context, session string, request AdminMigrationExportTargetRequest) (AdminMigrationExportPlanResponse, error) {
	var out AdminMigrationExportPlanResponse
	status, err := c.serverMigrationJSON(ctx, http.MethodPost, adminMigrationExportPlanPath, session, request, &out, adminMigrationDiscoveryTimeout, http.StatusOK, http.StatusBadRequest)
	if err != nil {
		return out, err
	}
	if status == http.StatusBadRequest {
		return out, &ResponseError{StatusCode: status, Message: serverMigrationMessage(out.Error, "Server migration export planning was rejected")}
	}
	if err := validateMigrationPlanIdentity(out.OK, out.SchemaVersion, out.PlanFingerprint); err != nil {
		return out, fmt.Errorf("management API returned invalid server migration export plan response: %w", err)
	}
	return out, nil
}

func (c *Client) AdminMigrationExportApply(ctx context.Context, session string, request AdminMigrationExportApplyRequest) (AdminMigrationApplyResponse, error) {
	return c.adminMigrationApply(ctx, adminMigrationExportApplyPath, session, request, request.PlanFingerprint, "migration_export")
}

func (c *Client) AdminMigrationImportDiscovery(ctx context.Context, session string) (AdminMigrationImportDiscoveryResponse, error) {
	var out AdminMigrationImportDiscoveryResponse
	status, err := c.serverMigrationJSON(ctx, http.MethodGet, adminMigrationImportPath, session, nil, &out, adminMigrationDiscoveryTimeout, http.StatusOK)
	if err != nil {
		return out, err
	}
	if status != http.StatusOK || out.SchemaVersion != "v1" || out.SourceKinds == nil {
		return out, errors.New("management API returned invalid server migration import discovery response")
	}
	return out, nil
}

func (c *Client) AdminMigrationImportPlan(ctx context.Context, session string, request AdminMigrationImportRequest) (AdminMigrationImportPlanResponse, error) {
	var out AdminMigrationImportPlanResponse
	status, err := c.serverMigrationJSON(ctx, http.MethodPost, adminMigrationImportPlanPath, session, request, &out, adminMigrationPlanTimeout, http.StatusOK, http.StatusBadRequest)
	if err != nil {
		return out, err
	}
	if status == http.StatusBadRequest {
		return out, &ResponseError{StatusCode: status, Message: serverMigrationMessage(out.Error, "Server migration import planning was rejected")}
	}
	if err := validateMigrationPlanIdentity(out.OK, out.SchemaVersion, out.PlanFingerprint); err != nil {
		return out, fmt.Errorf("management API returned invalid server migration import plan response: %w", err)
	}
	if out.Warnings == nil || out.Candidates == nil || out.SourceEntries == nil {
		return out, errors.New("management API returned incomplete server migration import plan response")
	}
	return out, nil
}

func (c *Client) AdminMigrationImportApply(ctx context.Context, session string, request AdminMigrationImportApplyRequest) (AdminMigrationApplyResponse, error) {
	return c.adminMigrationApply(ctx, adminMigrationImportApplyPath, session, request, request.PlanFingerprint, "migration_import")
}

func (c *Client) AdminMigrationRecoveryDiscovery(ctx context.Context, session string) (AdminMigrationRecoveryDiscoveryResponse, error) {
	var out AdminMigrationRecoveryDiscoveryResponse
	status, err := c.serverMigrationJSON(ctx, http.MethodGet, adminMigrationRecoveryPath, session, nil, &out, adminMigrationDiscoveryTimeout, http.StatusOK)
	if err != nil {
		return out, err
	}
	if status != http.StatusOK || out.SchemaVersion != "v1" || out.Transactions == nil {
		return out, errors.New("management API returned invalid server migration recovery discovery response")
	}
	return out, nil
}

func (c *Client) AdminMigrationRecoveryPlan(ctx context.Context, session string, request AdminMigrationRecoveryPlanRequest) (AdminMigrationRecoveryPlanResponse, error) {
	var out AdminMigrationRecoveryPlanResponse
	status, err := c.serverMigrationJSON(ctx, http.MethodPost, adminMigrationRecoveryPlanPath, session, request, &out, adminMigrationDiscoveryTimeout, http.StatusOK, http.StatusBadRequest)
	if err != nil {
		return out, err
	}
	if status == http.StatusBadRequest {
		return out, &ResponseError{StatusCode: status, Message: serverMigrationMessage(out.Error, "Server migration recovery planning was rejected")}
	}
	if err := validateMigrationPlanIdentity(out.OK, out.SchemaVersion, out.PlanFingerprint); err != nil {
		return out, fmt.Errorf("management API returned invalid server migration recovery plan response: %w", err)
	}
	if out.Warnings == nil {
		return out, errors.New("management API returned incomplete server migration recovery plan response")
	}
	return out, nil
}

func (c *Client) AdminMigrationRecoveryApply(ctx context.Context, session string, request AdminMigrationRecoveryApplyRequest) (AdminMigrationApplyResponse, error) {
	return c.adminMigrationApply(ctx, adminMigrationRecoveryApplyPath, session, request, request.PlanFingerprint, "migration_recovery")
}

func (c *Client) adminMigrationApply(ctx context.Context, path, session string, request any, fingerprint, operationType string) (AdminMigrationApplyResponse, error) {
	var out AdminMigrationApplyResponse
	status, err := c.serverMigrationJSON(ctx, http.MethodPost, path, session, request, &out, adminMigrationApplyTimeout,
		http.StatusOK, http.StatusAccepted, http.StatusBadRequest, http.StatusConflict)
	if err != nil {
		return out, err
	}
	if status != http.StatusOK && status != http.StatusAccepted {
		return out, &ResponseError{StatusCode: status, Message: serverMigrationMessage(out.Error, "Server migration operation could not start")}
	}
	if out.Operation != nil {
		if !persistentOperationIDPattern.MatchString(out.Operation.OperationID) ||
			out.Operation.OperationType != operationType ||
			out.Operation.PlanFingerprint != fingerprint ||
			!serverMigrationFingerprintPattern.MatchString(out.Operation.PlanFingerprint) {
			return out, errors.New("management API returned invalid server migration operation")
		}
	}
	return out, nil
}

func validateMigrationPlanIdentity(ok bool, schemaVersion, fingerprint string) error {
	if schemaVersion != "v1" {
		return errors.New("unexpected schema version")
	}
	if ok && !serverMigrationFingerprintPattern.MatchString(fingerprint) {
		return errors.New("invalid plan fingerprint")
	}
	return nil
}

func (c *Client) serverMigrationJSON(ctx context.Context, method, path, session string, body, out any, timeout time.Duration, decodedStatuses ...int) (int, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+session)

	client := *c.http
	client.Timeout = timeout
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("management API unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return resp.StatusCode, ErrUnauthorized
	}
	if resp.StatusCode == http.StatusForbidden {
		return resp.StatusCode, ErrPasswordChangeRequired
	}

	decode := false
	for _, status := range decodedStatuses {
		if resp.StatusCode == status {
			decode = true
			break
		}
	}
	if !decode {
		return resp.StatusCode, readResponseError(resp)
	}
	if err := decodeServerMigrationJSON(resp.Body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("management API returned invalid server migration response: %w", err)
	}
	return resp.StatusCode, nil
}

func decodeServerMigrationJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 512*1024))
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

func serverMigrationMessage(message, fallback string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return fallback
	}
	return message
}
