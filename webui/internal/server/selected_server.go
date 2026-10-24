package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
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

// Selection resolves only registered immutable identities. Missing context is
// accepted only when the actual inventory contains exactly one server.
func (a *App) selectedMinecraft(w http.ResponseWriter, r *http.Request, session string) (selectedServerContext, bool) {
	values, exists := r.URL.Query()["server"]
	id := ""
	if exists {
		if len(values) != 1 || !addServerIDPattern.MatchString(values[0]) {
			writeMinecraftWorkspaceError(w, 400, "Invalid server selection.")
			return selectedServerContext{}, false
		}
		id = values[0]
	}
	instances, err := a.independentServerInventory(r.Context(), session)
	if err != nil {
		writeMinecraftWorkspaceError(w, 503, "Server selection could not be checked.")
		return selectedServerContext{}, false
	}
	if !exists {
		if len(instances) == 0 {
			writeMinecraftWorkspaceError(w, 409, "No Minecraft servers are configured. Add a Minecraft server.")
			return selectedServerContext{}, false
		}
		if len(instances) != 1 {
			writeMinecraftWorkspaceError(w, 409, "Choose a Minecraft server.")
			return selectedServerContext{}, false
		}
		id = instances[0].ID
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
	instances, err := a.independentServerInventory(r.Context(), session)
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Server list is unavailable.")
		return
	}
	selected := selectedServerContext{}
	if _, explicit := r.URL.Query()["server"]; explicit || (len(instances) == 1 && r.URL.Query().Get("selection") != "none") {
		var valid bool
		selected, valid = a.selectedMinecraft(w, r, session)
		if !valid {
			return
		}
	}
	choices := []selectedServerContext{}
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
	client, ok := a.api.(selectedMinecraftAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Selected server controls are unavailable.")
		return true
	}
	path := r.URL.Path
	if strings.Contains(path, "/whitelist") {
		a.selectedWhitelist(w, r, session, selected, client)
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
		minecraft["version_mode"] = current.VersionPolicy
		if current.VersionPolicy == "" {
			minecraft["version_mode"] = "pinned"
			if current.Version == "LATEST" {
				minecraft["version_mode"] = "latest"
			}
		}
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
		writeMinecraftWorkspaceJSON(w, 200, map[string]any{"ok": true, "configured": true, "minecraft": minecraft, "defaults": defaults})
		return true
	}
	if r.FormValue("tab") == "version" {
		a.selectedVersionReview(w, r, session, selected, current, apply)
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
	// Compare the entire definition so running changes cannot include other settings.
	bootComparison := current
	bootComparison.StartOnBoot = next.StartOnBoot
	bootOnly := current.StartOnBoot != next.StartOnBoot && reflect.DeepEqual(bootComparison, next)
	state, err := client.MinecraftInstanceStatus(r.Context(), session, selected.ID)
	if err != nil || state.ID != selected.ID || (state.State != "stopped" && !(bootOnly && state.State == "running")) {
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
	warnings := []string{"The server stays stopped. Start it normally when ready."}
	if bootOnly {
		warnings = []string{"Only Start on Boot changes. The server's current running or stopped state is preserved."}
	}
	writeMinecraftWorkspaceJSON(w, 200, map[string]any{"ok": true, "applied": apply, "review": review, "changes": changes, "warnings": warnings, "message": "Server settings saved."})
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

func (a *App) selectedWhitelist(w http.ResponseWriter, r *http.Request, session string, selected selectedServerContext, settings selectedMinecraftAPI) {
	client, ok := a.api.(interface {
		MinecraftInstanceWhitelist(context.Context, string, string, any) (api.TextOutputResponse, error)
	})
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Whitelist is unavailable.")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/state") {
		enabled, err := strconv.ParseBool(r.FormValue("enabled"))
		if err != nil {
			writeMinecraftWorkspaceError(w, 400, "Whitelist state is required.")
			return
		}
		current, err := settings.MinecraftInstanceConfiguration(r.Context(), session, selected.ID)
		if err != nil {
			a.writeMinecraftWorkspaceAPIError(w, err, "Whitelist is unavailable.")
			return
		}
		current.Whitelist = enabled
		if err = settings.SetMinecraftInstanceConfiguration(r.Context(), session, selected.ID, current); err != nil {
			a.writeMinecraftWorkspaceAPIError(w, err, "Whitelist change failed.")
			return
		}
		writeMinecraftWorkspaceJSON(w, 200, minecraftWorkspaceTextResponse{OK: true, Message: "Whitelist updated."})
		return
	}
	var request any
	if r.Method != http.MethodGet {
		request = map[string]string{"platform": r.FormValue("platform"), "action": r.FormValue("action"), "name": r.FormValue("name")}
	}
	result, err := client.MinecraftInstanceWhitelist(r.Context(), session, selected.ID, request)
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Whitelist is unavailable.")
		return
	}
	if r.Method != http.MethodGet {
		writeMinecraftWorkspaceJSON(w, 200, minecraftWorkspaceTextResponse{OK: true, Output: result.Output, Message: "Whitelist updated."})
		return
	}
	entries, parsed := parseMinecraftWorkspaceWhitelist(result.Output)
	_, identity, ok := a.minecraftWorkspaceIdentity(w, r, "administrator", "operator")
	if !ok {
		return
	}
	writeMinecraftWorkspaceJSON(w, 200, minecraftWorkspaceWhitelistResponse{OK: true, Entries: entries, Parsed: parsed, Output: result.Output, WhitelistEnabled: result.WhitelistEnabled, CanToggle: identity.Role == "administrator"})
}
