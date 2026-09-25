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
	adminMinecraftResetPlanPath  = "/v1/admin/reset/minecraft/plan"
	adminMinecraftResetApplyPath = "/v1/admin/reset/minecraft/apply"
	adminFactoryResetPlanPath    = "/v1/admin/reset/factory/plan"
	adminFactoryResetApplyPath   = "/v1/admin/reset/factory/apply"
)

var (
	adminResetPlanTimeout  = 30 * time.Second
	adminResetApplyTimeout = 45 * time.Second
	adminResetFingerprintPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type AdminResetPlanResponse struct {
	OK                    bool     `json:"ok"`
	SchemaVersion         string   `json:"schema_version,omitempty"`
	Mode                  string   `json:"mode,omitempty"`
	PlanFingerprint       string   `json:"plan_fingerprint,omitempty"`
	Code                  string   `json:"code,omitempty"`
	Error                 string   `json:"error,omitempty"`
	MinecraftConfigured   bool     `json:"minecraft_configured,omitempty"`
	DataPath              string   `json:"data_path,omitempty"`
	DataScope             string   `json:"data_scope,omitempty"`
	DataAction            string   `json:"data_action,omitempty"`
	BackupPath            string   `json:"backup_path,omitempty"`
	BackupScope           string   `json:"backup_scope,omitempty"`
	BackupAction          string   `json:"backup_action,omitempty"`
	ConfigBackupsAction   string   `json:"config_backups_action,omitempty"`
	StorageLayoutAction   string   `json:"storage_layout_action,omitempty"`
	ExternalStorageAction string   `json:"external_storage_action,omitempty"`
	NetworkStorageAction  string   `json:"network_storage_action,omitempty"`
	AuthenticationAction  string   `json:"authentication_action,omitempty"`
	WebUIUsersAction      string   `json:"webui_users_action,omitempty"`
	PasswordAction        string   `json:"password_action,omitempty"`
	SessionsAction        string   `json:"sessions_action,omitempty"`
	PlayersOnline         int      `json:"players_online,omitempty"`
	Players               []string `json:"players"`
	Warnings              []string `json:"warnings"`
}

type AdminMinecraftResetApplyRequest struct {
	PlanFingerprint string `json:"plan_fingerprint"`
	ConfirmPlayers  bool   `json:"confirm_players"`
}

type AdminFactoryResetApplyRequest struct {
	PlanFingerprint string `json:"plan_fingerprint"`
	ConfirmPlayers  bool   `json:"confirm_players"`
	SystemPassword  string `json:"system_password"`
}

type AdminResetApplyResponse struct {
	OK        bool                 `json:"ok"`
	Code      string               `json:"code,omitempty"`
	Error     string               `json:"error,omitempty"`
	Created   bool                 `json:"created"`
	Players   []string             `json:"players,omitempty"`
	Operation *PersistentOperation `json:"operation,omitempty"`
}

func (c *Client) AdminMinecraftResetPlan(ctx context.Context, session string) (AdminResetPlanResponse, error) {
	return c.adminResetPlan(ctx, session, adminMinecraftResetPlanPath, "minecraft")
}

func (c *Client) AdminFactoryResetPlan(ctx context.Context, session string) (AdminResetPlanResponse, error) {
	return c.adminResetPlan(ctx, session, adminFactoryResetPlanPath, "factory")
}

func (c *Client) AdminMinecraftResetApply(ctx context.Context, session string, request AdminMinecraftResetApplyRequest) (AdminResetApplyResponse, error) {
	if !adminResetFingerprintPattern.MatchString(request.PlanFingerprint) {
		return AdminResetApplyResponse{}, errors.New("invalid Minecraft reset fingerprint")
	}
	return c.adminResetApply(ctx, session, adminMinecraftResetApplyPath, request, "minecraft_reset")
}

func (c *Client) AdminFactoryResetApply(ctx context.Context, session string, request AdminFactoryResetApplyRequest) (AdminResetApplyResponse, error) {
	if !adminResetFingerprintPattern.MatchString(request.PlanFingerprint) || strings.TrimSpace(request.SystemPassword) == "" {
		return AdminResetApplyResponse{}, errors.New("invalid factory reset request")
	}
	return c.adminResetApply(ctx, session, adminFactoryResetApplyPath, request, "factory_reset")
}

func (c *Client) adminResetPlan(ctx context.Context, session, path, mode string) (AdminResetPlanResponse, error) {
	var out AdminResetPlanResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+path, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)

	client := *c.http
	client.Timeout = adminResetPlanTimeout
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
	if err := decodeAdminResetPlan(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid reset plan response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, &ResponseError{StatusCode: resp.StatusCode, Message: resetResponseMessage(out.Error, "reset planning was rejected")}
	}
	if !out.OK || out.Mode != mode || !adminResetFingerprintPattern.MatchString(out.PlanFingerprint) {
		return out, errors.New("management API returned unsupported reset plan")
	}
	return out, nil
}

func (c *Client) adminResetApply(ctx context.Context, session, path string, body any, operationType string) (AdminResetApplyResponse, error) {
	var out AdminResetApplyResponse
	data, err := json.Marshal(body)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+path, bytes.NewReader(data))
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+session)

	client := *c.http
	client.Timeout = adminResetApplyTimeout
	resp, err := client.Do(req)
	if err != nil {
		return out, fmt.Errorf("management API unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return out, ErrUnauthorized
	}
	if err := decodeAdminResetApply(resp.Body, &out); err != nil {
		return out, fmt.Errorf("management API returned invalid reset apply response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, &ResponseError{StatusCode: resp.StatusCode, Message: resetResponseMessage(out.Error, "reset could not start")}
	}
	if !out.OK || out.Operation == nil || out.Operation.OperationType != operationType {
		return out, errors.New("management API returned unsupported reset operation")
	}
	return out, nil
}

func decodeAdminResetPlan(reader io.Reader, target *AdminResetPlanResponse) error {
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
	if target.Players == nil {
		target.Players = []string{}
	}
	if target.Warnings == nil {
		target.Warnings = []string{}
	}
	return nil
}

func decodeAdminResetApply(reader io.Reader, target *AdminResetApplyResponse) error {
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
			return errors.New("invalid operation id in reset response")
		}
		if target.Operation.PlanFingerprint != "" && !adminResetFingerprintPattern.MatchString(target.Operation.PlanFingerprint) {
			return errors.New("invalid reset fingerprint in operation response")
		}
	}
	return nil
}

func resetResponseMessage(message, fallback string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return fallback
	}
	return message
}
