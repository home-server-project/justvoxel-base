package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const adminSetupPlanPath = "/v1/admin/setup/plan"

var adminSetupPlanClientTimeout = 30 * time.Second

type AdminSetupPlanServerRequest struct {
	MOTD           string `json:"motd"`
	MaxPlayers     int    `json:"max_players"`
	BedrockEnabled bool   `json:"bedrock_enabled"`
	Timezone       string `json:"timezone"`
}

type AdminSetupPlanMinecraftRequest struct {
	JavaMemory      string `json:"java_memory"`
	ContainerMemory string `json:"container_memory"`
	JavaPort        int    `json:"java_port"`
	BedrockPort     int    `json:"bedrock_port"`
	ImageTag        string `json:"image_tag"`
	VersionPolicy   string `json:"version_policy"`
	Version         string `json:"version"`
}

type AdminSetupPlanStorageRequest struct {
	Type       string `json:"type"`
	Path       string `json:"path"`
	Device     string `json:"device"`
	MountPoint string `json:"mount_point"`
}

type AdminSetupPlanBackupsRequest struct {
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

type AdminSetupPlanRequest struct {
	DiagnosticSessionID string                         `json:"diagnostic_session_id,omitempty"`
	Server              AdminSetupPlanServerRequest    `json:"server"`
	Minecraft           AdminSetupPlanMinecraftRequest `json:"minecraft"`
	Storage             AdminSetupPlanStorageRequest   `json:"storage"`
	Backups             AdminSetupPlanBackupsRequest   `json:"backups"`
}

type AdminSetupPlanWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type AdminSetupPlanServer struct {
	MOTD           string `json:"motd"`
	MaxPlayers     int    `json:"max_players"`
	BedrockEnabled bool   `json:"bedrock_enabled"`
	Timezone       string `json:"timezone"`
}

type AdminSetupPlanMinecraft struct {
	JavaMemory             string `json:"java_memory"`
	ContainerMemory        string `json:"container_memory"`
	JavaPort               int    `json:"java_port"`
	BedrockPort            int    `json:"bedrock_port"`
	ImageTag               string `json:"image_tag"`
	RequestedVersionPolicy string `json:"requested_version_policy"`
	VersionPolicy          string `json:"version_policy"`
	Version                string `json:"version"`
	SystemMemoryMiB        int    `json:"system_memory_mib"`
	SystemReserveMiB       int    `json:"system_reserve_mib"`
	MinecraftUID           uint32 `json:"minecraft_uid"`
	MinecraftGID           uint32 `json:"minecraft_gid"`
}

type AdminSetupPlanStorage struct {
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

type AdminSetupPlanBackups struct {
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

type AdminSetupNormalizedPlan struct {
	Server    AdminSetupPlanServer    `json:"server"`
	Minecraft AdminSetupPlanMinecraft `json:"minecraft"`
	Storage   AdminSetupPlanStorage   `json:"storage"`
	Backups   AdminSetupPlanBackups   `json:"backups"`
}

type AdminSetupPlanRequirements struct {
	SMBPasswordRequired            bool `json:"smb_password_required"`
	NetworkBackupValidationOnApply bool `json:"network_backup_validation_on_apply"`
}

type AdminSetupPlanResponse struct {
	OK              bool                       `json:"ok"`
	SchemaVersion   string                     `json:"schema_version"`
	PlanFingerprint string                     `json:"plan_fingerprint,omitempty"`
	Code            string                     `json:"code,omitempty"`
	Error           string                     `json:"error,omitempty"`
	Normalized      AdminSetupNormalizedPlan   `json:"normalized,omitempty"`
	Warnings        []AdminSetupPlanWarning    `json:"warnings"`
	Requirements    AdminSetupPlanRequirements `json:"requirements,omitempty"`
}

func (c *Client) AdminSetupPlan(ctx context.Context, session string, request AdminSetupPlanRequest) (AdminSetupPlanResponse, error) {
	var out AdminSetupPlanResponse
	data, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+adminSetupPlanPath, bytes.NewReader(data))
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)

	client := *c.http
	client.Timeout = adminSetupPlanClientTimeout
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
		if err := decodeAdminSetupPlanResponse(resp.Body, &out); err != nil {
			return out, fmt.Errorf("management API returned invalid setup planning response: %w", err)
		}
		normalizeAdminSetupPlanResponse(&out)
		return out, &ResponseError{StatusCode: resp.StatusCode, Message: out.Error}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, readResponseError(resp)
	}
	if err := decodeAdminSetupPlanResponse(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid setup planning response: %w", err)
	}
	normalizeAdminSetupPlanResponse(&out)
	return out, nil
}

func decodeAdminSetupPlanResponse(reader io.Reader, target *AdminSetupPlanResponse) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 64*1024))
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

func normalizeAdminSetupPlanResponse(out *AdminSetupPlanResponse) {
	if out != nil && out.Warnings == nil {
		out.Warnings = []AdminSetupPlanWarning{}
	}
}
