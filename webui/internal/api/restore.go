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
	"time"
)

const (
	adminRestoreBackupsPath = "/v1/admin/restore/backups"
	adminRestorePlanPath    = "/v1/admin/restore/plan"
	adminRestoreApplyPath   = "/v1/admin/restore/apply"
)

var (
	adminRestoreBackupsTimeout = 20 * time.Second
	adminRestorePlanTimeout    = 25 * time.Second
	adminRestoreApplyTimeout   = 30 * time.Second
	restoreBackupIDPattern     = regexp.MustCompile(`^minecraft-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}\.tar\.gz$`)
	restoreFingerprintPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type AdminRestoreMetadataMinecraft struct {
	VersionMode           string `json:"version_mode"`
	ConfiguredVersion     string `json:"configured_version"`
	ServerReportedVersion string `json:"server_reported_version,omitempty"`
}

type AdminRestoreMetadataBedrock struct {
	Enabled               bool   `json:"enabled"`
	GeyserReportedVersion string `json:"geyser_reported_version,omitempty"`
	FloodgateConfigured   bool   `json:"floodgate_configured"`
}

type AdminRestoreMetadataJustVoxel struct {
	Variant string `json:"variant,omitempty"`
}

type AdminRestoreBackupMetadata struct {
	CreatedAt string                        `json:"created_at,omitempty"`
	Minecraft AdminRestoreMetadataMinecraft `json:"minecraft"`
	Bedrock   AdminRestoreMetadataBedrock   `json:"bedrock"`
	JustVoxel AdminRestoreMetadataJustVoxel `json:"justvoxel"`
}

type AdminRestoreBackup struct {
	ID             string                      `json:"id"`
	CreatedAt      string                      `json:"created_at"`
	SizeBytes      uint64                      `json:"size_bytes"`
	MetadataStatus string                      `json:"metadata_status"`
	Metadata       *AdminRestoreBackupMetadata `json:"metadata,omitempty"`
}

type AdminRestoreBackupsResponse struct {
	Backups []AdminRestoreBackup `json:"backups"`
}

type AdminRestorePlanRequest struct {
	BackupID string `json:"backup_id"`
	Mode     string `json:"mode"`
}

type AdminRestorePlanWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type AdminRestorePlanCurrent struct {
	VersionMode    string `json:"version_mode"`
	Version        string `json:"version"`
	BedrockEnabled bool   `json:"bedrock_enabled"`
}

type AdminRestorePlanValidation struct {
	ArchiveIntegrity string `json:"archive_integrity"`
	ArchiveSafety    string `json:"archive_safety"`
	StagingSpace     string `json:"staging_space"`
}

type AdminRestorePlanNormalized struct {
	BackupID        string                      `json:"backup_id"`
	Mode            string                      `json:"mode"`
	CreatedAt       string                      `json:"created_at"`
	SizeBytes       uint64                      `json:"size_bytes"`
	MetadataStatus  string                      `json:"metadata_status"`
	Metadata        *AdminRestoreBackupMetadata `json:"metadata,omitempty"`
	Current         AdminRestorePlanCurrent     `json:"current"`
	VersionRelation string                      `json:"version_relation"`
	Validation      AdminRestorePlanValidation  `json:"validation"`
}

type AdminRestorePlanRequirements struct {
	DestructiveConfirmationRequired   bool     `json:"destructive_confirmation_required"`
	PlayersConfirmationRequired       bool     `json:"players_confirmation_required"`
	MinecraftState                    string   `json:"minecraft_state"`
	Online                            int      `json:"online"`
	Players                           []string `json:"players"`
	ArchiveIntegrityValidationOnApply bool     `json:"archive_integrity_validation_on_apply"`
	ArchiveSafetyValidationOnApply    bool     `json:"archive_safety_validation_on_apply"`
	StagingSpaceValidationOnApply     bool     `json:"staging_space_validation_on_apply"`
}

type AdminRestorePlanResponse struct {
	OK              bool                          `json:"ok"`
	SchemaVersion   string                        `json:"schema_version"`
	PlanFingerprint string                        `json:"plan_fingerprint,omitempty"`
	Code            string                        `json:"code,omitempty"`
	Error           string                        `json:"error,omitempty"`
	Normalized      *AdminRestorePlanNormalized   `json:"normalized,omitempty"`
	Warnings        []AdminRestorePlanWarning     `json:"warnings"`
	Requirements    *AdminRestorePlanRequirements `json:"requirements,omitempty"`
}

type AdminRestoreApplyRequest struct {
	PlanFingerprint      string                  `json:"plan_fingerprint"`
	Request              AdminRestorePlanRequest `json:"request"`
	DestructiveConfirmed bool                    `json:"destructive_confirmed"`
	PlayersConfirmed     bool                    `json:"players_confirmed"`
}

type AdminRestoreApplyResponse struct {
	OK        bool                 `json:"ok"`
	Code      string               `json:"code,omitempty"`
	Error     string               `json:"error,omitempty"`
	Created   bool                 `json:"created"`
	Operation *PersistentOperation `json:"operation,omitempty"`
}

func (c *Client) AdminRestoreBackups(ctx context.Context, session string) (AdminRestoreBackupsResponse, error) {
	var out AdminRestoreBackupsResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix"+adminRestoreBackupsPath, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)

	client := *c.http
	client.Timeout = adminRestoreBackupsTimeout
	resp, err := client.Do(req)
	if err != nil {
		return out, fmt.Errorf("management API unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return out, ErrUnauthorized
	}
	if resp.StatusCode == http.StatusForbidden {
		return out, ErrPasswordChangeRequired
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, readResponseError(resp)
	}
	if err := decodeAdminRestoreBackupsResponse(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid restore backup response: %w", err)
	}
	return out, nil
}

func (c *Client) AdminRestorePlan(ctx context.Context, session string, request AdminRestorePlanRequest) (AdminRestorePlanResponse, error) {
	var out AdminRestorePlanResponse
	if !restoreBackupIDPattern.MatchString(request.BackupID) || (request.Mode != "world" && request.Mode != "full") {
		return out, errors.New("invalid restore plan request")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+adminRestorePlanPath, bytes.NewReader(data))
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)

	client := *c.http
	client.Timeout = adminRestorePlanTimeout
	resp, err := client.Do(req)
	if err != nil {
		return out, fmt.Errorf("management API unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return out, ErrUnauthorized
	}
	if resp.StatusCode == http.StatusForbidden {
		return out, ErrPasswordChangeRequired
	}
	if resp.StatusCode == http.StatusBadRequest {
		if err := decodeAdminRestorePlanResponse(resp.Body, &out); err != nil {
			return out, fmt.Errorf("management API returned invalid restore planning response: %w", err)
		}
		return out, &ResponseError{StatusCode: resp.StatusCode, Message: out.Error}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, readResponseError(resp)
	}
	if err := decodeAdminRestorePlanResponse(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid restore planning response: %w", err)
	}
	return out, nil
}

func (c *Client) AdminRestoreApply(ctx context.Context, session string, request AdminRestoreApplyRequest) (AdminRestoreApplyResponse, error) {
	var out AdminRestoreApplyResponse
	if !restoreFingerprintPattern.MatchString(request.PlanFingerprint) || !restoreBackupIDPattern.MatchString(request.Request.BackupID) || (request.Request.Mode != "world" && request.Request.Mode != "full") {
		return out, errors.New("invalid restore apply request")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+adminRestoreApplyPath, bytes.NewReader(data))
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)

	client := *c.http
	client.Timeout = adminRestoreApplyTimeout
	resp, err := client.Do(req)
	if err != nil {
		return out, fmt.Errorf("management API unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return out, ErrUnauthorized
	}
	if resp.StatusCode == http.StatusForbidden {
		return out, ErrPasswordChangeRequired
	}
	if err := decodeAdminRestoreApplyResponse(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid restore apply response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return out, &ResponseError{StatusCode: resp.StatusCode, Message: out.Error}
	}
	return out, nil
}

func decodeAdminRestoreBackupsResponse(reader io.Reader, target *AdminRestoreBackupsResponse) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 256*1024))
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
	if target.Backups == nil {
		target.Backups = []AdminRestoreBackup{}
	}
	return nil
}

func decodeAdminRestorePlanResponse(reader io.Reader, target *AdminRestorePlanResponse) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 128*1024))
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
	if target.Warnings == nil {
		target.Warnings = []AdminRestorePlanWarning{}
	}
	return nil
}

func decodeAdminRestoreApplyResponse(reader io.Reader, target *AdminRestoreApplyResponse) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 32*1024))
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
	if target.Operation != nil {
		if !persistentOperationIDPattern.MatchString(target.Operation.OperationID) {
			return errors.New("invalid operation id in restore response")
		}
		if target.Operation.OperationType != "restore" {
			return errors.New("invalid operation type in restore response")
		}
		if target.Operation.PlanFingerprint != "" && !restoreFingerprintPattern.MatchString(target.Operation.PlanFingerprint) {
			return errors.New("invalid restore fingerprint in operation response")
		}
	}
	return nil
}
