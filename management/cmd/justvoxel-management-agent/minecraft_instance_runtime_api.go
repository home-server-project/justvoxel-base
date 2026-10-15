package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Public settings deliberately omit private RCON credentials, host paths,
// image repositories, container/unit names, and container account IDs.
type instanceRuntimeSettings struct {
	JavaPort         int    `json:"java_port"`
	BedrockEnabled   bool   `json:"bedrock_enabled"`
	BedrockPort      int    `json:"bedrock_port"`
	JavaMemory       string `json:"java_memory"`
	ContainerMemory  string `json:"container_memory"`
	Software         string `json:"software"`
	Version          string `json:"version"`
	PaperChannel     string `json:"paper_channel"`
	ImageTag         string `json:"image_tag"`
	StartOnBoot      bool   `json:"start_on_boot"`
	EULAAccepted     bool   `json:"eula_accepted"`
	Timezone         string `json:"timezone"`
	GameMode         string `json:"game_mode"`
	Difficulty       string `json:"difficulty"`
	MaxPlayers       int    `json:"max_players"`
	Whitelist        bool   `json:"whitelist"`
	EnforceWhitelist bool   `json:"enforce_whitelist"`
	MOTD             string `json:"motd"`
}

func (settings instanceRuntimeSettings) runtime(id string, uid, gid uint32, password string) minecraftInstanceRuntimeConfig {
	return minecraftInstanceRuntimeConfig{ID: id, JavaPort: settings.JavaPort, BedrockEnabled: settings.BedrockEnabled, BedrockPort: settings.BedrockPort, JavaMemory: settings.JavaMemory, ContainerMemory: settings.ContainerMemory, Software: settings.Software, Version: settings.Version, PaperChannel: settings.PaperChannel, ImageTag: settings.ImageTag, StartOnBoot: settings.StartOnBoot, EULAAccepted: settings.EULAAccepted, UID: uid, GID: gid, Timezone: settings.Timezone, GameMode: settings.GameMode, Difficulty: settings.Difficulty, MaxPlayers: settings.MaxPlayers, Whitelist: settings.Whitelist, EnforceWhitelist: settings.EnforceWhitelist, MOTD: settings.MOTD, RCONPassword: password}
}
func instancePublicSettings(c minecraftInstanceRuntimeConfig) instanceRuntimeSettings {
	return instanceRuntimeSettings{JavaPort: c.JavaPort, BedrockEnabled: c.BedrockEnabled, BedrockPort: c.BedrockPort, JavaMemory: c.JavaMemory, ContainerMemory: c.ContainerMemory, Software: c.Software, Version: c.Version, PaperChannel: c.PaperChannel, ImageTag: c.ImageTag, StartOnBoot: c.StartOnBoot, EULAAccepted: c.EULAAccepted, Timezone: c.Timezone, GameMode: c.GameMode, Difficulty: c.Difficulty, MaxPlayers: c.MaxPlayers, Whitelist: c.Whitelist, EnforceWhitelist: c.EnforceWhitelist, MOTD: c.MOTD}
}
func registerInstanceRuntimeRoutes(mux *http.ServeMux, s *server) {
	registerInstanceMaintenanceRoutes(mux, s)
	mux.HandleFunc("POST /v1/minecraft/instances", s.instanceRuntimeCreate)
	mux.HandleFunc("GET /v1/minecraft/instances/runtime", s.instanceRuntimeList)
	mux.HandleFunc("GET /v1/minecraft/instances/{id}/configuration", s.instanceRuntimeConfiguration)
	mux.HandleFunc("PUT /v1/minecraft/instances/{id}/configuration", s.instanceRuntimeConfigure)
	mux.HandleFunc("POST /v1/minecraft/instances/{id}/activate", s.instanceRuntimeApply)
	mux.HandleFunc("POST /v1/minecraft/instances/{id}/recover", s.instanceRuntimeRecover)
	mux.HandleFunc("GET /v1/minecraft/instances/{id}/status", s.instanceRuntimeStatus)
	for _, action := range []string{"start", "stop", "restart"} {
		mux.HandleFunc("POST /v1/minecraft/instances/{id}/"+action, func(w http.ResponseWriter, r *http.Request) { s.instanceRuntimeAction(w, r, action) })
	}
}
func instanceRuntimeError(w http.ResponseWriter, err error, id string) {
	code := http.StatusConflict
	switch {
	case errors.Is(err, errInstanceID), errors.Is(err, errInstanceName):
		code = http.StatusBadRequest
	case errors.Is(err, errInstanceNotFound):
		code = http.StatusNotFound
	case errors.Is(err, os.ErrNotExist):
		code = http.StatusServiceUnavailable
	}
	// Never include filesystem/command error strings: they may contain private
	// configuration bytes or host diagnostics. Detailed safe state is read below.
	message := "Instance runtime operation could not be completed. Verify configuration, storage, ports, and resources."
	var safe *instanceRuntimeSafeError
	if errors.As(err, &safe) {
		message = safe.Error()
	}
	if errors.Is(err, errInstanceRecovery) {
		message = errInstanceRecovery.Error()
	}
	if errors.Is(err, errInstanceBusy) {
		message = errInstanceBusy.Error()
	}
	if errors.Is(err, errInstanceID) {
		message = errInstanceID.Error()
	}
	if errors.Is(err, errInstanceNotFound) {
		message = errInstanceNotFound.Error()
	}
	writeJSON(w, code, map[string]any{"error": message, "id": id, "recovery_required": errors.Is(err, errInstanceRecovery)})
}
func instanceRequestID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !instanceIDPattern.MatchString(id) {
		instanceRuntimeError(w, errInstanceID, "")
		return "", false
	}
	return id, true
}
func (s *server) withInstanceMutation(w http.ResponseWriter, r *http.Request, id string, fn func(context.Context) error) {
	release, err := s.lockInstanceRuntime()
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	err = fn(ctx)
	cancel()
	// Release failures are observable; never silently claim success.
	err = errors.Join(err, release())
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}
func (s *server) instanceRuntimeCreate(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	var request struct {
		DisplayName string                  `json:"display_name"`
		Runtime     instanceRuntimeSettings `json:"runtime"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	release, err := s.lockInstanceRuntime()
	if err != nil {
		instanceRuntimeError(w, err, "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	defer cancel()
	snapshot, uid, gid, err := instanceRuntimes.snapshot(ctx)
	id := ""
	if err == nil {
		var secret [24]byte
		if _, err = rand.Read(secret[:]); err == nil {
			c := request.Runtime.runtime("", uid, gid, hex.EncodeToString(secret[:]))
			id, err = instanceRuntimes.create(ctx, c, request.DisplayName, snapshot, minecraftLegacyNameSoftware(r))
		}
	}
	err = errors.Join(err, release())
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "id": id, "state": "stopped"})
}
func (s *server) instanceRuntimeConfiguration(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	record, err := instanceRuntimes.readRecord(id)
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "runtime": instancePublicSettings(record.Runtime)})
}
func (s *server) instanceRuntimeConfigure(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	var request struct {
		Runtime instanceRuntimeSettings `json:"runtime"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	s.withInstanceMutation(w, r, id, func(ctx context.Context) error {
		old, err := instanceRuntimes.readRecord(id)
		if errors.Is(err, os.ErrNotExist) {
			// Recover a registered identity whose initial config publication failed.
			// This path never adopts an existing environment, Quadlet, or data tree.
			snapshot, uid, gid, e := instanceRuntimes.snapshot(ctx)
			if e != nil {
				return e
			}
			return instanceRuntimes.configureRegistered(ctx, id, request.Runtime, snapshot, uid, gid)
		}
		if err != nil {
			return err
		}
		snapshot, _, _, err := instanceRuntimes.snapshot(ctx)
		if err != nil {
			return err
		}
		next := old
		next.Runtime = request.Runtime.runtime(id, old.Runtime.UID, old.Runtime.GID, old.Runtime.RCONPassword)
		records, err := instanceRuntimes.records()
		if err != nil {
			return err
		}
		return instanceRuntimes.apply(ctx, old, instanceReplaceRecord(records, next), snapshot)
	})
}
func (b *instanceRuntimeBackend) configureRegistered(ctx context.Context, id string, settings instanceRuntimeSettings, snapshot minecraftInstanceRuntimeContext, uid, gid uint32) error {
	registered, err := b.identities().readRecord(id + ".json")
	if err != nil || registered != id {
		return errInstanceNotFound
	}
	dir, env, q := b.paths(id)
	for _, path := range []string{env, q, filepath.Join(dir, "config.json"), filepath.Join(dir, "pending.json")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errInstanceRecovery
		}
	}
	records, err := b.records()
	if err != nil {
		return err
	}
	var secret [24]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return err
	}
	next := instanceRuntimeRecord{SchemaVersion: 1, Runtime: settings.runtime(id, uid, gid, hex.EncodeToString(secret[:])), Storage: instanceStorageFor(snapshot, id), Firewall: []instanceFirewallRule{}}
	candidates := instanceReplaceRecord(records, next)
	if _, err = b.render(snapshot, candidates); err != nil {
		return err
	}
	if err = b.storage(ctx, "preflight-new", next); err != nil {
		return err
	}
	if err = b.validateSoftware(ctx, next.Runtime); err != nil {
		return err
	}
	root, err := b.privateDirectory(b.root, true)
	if err != nil {
		return err
	}
	root.Close()
	f, err := b.privateDirectory(dir, true)
	if err != nil {
		return err
	}
	f.Close()
	if err = b.writeJSON(filepath.Join(dir, "config.json"), next, false); err != nil {
		return err
	}
	return b.apply(ctx, next, candidates, snapshot)
}
func (s *server) instanceRuntimeApply(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	var request struct{}
	if !decodeJSON(w, r, &request) {
		return
	}
	s.withInstanceMutation(w, r, id, func(ctx context.Context) error {
		record, err := instanceRuntimes.readRecord(id)
		if err != nil {
			return err
		}
		snapshot, _, _, err := instanceRuntimes.snapshot(ctx)
		if err != nil {
			return err
		}
		records, err := instanceRuntimes.records()
		if err != nil {
			return err
		}
		return instanceRuntimes.apply(ctx, record, records, snapshot)
	})
}
func (s *server) instanceRuntimeRecover(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	var request struct{}
	if !decodeJSON(w, r, &request) {
		return
	}
	s.withInstanceMutation(w, r, id, func(ctx context.Context) error { return instanceRuntimes.recover(ctx, id) })
}
func (s *server) instanceRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	status, err := instanceRuntimes.observe(ctx, id)
	if errors.Is(err, errInstanceNotFound) {
		instanceRuntimeError(w, err, id)
		return
	}
	if err != nil {
		status.Message = "Instance configuration, storage, or runtime evidence is unavailable."
	}
	writeJSON(w, http.StatusOK, status)
}
func (s *server) instanceRuntimeList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	// Include registered-but-unconfigured identities, not just active runtimes.
	instances, err := instanceRuntimes.identities().listInstances(minecraftLegacyNameSoftware(r))
	if err != nil {
		instanceRuntimeError(w, err, "")
		return
	}
	legacy, err := instanceRuntimes.identities().read()
	if err != nil {
		instanceRuntimeError(w, err, "")
		return
	}
	states := []instanceRuntimeStatus{}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	for _, i := range instances {
		if i.ID == legacy.ID {
			continue
		} // Legacy APIs and evidence retain their contracts.
		state, err := instanceRuntimes.observe(ctx, i.ID)
		if err != nil {
			state.Message = "Instance configuration, storage, or runtime evidence is unavailable."
		}
		states = append(states, state)
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": states})
}
func (s *server) instanceRuntimeAction(w http.ResponseWriter, r *http.Request, action string) {
	var actor session
	var ok bool
	if action == "stop" {
		actor, ok = s.requireAdministrator(w, r)
	} else {
		actor, ok = s.requireRoles(w, r, roleAdministrator, roleOperator)
	}
	if !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	var request struct{}
	if !decodeJSON(w, r, &request) {
		return
	}
	var guard *operatorRestartGuard
	if action == "restart" && actor.Role == roleOperator {
		operatorRestartMu.Lock()
		defer operatorRestartMu.Unlock()
		if s.store == nil {
			writeError(w, http.StatusForbidden, "Operator restart controls are unavailable.")
			return
		}
		var availability operatorRestartAvailability
		var err error
		guard, availability, err = s.store.beginOperatorRestart(actor.WebUserID)
		if err != nil || !availability.Allowed {
			writeError(w, http.StatusConflict, "Operator restart allowance or cooldown prevents this restart.")
			return
		}
		defer guard.cancel()
	}
	s.withInstanceMutation(w, r, id, func(ctx context.Context) error {
		if err := instanceRuntimes.actionWithWarnings(ctx, id, action, actor.Role == roleAdministrator); err != nil {
			return err
		}
		if guard != nil {
			_, err := guard.acceptTarget(actor, "minecraft-"+id+".service")
			return err
		}
		if s.store != nil {
			return s.store.recordAuditEvent(actor, "minecraft_"+action, "minecraft-"+id+".service", true, "Instance lifecycle action completed")
		}
		return nil
	})
}
