package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
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
	IP           string `json:"ip,omitempty"`
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

// NetBird uses the packaged CLI for login and connected-state information.
var netbirdCommand = exec.CommandContext
var netbirdStatus = func(ctx context.Context) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := netbirdCommand(ctx, "/usr/bin/netbird", "status", "--json")
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		return false, ""
	}
	var state struct {
		DaemonStatus string `json:"daemonStatus"`
		IP           string `json:"netbirdIp"`
		Management   struct {
			Connected bool `json:"connected"`
		} `json:"management"`
	}
	if json.Unmarshal(output, &state) != nil || state.DaemonStatus != "Connected" || !state.Management.Connected {
		return false, ""
	}
	ip := strings.TrimSpace(state.IP)
	if net.ParseIP(strings.Split(ip, "/")[0]) == nil {
		ip = ""
	}
	return true, ip
}

// The URL is supplied by NetBird's own CLI, never by a browser or user input.
// NetBird also supports self-hosted identity providers, so the host is not fixed.
func validNetbirdLoginURL(text string) string {
	text = strings.TrimSpace(text)
	if len(text) == 0 || len(text) > 2048 || strings.ContainsAny(text, " \t\r\n<>\"'") {
		return ""
	}
	parsed, err := url.Parse(text)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return ""
	}
	return text
}

var netbirdLogin = func() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	cmd := netbirdCommand(ctx, "/usr/bin/netbird", "up", "--no-browser")
	reader, writer := io.Pipe()
	cmd.Stdout, cmd.Stderr = writer, writer
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		cancel()
		_ = reader.Close()
		_ = writer.Close()
		return "", err
	}
	links := make(chan string, 1)
	readDone := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		defer close(readDone)
		defer reader.Close()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 8192)
		for scanner.Scan() {
			if link := validNetbirdLoginURL(scanner.Text()); link != "" {
				select {
				case links <- link:
				default:
				}
			}
		}
	}()
	go func() {
		err := cmd.Wait()
		_ = writer.Close()
		<-readDone
		cancel()
		finished <- err
	}()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case link := <-links:
		return link, nil
	case err := <-finished:
		select {
		case link := <-links:
			return link, nil
		default:
		}
		return "", err
	case <-timer.C:
		cancel()
		return "", context.DeadlineExceeded
	}
}

var remoteStat = os.Stat
var tailscaleStatusCommand = exec.CommandContext
var tailscaleStatus = func(ctx context.Context) (bool, string, string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := tailscaleStatusCommand(ctx, "/usr/bin/tailscale", "status", "--json")
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil || ctx.Err() != nil {
		return false, "Not configured", ""
	}
	var status struct {
		BackendState   string
		HaveNodeKey    bool
		CurrentTailnet *struct{}
		TailscaleIPs   []string
	}
	if json.Unmarshal(output, &status) != nil {
		return false, "Not configured", ""
	}
	switch status.BackendState {
	case "Running":
		ip := ""
		for _, candidate := range status.TailscaleIPs {
			if address := net.ParseIP(candidate); address != nil && address.To4() != nil {
				ip = address.String()
				break
			}
		}
		return true, "Connected", ip
	case "NeedsMachineAuth":
		return true, "Awaiting approval", ""
	case "Starting", "Stopped":
		if status.HaveNodeKey || status.CurrentTailnet != nil {
			if status.BackendState == "Starting" {
				return true, "Starting", ""
			}
			return true, "Stopped", ""
		}
	}
	return false, "Not configured", ""
}

// Tailscale up --json emits an AuthURL before it waits for browser approval.
var tailscaleUpCommand = exec.CommandContext

func validTailscaleLoginURL(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 2048 || strings.ContainsAny(value, " \t\r\n<>\"'") {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "login.tailscale.com" ||
		parsed.User != nil || !strings.HasPrefix(parsed.Path, "/a/") || len(parsed.Path) <= len("/a/") {
		return ""
	}
	return value
}

var tailscaleLogin = func() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	cmd := tailscaleUpCommand(ctx, "/usr/bin/tailscale", "up", "--json")
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return "", err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return "", err
	}
	type loginResult struct {
		url string
		err error
	}
	ready := make(chan loginResult, 1)
	go func() {
		defer cancel()
		decoder := json.NewDecoder(stdout)
		reported := false
		for {
			var message struct {
				AuthURL      string
				BackendState string
				Error        string
			}
			if err := decoder.Decode(&message); err != nil {
				break
			}
			if reported {
				continue // Drain the second JSON response until login completes.
			}
			if message.Error != "" {
				ready <- loginResult{err: os.ErrInvalid}
				reported = true
			} else if message.AuthURL != "" {
				if link := validTailscaleLoginURL(message.AuthURL); link != "" {
					ready <- loginResult{url: link}
				} else {
					ready <- loginResult{err: os.ErrInvalid}
				}
				reported = true
			} else if message.BackendState == "Running" {
				ready <- loginResult{} // Already authenticated; no login page needed.
				reported = true
			}
		}
		err := cmd.Wait()
		if !reported {
			if err == nil {
				err = os.ErrInvalid
			}
			ready <- loginResult{err: err}
		}
	}()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case result := <-ready:
		return result.url, result.err
	case <-timer.C:
		cancel()
		return "", context.DeadlineExceeded
	}
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
		status.Configured, status.Summary, status.IP = tailscaleStatus(ctx)
		status.Connected = status.Active && status.Summary == "Connected"
		if !status.Connected { status.IP = "" }
		if status.Configured && !status.Active && status.Summary == "Connected" {
			status.Summary = "Stopped"
		}
	} else if provider.id == "netbird" {
		if status.Configured {
			status.Summary = "Stopped"
		}
		if status.Active {
			connected, ip := netbirdStatus(ctx)
			if connected {
				status.Configured = true
				status.Connected = true
				status.Summary = "Connected"
				status.IP = ip
			} else if status.Configured {
				status.Summary = "Not connected"
			}
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
	if err == nil && (status.ServiceState == "deactivating" || (request.Action == "activate" && remoteTransitional(status.ServiceState) && !((provider.id == "netbird" || provider.id == "tailscale") && status.ServiceState == "activating"))) {
		writeError(w, http.StatusConflict, "provider service is changing state; refresh service status")
		return
	}
	loginURL := ""
	if err == nil {
		if request.Action == "activate" {
			err = remoteActivate(ctx, provider)
			if err == nil && !status.Connected {
				switch provider.id {
				case "netbird":
					loginURL, err = netbirdLogin()
				case "tailscale":
					loginURL, err = tailscaleLogin()
				}
			}
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
	if (provider.id == "netbird" || provider.id == "tailscale") && request.Action == "activate" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "login_url": loginURL})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func remoteTransitional(state string) bool {
	return state == "activating" || state == "deactivating" || state == "reloading" || state == "refreshing"
}

func remoteActivate(ctx context.Context, provider remoteProviderMetadata) error {
	if provider.id == "netbird" {
		// NetBird login must not wait for the daemon to report fully active.
		if _, err := remoteSystemctl(ctx, "enable", provider.unit); err != nil {
			return err
		}
		_, err := remoteSystemctl(ctx, "start", "--no-block", provider.unit)
		return err
	}
	_, err := remoteSystemctl(ctx, "enable", "--now", provider.unit)
	return err
}
