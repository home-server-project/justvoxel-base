package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const nordAccessTokenLimit = 4096
const nordCommandOutputLimit = 64 * 1024

type nordCommandOutput struct {
	data []byte
}

func (b *nordCommandOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := nordCommandOutputLimit + 1 - len(b.data); n > remaining {
		n = remaining
	}
	b.data = append(b.data, p[:n]...)
	if len(b.data) > nordCommandOutputLimit {
		return n, errors.New("NordVPN output unavailable")
	}
	return n, nil
}

var nordOperation sync.Mutex
var nordCommand = exec.CommandContext
var nordCLIHome = "/run/justvoxel/nordvpn-cli"

// Vendor output is never logged or returned. Even unsuccessful commands may
// contain authentication material. Bound combined output and reject overflow.
var runNordCommand = func(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if err := os.MkdirAll(nordCLIHome, 0o700); err != nil {
		return "", errors.New("NordVPN output unavailable")
	}
	cmd := nordCommand(ctx, "/usr/bin/nordvpn", args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin", "HOME=" + nordCLIHome, "LANG=C", "LC_ALL=C", "NO_COLOR=1", "TERM=dumb"}
	cmd.WaitDelay = time.Second
	var output nordCommandOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	// Decline optional vendor analytics if first login asks for consent.
	if len(args) > 0 && args[0] == "login" {
		cmd.Stdin = strings.NewReader("no\n")
	}
	err := cmd.Run()
	if len(output.data) > nordCommandOutputLimit || ctx.Err() != nil {
		return "", errors.New("NordVPN output unavailable")
	}
	return string(output.data), err
}

func nordSoftwareInstalled() bool {
	for _, path := range []string{"/usr/bin/nordvpn", "/usr/sbin/nordvpnd"} {
		info, err := remoteStat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			return false
		}
	}
	return true
}

func nordAccount(output string, err error) string {
	// Only explicit CLI evidence; never return email, subscription or login URLs.
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "Account information" && err == nil {
			return "Logged in"
		}
		if line == "You are not logged in." || line == "You're not logged in." ||
			line == "You are not logged in. Please log in using 'nordvpn login'." ||
			line == "You’re not logged in. Please log in using ‘nordvpn login’." {
			return "Not logged in"
		}
	}
	return "Unknown"
}

func nordMeshnet(output string, err error) string {
	if err != nil {
		return "Unknown"
	}
	state := "Unknown"
	for _, line := range strings.Split(output, "\n") {
		switch strings.TrimSpace(line) {
		case "Meshnet: enabled":
			if state != "Unknown" {
				return "Unknown"
			}
			state = "Enabled"
		case "Meshnet: disabled":
			if state != "Unknown" {
				return "Unknown"
			}
			state = "Disabled"
		}
	}
	return state
}

var nordNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,248}[a-z0-9])?\.nord$`)

func nordIdentity(output string) (string, string) {
	local := false
	name, ip := "", ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "This device:" {
			local = true
			continue
		}
		if !local {
			continue
		}
		if line == "Local Peers:" || line == "External Peers:" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if key == "Hostname" && nordNamePattern.MatchString(value) {
			name = value
		}
		if key == "IP" {
			if parsed := net.ParseIP(value); parsed != nil {
				ip = parsed.String()
			}
		}
	}
	return name, ip
}

func nordStatus(ctx context.Context) (string, string, string, string) {
	output, err := runNordCommand(ctx, "account")
	account := nordAccount(output, err)
	output, err = runNordCommand(ctx, "settings")
	mesh := nordMeshnet(output, err)
	name, ip := "", ""
	if mesh == "Enabled" {
		if output, err = runNordCommand(ctx, "meshnet", "peer", "list"); err == nil {
			name, ip = nordIdentity(output)
		}
	}
	return account, mesh, name, ip
}

// Input hygiene only: the access token is an opaque, unchanged secret.
func validNordAccessToken(value string) bool {
	if len(value) == 0 || len(value) > nordAccessTokenLimit {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func nordEnableMeshnet(ctx context.Context) error {
	if _, err := runNordCommand(ctx, "set", "meshnet", "on"); err != nil {
		return errors.New("Meshnet activation failed")
	}
	output, err := runNordCommand(ctx, "settings")
	if nordMeshnet(output, err) != "Enabled" {
		return errors.New("Meshnet activation could not be verified")
	}
	return nil
}

func nordDisableMeshnet(ctx context.Context) error {
	output, err := runNordCommand(ctx, "settings")
	switch nordMeshnet(output, err) {
	case "Disabled":
		return nil
	case "Enabled":
		// A concurrent disable can make the vendor command fail. Only the
		// authoritative follow-up settings can establish the desired state.
		_, _ = runNordCommand(ctx, "set", "meshnet", "off")
		output, err = runNordCommand(ctx, "settings")
		if nordMeshnet(output, err) == "Disabled" {
			return nil
		}
	}
	return errors.New("Meshnet deactivation could not be verified")
}

func (s *server) networkNordChange(w http.ResponseWriter, r *http.Request, actor session, provider remoteProviderMetadata) {
	var request struct {
		Action string `json:"action"`
		Token  string `json:"token,omitempty"`
	}
	// JSON may escape each visible token byte as six bytes (for example "<").
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		request.Token = ""
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}
	if request.Action != "activate" && request.Action != "deactivate" && request.Action != "token-login" {
		request.Token = ""
		writeError(w, http.StatusBadRequest, "invalid NordVPN action")
		return
	}
	validToken := (request.Action == "token-login" && validNordAccessToken(request.Token)) ||
		(request.Action != "token-login" && request.Token == "")
	token := ""
	if validToken {
		token = request.Token
	}
	request.Token = "" // Discard before command, error or audit handling.
	if !validToken {
		writeError(w, http.StatusBadRequest, "invalid NordVPN access token")
		return
	}
	defer func() { token = "" }()
	if !nordOperation.TryLock() {
		writeError(w, http.StatusConflict, "NordVPN operation already in progress")
		return
	}
	defer nordOperation.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	var err error
	loginRequired := false
	if !nordSoftwareInstalled() {
		err = errors.New("NordVPN unavailable")
	}
	if err == nil {
		if request.Action == "deactivate" {
			_, err = remoteSystemctl(ctx, "start", "nordvpnd.socket", provider.unit)
		} else {
			_, err = remoteSystemctl(ctx, "enable", "--now", "nordvpnd.socket", provider.unit)
		}
	}
	if err == nil {
		switch request.Action {
		case "deactivate":
			err = nordDisableMeshnet(ctx)
			if err == nil {
				_, err = remoteSystemctl(ctx, "stop", "nordvpnd.socket", provider.unit)
			}
			if err == nil {
				_, err = remoteSystemctl(ctx, "disable", "nordvpnd.socket", provider.unit)
			}
		case "token-login":
			_, err = runNordCommand(ctx, "login", "--token", token)
			token = "" // No persistence, error wrapping or auditing of token/CLI output.
			if err != nil {
				err = errors.New("NordVPN login failed")
			}
			if err == nil {
				output, checkErr := runNordCommand(ctx, "account")
				if nordAccount(output, checkErr) != "Logged in" {
					err = errors.New("NordVPN login could not be verified")
				}
			}
			if err == nil {
				err = nordEnableMeshnet(ctx)
			}
		case "activate":
			output, checkErr := runNordCommand(ctx, "account")
			switch nordAccount(output, checkErr) {
			case "Not logged in":
				loginRequired = true
			case "Logged in":
				err = nordEnableMeshnet(ctx)
			default:
				err = errors.New("NordVPN account state unavailable")
			}
		}
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "network_remote_access_"+request.Action, provider.id, err == nil, "fixed Meshnet lifecycle")
	}
	if err != nil {
		message := "NordVPN login could not be completed. Check the access token and retry."
		switch request.Action {
		case "activate":
			message = "NordVPN could not be activated. Open Troubleshoot and generate a Network report before retrying."
		case "deactivate":
			message = "NordVPN could not be deactivated. Open Troubleshoot and generate a Network report before retrying."
		}
		writeError(w, http.StatusServiceUnavailable, message)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "login_required": loginRequired})
}
