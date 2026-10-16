package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type selectedMinecraftAPI interface {
	MinecraftInstanceConfiguration(context.Context, string, string) (api.InstanceRuntimeSettings, error)
	SetMinecraftInstanceConfiguration(context.Context, string, string, api.InstanceRuntimeSettings) error
	MinecraftInstanceStatus(context.Context, string, string) (api.MinecraftInstanceStatus, error)
	MinecraftInstancePlayers(context.Context, string, string) (api.InstancePlayers, error)
	MinecraftInstanceLogs(context.Context, string, string) (string, error)
	RenameMinecraftInstance(context.Context, string, string, string) error
	DeleteMinecraftInstance(context.Context, string, string, string) error
}
type selectedServerContext struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Empty context is the pre-existing Original contract. An explicit invalid or
// stale independent ID NEVER becomes Original, including on a mutation.
func (a *App) selectedMinecraft(w http.ResponseWriter, r *http.Request, session string) (selectedServerContext, bool) {
	values, exists := r.URL.Query()["server"]
	if !exists {
		return selectedServerContext{ID: "original", Name: "Original server"}, true
	}
	if len(values) != 1 || values[0] == "" {
		writeMinecraftWorkspaceError(w, 400, "Choose a server again.")
		return selectedServerContext{}, false
	}
	id := values[0]
	if id == "original" {
		return selectedServerContext{ID: id, Name: "Original server"}, true
	}
	if !addServerIDPattern.MatchString(id) {
		writeMinecraftWorkspaceError(w, 400, "Invalid server selection.")
		return selectedServerContext{}, false
	}
	instances, err := a.independentServerInventory(r.Context(), session)
	if err != nil {
		writeMinecraftWorkspaceError(w, 503, "Server selection could not be checked.")
		return selectedServerContext{}, false
	}
	for _, instance := range instances {
		if instance.ID == id {
			return selectedServerContext{ID: id, Name: instance.DisplayName}, true
		}
	}
	writeMinecraftWorkspaceError(w, 404, "Selected server is no longer available.")
	return selectedServerContext{}, false
}
func (a *App) minecraftSelection(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator", "operator", "viewer")
	if !ok {
		return
	}
	selected, ok := a.selectedMinecraft(w, r, session)
	if !ok {
		return
	}
	instances, err := a.independentServerInventory(r.Context(), session)
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Server list is unavailable.")
		return
	}
	choices := []selectedServerContext{{ID: "original", Name: "Original server"}}
	for _, i := range instances {
		choices = append(choices, selectedServerContext{ID: i.ID, Name: i.DisplayName})
	}
	writeMinecraftWorkspaceJSON(w, 200, map[string]any{"selected": selected, "servers": choices})
}

// Return true when selected handling consumed the request, including refusals.
func (a *App) selectedMinecraftWorkspace(w http.ResponseWriter, r *http.Request, session string, apply bool) bool {
	selected, ok := a.selectedMinecraft(w, r, session)
	if !ok {
		return true
	}
	if selected.ID == "original" {
		return false
	}
	client, ok := a.api.(selectedMinecraftAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Selected server controls are unavailable.")
		return true
	}
	path := r.URL.Path
	if strings.Contains(path, "/whitelist") {
		if r.Method != http.MethodGet {
			writeMinecraftWorkspaceError(w, 409, "Whitelist changes are unavailable for this server.")
			return true
		}
		players, err := client.MinecraftInstancePlayers(r.Context(), session, selected.ID)
		if err != nil {
			a.writeMinecraftWorkspaceAPIError(w, err, "Players are unavailable.")
			return true
		}
		if players.ID != selected.ID {
			writeMinecraftWorkspaceError(w, 503, "Selected player status could not be verified.")
			return true
		}
		writeMinecraftWorkspaceJSON(w, 200, map[string]any{"ok": true, "independent": true, "players": players, "message": "Whitelist changes are unavailable for this server."})
		return true
	}
	if strings.HasSuffix(path, "/logs") {
		logs, err := client.MinecraftInstanceLogs(r.Context(), session, selected.ID)
		if err != nil {
			a.writeMinecraftWorkspaceAPIError(w, err, "Logs are unavailable.")
		} else {
			writeMinecraftWorkspaceJSON(w, 200, minecraftWorkspaceLogsResponse{OK: true, Lines: strings.Split(logs, "\n")})
		}
		return true
	}
	current, err := client.MinecraftInstanceConfiguration(r.Context(), session, selected.ID)
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Selected server settings are unavailable.")
		return true
	}
	if r.Method == http.MethodGet {
		// Reuse exactly the existing controls' public presentation contract.
		raw, _ := json.Marshal(current)
		var minecraft map[string]any
		_ = json.Unmarshal(raw, &minecraft)
		minecraft["server_type"] = current.Software
		defaults := map[string]any{}
		if discovery, ok := a.api.(interface {
			AdminSetupDefaults(context.Context, string) (api.AdminSetupDefaults, error)
		}); ok {
			if v, e := discovery.AdminSetupDefaults(r.Context(), session); e == nil {
				defaults["system_memory_mib"] = v.SystemMemoryMiB
				defaults["system_reserve_minimum_mib"] = v.SystemReserveMinimumMiB
				defaults["system_reserve_recommended_mib"] = v.SystemReserveRecommendedMiB
			}
		}
		writeMinecraftWorkspaceJSON(w, 200, map[string]any{"ok": true, "configured": true, "independent": true, "minecraft": minecraft, "defaults": defaults})
		return true
	}
	next, err := selectedSettingsOverlay(r, current)
	if err != nil {
		writeMinecraftWorkspaceError(w, 400, err.Error())
		return true
	}
	before, _ := json.Marshal(current)
	after, _ := json.Marshal(next)
	digest := sha256.Sum256(append(append([]byte(selected.ID+":"), before...), after...))
	review := hex.EncodeToString(digest[:])
	if apply && r.FormValue("review") != review {
		writeMinecraftWorkspaceError(w, 409, "Settings changed since Review. Review this server again.")
		return true
	}
	state, err := client.MinecraftInstanceStatus(r.Context(), session, selected.ID)
	if err != nil || state.ID != selected.ID || state.State != "stopped" {
		writeMinecraftWorkspaceError(w, 409, "Stop this server normally before applying settings.")
		return true
	}
	var oldFields, newFields map[string]any
	_ = json.Unmarshal(before, &oldFields)
	_ = json.Unmarshal(after, &newFields)
	changes := []api.AdminConfigurationChange{}
	for _, field := range []string{"java_memory", "container_memory", "motd", "max_players", "game_mode", "java_port", "bedrock_enabled", "bedrock_port", "start_on_boot"} {
		if fmt.Sprint(oldFields[field]) != fmt.Sprint(newFields[field]) {
			changes = append(changes, api.AdminConfigurationChange{Field: field, Label: strings.ReplaceAll(field, "_", " "), Before: fmt.Sprint(oldFields[field]), After: fmt.Sprint(newFields[field])})
		}
	}
	if apply {
		if err = client.SetMinecraftInstanceConfiguration(r.Context(), session, selected.ID, next); err != nil {
			a.writeMinecraftWorkspaceAPIError(w, err, "Settings could not be applied.")
			return true
		}
	}
	writeMinecraftWorkspaceJSON(w, 200, map[string]any{"ok": true, "applied": apply, "review": review, "changes": changes, "warnings": []string{"The server stays stopped. Start it normally when ready."}, "message": "Server settings saved."})
	return true
}
func selectedSettingsOverlay(r *http.Request, current api.InstanceRuntimeSettings) (api.InstanceRuntimeSettings, error) {
	// This parser already limits sections and validates user-friendly safe fields.
	if r.FormValue("tab") == "overview" {
		v, e := strconv.ParseBool(r.FormValue("start_on_boot"))
		if e != nil {
			return current, errors.New("Choose Start on Boot.")
		}
		current.StartOnBoot = v
		return current, nil
	}
	if r.FormValue("tab") == "version" {
		return current, errors.New("Software and version are read-only for this server.")
	}
	request, err := minecraftWorkspaceConfigurationRequest(r, api.AdminConfigurationDiscovery{})
	if err != nil {
		return current, err
	}
	switch r.FormValue("tab") {
	case "memory":
		current.JavaMemory = request.JavaMemory
		current.ContainerMemory = request.ContainerMemory
	case "gameplay":
		current.MOTD = request.MOTD
		current.MaxPlayers = request.MaxPlayers
		current.GameMode = request.GameMode
	case "crossplay":
		current.JavaPort = request.JavaPort
		current.BedrockEnabled = request.BedrockEnabled
		current.BedrockPort = request.BedrockPort
	default:
		return current, errors.New("Choose a supported settings section.")
	}
	return current, nil
}
func (a *App) selectedMinecraftSnapshot(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator", "operator", "viewer")
	if !ok {
		return
	}
	selected, ok := a.selectedMinecraft(w, r, session)
	if !ok {
		return
	}
	if selected.ID == "original" {
		writeMinecraftWorkspaceError(w, 400, "Use Original status.")
		return
	}
	client, ok := a.api.(selectedMinecraftAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Server status is unavailable.")
		return
	}
	c, err := client.MinecraftInstanceConfiguration(r.Context(), session, selected.ID)
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Server settings are unavailable.")
		return
	}
	state, err := client.MinecraftInstanceStatus(r.Context(), session, selected.ID)
	if err != nil || state.ID != selected.ID {
		a.writeMinecraftWorkspaceAPIError(w, err, "Server status is unavailable.")
		return
	}
	players, err := client.MinecraftInstancePlayers(r.Context(), session, selected.ID)
	if err != nil || players.ID != selected.ID {
		players.State = "unknown"
	}
	playerState := "unavailable"
	var online any = "Unavailable"
	if players.State == "online" {
		playerState, online = "online", players.Online
	} else if players.State == "offline" {
		playerState, online = "stopped", 0
	}
	writeMinecraftWorkspaceJSON(w, 200, map[string]any{"status": map[string]any{"minecraft": map[string]any{"configured": true, "state": state.State, "version": c.Version, "max_players": c.MaxPlayers, "start_on_boot": c.StartOnBoot}}, "players": map[string]any{"state": playerState, "online": online, "max": c.MaxPlayers}})
}
func (a *App) selectedMinecraftPresentation(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeMinecraftWorkspaceError(w, 403, "Invalid CSRF token.")
		return
	}
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	selected, ok := a.selectedMinecraft(w, r, session)
	if !ok {
		return
	}
	if selected.ID == "original" {
		writeMinecraftWorkspaceError(w, 400, "This action is available only for independent servers.")
		return
	}
	client, ok := a.api.(selectedMinecraftAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Server controls are unavailable.")
		return
	}
	var err error
	if strings.HasSuffix(r.URL.Path, "/rename") {
		err = client.RenameMinecraftInstance(r.Context(), session, selected.ID, r.FormValue("display_name"))
	} else {
		err = client.DeleteMinecraftInstance(r.Context(), session, selected.ID, r.FormValue("confirm_name"))
	}
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Server action did not complete. Retry the same action.")
		return
	}
	writeMinecraftWorkspaceJSON(w, 200, map[string]any{"ok": true, "redirect": "/minecraft/servers?deleted=yes"})
}
