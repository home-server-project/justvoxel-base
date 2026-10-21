package server

import (
	"context"
	"net/http"
	"slices"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type minecraftContainerAPI interface {
	MinecraftInstanceContainerStatus(context.Context, string, string) (api.MinecraftContainerStatus, error)
	MinecraftInstanceContainerUpdate(context.Context, string, string, api.MinecraftContainerUpdateRequest) (api.PersistentOperationResponse, error)
	MinecraftInstanceContainerOperation(context.Context, string, string, string) (api.PersistentOperationResponse, error)
	MinecraftInstanceContainerAcknowledge(context.Context, string, string, string) (api.PersistentOperationResponse, error)
}

func (a *App) minecraftContainerWorkspace(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	selected, ok := a.selectedMinecraft(w, r, session)
	if !ok {
		return
	}
	client, ok := a.api.(minecraftContainerAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Container maintenance is unavailable.")
		return
	}
	query := r.URL.Query()
	query.Del("server")
	r.URL.RawQuery = query.Encode()
	if r.Method == http.MethodGet && r.URL.Path == "/api/version/container/status" {
		if len(query) != 0 {
			writeMinecraftWorkspaceError(w, 400, "Invalid container status request.")
			return
		}
		out, err := client.MinecraftInstanceContainerStatus(r.Context(), session, selected.ID)
		if err != nil {
			a.writeMinecraftWorkspaceAPIError(w, err, "Container information is unavailable.")
			return
		}
		if out.InstanceID != selected.ID {
			writeMinecraftWorkspaceError(w, 503, "Selected container identity could not be verified.")
			return
		}
		writeMinecraftWorkspaceJSON(w, 200, struct {
			api.MinecraftContainerStatus
			ServerName string `json:"server_name"`
		}{out, selected.Name})
		return
	}
	var out api.PersistentOperationResponse
	var err error
	code := 200
	if r.Method == http.MethodGet {
		out, err = client.MinecraftInstanceContainerOperation(r.Context(), session, selected.ID, r.URL.Query().Get("id"))
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if r.ParseForm() != nil || len(query) != 0 {
			writeMinecraftWorkspaceError(w, 400, "Invalid container update request.")
			return
		}
		allowed := []string{"csrf", "plan_fingerprint", "restart_now", "backup_before_restart", "confirm_players"}
		acknowledge := r.URL.Path == "/api/version/container/acknowledge"
		if acknowledge {
			allowed = []string{"csrf", "operation_id"}
		}
		for key, values := range r.PostForm {
			if !slices.Contains(allowed, key) || len(values) != 1 {
				writeMinecraftWorkspaceError(w, 400, "Invalid container update request.")
				return
			}
		}
		for _, key := range []string{"restart_now", "backup_before_restart", "confirm_players"} {
			if value := r.FormValue(key); value != "" && value != "yes" && value != "no" {
				writeMinecraftWorkspaceError(w, 400, "Invalid container update choice.")
				return
			}
		}
		if !a.validCSRF(r) {
			writeMinecraftWorkspaceError(w, 403, "Invalid CSRF token.")
			return
		}
		if acknowledge {
			out, err = client.MinecraftInstanceContainerAcknowledge(r.Context(), session, selected.ID, r.FormValue("operation_id"))
		} else {
			out, err = client.MinecraftInstanceContainerUpdate(r.Context(), session, selected.ID, api.MinecraftContainerUpdateRequest{PlanFingerprint: r.FormValue("plan_fingerprint"), RestartNow: r.FormValue("restart_now") == "yes", BackupBeforeRestart: r.FormValue("backup_before_restart") == "yes", ConfirmPlayers: r.FormValue("confirm_players") == "yes"})
			code = 202
		}
	}
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Container operation is unavailable.")
		return
	}
	if out.Operation != nil && (out.Operation.InstanceID != selected.ID || out.Operation.OperationType != "minecraft_container_update") {
		writeMinecraftWorkspaceError(w, 404, "Selected container operation not found.")
		return
	}
	writeMinecraftWorkspaceJSON(w, code, out)
}
