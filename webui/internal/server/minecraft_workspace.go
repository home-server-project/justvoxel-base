package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type minecraftWorkspaceSettingsAPI interface {
	AdminConfiguration(ctx context.Context, session string) (api.AdminConfigurationDiscovery, error)
	AdminSetupDefaults(ctx context.Context, session string) (api.AdminSetupDefaults, error)
	AdminConfigurationPlan(ctx context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error)
	AdminConfigurationApply(ctx context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error)
}

type minecraftWorkspaceOperationsAPI interface {
	MinecraftLogs(ctx context.Context, session, id string, limit int) (api.LogsResponse, error)
}

type minecraftWorkspaceSettingsResponse struct {
	OK         bool                            `json:"ok"`
	Configured bool                            `json:"configured"`
	Minecraft  api.AdminMinecraftConfiguration `json:"minecraft"`
	Defaults   api.AdminSetupDefaults          `json:"defaults"`
}

type minecraftWorkspaceTextResponse struct {
	OK      bool   `json:"ok"`
	Output  string `json:"output,omitempty"`
	Message string `json:"message,omitempty"`
}

type minecraftWorkspaceWhitelistEntry struct {
	Display  string `json:"display"`
	Platform string `json:"platform"`
	Identity string `json:"identity"`
}

type minecraftWorkspaceWhitelistResponse struct {
	WhitelistEnabled *bool                              `json:"whitelist_enabled"`
	CanToggle        bool                               `json:"can_toggle"`
	OK               bool                               `json:"ok"`
	Entries          []minecraftWorkspaceWhitelistEntry `json:"entries"`
	Parsed           bool                               `json:"parsed"`
	Output           string                             `json:"output,omitempty"`
}

var minecraftWhitelistListPattern = regexp.MustCompile(`^There are ([0-9]+) whitelisted player\(s\):\s*(.*)$`)
var minecraftWhitelistIdentityPattern = regexp.MustCompile(`^[.A-Za-z0-9_ ]+$`)

// Parse only the known list format; retain the original output for diagnostics.
// Display names are never used to deduplicate Java and Floodgate identities.
func parseMinecraftWorkspaceWhitelist(output string) ([]minecraftWorkspaceWhitelistEntry, bool) {
	entries := []minecraftWorkspaceWhitelistEntry{}
	output = strings.TrimSpace(output)
	if output == "" || output == "There are no whitelisted players" {
		return entries, true
	}
	match := minecraftWhitelistListPattern.FindStringSubmatch(output)
	if match == nil {
		return entries, false
	}
	count, err := strconv.Atoi(match[1])
	if err != nil {
		return entries, false
	}
	if count == 0 && match[2] == "" {
		return entries, true
	}
	identities := strings.Split(match[2], ",")
	if len(identities) != count {
		return entries, false
	}
	for _, value := range identities {
		identity := strings.TrimSpace(value)
		display := identity
		platform := "java"
		if strings.HasPrefix(identity, ".") {
			display = strings.TrimPrefix(identity, ".")
			platform = "bedrock"
		}
		if display == "" || len(identity) > 64 || !minecraftWhitelistIdentityPattern.MatchString(identity) {
			return []minecraftWorkspaceWhitelistEntry{}, false
		}
		entries = append(entries, minecraftWorkspaceWhitelistEntry{Display: display, Platform: platform, Identity: identity})
	}
	return entries, true
}

type minecraftWorkspaceLogsResponse struct {
	OK    bool     `json:"ok"`
	Lines []string `json:"lines"`
}

type minecraftWorkspaceErrorResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func (a *App) registerMinecraftWorkspaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/minecraft/workspace/selection", a.minecraftSelection)
	mux.HandleFunc("GET /api/minecraft/workspace/snapshot", a.selectedMinecraftSnapshot)
	mux.HandleFunc("POST /api/minecraft/workspace/rename", a.selectedMinecraftPresentation)
	mux.HandleFunc("POST /api/minecraft/workspace/delete", a.selectedMinecraftPresentation)
	mux.HandleFunc("GET /api/minecraft/workspace/identity", a.minecraftWorkspaceInstance)
	mux.HandleFunc("POST /api/minecraft/workspace/identity/ensure", a.minecraftWorkspaceInstance)
	mux.HandleFunc("POST /api/minecraft/workspace/identity/manual", a.minecraftWorkspaceInstance)
	mux.HandleFunc("GET /api/version/workspace/status", a.versionWorkspaceStatus)
	mux.HandleFunc("POST /api/version/workspace/update", a.versionWorkspaceUpdate)
	mux.HandleFunc("GET /api/version/workspace/update-operation", a.versionWorkspaceUpdateOperation)
	mux.HandleFunc("GET /api/minecraft/workspace/settings", a.minecraftWorkspaceSettings)
	mux.HandleFunc("POST /api/minecraft/workspace/settings/plan", a.minecraftWorkspaceSettingsPlan)
	mux.HandleFunc("POST /api/minecraft/workspace/settings/apply", a.minecraftWorkspaceSettingsApply)
	mux.HandleFunc("GET /api/minecraft/workspace/whitelist", a.minecraftWorkspaceWhitelist)
	mux.HandleFunc("POST /api/minecraft/workspace/whitelist", a.minecraftWorkspaceWhitelistChange)
	mux.HandleFunc("POST /api/minecraft/workspace/whitelist/state", a.minecraftWorkspaceWhitelistState)
	mux.HandleFunc("GET /api/minecraft/workspace/logs", a.minecraftWorkspaceLogs)
}

func (a *App) minecraftWorkspaceIdentity(w http.ResponseWriter, r *http.Request, allowed ...string) (string, api.SessionInfo, bool) {
	if mustChange(r) {
		writeMinecraftWorkspaceError(w, http.StatusForbidden, "Password change required.")
		return "", api.SessionInfo{}, false
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		writeMinecraftWorkspaceError(w, http.StatusUnauthorized, "Authentication required.")
		return "", api.SessionInfo{}, false
	}
	client, ok := a.api.(sessionIdentityAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, http.StatusServiceUnavailable, "Role-aware controls are unavailable.")
		return "", api.SessionInfo{}, false
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Session information is unavailable.")
		return "", api.SessionInfo{}, false
	}
	if identity.MustChange {
		writeMinecraftWorkspaceError(w, 403, "Password change required.")
		return "", api.SessionInfo{}, false
	}
	for _, role := range allowed {
		if identity.Role == role {
			return session, identity, true
		}
	}
	writeMinecraftWorkspaceError(w, http.StatusForbidden, "Operation not permitted for this role.")
	return "", api.SessionInfo{}, false
}

func (a *App) minecraftWorkspaceSettings(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	a.selectedMinecraftWorkspace(w, r, session, false)
}

func (a *App) minecraftWorkspaceSettingsPlan(w http.ResponseWriter, r *http.Request) {
	a.minecraftWorkspaceSettingsChange(w, r, false)
}

func (a *App) minecraftWorkspaceSettingsApply(w http.ResponseWriter, r *http.Request) {
	a.minecraftWorkspaceSettingsChange(w, r, true)
}

func (a *App) minecraftWorkspaceSettingsChange(w http.ResponseWriter, r *http.Request, apply bool) {
	if !a.validCSRF(r) {
		writeMinecraftWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
		return
	}
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	a.selectedMinecraftWorkspace(w, r, session, apply)
}

func minecraftWorkspaceConfigurationRequest(r *http.Request, current api.AdminConfigurationDiscovery) (api.AdminConfigurationChangeRequest, error) {
	if err := r.ParseForm(); err != nil {
		return api.AdminConfigurationChangeRequest{}, errors.New("Could not read the Minecraft settings.")
	}
	request := configurationRequestFromDiscovery(current)
	request.ConfirmPlayers = r.FormValue("confirm_players") == "yes"

	switch strings.TrimSpace(r.FormValue("tab")) {
	case "memory":
		request.JavaMemory = strings.TrimSpace(r.FormValue("java_memory"))
		request.ContainerMemory = strings.TrimSpace(r.FormValue("container_memory"))
		if request.JavaMemory == "" || request.ContainerMemory == "" {
			return request, errors.New("Minecraft memory values are required.")
		}
	case "gameplay":
		maxPlayers, err := parsePositiveFormInt(r.FormValue("max_players"), "Maximum players")
		if err != nil {
			return request, err
		}
		request.MaxPlayers = maxPlayers
		request.MOTD = strings.TrimSpace(r.FormValue("motd"))
		if request.MOTD == "" {
			return request, errors.New("Server welcome message is required.")
		}
		request.GameMode = r.FormValue("game_mode")
		switch request.GameMode {
		case "survival", "creative", "adventure", "spectator":
		default:
			return request, errors.New("Choose a valid Minecraft game mode.")
		}
	case "crossplay":
		javaPort, err := parsePositiveFormInt(r.FormValue("java_port"), "Minecraft Java port")
		if err != nil {
			return request, err
		}
		bedrockPort, err := parsePositiveFormInt(r.FormValue("bedrock_port"), "Bedrock port")
		if err != nil {
			return request, err
		}
		if javaPort > 65535 || bedrockPort > 65535 {
			return request, errors.New("Minecraft ports must be between 1 and 65535.")
		}
		request.JavaPort = javaPort
		request.BedrockPort = bedrockPort
		request.BedrockEnabled = r.FormValue("bedrock_enabled") == "on"
	case "version":
		request.ImageTag = strings.TrimSpace(r.FormValue("image_tag"))
		request.VersionPolicy = strings.TrimSpace(r.FormValue("version_policy"))
		request.Version = strings.TrimSpace(r.FormValue("version"))
		if request.ImageTag == "" || request.VersionPolicy == "" {
			return request, errors.New("Minecraft image tag and version policy are required.")
		}
	default:
		return request, errors.New("Choose a Minecraft settings section.")
	}
	return request, nil
}

func (a *App) minecraftWorkspaceWhitelist(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator", "operator")
	if !ok {
		return
	}
	a.selectedMinecraftWorkspace(w, r, session, false)
}

func (a *App) minecraftWorkspaceWhitelistChange(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeMinecraftWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
		return
	}
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator", "operator")
	if !ok {
		return
	}
	a.selectedMinecraftWorkspace(w, r, session, false)
}

func (a *App) minecraftWorkspaceLogs(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator", "operator")
	if !ok {
		return
	}
	a.selectedMinecraftWorkspace(w, r, session, false)
}

func (a *App) writeMinecraftWorkspaceAPIError(w http.ResponseWriter, err error, fallback string) {
	status := http.StatusBadGateway
	message := fallback
	if errors.Is(err, api.ErrUnauthorized) {
		status = http.StatusUnauthorized
		message = "Authentication required."
	} else if errors.Is(err, api.ErrPasswordChangeRequired) {
		status = http.StatusForbidden
		message = "Password change required."
	} else {
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) {
			if responseErr.StatusCode >= 400 && responseErr.StatusCode <= 599 {
				status = responseErr.StatusCode
			}
			if responseErr.Message != "" {
				message = responseErr.Message
			}
		}
	}
	writeMinecraftWorkspaceError(w, status, message)
}

func writeMinecraftWorkspaceError(w http.ResponseWriter, status int, message string) {
	writeMinecraftWorkspaceJSON(w, status, minecraftWorkspaceErrorResponse{OK: false, Error: message})
}

func writeMinecraftWorkspaceJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// Only the administrator can change the authoritative whitelist setting.
func (a *App) minecraftWorkspaceWhitelistState(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeMinecraftWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
		return
	}
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	a.selectedMinecraftWorkspace(w, r, session, false)
}

func (a *App) minecraftWorkspaceInstance(w http.ResponseWriter, r *http.Request) {
	roles := []string{"administrator", "operator", "viewer"}
	if r.Method == http.MethodPost {
		if !a.validCSRF(r) {
			writeMinecraftWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
			return
		}
		roles = []string{"administrator"}
	}
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, roles...)
	if !ok {
		return
	}
	selected, ok := a.selectedMinecraft(w, r, session)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeMinecraftWorkspaceError(w, http.StatusConflict, "Server identity is immutable.")
		return
	}
	writeMinecraftWorkspaceJSON(w, http.StatusOK, api.MinecraftIdentity{Status: "available", ID: selected.ID})
}
