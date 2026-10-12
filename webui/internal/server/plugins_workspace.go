package server

import (
	"context"
	"net/http"
	"net/url"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type pluginsWorkspaceAPI interface {
	AdminPluginInventory(context.Context, string, bool) (api.PluginInventory, error)
	AdminPluginDiscover(context.Context, string, url.Values) (api.PluginCatalog, error)
}

func (a *App) registerPluginsWorkspaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/plugins/workspace/installed", a.pluginsWorkspaceInventory)
	mux.HandleFunc("GET /api/plugins/workspace/updates", a.pluginsWorkspaceInventory)
	mux.HandleFunc("GET /api/plugins/workspace/discover", a.pluginsWorkspaceDiscover)
}
func (a *App) pluginsWorkspaceInventory(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(pluginsWorkspaceAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, http.StatusServiceUnavailable, "Plugin inventory is unavailable.")
		return
	}
	out, err := client.AdminPluginInventory(r.Context(), session, r.URL.Path == "/api/plugins/workspace/updates")
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Plugin inventory is unavailable.")
		return
	}
	writeMinecraftWorkspaceJSON(w, http.StatusOK, out)
}
func (a *App) pluginsWorkspaceDiscover(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(pluginsWorkspaceAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, http.StatusServiceUnavailable, "Plugin catalog is unavailable.")
		return
	}
	out, err := client.AdminPluginDiscover(r.Context(), session, r.URL.Query())
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Plugin catalog is unavailable.")
		return
	}
	writeMinecraftWorkspaceJSON(w, http.StatusOK, out)
}
