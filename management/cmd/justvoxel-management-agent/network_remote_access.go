package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

type remoteProvider struct {
	ID           string `json:"id"`
	Name         string `json:"display_name"`
	Installed    bool   `json:"installed"`
	Enabled      bool   `json:"service_enabled"`
	Active       bool   `json:"service_active"`
	Configured   bool   `json:"configured"`
	Dashboard    string `json:"dashboard_url"`
	ServiceState string `json:"service_state"`
	Summary      string `json:"summary"`
	Connected    bool   `json:"connected"`
}

type remoteProviderMetadata struct {
	id, name, unit, dashboard string
	config                    []string
}

var remoteProviders = []remoteProviderMetadata{
	{"tailscale", "Tailscale", "tailscaled.service", "https://console.tailscale.com/admin/", nil},
	{"netbird", "NetBird", "netbird.service", "https://app.netbird.io/", []string{"/var/lib/netbird/default.json", "/etc/netbird/config.json", "/etc/netbird/config.yaml"}},
	{"playit", "Playit.gg", "playit.service", "https://playit.gg/account/", []string{"/etc/playit/playit.toml"}},
}

var remoteStat = os.Stat
var tailscaleStatusCommand = exec.CommandContext
var tailscaleStatus = func(ctx context.Context) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := tailscaleStatusCommand(ctx, "/usr/bin/tailscale", "status", "--json")
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil || ctx.Err() != nil {
		return false, "Not configured"
	}
	var status struct {
		BackendState   string
		HaveNodeKey    bool
		CurrentTailnet *struct{}
	}
	if json.Unmarshal(output, &status) != nil {
		return false, "Not configured"
	}
	switch status.BackendState {
	case "Running":
		return true, "Connected"
	case "NeedsMachineAuth":
		return true, "Awaiting approval"
	case "Starting", "Stopped":
		if status.HaveNodeKey || status.CurrentTailnet != nil {
			if status.BackendState == "Starting" {
				return true, "Starting"
			}
			return true, "Stopped"
		}
	}
	return false, "Not configured"
}
var remoteSystemctl = func(ctx context.Context, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, "systemctl", args...).Output()
	return strings.TrimSpace(string(output)), err
}

func remoteProviderByID(id string) (remoteProviderMetadata, bool) {
	for _, provider := range remoteProviders {
		if provider.id == id {
			return provider, true
		}
	}
	return remoteProviderMetadata{}, false
}

func remoteConfigured(provider remoteProviderMetadata) bool {
	if provider.id == "tailscale" {
		return false // Only fixed CLI status provides Tailscale identity evidence.
	}
	for _, path := range provider.config {
		if info, err := remoteStat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return true
		}
	}
	return false
}

func remoteProviderStatus(ctx context.Context, provider remoteProviderMetadata) (remoteProvider, error) {
	output, err := remoteSystemctl(ctx, "show", "--property=LoadState,UnitFileState,ActiveState", provider.unit)
	if err != nil {
		return remoteProvider{}, err
	}
	properties := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			properties[key] = value
		}
	}
	if properties["LoadState"] == "" || properties["ActiveState"] == "" {
		return remoteProvider{}, os.ErrInvalid
	}
	status := remoteProvider{
		ID: provider.id, Name: provider.name, Dashboard: provider.dashboard,
		Installed:    properties["LoadState"] == "loaded",
		Enabled:      properties["UnitFileState"] == "enabled",
		Active:       properties["ActiveState"] == "active",
		ServiceState: properties["ActiveState"], Configured: remoteConfigured(provider),
	}
	status.Summary = "Not configured"
	if provider.id == "tailscale" {
		status.Configured, status.Summary = tailscaleStatus(ctx)
		status.Connected = status.Active && status.Summary == "Connected"
		if status.Configured && !status.Active && status.Summary == "Connected" {
			status.Summary = "Stopped"
		}
	} else if status.Configured {
		status.Summary = "Stopped"
		if provider.id == "playit" {
			status.Summary = "Configured"
		}
		if status.Active {
			status.Connected = true
			status.Summary = "Connected"
			if provider.id == "playit" {
				status.Summary = "Running"
			}
		}
	}
	return status, nil
}

func (s *server) networkRemoteAccessStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	providers := make([]remoteProvider, 0, len(remoteProviders))
	for _, provider := range remoteProviders {
		status, err := remoteProviderStatus(ctx, provider)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "remote-access service status is unavailable")
			return
		}
		if provider.id == "playit" && !status.Configured {
			playitSetup.mu.Lock()
			if playitSetup.state.running() {
				status.Summary = "Setup pending"
			}
			playitSetup.mu.Unlock()
		}
		providers = append(providers, status)
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
}

func (s *server) networkRemoteAccessChange(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	provider, ok := remoteProviderByID(r.PathValue("provider"))
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown remote-access provider")
		return
	}
	var request struct {
		Action string `json:"action"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Action != "activate" && request.Action != "deactivate" {
		writeError(w, http.StatusBadRequest, "action must be activate or deactivate")
		return
	}
	playitSetup.operation.Lock()
	defer playitSetup.operation.Unlock()
	playitSetup.mu.Lock()
	runningSetup := provider.id == "playit" && playitSetup.state.running()
	playitSetup.mu.Unlock()
	if runningSetup && request.Action == "activate" {
		writeError(w, http.StatusConflict, "Playit setup is in progress")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if provider.id == "playit" && request.Action == "deactivate" {
		playitSetup.mu.Lock()
		var done chan struct{}
		if playitSetup.cancel != nil {
			playitSetup.cancel()
			done = playitSetup.done
		}
		playitSetup.generation++
		playitSetup.cancel = nil
		playitSetup.state = playitSetupState{State: "idle"}
		playitSetup.mu.Unlock()
		if done != nil {
			select {
			case <-done:
			case <-ctx.Done():
				if s.store != nil {
					_ = s.store.recordAuditEvent(actor, "network_remote_access_deactivate", provider.id, false, "fixed provider service lifecycle")
				}
				writeError(w, http.StatusServiceUnavailable, "Playit setup cancellation timed out")
				return
			}
		}
	}
	status, err := remoteProviderStatus(ctx, provider)
	if err == nil && !status.Installed {
		err = os.ErrNotExist
	}
	if err == nil && (status.ServiceState == "deactivating" || (request.Action == "activate" && remoteTransitional(status.ServiceState))) {
		writeError(w, http.StatusConflict, "provider service is changing state; refresh service status")
		return
	}
	if err == nil {
		if request.Action == "activate" {
			err = remoteActivate(ctx, provider)
		} else {
			// Stop first: an interrupted operation must not leave an active service
			// that the response presents as deactivated.
			_, err = remoteSystemctl(ctx, "stop", provider.unit)
			if err == nil {
				_, err = remoteSystemctl(ctx, "disable", provider.unit)
			}
		}
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "network_remote_access_"+request.Action, provider.id, err == nil, "fixed provider service lifecycle")
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "provider lifecycle change failed; refresh service status")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func remoteTransitional(state string) bool {
	return state == "activating" || state == "deactivating" || state == "reloading" || state == "refreshing"
}

func remoteActivate(ctx context.Context, provider remoteProviderMetadata) error {
	_, err := remoteSystemctl(ctx, "enable", "--now", provider.unit)
	return err
}
