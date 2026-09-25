package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	Whitelist(ctx context.Context, session string) (api.TextOutputResponse, error)
	WhitelistChange(ctx context.Context, session, platform, action, name string) (api.TextOutputResponse, error)
	MinecraftLogs(ctx context.Context, session string, limit int) (api.LogsResponse, error)
}

type minecraftWorkspaceSettingsResponse struct {
	OK            bool                            `json:"ok"`
	Configured    bool                            `json:"configured"`
	Minecraft     api.AdminMinecraftConfiguration `json:"minecraft"`
	Defaults      api.AdminSetupDefaults          `json:"defaults"`
}

type minecraftWorkspaceTextResponse struct {
	OK      bool   `json:"ok"`
	Output  string `json:"output,omitempty"`
	Message string `json:"message,omitempty"`
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
	mux.HandleFunc("GET /api/minecraft/workspace/settings", a.minecraftWorkspaceSettings)
	mux.HandleFunc("POST /api/minecraft/workspace/settings/plan", a.minecraftWorkspaceSettingsPlan)
	mux.HandleFunc("POST /api/minecraft/workspace/settings/apply", a.minecraftWorkspaceSettingsApply)
	mux.HandleFunc("GET /api/minecraft/workspace/whitelist", a.minecraftWorkspaceWhitelist)
	mux.HandleFunc("POST /api/minecraft/workspace/whitelist", a.minecraftWorkspaceWhitelistChange)
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
	client, ok := a.api.(minecraftWorkspaceSettingsAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, http.StatusServiceUnavailable, "Minecraft settings are unavailable.")
		return
	}
	configuration, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Minecraft settings are unavailable.")
		return
	}
	defaults, err := client.AdminSetupDefaults(r.Context(), session)
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Minecraft settings defaults are unavailable.")
		return
	}
	writeMinecraftWorkspaceJSON(w, http.StatusOK, minecraftWorkspaceSettingsResponse{
		OK: true, Configured: configuration.Configured, Minecraft: configuration.Minecraft, Defaults: defaults,
	})
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
	client, ok := a.api.(minecraftWorkspaceSettingsAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, http.StatusServiceUnavailable, "Minecraft settings are unavailable.")
		return
	}
	current, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Minecraft settings are unavailable.")
		return
	}
	if !current.Configured {
		writeMinecraftWorkspaceError(w, http.StatusConflict, "Minecraft is not configured yet.")
		return
	}
	request, err := minecraftWorkspaceConfigurationRequest(r, current)
	if err != nil {
		writeMinecraftWorkspaceError(w, http.StatusBadRequest, err.Error())
		return
	}
	var result api.AdminConfigurationChangeResponse
	if apply {
		result, err = client.AdminConfigurationApply(r.Context(), session, request)
	} else {
		result, err = client.AdminConfigurationPlan(r.Context(), session, request)
	}
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Minecraft settings could not be processed.")
		return
	}
	writeMinecraftWorkspaceJSON(w, http.StatusOK, result)
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
	case "players":
		maxPlayers, err := parsePositiveFormInt(r.FormValue("max_players"), "Maximum players")
		if err != nil {
			return request, err
		}
		request.MaxPlayers = maxPlayers
		request.MOTD = strings.TrimSpace(r.FormValue("motd"))
		request.Timezone = strings.TrimSpace(r.FormValue("timezone"))
		if request.MOTD == "" || request.Timezone == "" {
			return request, errors.New("Server welcome message and timezone are required.")
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
	client, ok := a.api.(minecraftWorkspaceOperationsAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, http.StatusServiceUnavailable, "Minecraft whitelist is unavailable.")
		return
	}
	result, err := client.Whitelist(r.Context(), session)
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Minecraft whitelist is unavailable.")
		return
	}
	writeMinecraftWorkspaceJSON(w, http.StatusOK, minecraftWorkspaceTextResponse{OK: true, Output: result.Output})
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
	client, ok := a.api.(minecraftWorkspaceOperationsAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, http.StatusServiceUnavailable, "Minecraft whitelist is unavailable.")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeMinecraftWorkspaceError(w, http.StatusBadRequest, "Could not read the whitelist request.")
		return
	}
	platform := strings.ToLower(strings.TrimSpace(r.FormValue("platform")))
	action := strings.ToLower(strings.TrimSpace(r.FormValue("action")))
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		writeMinecraftWorkspaceError(w, http.StatusBadRequest, "Player name or UUID is required.")
		return
	}
	result, err := client.WhitelistChange(r.Context(), session, platform, action, name)
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Whitelist change was rejected.")
		return
	}
	writeMinecraftWorkspaceJSON(w, http.StatusOK, minecraftWorkspaceTextResponse{
		OK: true, Output: result.Output, Message: "Whitelist updated.",
	})
}

func (a *App) minecraftWorkspaceLogs(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator", "operator")
	if !ok {
		return
	}
	client, ok := a.api.(minecraftWorkspaceOperationsAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, http.StatusServiceUnavailable, "Minecraft logs are unavailable.")
		return
	}
	result, err := client.MinecraftLogs(r.Context(), session, 100)
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Minecraft logs are unavailable.")
		return
	}
	if result.Lines == nil {
		result.Lines = []string{}
	}
	writeMinecraftWorkspaceJSON(w, http.StatusOK, minecraftWorkspaceLogsResponse{OK: true, Lines: result.Lines})
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
