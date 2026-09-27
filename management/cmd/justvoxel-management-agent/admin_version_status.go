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
	ServerSoftware string `json:"server_software"`
	Installed string `json:"installed"`
	Available string `json:"available"`
	Recommended string `json:"recommended"`
	SelectedCandidate string `json:"selected_candidate"`
	ImageTag string `json:"image_tag"`
	Policy string `json:"policy"`
	ConfiguredVersion string `json:"configured_version"`
	UpdateAvailable bool `json:"update_available"`
	PaperSupported bool `json:"paper_supported"`
	CrossplayEnabled bool `json:"crossplay_enabled"`
	GeyserSupportedVersion string `json:"geyser_supported_version"`
	CrossplayCompatible bool `json:"crossplay_compatible"`
	Reason string `json:"reason"`
}

var versionInputPattern = regexp.MustCompile(`^[0-9A-Za-z._-]+$`)
var runAdminVersionStatus = func(ctx context.Context, policy, version string) ([]byte, error) {
	return exec.CommandContext(ctx, "/usr/libexec/justvoxel/mjust/admin-version-status-json", policy, version).CombinedOutput()
}

func registerAdminVersionStatusRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/version/status", s.adminVersionStatus)
}

func (s *server) adminVersionStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok { return }
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	policy, version := r.URL.Query().Get("policy"), r.URL.Query().Get("version")
	if policy != "" && policy != "recommended" && policy != "latest" && policy != "pinned" { writeError(w, http.StatusBadRequest, "Invalid version policy"); return }
	if version != "" && (len(version) > 64 || !versionInputPattern.MatchString(version)) { writeError(w, http.StatusBadRequest, "Invalid Minecraft version"); return }
	output, err := runAdminVersionStatus(ctx, policy, version)
	if err != nil { writeError(w, http.StatusServiceUnavailable, "Version information is unavailable"); return }
	var out adminVersionStatus
	if err := json.Unmarshal(output, &out); err != nil { writeError(w, http.StatusServiceUnavailable, "Version information is unavailable"); return }
	writeJSON(w, http.StatusOK, out)
}
