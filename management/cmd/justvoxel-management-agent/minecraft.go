package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"time"
)

type minecraftActionRequest struct {
	ConfirmPlayers bool `json:"confirm_players"`
}

type operatorRestartUsageView struct {
	RestartUsed     int `json:"restart_used"`
	RestartLimit    int `json:"restart_limit"`
	CooldownSeconds int `json:"cooldown_seconds"`
}

type minecraftActionResponse struct {
	ID                   string                    `json:"id,omitempty"`
	OK                   bool                      `json:"ok"`
	Action               string                    `json:"action,omitempty"`
	Message              string                    `json:"message,omitempty"`
	ConfirmationRequired bool                      `json:"confirmation_required,omitempty"`
	Reason               string                    `json:"reason,omitempty"`
	Players              []string                  `json:"players,omitempty"`
	Online               int                       `json:"online,omitempty"`
	OperatorUsage        *operatorRestartUsageView `json:"operator_usage,omitempty"`
}

type minecraftActionExecution struct {
	Output       []byte
	Result       minecraftActionResponse
	Status       int
	Accepted     bool
	ErrorMessage string
}

var runWebHelper = func(ctx context.Context, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, statusHelper, args...)
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

func registerMinecraftRoutes(mux *http.ServeMux, s *server) {
	registerInstanceRuntimeRoutes(mux, s)
	mux.HandleFunc("GET /v1/minecraft/instances", s.minecraftInstancesList)
	mux.HandleFunc("POST /v1/minecraft/instances/{id}/name", s.minecraftInstanceRename)
	mux.HandleFunc("GET /v1/minecraft/identity", s.minecraftInstanceRead)
	mux.HandleFunc("POST /v1/minecraft/identity/ensure", s.minecraftInstanceRegister)
	mux.HandleFunc("POST /v1/minecraft/identity/manual", s.minecraftInstanceRegister)
	mux.HandleFunc("GET /v1/players", s.players)
	mux.HandleFunc("POST /v1/minecraft/start", s.minecraftStart)
	mux.HandleFunc("POST /v1/minecraft/stop", s.minecraftStop)
}

func (s *server) players(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	output, exitCode, err := runWebHelper(ctx, "players")
	if err != nil || exitCode != 0 {
		writeError(w, http.StatusServiceUnavailable, "player status collection failed")
		return
	}
	writeHelperJSON(w, http.StatusOK, output, "player status collector returned invalid data")
}

func (s *server) minecraftStart(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireRoles(w, r, roleAdministrator, roleOperator); !ok {
		return
	}
	s.minecraftAction(w, r, "start")
}

func (s *server) minecraftStop(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	s.minecraftAction(w, r, "stop")
}

func (s *server) minecraftAction(w http.ResponseWriter, r *http.Request, action string) {
	var request minecraftActionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	args := []string{action}
	if request.ConfirmPlayers {
		args = append(args, "--confirm-players")
	}
	writeMinecraftActionExecution(w, executeMinecraftAction(r.Context(), args))
}

func executeMinecraftAction(parent context.Context, args []string) minecraftActionExecution {
	ctx, cancel := context.WithTimeout(parent, 180*time.Second)
	defer cancel()
	output, exitCode, runErr := runWebHelper(ctx, args...)
	if !json.Valid(output) {
		message := "Minecraft control helper returned invalid data"
		status := http.StatusInternalServerError
		if runErr != nil {
			message = "Minecraft control helper failed"
			status = http.StatusServiceUnavailable
		}
		return minecraftActionExecution{Status: status, ErrorMessage: message}
	}
	var result minecraftActionResponse
	if err := json.Unmarshal(output, &result); err != nil {
		return minecraftActionExecution{Status: http.StatusInternalServerError, ErrorMessage: "Minecraft control helper returned invalid data"}
	}
	if runErr == nil && exitCode == 0 {
		return minecraftActionExecution{Output: output, Result: result, Status: http.StatusOK, Accepted: true}
	}
	if exitCode == 10 && result.ConfirmationRequired {
		return minecraftActionExecution{Output: output, Result: result, Status: http.StatusOK}
	}
	status := http.StatusInternalServerError
	switch exitCode {
	case 2:
		status = http.StatusConflict
	case 3:
		status = http.StatusServiceUnavailable
	case 4:
		status = http.StatusInternalServerError
	}
	return minecraftActionExecution{Output: output, Result: result, Status: status}
}

func writeMinecraftActionExecution(w http.ResponseWriter, execution minecraftActionExecution) {
	if execution.ErrorMessage != "" {
		writeError(w, execution.Status, execution.ErrorMessage)
		return
	}
	writeHelperJSON(w, execution.Status, execution.Output, "Minecraft control helper returned invalid data")
}

func writeHelperJSON(w http.ResponseWriter, status int, output []byte, invalidMessage string) {
	if !json.Valid(output) {
		writeError(w, http.StatusInternalServerError, invalidMessage)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(output)
}
