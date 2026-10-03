package main

import (
	"context"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"sync"
	"time"
)

type playitSetupState struct {
	State    string `json:"state"`
	ClaimURL string `json:"claim_url,omitempty"`
}

func (state playitSetupState) running() bool {
	return state.State == "starting" || state.State == "waiting"
}

// One appliance-wide attempt, shared with lifecycle requests. No identity data.
var playitSetup = struct {
	mu    sync.Mutex
	state playitSetupState
}{state: playitSetupState{State: "idle"}}

var playitSetupTimeout = 10 * time.Minute
var playitExecutableStat = remoteStat
var playitSetupCommand = exec.CommandContext
var playitSetupRun = func(ctx context.Context, output io.Writer) error {
	cmd := playitSetupCommand(ctx, "/usr/bin/playit", "setup")
	cmd.Stdout, cmd.Stderr = output, output
	cmd.WaitDelay = time.Second
	return cmd.Run()
}

var playitClaimURL = regexp.MustCompile(`^https://playit\.gg/claim/[0-9a-fA-F]{10}$`)

// Retain at most one bounded token until its delimiter arrives; discard all
// output except a validated claim URL. stdout and stderr can write concurrently.
type playitClaimWriter struct {
	mu      sync.Mutex
	token   []byte
	discard bool
	onClaim func(string)
}

func (w *playitClaimWriter) finishToken() {
	if !w.discard && playitClaimURL.Match(w.token) {
		w.onClaim(string(w.token))
	}
	clear(w.token)
	w.token = w.token[:0]
	w.discard = false
}

func (w *playitClaimWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, b := range data {
		if b == ' ' || b == '\n' || b == '\r' || b == '\t' {
			w.finishToken()
		} else if !w.discard {
			if len(w.token) >= 256 {
				clear(w.token)
				w.token = w.token[:0]
				w.discard = true
			} else {
				w.token = append(w.token, b)
			}
		}
	}
	return len(data), nil
}

func registerPlayitSetupRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/network/remote-access/playit/setup", s.networkPlayitSetup)
	mux.HandleFunc("POST /v1/admin/network/remote-access/playit/setup", s.networkPlayitSetup)
}

func (s *server) networkPlayitSetup(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	playitSetup.mu.Lock()
	defer playitSetup.mu.Unlock()
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, playitSetup.state)
		return
	}
	provider, _ := remoteProviderByID("playit")
	if remoteConfigured(provider) {
		writeError(w, http.StatusConflict, "Playit is already configured")
		return
	}
	if playitSetup.state.running() {
		writeJSON(w, http.StatusAccepted, playitSetup.state)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	status, err := remoteProviderStatus(ctx, provider)
	info, executableErr := playitExecutableStat("/usr/bin/playit")
	if err != nil || !status.Installed || executableErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		writeError(w, http.StatusServiceUnavailable, "Playit service or executable is unavailable")
		return
	}
	if remoteTransitional(status.ServiceState) {
		writeError(w, http.StatusConflict, "Playit service is changing state; try setup when stable")
		return
	}
	if !status.Active || !status.Enabled {
		if err := remoteActivate(ctx, provider); err != nil {
			writeError(w, http.StatusServiceUnavailable, "Playit service could not be activated for setup")
			return
		}
	}
	playitSetup.state = playitSetupState{State: "starting"}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "network_playit_setup_start", "playit", true, "packaged setup started")
	}
	// The CLI owns daemon readiness, approval, exchange and provisioning.
	// Request disconnects must not abandon the bounded setup attempt.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), playitSetupTimeout)
		defer cancel()
		output := &playitClaimWriter{onClaim: func(url string) {
			playitSetup.mu.Lock()
			defer playitSetup.mu.Unlock()
			playitSetup.state = playitSetupState{State: "waiting", ClaimURL: url}
		}}
		err := playitSetupRun(ctx, output)
		output.mu.Lock()
		output.finishToken()
		output.mu.Unlock()
		playitSetup.mu.Lock()
		defer playitSetup.mu.Unlock()
		success := err == nil && ctx.Err() == nil && remoteConfigured(provider)
		action := "network_playit_setup_failed"
		playitSetup.state = playitSetupState{State: "failed"}
		if success {
			playitSetup.state.State = "complete"
			action = "network_playit_setup_complete"
		}
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, action, "playit", success, "packaged setup finished")
		}
	}()
	writeJSON(w, http.StatusAccepted, playitSetup.state)
}
