package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type minecraftNetworkAPI interface {
	MinecraftNetwork(context.Context, string) (api.MinecraftNetworkStatus, error)
	ConfigureMinecraftNetwork(context.Context, string, api.MinecraftNetworkConfig) error
	RecoverMinecraftNetwork(context.Context, string) error
	MinecraftNetworkWhitelist(context.Context, string, string, string, string) error
}
type minecraftNetworkRow struct {
	ID, Name, Software, State string
	Eligible, Selected        bool
}
type minecraftNetworkPage struct {
	Title, Version, ManagementAPI, CSRF, Error, Message string
	Network                                             api.MinecraftNetworkStatus
	Servers                                             []minecraftNetworkRow
}

func (a *App) minecraftNetwork(w http.ResponseWriter, r *http.Request) {
	if mustChange(r) {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	session, _, _, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/minecraft/network" {
		http.Redirect(w, r, "/?workspace=minecraft-network", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodPost && !a.validCSRF(r) {
		http.Error(w, "Invalid CSRF token.", http.StatusForbidden)
		return
	}
	client, ok := a.api.(minecraftNetworkAPI)
	if !ok {
		http.Error(w, "Minecraft Network is unavailable.", 503)
		return
	}
	data := minecraftNetworkPage{Title: "Minecraft Network", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI, CSRF: csrfFromRequest(r)}
	if r.Method == http.MethodPost {
		var err error
		switch r.FormValue("action") {
		case "configure":
			java, e1 := strconv.Atoi(r.FormValue("java_port"))
			bedrock, e2 := strconv.Atoi(r.FormValue("bedrock_port"))
			if e1 != nil || e2 != nil {
				err = errors.New("invalid ports")
			} else {
				err = client.ConfigureMinecraftNetwork(r.Context(), session, api.MinecraftNetworkConfig{Enabled: r.FormValue("enabled") == "on", JavaPort: java, BedrockPort: bedrock, Members: r.Form["members"], WelcomeMessage: r.FormValue("welcome_message")})
			}
		case "recover":
			err = client.RecoverMinecraftNetwork(r.Context(), session)
		case "add", "remove":
			err = client.MinecraftNetworkWhitelist(r.Context(), session, r.FormValue("platform"), r.FormValue("action"), r.FormValue("name"))
		default:
			http.Error(w, "Invalid Minecraft Network operation.", 400)
			return
		}
		if err != nil {
			data.Error = "Minecraft Network could not complete this change. If recovery is required, choose Retry recovery."
			if message, ok := api.ErrorMessage(err); ok {
				data.Error = message
			}
		} else {
			data.Message = "Minecraft Network was updated."
		}
	}
	network, err := client.MinecraftNetwork(r.Context(), session)
	if err != nil {
		http.Error(w, "Minecraft Network status is unavailable.", 503)
		return
	}
	data.Network = network
	inventory, err := a.independentServerInventory(r.Context(), session)
	if err != nil {
		http.Error(w, "Minecraft server list is unavailable.", 503)
		return
	}
	names := map[string]string{}
	for _, instance := range inventory {
		names[instance.ID] = instance.DisplayName
	}
	for _, server := range network.Servers {
		selected := false
		for _, id := range network.Config.Members {
			if id == server.ID {
				selected = true
			}
		}
		data.Servers = append(data.Servers, minecraftNetworkRow{ID: server.ID, Name: names[server.ID], Software: serverSoftware(server.Software), State: server.State, Eligible: server.Eligible, Selected: selected})
	}
	a.renderAdminDiscovery(w, "minecraft_network.html", data)
}

// Only connection/invitation presentation changes. Stored per-instance settings
// continue to show and preserve each server's standalone port choices.
func (a *App) minecraftConnectionSettings(ctx context.Context, session, id string, c api.InstanceRuntimeSettings) (api.InstanceRuntimeSettings, error) {
	client, ok := a.api.(interface {
		MinecraftInstanceConnection(context.Context, string, string) (api.MinecraftConnection, error)
	})
	if !ok {
		return c, nil
	}
	connection, err := client.MinecraftInstanceConnection(ctx, session, id)
	if err != nil {
		return c, err
	}
	if connection.ID != id {
		return c, errors.New("connection identity mismatch")
	}
	c.JavaPort, c.BedrockPort, c.BedrockEnabled = connection.JavaPort, connection.BedrockPort, connection.BedrockEnabled
	return c, nil
}
