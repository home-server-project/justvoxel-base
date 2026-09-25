package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

type bootcImageReference struct {
	Image string `json:"image"`
}

type bootcDeploymentImage struct {
	Image       bootcImageReference `json:"image"`
	Version     string              `json:"version"`
	ImageDigest string              `json:"imageDigest"`
}

type bootcDeployment struct {
	Image        bootcDeploymentImage  `json:"image"`
	CachedUpdate *bootcDeploymentImage `json:"cachedUpdate,omitempty"`
}

type bootcStatusPayload struct {
	Booted   *bootcDeployment `json:"booted"`
	Staged   *bootcDeployment `json:"staged"`
	Rollback *bootcDeployment `json:"rollback"`
	ReadOnly bool             `json:"readOnly"`
}

type bootcStatusDocument struct {
	APIVersion string             `json:"apiVersion"`
	Status     bootcStatusPayload `json:"status"`
}

type adminSystemUpdateDeployment struct {
	Image   string `json:"image,omitempty"`
	Version string `json:"version,omitempty"`
	Digest  string `json:"digest"`
}

type adminSystemUpdateStatus struct {
	OK             bool                         `json:"ok"`
	ReadOnly       bool                         `json:"read_only"`
	RebootRequired bool                         `json:"reboot_required"`
	Checked        bool                         `json:"checked,omitempty"`
	CheckState     string                       `json:"check_state,omitempty"`
	Running        adminSystemUpdateDeployment  `json:"running"`
	Staged         *adminSystemUpdateDeployment `json:"staged,omitempty"`
	Available      *adminSystemUpdateDeployment `json:"available,omitempty"`
	Rollback       *adminSystemUpdateDeployment `json:"rollback,omitempty"`
	Message        string                       `json:"message,omitempty"`
}

var runBootcStatus = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "bootc", "status", "--json", "--format-version=1").Output()
}

var runBootcUpgrade = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "bootc", "upgrade").CombinedOutput()
}

var runBootcUpgradeCheck = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "bootc", "upgrade", "--check").CombinedOutput()
}

func registerAdminSystemUpdateRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/system/updates", s.adminSystemUpdateStatus)
	mux.HandleFunc("POST /v1/admin/system/updates/check", s.adminSystemUpdateCheck)
	mux.HandleFunc("POST /v1/admin/system/updates", s.adminSystemUpdate)
}

func bootcImageForAPI(value *bootcDeploymentImage) *adminSystemUpdateDeployment {
	if value == nil || value.ImageDigest == "" {
		return nil
	}
	return &adminSystemUpdateDeployment{
		Image:   value.Image.Image,
		Version: value.Version,
		Digest:  value.ImageDigest,
	}
}

func bootcDeploymentForAPI(value *bootcDeployment) *adminSystemUpdateDeployment {
	if value == nil {
		return nil
	}
	return bootcImageForAPI(&value.Image)
}

func collectAdminSystemUpdateStatus(ctx context.Context) (adminSystemUpdateStatus, error) {
	output, err := runBootcStatus(ctx)
	if err != nil {
		return adminSystemUpdateStatus{}, err
	}

	var document bootcStatusDocument
	if err := json.Unmarshal(output, &document); err != nil {
		return adminSystemUpdateStatus{}, err
	}
	if document.APIVersion != "org.containers.bootc/v1" {
		return adminSystemUpdateStatus{}, errors.New("unexpected bootc status API version")
	}

	running := bootcDeploymentForAPI(document.Status.Booted)
	if running == nil {
		return adminSystemUpdateStatus{}, errors.New("bootc status did not include a running deployment")
	}
	staged := bootcDeploymentForAPI(document.Status.Staged)
	rollback := bootcDeploymentForAPI(document.Status.Rollback)
	available := bootcImageForAPI(document.Status.Booted.CachedUpdate)

	return adminSystemUpdateStatus{
		OK:             true,
		ReadOnly:       document.Status.ReadOnly,
		RebootRequired: staged != nil && staged.Digest != running.Digest,
		Running:        *running,
		Staged:         staged,
		Available:      available,
		Rollback:       rollback,
	}, nil
}

func (s *server) adminSystemUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, err := collectAdminSystemUpdateStatus(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "system update status is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func parseBootcUpgradeCheck(output string) (string, *adminSystemUpdateDeployment) {
	trimmed := strings.TrimSpace(output)
	if strings.Contains(trimmed, "Update already staged.") ||
		strings.Contains(trimmed, "No changes in staged image:") ||
		strings.Contains(trimmed, "No changes in:") ||
		strings.Contains(trimmed, "No update available.") {
		return "no_changes", nil
	}

	available := &adminSystemUpdateDeployment{}
	if strings.Contains(trimmed, "Update available for:") {
		for _, line := range strings.Split(trimmed, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "Version:"):
				available.Version = strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
			case strings.HasPrefix(line, "Digest:"):
				available.Digest = strings.TrimSpace(strings.TrimPrefix(line, "Digest:"))
			}
		}
		return "update_available", available
	}

	// The composefs backend currently prints only the manifest diff for --check.
	// Reaching this output means the registry manifest differs from the deployed/staged image.
	if strings.Contains(trimmed, "Total new layers:") &&
		strings.Contains(trimmed, "Removed layers:") &&
		strings.Contains(trimmed, "Added layers:") {
		return "update_available", nil
	}
	return "unknown", nil
}

func finalizeAdminSystemUpdateCheck(status adminSystemUpdateStatus, output string) adminSystemUpdateStatus {
	status.Checked = true
	outputState, parsed := parseBootcUpgradeCheck(output)

	if outputState == "update_available" && parsed != nil {
		if parsed.Image == "" {
			parsed.Image = status.Running.Image
		}
		if parsed.Digest != "" || parsed.Version != "" {
			status.Available = parsed
		}
	} else if outputState == "no_changes" {
		status.Available = nil
	}

	switch {
	case status.Available != nil && status.Staged != nil && status.Available.Digest != "" && status.Available.Digest == status.Staged.Digest:
		status.CheckState = "staged_current"
		status.Message = "The latest registry image is already staged. Reboot when convenient to use it."
	case outputState == "update_available":
		status.CheckState = "update_available"
		if status.Staged != nil {
			status.Message = "A newer JustVoxel image is available than the currently staged update."
		} else {
			status.Message = "A newer JustVoxel image is available."
		}
	case outputState == "no_changes" && status.RebootRequired:
		status.CheckState = "staged_current"
		status.Message = "The latest registry image is already staged. Reboot when convenient to use it."
	case outputState == "no_changes":
		status.CheckState = "current"
		status.Message = "JustVoxel is up to date."
	case status.Available != nil && status.Available.Digest != "" && status.Available.Digest != status.Running.Digest:
		if status.Staged != nil && status.Available.Digest == status.Staged.Digest {
			status.CheckState = "staged_current"
			status.Message = "The latest registry image is already staged. Reboot when convenient to use it."
		} else {
			status.CheckState = "update_available"
			if status.Staged != nil {
				status.Message = "A newer JustVoxel image is available than the currently staged update."
			} else {
				status.Message = "A newer JustVoxel image is available."
			}
		}
	default:
		status.CheckState = "unknown"
		status.Message = "Registry check completed, but update availability could not be determined safely."
	}
	return status
}

func sanitizeBootcCheckFailure(output []byte) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.Join(strings.Fields(line), " ")
		if len(line) > 240 {
			line = line[:240] + "…"
		}
		clean = append(clean, line)
		if len(clean) >= 4 {
			break
		}
	}
	result := strings.Join(clean, " · ")
	if len(result) > 700 {
		result = result[:700] + "…"
	}
	return result
}

func (s *server) adminSystemUpdateCheck(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	if !s.systemUpdateMu.TryLock() {
		writeError(w, http.StatusConflict, "a system update operation is already in progress")
		return
	}
	defer s.systemUpdateMu.Unlock()

	statusCtx, statusCancel := context.WithTimeout(r.Context(), 10*time.Second)
	before, err := collectAdminSystemUpdateStatus(statusCtx)
	statusCancel()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "system update status is unavailable")
		return
	}
	if before.ReadOnly {
		writeError(w, http.StatusConflict, "this bootc system is read-only; update checks are unavailable")
		return
	}

	checkCtx, checkCancel := context.WithTimeout(r.Context(), 2*time.Minute)
	output, err := runBootcUpgradeCheck(checkCtx)
	checkCancel()
	if err != nil {
		message := "bootc update check failed"
		if detail := sanitizeBootcCheckFailure(output); detail != "" {
			message += ": " + detail
		}
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "system_update_check", "host", false, message)
		}
		writeError(w, http.StatusServiceUnavailable, message)
		return
	}

	afterCtx, afterCancel := context.WithTimeout(r.Context(), 10*time.Second)
	after, err := collectAdminSystemUpdateStatus(afterCtx)
	afterCancel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update check completed but deployment status could not be verified")
		return
	}
	after = finalizeAdminSystemUpdateCheck(after, string(output))
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "system_update_check", "host", true, after.Message)
	}
	writeJSON(w, http.StatusOK, after)
}

func (s *server) adminSystemUpdate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	if !s.systemUpdateMu.TryLock() {
		writeError(w, http.StatusConflict, "a system update is already in progress")
		return
	}
	defer s.systemUpdateMu.Unlock()

	statusCtx, statusCancel := context.WithTimeout(r.Context(), 10*time.Second)
	before, err := collectAdminSystemUpdateStatus(statusCtx)
	statusCancel()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "system update status is unavailable")
		return
	}
	if before.ReadOnly {
		writeError(w, http.StatusConflict, "this bootc system is read-only; OS updates are unavailable")
		return
	}

	upgradeCtx, upgradeCancel := context.WithTimeout(r.Context(), 15*time.Minute)
	_, err = runBootcUpgrade(upgradeCtx)
	upgradeCancel()
	if err != nil {
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "system_update", "host", false, "bootc upgrade failed")
		}
		writeError(w, http.StatusServiceUnavailable, "bootc upgrade failed")
		return
	}

	afterCtx, afterCancel := context.WithTimeout(r.Context(), 10*time.Second)
	after, err := collectAdminSystemUpdateStatus(afterCtx)
	afterCancel()
	if err != nil {
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "system_update", "host", false, "bootc upgrade completed but deployment status could not be verified")
		}
		writeError(w, http.StatusInternalServerError, "bootc upgrade completed but deployment status could not be verified")
		return
	}

	after.Checked = true
	if after.RebootRequired {
		after.CheckState = "staged_current"
		after.Available = nil
		after.Message = "The latest JustVoxel image is downloaded and staged. Reboot when convenient to use it."
	} else {
		after.CheckState = "current"
		after.Available = nil
		after.Message = "JustVoxel is up to date."
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "system_update", "host", true, after.Message)
	}
	writeJSON(w, http.StatusOK, after)
}
