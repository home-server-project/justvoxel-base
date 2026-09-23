package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"time"
)

const adminSystemUpdateRebootHelper = "/usr/libexec/justvoxel/mjust/admin-system-update-reboot-json"

type adminSystemUpdateRebootRequest struct {
	ActionConfirmed bool `json:"action_confirmed"`
	ConfirmPlayers  bool `json:"confirm_players"`
	BackupMinecraft bool `json:"backup_minecraft"`
	WarningSeconds  int  `json:"warning_seconds"`
}

type adminSystemUpdateRebootResponse struct {
	OK                   bool     `json:"ok"`
	State                string   `json:"state"`
	Accepted             bool     `json:"accepted,omitempty"`
	ConfirmationRequired bool     `json:"confirmation_required,omitempty"`
	Reason               string   `json:"reason,omitempty"`
	Message              string   `json:"message,omitempty"`
	Online               int      `json:"online,omitempty"`
	Players              []string `json:"players,omitempty"`
	WarningSeconds       int      `json:"warning_seconds,omitempty"`
	BackupMinecraft      bool     `json:"backup_minecraft,omitempty"`
	DeadlineUnix         int64    `json:"deadline_unix,omitempty"`
}

var runAdminSystemUpdateRebootHelper = func(ctx context.Context, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, adminSystemUpdateRebootHelper, args...)
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

func registerAdminSystemUpdateRebootRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/system/update-reboot", s.adminSystemUpdateRebootStatus)
	mux.HandleFunc("POST /v1/admin/system/update-reboot", s.adminSystemUpdateReboot)
}

func decodeAdminSystemUpdateRebootResponse(output []byte) (adminSystemUpdateRebootResponse, bool) {
	var result adminSystemUpdateRebootResponse
	if !json.Valid(output) || json.Unmarshal(output, &result) != nil || result.State == "" {
		return result, false
	}
	if result.Players == nil {
		result.Players = []string{}
	}
	return result, true
}

func (s *server) adminSystemUpdateRebootStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	output, exitCode, err := runAdminSystemUpdateRebootHelper(ctx, "status")
	if err != nil || exitCode != 0 {
		writeError(w, http.StatusServiceUnavailable, "system update reboot status is unavailable")
		return
	}
	result, ok := decodeAdminSystemUpdateRebootResponse(output)
	if !ok {
		writeError(w, http.StatusInternalServerError, "system update reboot status returned invalid data")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) adminSystemUpdateReboot(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	var request adminSystemUpdateRebootRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !request.ActionConfirmed {
		writeError(w, http.StatusBadRequest, "explicit reboot confirmation is required")
		return
	}
	if request.WarningSeconds == 0 {
		request.WarningSeconds = 60
	}
	if request.WarningSeconds != 60 && request.WarningSeconds != 10 {
		writeError(w, http.StatusBadRequest, "reboot warning must be 60 or 10 seconds")
		return
	}

	args := []string{"request"}
	if request.ConfirmPlayers {
		args = append(args, "--confirm-players")
	}
	if request.BackupMinecraft {
		args = append(args, "--backup-minecraft")
	}
	args = append(args, fmt.Sprintf("--warning-seconds=%d", request.WarningSeconds))

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	output, exitCode, runErr := runAdminSystemUpdateRebootHelper(ctx, args...)
	result, valid := decodeAdminSystemUpdateRebootResponse(output)
	if !valid {
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "system_update_reboot", "host", false, "update reboot helper returned invalid data")
		}
		if runErr != nil {
			writeError(w, http.StatusServiceUnavailable, "system update reboot backend failed")
		} else {
			writeError(w, http.StatusInternalServerError, "system update reboot backend returned invalid data")
		}
		return
	}

	if exitCode == 10 && result.ConfirmationRequired && result.Reason == "players_online" {
		writeJSON(w, http.StatusOK, result)
		return
	}
	if runErr == nil && exitCode == 0 && result.OK && result.Accepted {
		if s.store != nil {
			detail := "reboot workflow accepted"
			if request.BackupMinecraft {
				detail = "Minecraft backup and reboot workflow accepted"
			}
			_ = s.store.recordAuditEvent(actor, "system_update_reboot", "host", true, detail)
		}
		writeJSON(w, http.StatusAccepted, result)
		return
	}

	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "system_update_reboot", "host", false, result.Reason)
	}
	switch exitCode {
	case 2:
		writeJSON(w, http.StatusConflict, result)
	case 3:
		writeJSON(w, http.StatusServiceUnavailable, result)
	default:
		writeError(w, http.StatusServiceUnavailable, "system update reboot backend failed")
	}
}
