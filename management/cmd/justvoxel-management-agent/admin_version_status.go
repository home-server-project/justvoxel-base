package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"regexp"
	"time"
)

type adminVersionStatus struct {
	ServerType             string `json:"server_type"`
	ServerSupported        bool   `json:"server_supported"`
	StackState             string `json:"stack_state"`
	MinecraftState         string `json:"minecraft_state"`
	GeyserState            string `json:"geyser_state"`
	FloodgateState         string `json:"floodgate_state"`
	ViaVersionState        string `json:"viaversion_state"`
	ImageState             string `json:"image_state"`
	PlanFingerprint        string `json:"plan_fingerprint"`
	ServerSoftware         string `json:"server_software"`
	Installed              string `json:"installed"`
	Available              string `json:"available"`
	AvailableChannel       string `json:"available_channel"`
	Recommended            string `json:"recommended"`
	SelectedCandidate      string `json:"selected_candidate"`
	CandidateChannel       string `json:"candidate_channel"`
	ImageTag               string `json:"image_tag"`
	Policy                 string `json:"policy"`
	ConfiguredVersion      string `json:"configured_version"`
	UpdateAvailable        bool   `json:"update_available"`
	PaperSupported         bool   `json:"paper_supported"`
	CrossplayEnabled       bool   `json:"crossplay_enabled"`
	GeyserSupportedVersion string `json:"geyser_supported_version"`
	CrossplayCompatible    bool   `json:"crossplay_compatible"`
	Reason                 string `json:"reason"`
}

var versionInputPattern = regexp.MustCompile(`^[0-9A-Za-z._-]+$`)
var runAdminVersionStatus = func(ctx context.Context, policy, version, serverType string) ([]byte, error) {
	args := []string{policy, version}
	if serverType != "" {
		args = append([]string{"--server-type", serverType}, args...)
	}
	return exec.CommandContext(ctx, "/usr/libexec/justvoxel/mjust/admin-version-status-json", args...).CombinedOutput()
}
var runSetupVersionPreview = func(ctx context.Context, policy, version, bedrock, serverType string) ([]byte, error) {
	args := []string{policy, version, bedrock}
	if serverType != "" {
		args = append([]string{"--server-type", serverType}, args...)
	}
	return exec.CommandContext(ctx, "/usr/libexec/justvoxel/mjust/admin-version-status-json", args...).CombinedOutput()
}

func registerAdminVersionStatusRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/version/status", s.adminVersionStatus)
	mux.HandleFunc("POST /v1/admin/version/update", s.adminMinecraftStackUpdate)
	mux.HandleFunc("GET /v1/admin/version/update/current", s.adminMinecraftStackUpdateCurrent)
	mux.HandleFunc("POST /v1/admin/version/update/acknowledge", s.adminMinecraftStackUpdateAcknowledge)
	mux.HandleFunc("GET /v1/admin/setup/version-preview", s.adminSetupVersionPreview)
}

func (s *server) adminSetupVersionPreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	policy, version, bedrock := r.URL.Query().Get("policy"), r.URL.Query().Get("version"), r.URL.Query().Get("bedrock")
	if policy != "recommended" && policy != "latest" && policy != "pinned" {
		writeError(w, http.StatusBadRequest, "Invalid version policy")
		return
	}
	if version != "" && (len(version) > 64 || !versionInputPattern.MatchString(version)) {
		writeError(w, http.StatusBadRequest, "Invalid Minecraft version")
		return
	}
	if bedrock != "yes" && bedrock != "no" {
		writeError(w, http.StatusBadRequest, "Invalid Bedrock choice")
		return
	}
	serverType := r.URL.Query().Get("server_type")
	if serverType != "" && !validSetupServerType(serverType) {
		writeError(w, http.StatusBadRequest, "Invalid server type")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	output, err := runSetupVersionPreview(ctx, policy, version, bedrock, serverType)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Version information is unavailable")
		return
	}
	var out adminVersionStatus
	if err := json.Unmarshal(output, &out); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Version information is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) adminVersionStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	serverType := r.URL.Query().Get("server_type")
	if serverType != "" && !validSetupServerType(serverType) {
		writeError(w, http.StatusBadRequest, "Invalid server type")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	policy, version := r.URL.Query().Get("policy"), r.URL.Query().Get("version")
	if policy != "" && policy != "recommended" && policy != "latest" && policy != "pinned" {
		writeError(w, http.StatusBadRequest, "Invalid version policy")
		return
	}
	if version != "" && (len(version) > 64 || !versionInputPattern.MatchString(version)) {
		writeError(w, http.StatusBadRequest, "Invalid Minecraft version")
		return
	}
	output, err := runAdminVersionStatus(ctx, policy, version, serverType)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Version information is unavailable")
		return
	}
	var out adminVersionStatus
	if err := json.Unmarshal(output, &out); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Version information is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, out)
}
