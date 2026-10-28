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
	ContextID, SoftwareIcon                                 string
	JavaMemory, ContainerMemory, StateClass                 string
	Running, CanSwitch, CanConfigureBoot                    bool
	JavaPort, BedrockPort                                   int
	ConfigurationAvailable, BedrockEnabled                  bool
	BootKnown, StartOnBoot                                  bool
	ConnectionAvailable, NetworkMember                      bool
	ConnectionJavaPort, ConnectionBedrockPort               int
	ConnectionBedrockEnabled                                bool
	MOTD                                                    string
	CanStart, CanStop, CanRestart                           bool
}

type minecraftInstanceInventoryAPI interface {
	MinecraftInstances(context.Context, string) ([]api.RegisteredMinecraftInstance, error)
	MinecraftInstanceRuntimes(context.Context, string) ([]string, error)
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
	case "pufferfish":
		return "Pufferfish"
	case "leaf":
		return "Leaf"
	case "vanilla":
		return "Vanilla"
	case "spigot":
		return "Spigot"
	case "bedrock":
		return "Bedrock"
	case "quilt":
		return "Quilt"
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

func serverSoftwareIcon(value string) string {
	switch strings.ToLower(value) {
	case "vanilla", "paper", "purpur", "pufferfish", "leaf", "spigot", "bedrock", "fabric", "forge", "neoforge", "quilt":
		return "/static/server-software/" + strings.ToLower(value) + ".svg"
	default:
		return ""
	}
}

func (s *minecraftServerSummary) setState(state string) {
	// Presentation of authoritative states only; no runtime inference here.
	s.StateClass = "warning"
	switch strings.ToLower(state) {
	case "running":
		s.State = "Running"
		s.StateClass = "success"
		s.Running, s.CanSwitch, s.CanConfigureBoot = true, true, true
		s.CanStop = true
		s.CanRestart = true
	case "stopped":
		s.State = "Stopped"
		s.StateClass = "error"
		s.CanSwitch, s.CanConfigureBoot = true, true
		s.CanStart = true
	case "starting":
		s.State = "Starting"
	case "stopping":
		s.State = "Stopping"
	case "failed":
		s.State = "Failed"
		s.StateClass = "error"
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

// The runtime inventory explicitly excludes the legacy identity and includes
// registered but unconfigured servers. Never guess identity from list position.
func (a *App) independentServerInventory(ctx context.Context, session string) ([]api.RegisteredMinecraftInstance, error) {
	client, ok := a.api.(minecraftInstanceInventoryAPI)
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
		if addServerIDPattern.MatchString(instance.ID) && independent[instance.ID] {
			instances = append(instances, instance)
		}
	}
	if recovery, ok := a.api.(interface {
		MinecraftInstanceDeletions(context.Context, string) ([]api.RegisteredMinecraftInstance, error)
	}); ok {
		pending, e := recovery.MinecraftInstanceDeletions(ctx, session)
		if e != nil {
			return nil, e
		}
		seen := map[string]bool{}
		for _, i := range instances {
			seen[i.ID] = true
		}
		for _, i := range pending {
			if !seen[i.ID] && addServerIDPattern.MatchString(i.ID) {
				instances = append(instances, i)
			}
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
	s := minecraftServerSummary{ContextID: instance.ID, Name: instance.DisplayName, URL: serverOverviewURL(instance.ID), Software: "Software unavailable", Version: "Version unavailable", State: "Status unavailable", StateClass: "warning"}
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
		s.SoftwareIcon = serverSoftwareIcon(c.Software)
		if c.Version != "" {
			s.Version = c.Version
		}
		s.JavaPort, s.BedrockEnabled, s.BedrockPort = c.JavaPort, c.BedrockEnabled, c.BedrockPort
		s.MOTD = c.MOTD
		s.JavaMemory, s.ContainerMemory = c.JavaMemory, c.ContainerMemory
		s.BootKnown, s.StartOnBoot = true, c.StartOnBoot
	} else {
		s.ConfigurationError = "Server configuration could not be checked."
	}
	if connectionAPI, ok := a.api.(interface {
		MinecraftInstanceConnection(context.Context, string, string) (api.MinecraftConnection, error)
	}); ok {
		connection, err := connectionAPI.MinecraftInstanceConnection(ctx, session, instance.ID)
		if err == nil && connection.ID == instance.ID {
			s.ConnectionAvailable, s.NetworkMember = true, connection.NetworkMember
			s.ConnectionJavaPort, s.ConnectionBedrockPort = connection.JavaPort, connection.BedrockPort
			s.ConnectionBedrockEnabled = connection.BedrockEnabled
		}
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
		s.CanSwitch, s.CanConfigureBoot = false, false
	}
	return s
}

func (a *App) minecraftServerInventory(ctx context.Context, session string) ([]minecraftServerSummary, string) {
	servers := []minecraftServerSummary{}
	instances, err := a.independentServerInventory(ctx, session)
	if err != nil {
		return servers, "Minecraft instances could not be checked. Try opening Minecraft Servers again."
	}
	for _, instance := range instances {
		servers = append(servers, a.independentServerSummary(ctx, session, instance))
	}
	return servers, ""
}

func (a *App) selectedServerSummary(w http.ResponseWriter, r *http.Request, session string) (minecraftServerSummary, bool) {
	id := r.PathValue("id")
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
	jsonRequest := r.Header.Get("Accept") == "application/json"
	if jsonRequest && (action != "restart" || !s.CanRestart) {
		writeMinecraftWorkspaceError(w, 409, "Server state does not permit Restart.")
		return
	}
	var err error
	if client, supported := a.api.(minecraftServerLifecycleAPI); supported {
		err = client.MinecraftInstanceAction(r.Context(), session, r.PathValue("id"), action)
	} else {
		err = errors.New("lifecycle unavailable")
	}
	if jsonRequest {
		if err != nil {
			a.writeMinecraftWorkspaceAPIError(w, err, "Restart could not be completed.")
		} else {
			writeMinecraftWorkspaceJSON(w, 200, map[string]bool{"ok": true})
		}
		return
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

// Card operations require explicit identity, even for a single-server appliance.
func (a *App) explicitCardServer(w http.ResponseWriter, r *http.Request, session string) (selectedServerContext, bool) {
	if _, exists := r.URL.Query()["server"]; !exists {
		writeMinecraftWorkspaceError(w, 400, "Choose a Minecraft server.")
		return selectedServerContext{}, false
	}
	return a.selectedMinecraft(w, r, session)
}

func (a *App) minecraftServersLifecycle(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	if !a.validCSRF(r) {
		writeMinecraftWorkspaceError(w, 403, "Invalid CSRF token.")
		return
	}
	action := r.FormValue("action")
	if action != "start" && action != "stop" {
		writeMinecraftWorkspaceError(w, 400, "Choose Start or Stop.")
		return
	}
	selected, ok := a.explicitCardServer(w, r, session)
	if !ok {
		return
	}
	summary := a.independentServerSummary(r.Context(), session, api.RegisteredMinecraftInstance{ID: selected.ID, DisplayName: selected.Name})
	if !summary.CanSwitch || (action == "start" && !summary.CanStart) || (action == "stop" && !summary.CanStop) {
		writeMinecraftWorkspaceError(w, 409, "Server state does not permit this action. Refresh Minecraft Servers.")
		return
	}
	client, ok := a.api.(minecraftServerLifecycleAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Server controls are unavailable.")
		return
	}
	if err := client.MinecraftInstanceAction(r.Context(), session, selected.ID, action); err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Server action could not be completed. Check server status before trying again.")
		return
	}
	writeMinecraftWorkspaceJSON(w, 200, map[string]bool{"ok": true})
}
