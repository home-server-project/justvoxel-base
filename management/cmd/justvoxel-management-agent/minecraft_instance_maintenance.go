package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type instancePlayers struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Online  int    `json:"online"`
	Maximum int    `json:"maximum"`
}

// Successful but oversized console output is invalid evidence, not a readiness failure.
var errInstanceConsoleResponseTooLarge = instanceRuntimeFailure("Instance console is unavailable.")

// A failed or truncated response is never interpreted as an empty server.
func parseInstancePlayers(id string, raw []byte) (instancePlayers, error) {
	p := instancePlayers{ID: id, State: "unknown"}
	if len(raw) > 8192 {
		return p, instanceRuntimeFailure("Player status is unavailable.")
	}
	parts := instancePlayerListPattern.FindStringSubmatch(strings.TrimSpace(string(raw)))
	if parts != nil {
		online, e1 := strconv.Atoi(parts[1])
		maximum, e2 := strconv.Atoi(parts[2])
		names := strings.TrimSpace(parts[3])
		if e1 == nil && e2 == nil && maximum > 0 && maximum <= 1000 && online <= maximum && ((online == 0 && names == "") || (online > 0 && len(strings.Split(names, ", ")) == online && names != "")) {
			p.State = "online"
			p.Online = online
			p.Maximum = maximum
			return p, nil
		}
	}
	return p, instanceRuntimeFailure("Player presence cannot be verified; the instance was not interrupted.")
}
func (b *instanceRuntimeBackend) rcon(ctx context.Context, id, command string) ([]byte, error) {
	if !instanceIDPattern.MatchString(id) {
		return nil, errInstanceID
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tool := "rcon-cli"
	record, readErr := b.readRuntimeConfig(id)
	if readErr != nil {
		return nil, readErr
	}
	if record.Runtime.Software == "bedrock" {
		tool = "send-command"
	}
	out, err := b.command(ctx, "podman", "exec", "minecraft-"+id, tool, command)
	if err != nil {
		return nil, instanceRuntimeFailure("Instance console is unavailable.")
	}
	if len(out) > 8192 {
		return nil, errInstanceConsoleResponseTooLarge
	}
	return out, nil
}
func (b *instanceRuntimeBackend) players(ctx context.Context, id string) (instancePlayers, error) {
	record, err := b.readRuntimeConfig(id)
	if err != nil {
		return instancePlayers{ID: id, State: "unknown"}, err
	}
	if record.Runtime.Software == "bedrock" {
		return b.bedrockPlayers(ctx, id)
	}
	out, err := b.rcon(ctx, id, "list")
	if err != nil {
		return instancePlayers{ID: id, State: "unknown"}, instanceRuntimeFailure("Player presence cannot be verified; the instance was not interrupted.")
	}
	return parseInstancePlayers(id, out)
}
func instancePause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (b *instanceRuntimeBackend) waitMaintenance(ctx context.Context, d time.Duration) error {
	if b.pause != nil {
		return b.pause(ctx, d)
	}
	return instancePause(ctx, d)
}
func (b *instanceRuntimeBackend) stopWithWarnings(ctx context.Context, id string) error {
	record, err := b.readRuntimeConfig(id)
	if err != nil {
		return err
	}
	if record.Runtime.Software == "bedrock" {
		// The image announces and waits 60 seconds, then sends BDS stop and saves.
		_, err = b.command(ctx, "systemctl", "stop", "--", "minecraft-"+id+".service")
		return err
	}
	// Match interrupt-safety.sh: itzg's normal 60-second delay plus the same
	// announcement points. Never bypass that delay for an occupied instance.
	ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	announce := func(seconds int) error {
		unit := "seconds"
		if seconds == 1 {
			unit = "second"
		}
		_, err := b.rcon(ctx, id, "say Server shutting down in "+strconv.Itoa(seconds)+" "+unit)
		return err
	}
	if err := announce(60); err != nil {
		return instanceRuntimeFailure("Shutdown warning failed; the instance was not interrupted.")
	}
	if _, err := b.command(ctx, "systemctl", "--no-block", "stop", "--", "minecraft-"+id+".service"); err != nil {
		return instanceRuntimeFailure("Instance graceful stop request failed.")
	}
	previous := 60
	for _, seconds := range []int{30, 15, 10, 5, 4, 3, 2, 1, 0} {
		if b.waitMaintenance(ctx, time.Duration(previous-seconds)*time.Second) != nil {
			return instanceRuntimeFailure("Safe shutdown countdown could not complete.")
		}
		state, err := b.observeRaw(ctx, id)
		if err != nil {
			return instanceRuntimeFailure("Graceful shutdown state is unavailable.")
		}
		if state.State == "stopped" {
			return nil
		}
		if seconds > 0 {
			if err = announce(seconds); err != nil {
				return instanceRuntimeFailure("Shutdown countdown announcement failed; normal graceful shutdown continues.")
			}
		}
		previous = seconds
	}
	for {
		state, err := b.observeRaw(ctx, id)
		if err != nil {
			return instanceRuntimeFailure("Graceful shutdown state is unavailable.")
		}
		if state.State == "stopped" {
			return nil
		}
		if b.waitMaintenance(ctx, time.Second) != nil {
			return instanceRuntimeFailure("Instance did not finish graceful shutdown before the safety timeout.")
		}
	}
}

func registerInstanceMaintenanceRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/minecraft/instances/{id}/players", s.instanceMaintenanceRead)
	mux.HandleFunc("GET /v1/minecraft/instances/{id}/logs", s.minecraftLogs)
	mux.HandleFunc("GET /v1/minecraft/instances/{id}/version", s.instanceMaintenanceRead)
	mux.HandleFunc("POST /v1/minecraft/instances/{id}/console", s.instanceConsole)
	mux.HandleFunc("POST /v1/minecraft/instances/{id}/version/verify", s.instanceVersionVerify)
}
func (s *server) instanceMaintenanceRead(w http.ResponseWriter, r *http.Request) {
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
	if strings.HasSuffix(r.URL.Path, "/version") {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "software": record.Runtime.Software, "version": record.Runtime.Version, "backup_available": true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	state, err := instanceRuntimes.observe(ctx, id)
	if err != nil || (state.State != "running" && state.State != "stopped" && state.State != "configured" && state.State != "failed") {
		writeError(w, http.StatusConflict, "Instance runtime is unavailable.")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/players") {
		p := instancePlayers{ID: id, State: "offline"}
		if state.State == "running" {
			p, _ = instanceRuntimes.players(ctx, id)
		}
		writeJSON(w, http.StatusOK, p)
		return
	}
}
func instanceDiagnosticText(out []byte, secret string) string {
	text := strings.ToValidUTF8(string(out), "?")
	text = strings.Map(func(r rune) rune {
		if (r < 32 && r != '\t' && r != '\n') || r == 127 {
			return -1
		}
		return r
	}, text)
	if secret != "" {
		text = strings.ReplaceAll(text, secret, "[redacted]")
	}
	lines := []string{}
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "environment") || strings.Contains(line, "=") {
			line = "[private diagnostic line omitted]"
		}
		lines = append(lines, line)
	}
	text = strings.Join(lines, "\n")
	if len(text) > 16384 {
		text = strings.ToValidUTF8(text[:16384], "") + "\n[truncated]"
	}
	return text
}
func (s *server) instanceConsole(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	var request struct {
		Command  string `json:"command"`
		Discover bool   `json:"discover,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	command, err := normalizeInstanceConsoleCommand(request.Command)
	if (request.Discover && request.Command != "") || (!request.Discover && err != nil) {
		writeError(w, http.StatusBadRequest, "Enter a single Minecraft command. Use the normal server controls to stop the server.")
		return
	}
	release, err := s.lockInstanceRuntime()
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	result := instanceConsoleResponse{OK: true, ID: id}
	run := func() error {
		state, err := instanceRuntimes.observe(ctx, id)
		if err != nil {
			return err
		}
		if state.State != "running" {
			return instanceRuntimeFailure("Server is stopped. Start it before using RCON.")
		}
		record, err := instanceRuntimes.readRuntimeConfig(id)
		if err != nil {
			return err
		}
		if request.Discover {
			result.Commands, result.Partial, err = instanceRuntimes.discoverCommands(ctx, id, record.Runtime.RCONPassword)
		} else {
			var out []byte
			out, err = instanceRuntimes.rcon(ctx, id, command)
			if err == nil {
				result.Output = instanceDiagnosticText(out, record.Runtime.RCONPassword)
			}
		}
		if err != nil {
			return err
		}
		if s.store != nil {
			return s.store.recordAuditEvent(actor, "minecraft_console", "minecraft-"+id+".service", true, "Instance console request completed")
		}
		return nil
	}
	err = errors.Join(run(), release())
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) instanceVersionVerify(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	var request struct {
		Version string `json:"version"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if !instanceRuntimeVersionPattern.MatchString(request.Version) {
		writeError(w, http.StatusBadRequest, "Invalid Minecraft version.")
		return
	}
	record, err := instanceRuntimes.readRecord(id)
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	record.Runtime.Version = request.Version
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if err = instanceRuntimes.validateSoftware(ctx, record.Runtime); err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "version": request.Version, "available": true, "backup_available": true})
}
