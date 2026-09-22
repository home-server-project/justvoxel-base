package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os/exec"
	"strconv"
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
	mux.HandleFunc("GET /v1/players", s.players)
	mux.HandleFunc("POST /v1/minecraft/start", s.minecraftStart)
	mux.HandleFunc("POST /v1/minecraft/stop", s.minecraftStop)
	mux.HandleFunc("POST /v1/minecraft/restart", s.minecraftRestart)
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

func (s *server) minecraftRestart(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireRoles(w, r, roleAdministrator, roleOperator)
	if !ok {
		return
	}
	if sess.Role == roleAdministrator {
		s.minecraftAction(w, r, "restart")
		return
	}
	if s.store == nil {
		writeError(w, http.StatusForbidden, "operator restart controls are unavailable")
		return
	}
	s.operatorMinecraftRestart(w, r, sess)
}

func (s *server) operatorMinecraftRestart(w http.ResponseWriter, r *http.Request, sess session) {
	var request minecraftActionRequest
	if !decodeJSON(w, r, &request) {
		return
	}

	operatorRestartMu.Lock()
	defer operatorRestartMu.Unlock()

	guard, availability, err := s.store.beginOperatorRestart(sess.WebUserID)
	if err != nil {
		_ = s.store.recordAuditEvent(sess, "minecraft_restart", "minecraft.service", false, "operator restart eligibility check failed")
		writeError(w, http.StatusServiceUnavailable, "operator restart allowance is unavailable")
		return
	}
	if !availability.Allowed {
		contextText := availability.Reason
		_ = s.store.recordAuditEvent(sess, "minecraft_restart", "minecraft.service", false, contextText)
		switch availability.Reason {
		case "restart_limit_reached":
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":                        "restart allowance exhausted",
				"reason":                       availability.Reason,
				"restart_used":                 availability.Used,
				"restart_limit":                availability.Limit,
				"administrator_reset_required": true,
			})
		case "restart_cooldown":
			seconds := int(math.Ceil(availability.RetryAfter.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error":               "restart cooldown active",
				"reason":              availability.Reason,
				"restart_used":        availability.Used,
				"restart_limit":       availability.Limit,
				"retry_after_seconds": seconds,
			})
		default:
			writeError(w, http.StatusConflict, "operator restart is not currently available")
		}
		return
	}

	args := []string{"restart"}
	if request.ConfirmPlayers {
		args = append(args, "--confirm-players")
	}
	execution := executeMinecraftAction(r.Context(), args)
	if !execution.Accepted {
		guard.cancel()
		if execution.Status >= 400 {
			contextText := execution.Result.Reason
			if contextText == "" {
				contextText = execution.ErrorMessage
			}
			_ = s.store.recordAuditEvent(sess, "minecraft_restart", "minecraft.service", false, contextText)
		}
		writeMinecraftActionExecution(w, execution)
		return
	}

	used, err := guard.accept(sess)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Minecraft restart was requested, but restart allowance accounting failed")
		return
	}
	execution.Result.OperatorUsage = &operatorRestartUsageView{
		RestartUsed:     used,
		RestartLimit:    operatorRestartLimit,
		CooldownSeconds: int(operatorRestartCooldown.Seconds()),
	}
	writeJSON(w, execution.Status, execution.Result)
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
