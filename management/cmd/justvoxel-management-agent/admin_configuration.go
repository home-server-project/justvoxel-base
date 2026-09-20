package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"time"
)

type adminConfigurationChangeRequest struct {
	JavaMemory        string `json:"java_memory"`
	ContainerMemory   string `json:"container_memory"`
	JavaPort          int    `json:"java_port"`
	BedrockEnabled    bool   `json:"bedrock_enabled"`
	BedrockPort       int    `json:"bedrock_port"`
	Timezone          string `json:"timezone"`
	MaxPlayers        int    `json:"max_players"`
	MOTD              string `json:"motd"`
	ImageTag          string `json:"image_tag"`
	VersionPolicy     string `json:"version_policy"`
	Version           string `json:"version"`
	BackupKeep        int    `json:"backup_keep"`
	BackupSchedule    string `json:"backup_schedule"`
	BackupTimerEnabled bool  `json:"backup_timer_enabled"`
	ConfirmPlayers      bool  `json:"confirm_players"`
}

type adminConfigurationChange struct {
	Field           string `json:"field"`
	Label           string `json:"label"`
	Before          string `json:"before"`
	After           string `json:"after"`
	RestartRequired bool   `json:"restart_required"`
}

type adminConfigurationChangeResponse struct {
	OK                 bool                        `json:"ok"`
	Error              string                      `json:"error,omitempty"`
	Changes            []adminConfigurationChange  `json:"changes"`
	Warnings           []string                    `json:"warnings"`
	RestartRequired    bool                        `json:"restart_required"`
	MemoryRestartRequired bool                        `json:"memory_restart_required"`
	MemoryRemainingMiB   int                         `json:"memory_remaining_mib"`
	Proposed             adminConfigurationDiscovery `json:"proposed"`
	Applied              bool                        `json:"applied"`
	ConfirmationRequired bool                        `json:"confirmation_required"`
	Online               int                         `json:"online"`
	Players              []string                    `json:"players"`
	Restarted            bool                        `json:"restarted"`
	RestartDeferred      bool                        `json:"restart_deferred"`
	Message              string                      `json:"message,omitempty"`
}

var runAdminConfigurationHelper = func(ctx context.Context, action string, request []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, adminDiscoveryHelper, action)
	cmd.Stdin = bytes.NewReader(request)
	return cmd.CombinedOutput()
}

func registerAdminConfigurationRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("POST /v1/admin/configuration/plan", s.adminConfigurationPlan)
	mux.HandleFunc("POST /v1/admin/configuration/apply", s.adminConfigurationApply)
}

func (s *server) adminConfigurationPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	s.runAdminConfigurationChange(w, r, "plan", session{})
}

func (s *server) adminConfigurationApply(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	s.runAdminConfigurationChange(w, r, "apply", actor)
}

func (s *server) runAdminConfigurationChange(w http.ResponseWriter, r *http.Request, action string, actor session) {
	var request adminConfigurationChangeRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	payload, err := json.Marshal(request)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "configuration request could not be prepared")
		return
	}
	timeout := 25 * time.Second
	if action == "apply" {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	output, err := runAdminConfigurationHelper(ctx, action, payload)
	if err != nil {
		if action == "apply" {
			_ = s.store.recordAuditEvent(actor, "update_minecraft_configuration", "justvoxel.conf", false, "configuration apply failed")
		}
		writeError(w, http.StatusServiceUnavailable, "configuration operation failed; previous settings were preserved")
		return
	}
	var out adminConfigurationChangeResponse
	if err := json.Unmarshal(output, &out); err != nil {
		writeError(w, http.StatusInternalServerError, "configuration operation returned invalid data")
		return
	}
	if out.Changes == nil {
		out.Changes = []adminConfigurationChange{}
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	if !out.OK {
		if out.Error == "" {
			out.Error = "configuration could not be validated"
		}
		writeJSON(w, http.StatusBadRequest, out)
		return
	}
	if action == "apply" && out.Applied {
		context := fmt.Sprintf("changes=%d restart_required=%t memory_restart_required=%t restarted=%t restart_deferred=%t", len(out.Changes), out.RestartRequired, out.MemoryRestartRequired, out.Restarted, out.RestartDeferred)
		_ = s.store.recordAuditEvent(actor, "update_minecraft_configuration", "justvoxel.conf", true, context)
	}
	writeJSON(w, http.StatusOK, out)
}
