package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

// Only public, displayable fields reach the templates. Never pass discovery or
// runtime objects wholesale: legacy discovery also contains private host paths.
type minecraftServerSummary struct {
	Name, URL, Software, Version, State, ConfigurationError string
	JavaPort, BedrockPort                                   int
	ConfigurationAvailable, BedrockEnabled                  bool
	BootKnown, StartOnBoot                                  bool
	MOTD                                                    string
	CanStart, CanStop, CanRestart                           bool
}

type minecraftServerInventoryAPI interface {
	MinecraftInstances(context.Context, string) ([]api.RegisteredMinecraftInstance, error)
	MinecraftInstanceRuntimes(context.Context, string) ([]string, error)
	MinecraftInstanceConfiguration(context.Context, string, string) (api.InstanceRuntimeSettings, error)
}

type minecraftServerLifecycleAPI interface {
	MinecraftInstanceStatus(context.Context, string, string) (api.MinecraftInstanceStatus, error)
	MinecraftInstanceAction(context.Context, string, string, string) error
}

func serverOverviewURL(id string) string { return minecraftServersPath + "/view/" + url.PathEscape(id) }

func serverSoftware(value string) string {
	switch strings.ToLower(value) {
	case "paper":
		return "Paper"
	case "purpur":
		return "Purpur"
	case "vanilla":
		return "Vanilla"
	case "fabric":
		return "Fabric"
	case "forge":
		return "Forge"
	case "neoforge":
		return "NeoForge"
	default:
		return "Software unavailable"
	}
}

func (s *minecraftServerSummary) setState(state string) {
	// Presentation of authoritative states only; no runtime inference here.
	switch strings.ToLower(state) {
	case "running":
		s.State = "Running"
		s.CanStop = true
		s.CanRestart = true
	case "stopped":
		s.State = "Stopped"
		s.CanStart = true
	case "starting":
		s.State = "Starting"
	case "stopping":
		s.State = "Stopping"
	case "failed":
		s.State = "Failed"
		s.CanStart = true
	case "configured":
		s.State = "Configured"
	case "recovery_required":
		s.State = "Recovery required"
	case "invalid_configuration":
		s.State = "Configuration needs attention"
	case "degraded":
		s.State = "Degraded"
	case "not_configured", "not configured":
		s.State = "Not configured"
	default:
		s.State = "Status unavailable"
	}
}

func (a *App) originalServerSummary(ctx context.Context, session string) minecraftServerSummary {
	s := minecraftServerSummary{Name: "Original server", URL: serverOverviewURL("original"), State: "Status unavailable", Software: "Software unavailable", Version: "Version unavailable"}
	if client, ok := a.api.(interface {
		AdminConfiguration(context.Context, string) (api.AdminConfigurationDiscovery, error)
	}); ok {
		config, err := client.AdminConfiguration(ctx, session)
		if err == nil && config.Configured {
			c := config.Minecraft
			s.ConfigurationAvailable = true
			s.Software = serverSoftware(c.ServerType)
			if c.Version != "" {
				s.Version = c.Version
			}
			s.JavaPort, s.BedrockEnabled, s.BedrockPort, s.MOTD = c.JavaPort, c.BedrockEnabled, c.BedrockPort, c.MOTD
		}
	}
	if !s.ConfigurationAvailable {
		s.ConfigurationError = "Server configuration could not be checked."
	}
	status, err := a.api.Status(ctx, session)
	if err == nil {
		s.setState(status.Minecraft.State)
		if status.Minecraft.ServerSoftware != "" {
			s.Software = serverSoftware(status.Minecraft.ServerSoftware)
		}
		if status.Minecraft.Version != "" {
			s.Version = status.Minecraft.Version
		}
	}
	return s
}

// The runtime inventory explicitly excludes the legacy identity and includes
// registered but unconfigured servers. Never guess identity from list position.
func (a *App) independentServerInventory(ctx context.Context, session string) ([]api.RegisteredMinecraftInstance, error) {
	client, ok := a.api.(minecraftServerInventoryAPI)
	if !ok {
		return nil, errors.New("inventory unavailable")
	}
	registry, err := client.MinecraftInstances(ctx, session)
	if err != nil {
		return nil, err
	}
	ids, err := client.MinecraftInstanceRuntimes(ctx, session)
	if err != nil {
		return nil, err
	}
	independent := make(map[string]bool, len(ids))
	for _, id := range ids {
		independent[id] = true
	}
	instances := []api.RegisteredMinecraftInstance{}
	for _, instance := range registry {
		if independent[instance.ID] {
			instances = append(instances, instance)
		}
	}
	sort.Slice(instances, func(i, j int) bool {
		left, right := strings.ToLower(instances[i].DisplayName), strings.ToLower(instances[j].DisplayName)
		if left == right {
			return instances[i].ID < instances[j].ID
		}
		return left < right
	})
	return instances, nil
}

func (a *App) independentServerSummary(ctx context.Context, session string, instance api.RegisteredMinecraftInstance) minecraftServerSummary {
	s := minecraftServerSummary{Name: instance.DisplayName, URL: serverOverviewURL(instance.ID), Software: "Software unavailable", Version: "Version unavailable", State: "Status unavailable"}
	if s.Name == "" {
		s.Name = "Minecraft server"
	}
	client, ok := a.api.(minecraftServerInventoryAPI)
	if !ok {
		return s
	}
	c, err := client.MinecraftInstanceConfiguration(ctx, session, instance.ID)
	if err == nil {
		s.ConfigurationAvailable = true
		s.Software = serverSoftware(c.Software)
		if c.Version != "" {
			s.Version = c.Version
		}
		s.JavaPort, s.BedrockEnabled, s.BedrockPort = c.JavaPort, c.BedrockEnabled, c.BedrockPort
		s.MOTD = c.MOTD
		s.BootKnown, s.StartOnBoot = true, c.StartOnBoot
	} else {
		s.ConfigurationError = "Server configuration could not be checked."
	}
	if lifecycle, ok := a.api.(minecraftServerLifecycleAPI); ok {
		status, err := lifecycle.MinecraftInstanceStatus(ctx, session, instance.ID)
		if err == nil && status.ID == instance.ID {
			s.setState(status.State)
		}
	}
	// A summary whose configuration cannot be checked offers no lifecycle controls.
	if !s.ConfigurationAvailable {
		s.CanStart, s.CanStop, s.CanRestart = false, false, false
	}
	return s
}

func (a *App) minecraftServerInventory(ctx context.Context, session string) ([]minecraftServerSummary, string) {
	servers := []minecraftServerSummary{a.originalServerSummary(ctx, session)}
	instances, err := a.independentServerInventory(ctx, session)
	if err != nil {
		return servers, "Independent servers could not be checked. Try opening Minecraft Servers again."
	}
	for _, instance := range instances {
		servers = append(servers, a.independentServerSummary(ctx, session, instance))
	}
	return servers, ""
}

func (a *App) selectedServerSummary(w http.ResponseWriter, r *http.Request, session string) (minecraftServerSummary, bool) {
	id := r.PathValue("id")
	if id == "original" {
		return a.originalServerSummary(r.Context(), session), true
	}
	if !addServerIDPattern.MatchString(id) {
		http.Error(w, "Server not found.", http.StatusNotFound)
		return minecraftServerSummary{}, false
	}
	instances, err := a.independentServerInventory(r.Context(), session)
	if err != nil {
		http.Error(w, "Server inventory is unavailable.", http.StatusServiceUnavailable)
		return minecraftServerSummary{}, false
	}
	for _, instance := range instances {
		if instance.ID == id {
			return a.independentServerSummary(r.Context(), session, instance), true
		}
	}
	http.Error(w, "Server not found.", http.StatusNotFound)
	return minecraftServerSummary{}, false
}

func (a *App) minecraftSelectedServer(w http.ResponseWriter, r *http.Request) {
	if mustChange(r) {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	session, _, identity, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	s, ok := a.selectedServerSummary(w, r, session)
	if !ok {
		return
	}
	a.renderAdminDiscovery(w, "minecraft_server_overview.html", minecraftServersPageData{Title: s.Name, Version: a.config.Version, ManagementAPI: a.config.ManagementAPI, Identity: identity, CSRF: csrfFromRequest(r), Selected: &s})
}

func (a *App) minecraftSelectedServerAction(w http.ResponseWriter, r *http.Request) {
	if mustChange(r) {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	if !a.validCSRF(r) {
		http.Error(w, "Invalid CSRF token.", http.StatusForbidden)
		return
	}
	session, _, identity, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	action := r.PathValue("action")
	switch action {
	case "start", "stop", "restart":
	default:
		http.NotFound(w, r)
		return
	}
	s, ok := a.selectedServerSummary(w, r, session)
	if !ok {
		return
	}
	var err error
	if r.PathValue("id") == "original" {
		var result api.MinecraftActionResponse
		result, err = a.api.MinecraftAction(r.Context(), session, action, false)
		if err == nil && (result.ConfirmationRequired || !result.OK) {
			message := result.Message
			if message == "" {
				message = "The server could not be interrupted safely. Check player and server status before trying again."
			}
			err = &api.ResponseError{StatusCode: http.StatusConflict, Message: message}
		}
	} else if client, supported := a.api.(minecraftServerLifecycleAPI); supported {
		err = client.MinecraftInstanceAction(r.Context(), session, r.PathValue("id"), action)
	} else {
		err = errors.New("lifecycle unavailable")
	}
	if err == nil {
		http.Redirect(w, r, s.URL, http.StatusSeeOther)
		return
	}
	if errors.Is(err, api.ErrUnauthorized) || errors.Is(err, api.ErrPasswordChangeRequired) {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	message := "Server action could not be completed. Check server status before trying again."
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) && responseErr.Message != "" {
		message = responseErr.Message
	}
	// Render the refusal in the selected workspace, without retries or overrides.
	a.renderAdminDiscovery(w, "minecraft_server_overview.html", minecraftServersPageData{Title: s.Name, Version: a.config.Version, ManagementAPI: a.config.ManagementAPI, Identity: identity, CSRF: csrfFromRequest(r), Selected: &s, Error: message})
}
