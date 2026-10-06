package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

func (s *server) plusSetupDeploy(w http.ResponseWriter, r *http.Request) {
	if !s.plus {
		http.NotFound(w, r)
		return
	}
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	var body struct{}
	if !decodePlusSetup(w, r, &body) {
		return
	}
	if s.plusResetBlocksSetup(w, r) {
		return
	}
	s.plusSetupMu.Lock()
	defer s.plusSetupMu.Unlock()
	p := defaultPlusDeploymentPaths()
	lock, err := plusPreparationLock(p.Preparation)
	if err != nil {
		writeError(w, http.StatusConflict, "setup is already running")
		return
	}
	defer lock.Close()
	deployed, err := plusSetupDeployed()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "setup state is unavailable")
		return
	}
	if deployed {
		writeError(w, http.StatusConflict, "this appliance is already deployed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if plusSetupUnitRunning(ctx) {
		writeError(w, http.StatusConflict, "setup is already running")
		return
	}
	var prepared plusSetupRequest
	if err := readPlusJSON(filepath.Join(p.Preparation, "setup.json"), &prepared, true); err != nil {
		writeError(w, http.StatusConflict, "complete the setup questions first")
		return
	}
	status := plusDeploymentStatus{State: "running", Stage: "queued", Message: "Application deployment queued.", Started: true, Components: prepared.Components}
	if err := savePlusDeploymentStatus(p, status); err != nil {
		writeError(w, http.StatusInternalServerError, "deployment could not be queued")
		return
	}
	// The worker takes this same lock. Release it before asking systemd to start.
	if err := lock.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, "setup lock could not be released")
		return
	}
	if _, err := runPlusDeploymentCommand(ctx, "systemctl", []string{"start", "--no-block", plusSetupUnit}, nil); err != nil {
		status.State = "failed"
		status.Message = "The setup service could not be started."
		_ = savePlusDeploymentStatus(p, status)
		writeError(w, http.StatusServiceUnavailable, "the setup service could not be started")
		return
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "deploy_plus_stack", "plus", true, "application deployment requested")
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "deployment": status})
}

func plusPublicApplications(p plusDeploymentPaths) (plusDeploymentStatus, error) {
	var status plusDeploymentStatus
	err := readPlusJSON(filepath.Join(p.Configuration, "deployed"), &status, false)
	if os.IsNotExist(err) {
		return status, nil
	}
	if err != nil {
		return plusDeploymentStatus{}, err
	}
	for _, address := range []string{status.PanelURL, status.DrydockURL} {
		if address == "" {
			continue
		}
		u, err := url.Parse(address)
		if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
			return plusDeploymentStatus{}, os.ErrInvalid
		}
	}
	return status, nil
}

func (s *server) plusApplications(w http.ResponseWriter, r *http.Request) {
	if !s.plus {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	applications, err := plusPublicApplications(defaultPlusDeploymentPaths())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "application addresses are unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": applications.State == "succeeded", "panel_url": applications.PanelURL, "drydock_url": applications.DrydockURL})
}

func plusStatusWithApplications(output []byte, p plusDeploymentPaths) ([]byte, error) {
	var status map[string]any
	if err := json.Unmarshal(output, &status); err != nil {
		return nil, err
	}
	applications, err := plusPublicApplications(p)
	if err != nil {
		return nil, err
	}
	plus, ok := status["plus"].(map[string]any)
	if !ok {
		plus = map[string]any{}
	}
	plus["configured"] = applications.State == "succeeded"
	plus["panel_url"] = applications.PanelURL
	plus["drydock_url"] = applications.DrydockURL
	status["plus"] = plus
	return json.Marshal(status)
}
