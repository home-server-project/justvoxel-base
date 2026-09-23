package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
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
	Image bootcDeploymentImage `json:"image"`
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
	Running        adminSystemUpdateDeployment  `json:"running"`
	Staged         *adminSystemUpdateDeployment `json:"staged,omitempty"`
	Rollback       *adminSystemUpdateDeployment `json:"rollback,omitempty"`
	Message        string                       `json:"message,omitempty"`
}

var runBootcStatus = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "bootc", "status", "--json", "--format-version=1").Output()
}

var runBootcUpgrade = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "bootc", "upgrade").CombinedOutput()
}

func registerAdminSystemUpdateRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/system/updates", s.adminSystemUpdateStatus)
	mux.HandleFunc("POST /v1/admin/system/updates", s.adminSystemUpdate)
}

func bootcDeploymentForAPI(value *bootcDeployment) *adminSystemUpdateDeployment {
	if value == nil || value.Image.ImageDigest == "" {
		return nil
	}
	return &adminSystemUpdateDeployment{
		Image:   value.Image.Image.Image,
		Version: value.Image.Version,
		Digest:  value.Image.ImageDigest,
	}
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

	return adminSystemUpdateStatus{
		OK:             true,
		ReadOnly:       document.Status.ReadOnly,
		RebootRequired: staged != nil && staged.Digest != running.Digest,
		Running:        *running,
		Staged:         staged,
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

	if after.RebootRequired {
		after.Message = "JustVoxel OS update is staged. Reboot when convenient to use it."
	} else {
		after.Message = "JustVoxel OS is current."
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "system_update", "host", true, after.Message)
	}
	writeJSON(w, http.StatusOK, after)
}
