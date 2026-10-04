package server

import (
	"context"
	"net/http"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type versionStatusAPI interface {
	AdminVersionStatus(context.Context, string, string, string, string) (api.AdminVersionStatus, error)
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
	status, err := client.AdminVersionStatus(r.Context(), session, r.URL.Query().Get("policy"), r.URL.Query().Get("version"), r.URL.Query().Get("server_type"))
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Version information is unavailable.")
		return
	}
	writeMinecraftWorkspaceJSON(w, http.StatusOK, status)
}

type versionUpdateAPI interface {
	AdminMinecraftStackUpdate(context.Context, string, api.MinecraftStackUpdateRequest) (api.PersistentOperationResponse, error)
	AdminOperation(context.Context, string, string) (api.PersistentOperationResponse, error)
	AdminCurrentMinecraftStackUpdate(context.Context, string) (api.PersistentOperationResponse, error)
	AdminAcknowledgeMinecraftStackUpdate(context.Context, string, string) (api.PersistentOperationResponse, error)
}

func (a *App) versionWorkspaceUpdate(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeMinecraftWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
		return
	}
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(versionUpdateAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, http.StatusServiceUnavailable, "Server updates are unavailable.")
		return
	}
	if r.FormValue("acknowledge") == "yes" {
		result, err := client.AdminAcknowledgeMinecraftStackUpdate(r.Context(), session, r.FormValue("operation_id"))
		if err != nil {
			a.writeMinecraftWorkspaceAPIError(w, err, "Current Minecraft runtime could not be verified.")
			return
		}
		writeMinecraftWorkspaceJSON(w, http.StatusOK, result)
		return
	}
	result, err := client.AdminMinecraftStackUpdate(r.Context(), session, api.MinecraftStackUpdateRequest{
		ServerType: r.FormValue("server_type"), PlanFingerprint: r.FormValue("plan_fingerprint"), ConfirmPlayers: r.FormValue("confirm_players") == "yes",
	})
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Server update could not start.")
		return
	}
	writeMinecraftWorkspaceJSON(w, http.StatusAccepted, result)
}

func (a *App) versionWorkspaceUpdateOperation(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(versionUpdateAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, http.StatusServiceUnavailable, "Server update status is unavailable.")
		return
	}
	var result api.PersistentOperationResponse
	var err error
	if id := r.URL.Query().Get("id"); id != "" {
		result, err = client.AdminOperation(r.Context(), session, id)
	} else {
		result, err = client.AdminCurrentMinecraftStackUpdate(r.Context(), session)
	}
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Server update status is unavailable.")
		return
	}
	if result.Operation != nil && result.Operation.OperationType != "minecraft_update" {
		writeMinecraftWorkspaceError(w, http.StatusNotFound, "Server update not found.")
		return
	}
	writeMinecraftWorkspaceJSON(w, http.StatusOK, result)
}
