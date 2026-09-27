package server

import (
	"context"
	"net/http"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type versionStatusAPI interface {
	AdminVersionStatus(context.Context, string, string, string) (api.AdminVersionStatus, error)
}

func (a *App) versionWorkspaceStatus(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(versionStatusAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, http.StatusServiceUnavailable, "Version information is unavailable.")
		return
	}
	status, err := client.AdminVersionStatus(r.Context(), session, r.URL.Query().Get("policy"), r.URL.Query().Get("version"))
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Version information is unavailable.")
		return
	}
	writeMinecraftWorkspaceJSON(w, http.StatusOK, status)
}
