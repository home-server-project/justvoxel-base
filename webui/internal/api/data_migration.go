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
	adminDataMigrationDiscoveryPath = "/v1/admin/data-migration"
	adminDataMigrationPlanPath      = "/v1/admin/data-migration/plan"
	adminDataMigrationApplyPath     = "/v1/admin/data-migration/apply"
)

var (
	adminDataMigrationDiscoveryTimeout = 25 * time.Second
	adminDataMigrationPlanTimeout      = 30 * time.Second
	adminDataMigrationApplyTimeout     = 35 * time.Second
	dataMigrationFingerprintPattern    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type AdminDataMigrationCandidate struct {
	Path       string `json:"path"`
	Parent     string `json:"parent,omitempty"`
	Model      string `json:"model,omitempty"`
	Transport  string `json:"transport,omitempty"`
	Filesystem string `json:"filesystem,omitempty"`
	Mountpoint string `json:"mountpoint,omitempty"`
	SizeBytes  uint64 `json:"size_bytes"`
	SystemDisk bool   `json:"system_disk,omitempty"`
}

type AdminDataMigrationWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type AdminDataMigrationDiscoveryResponse struct {
	OK              bool                          `json:"ok"`
	SchemaVersion   string                        `json:"schema_version"`
	Code            string                        `json:"code,omitempty"`
	Error           string                        `json:"error,omitempty"`
	Warnings        []AdminDataMigrationWarning   `json:"warnings"`
	WholeDisks      []AdminDataMigrationCandidate `json:"whole_disks"`
	Partitions      []AdminDataMigrationCandidate `json:"partitions"`
	BlankPartitions []AdminDataMigrationCandidate `json:"blank_partitions"`
	FreeSpaceDisks  []AdminDataMigrationCandidate `json:"free_space_disks"`
}

type AdminDataMigrationPlanRequest struct {
	Operation  string `json:"operation"`
	Device     string `json:"device"`
	MountPoint string `json:"mount_point"`
	Path       string `json:"path"`
	SizeGiB    string `json:"size_gib,omitempty"`
}

type AdminDataMigrationPlanNormalized struct {
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

type AdminDataMigrationRequirements struct {
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

type AdminDataMigrationPlanResponse struct {
	OK              bool                            `json:"ok"`
	SchemaVersion   string                          `json:"schema_version"`
	PlanFingerprint string                          `json:"plan_fingerprint,omitempty"`
	Code            string                          `json:"code,omitempty"`
	Error           string                          `json:"error,omitempty"`
	Normalized      *AdminDataMigrationPlanNormalized `json:"normalized,omitempty"`
	Warnings        []AdminDataMigrationWarning     `json:"warnings"`
	Requirements    *AdminDataMigrationRequirements `json:"requirements,omitempty"`
}

type AdminDataMigrationApplyRequest struct {
	PlanFingerprint    string                        `json:"plan_fingerprint"`
	Request            AdminDataMigrationPlanRequest `json:"request"`
	MigrationConfirmed bool                          `json:"migration_confirmed"`
	Confirmation       string                        `json:"confirmation,omitempty"`
	PlayersConfirmed   bool                          `json:"players_confirmed"`
}

type AdminDataMigrationApplyResponse struct {
	OK        bool                 `json:"ok"`
	Code      string               `json:"code,omitempty"`
	Error     string               `json:"error,omitempty"`
	Created   bool                 `json:"created"`
	Operation *PersistentOperation `json:"operation,omitempty"`
}

func (c *Client) AdminDataMigrationDiscovery(ctx context.Context, session string) (AdminDataMigrationDiscoveryResponse, error) {
	var out AdminDataMigrationDiscoveryResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix"+adminDataMigrationDiscoveryPath, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)

	client := *c.http
	client.Timeout = adminDataMigrationDiscoveryTimeout
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
	if err := decodeAdminDataMigrationDiscovery(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid data migration discovery response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, &ResponseError{StatusCode: resp.StatusCode, Message: migrationResponseMessage(out.Error, "Minecraft data migration discovery was rejected")}
	}
	return out, nil
}

func (c *Client) AdminDataMigrationPlan(ctx context.Context, session string, request AdminDataMigrationPlanRequest) (AdminDataMigrationPlanResponse, error) {
	var out AdminDataMigrationPlanResponse
	if !validDataMigrationPlanRequest(request) {
		return out, errors.New("invalid Minecraft data migration plan request")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+adminDataMigrationPlanPath, bytes.NewReader(data))
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)

	client := *c.http
	client.Timeout = adminDataMigrationPlanTimeout
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
	if err := decodeAdminDataMigrationPlan(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid data migration plan response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, &ResponseError{StatusCode: resp.StatusCode, Message: migrationResponseMessage(out.Error, "Minecraft data migration planning was rejected")}
	}
	return out, nil
}

func (c *Client) AdminDataMigrationApply(ctx context.Context, session string, request AdminDataMigrationApplyRequest) (AdminDataMigrationApplyResponse, error) {
	var out AdminDataMigrationApplyResponse
	if !dataMigrationFingerprintPattern.MatchString(request.PlanFingerprint) ||
		!validDataMigrationPlanRequest(request.Request) ||
		!request.MigrationConfirmed {
		return out, errors.New("invalid Minecraft data migration apply request")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+adminDataMigrationApplyPath, bytes.NewReader(data))
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)

	client := *c.http
	client.Timeout = adminDataMigrationApplyTimeout
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
	if err := decodeAdminDataMigrationApply(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid data migration apply response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return out, &ResponseError{StatusCode: resp.StatusCode, Message: migrationResponseMessage(out.Error, "Minecraft data migration could not start")}
	}
	return out, nil
}

func validDataMigrationPlanRequest(request AdminDataMigrationPlanRequest) bool {
	switch request.Operation {
	case "erase_disk", "use_partition", "format_partition", "create_partition":
	default:
		return false
	}
	if !strings.HasPrefix(request.Device, "/dev/") ||
		!strings.HasPrefix(request.MountPoint, "/") ||
		!strings.HasPrefix(request.Path, "/") {
		return false
	}
	if request.SizeGiB == "" {
		return false
	}
	return true
}

func decodeAdminDataMigrationDiscovery(reader io.Reader, target *AdminDataMigrationDiscoveryResponse) error {
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
	if target.Warnings == nil {
		target.Warnings = []AdminDataMigrationWarning{}
	}
	if target.WholeDisks == nil {
		target.WholeDisks = []AdminDataMigrationCandidate{}
	}
	if target.Partitions == nil {
		target.Partitions = []AdminDataMigrationCandidate{}
	}
	if target.BlankPartitions == nil {
		target.BlankPartitions = []AdminDataMigrationCandidate{}
	}
	if target.FreeSpaceDisks == nil {
		target.FreeSpaceDisks = []AdminDataMigrationCandidate{}
	}
	return nil
}

func decodeAdminDataMigrationPlan(reader io.Reader, target *AdminDataMigrationPlanResponse) error {
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
		target.Warnings = []AdminDataMigrationWarning{}
	}
	return nil
}

func decodeAdminDataMigrationApply(reader io.Reader, target *AdminDataMigrationApplyResponse) error {
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
			return errors.New("invalid operation id in data migration response")
		}
		if target.Operation.OperationType != "data_migration" {
			return errors.New("invalid operation type in data migration response")
		}
		if target.Operation.PlanFingerprint != "" && !dataMigrationFingerprintPattern.MatchString(target.Operation.PlanFingerprint) {
			return errors.New("invalid data migration fingerprint in operation response")
		}
	}
	return nil
}

func migrationResponseMessage(message, fallback string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return fallback
	}
	return message
}
