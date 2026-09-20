package main

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const whitelistHelper = "/usr/libexec/justvoxel/mjust/whitelist-backend"

var runWhitelistHelper = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, whitelistHelper, args...).CombinedOutput()
}

var readMinecraftLogs = func(ctx context.Context, limit int, outputMode string) ([]byte, error) {
	return exec.CommandContext(ctx, "journalctl", "--no-pager", "-u", "minecraft.service", "-n", strconv.Itoa(limit), "-o", outputMode).CombinedOutput()
}

type publicActivityView struct {
	OccurredAt string `json:"occurred_at"`
	Action     string `json:"action"`
	Success    bool   `json:"success"`
}

func registerOperationalRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/whitelist", s.whitelistList)
	mux.HandleFunc("POST /v1/whitelist", s.whitelistChange)
	mux.HandleFunc("GET /v1/logs/minecraft", s.minecraftLogs)
	mux.HandleFunc("GET /v1/activity", s.publicActivity)
}

func (s *server) whitelistList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireRoles(w, r, roleAdministrator, roleOperator); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	output, err := runWhitelistHelper(ctx, "list")
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "whitelist is unavailable", "output": strings.TrimSpace(string(output))})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": strings.TrimSpace(string(output))})
}

func (s *server) whitelistChange(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireRoles(w, r, roleAdministrator, roleOperator)
	if !ok {
		return
	}
	var request struct {
		Platform string `json:"platform"`
		Action   string `json:"action"`
		Name     string `json:"name"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	platform := strings.ToLower(strings.TrimSpace(request.Platform))
	action := strings.ToLower(strings.TrimSpace(request.Action))
	name := strings.TrimSpace(request.Name)
	if (platform != "java" && platform != "bedrock") || (action != "add" && action != "remove") || name == "" {
		writeError(w, http.StatusBadRequest, "platform, action, and player name are invalid")
		return
	}
	if platform == "bedrock" {
		name = strings.TrimPrefix(name, ".")
		if name == "" {
			writeError(w, http.StatusBadRequest, "Bedrock player name or Floodgate UUID is required")
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	listOutput, listErr := runWhitelistHelper(ctx, "list")
	if listErr != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "whitelist is unavailable"})
		return
	}
	present := whitelistContains(string(listOutput), platform, name)
	if action == "add" && present {
		message := name + " is already on the whitelist."
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "whitelist_add", name, true, platform+" already_present")
		}
		writeJSON(w, http.StatusOK, map[string]string{"output": message})
		return
	}
	if action == "remove" && !present {
		message := name + " is not currently on the whitelist."
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "whitelist_remove", name, true, platform+" already_absent")
		}
		writeJSON(w, http.StatusOK, map[string]string{"output": message})
		return
	}

	helperAction := action + "-" + platform
	output, err := runWhitelistHelper(ctx, helperAction, name)
	if err != nil {
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "whitelist_"+action, name, false, platform)
		}
		message := whitelistChangeError(platform, string(output))
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": message})
		return
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "whitelist_"+action, name, true, platform)
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": strings.TrimSpace(string(output))})
}

func whitelistContains(output, platform, name string) bool {
	lowerOutput := strings.ToLower(output)
	candidates := []string{name}
	if platform == "bedrock" {
		candidates = append(candidates, "."+name)
	}
	for _, candidate := range candidates {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" {
			continue
		}
		for _, token := range strings.FieldsFunc(lowerOutput, func(r rune) bool {
			return r == ',' || r == ':' || r == '\n' || r == '\r' || r == '\t' || r == ' '
		}) {
			if strings.TrimSpace(token) == candidate {
				return true
			}
		}
	}
	return false
}

func whitelistChangeError(platform, output string) string {
	text := strings.TrimSpace(output)
	lower := strings.ToLower(text)
	if platform == "bedrock" && (strings.Contains(lower, "unable to find user in our cache") || strings.Contains(lower, "floodgate could not resolve")) {
		return "Floodgate could not resolve this Bedrock player. Use the Xbox gamertag without the leading '.' or enter the player's Floodgate UUID."
	}
	for _, prefix := range []string{
		"Java player name must",
		"Invalid Bedrock/Xbox gamertag",
		"Invalid Bedrock/Xbox gamertag or Floodgate UUID",
		"Minecraft must be running",
	} {
		if strings.HasPrefix(text, prefix) {
			return text
		}
	}
	return "Whitelist change was rejected."
}

func (s *server) minecraftLogs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireRoles(w, r, roleAdministrator, roleOperator); !ok {
		return
	}
	limit := boundedQueryLimit(r, 100, 200)
	outputMode := "short-iso"
	if r.URL.Query().Get("format") == "cat" {
		outputMode = "cat"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	output, err := readMinecraftLogs(ctx, limit, outputMode)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Minecraft logs are unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": strings.Split(strings.TrimRight(string(output), "\n"), "\n")})
}

func (s *server) publicActivity(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	limit := boundedQueryLimit(r, 30, 100)
	rows, err := s.store.db.Query(`SELECT occurred_at, action, success FROM audit_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "activity history is unavailable")
		return
	}
	defer rows.Close()
	items := make([]publicActivityView, 0)
	for rows.Next() {
		var item publicActivityView
		var success int
		if err := rows.Scan(&item.OccurredAt, &item.Action, &success); err != nil {
			writeError(w, http.StatusServiceUnavailable, "activity history is unavailable")
			return
		}
		item.Success = success == 1
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "activity history is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": items})
}

func operationalAuditContext(platform string) string {
	return fmt.Sprintf("platform=%s", platform)
}
