package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"time"
)

const adminSystemActionsHelper = "/usr/libexec/justvoxel/mjust/admin-system-actions-json"

type adminSystemActionRequest struct {
	ActionConfirmed bool `json:"action_confirmed"`
	ConfirmPlayers  bool `json:"confirm_players"`
}

type adminSystemFirmwareStatus struct {
	Available    bool   `json:"available"`
	EFI          bool   `json:"efi"`
	DisplayState string `json:"display_state"`
	Reason       string `json:"reason,omitempty"`
}

type adminSystemActionsStatus struct {
	OK                bool                      `json:"ok"`
	Variant           string                    `json:"variant"`
	MinecraftState    string                    `json:"minecraft_state"`
	StagedUpdate      bool                      `json:"staged_update"`
	OSStatusAvailable bool                      `json:"os_status_available"`
	Firmware          adminSystemFirmwareStatus `json:"firmware"`
}

type adminSystemActionResponse struct {
	OK                   bool     `json:"ok"`
	Action               string   `json:"action,omitempty"`
	Accepted             bool     `json:"accepted,omitempty"`
	Message              string   `json:"message,omitempty"`
	Reason               string   `json:"reason,omitempty"`
	ConfirmationRequired bool     `json:"confirmation_required,omitempty"`
	Online               int      `json:"online,omitempty"`
	Players              []string `json:"players,omitempty"`
}

var runAdminSystemActionHelper = func(ctx context.Context, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, adminSystemActionsHelper, args...)
	output, err := cmd.Output()
	if err == nil {
		return output, 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return output, exitErr.ExitCode(), err
	}
	return output, -1, err
}

func registerAdminSystemActionRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/system/actions", s.adminSystemActionsStatus)
	mux.HandleFunc("POST /v1/admin/system/reboot", func(w http.ResponseWriter, r *http.Request) {
		s.adminSystemAction(w, r, "reboot")
	})
	mux.HandleFunc("POST /v1/admin/system/poweroff", func(w http.ResponseWriter, r *http.Request) {
		s.adminSystemAction(w, r, "poweroff")
	})
	mux.HandleFunc("POST /v1/admin/system/firmware-reboot", func(w http.ResponseWriter, r *http.Request) {
		s.adminSystemAction(w, r, "firmware-reboot")
	})
}

func (s *server) adminSystemActionsStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	output, exitCode, err := runAdminSystemActionHelper(ctx, "status")
	if err != nil || exitCode != 0 {
		writeError(w, http.StatusServiceUnavailable, "system action status is unavailable")
		return
	}

	var status adminSystemActionsStatus
	if err := json.Unmarshal(output, &status); err != nil || !status.OK {
		writeError(w, http.StatusInternalServerError, "system action status returned invalid data")
		return
	}
	if status.Variant != "vm" && status.Variant != "hws" && status.Variant != "unknown" {
		writeError(w, http.StatusInternalServerError, "system action status returned invalid variant data")
		return
	}
	if status.MinecraftState != "running" && status.MinecraftState != "stopped" {
		writeError(w, http.StatusInternalServerError, "system action status returned invalid Minecraft state")
		return
	}
	switch status.Firmware.DisplayState {
	case "connected", "disconnected", "unknown":
	default:
		writeError(w, http.StatusInternalServerError, "system action status returned invalid display state")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *server) adminSystemAction(w http.ResponseWriter, r *http.Request, action string) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	var request adminSystemActionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !request.ActionConfirmed {
		writeError(w, http.StatusBadRequest, "explicit system action confirmation is required")
		return
	}

	args := []string{"apply", action}
	if request.ConfirmPlayers {
		args = append(args, "--confirm-players")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()
	output, exitCode, runErr := runAdminSystemActionHelper(ctx, args...)

	if !json.Valid(output) {
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "system_"+action, "host", false, "system action helper returned invalid data")
		}
		if runErr != nil {
			writeError(w, http.StatusServiceUnavailable, "system action backend failed")
		} else {
			writeError(w, http.StatusInternalServerError, "system action backend returned invalid data")
		}
		return
	}

	var result adminSystemActionResponse
	if err := json.Unmarshal(output, &result); err != nil || result.Action != action {
		writeError(w, http.StatusInternalServerError, "system action backend returned invalid data")
		return
	}

	if runErr == nil && exitCode == 0 && result.OK && result.Accepted {
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "system_"+action, "host", true, "system action accepted")
		}
		writeJSON(w, http.StatusAccepted, result)
		return
	}
	if exitCode == 10 && result.ConfirmationRequired && result.Reason == "players_online" {
		writeJSON(w, http.StatusOK, result)
		return
	}

	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "system_"+action, "host", false, result.Reason)
	}
	switch exitCode {
	case 2:
		writeJSON(w, http.StatusConflict, result)
	case 3:
		writeJSON(w, http.StatusServiceUnavailable, result)
	case 4:
		writeJSON(w, http.StatusInternalServerError, result)
	default:
		writeError(w, http.StatusServiceUnavailable, "system action backend failed")
	}
}
