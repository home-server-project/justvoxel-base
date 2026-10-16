package server

import (
	"context"
	"net/http"
	"slices"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type versionStatusAPI interface {
	MinecraftInstanceVersionStatus(context.Context, string, string, string, string, string, ...string) (api.AdminVersionStatus, error)
}

func (a *App) versionWorkspaceStatus(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	selected, ok := a.selectedMinecraft(w, r, session)
	if !ok {
		return
	}
	client, ok := a.api.(versionStatusAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, http.StatusServiceUnavailable, "Version information is unavailable.")
		return
	}
	status, err := client.MinecraftInstanceVersionStatus(r.Context(), session, selected.ID, r.URL.Query().Get("policy"), r.URL.Query().Get("version"), r.URL.Query().Get("server_type"), r.URL.Query().Get("image_tag"))
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Version information is unavailable.")
		return
	}
	writeMinecraftWorkspaceJSON(w, http.StatusOK, status)
}

type versionUpdateAPI interface {
	MinecraftInstanceStackUpdate(context.Context, string, string, api.MinecraftStackUpdateRequest) (api.PersistentOperationResponse, error)
	MinecraftInstanceStackUpdateOperation(context.Context, string, string, string) (api.PersistentOperationResponse, error)
	MinecraftInstanceAcknowledgeStackUpdate(context.Context, string, string, string) (api.PersistentOperationResponse, error)
}

func (a *App) versionWorkspaceUpdate(w http.ResponseWriter, r *http.Request) {
	// Selection is resolved before removing the server query for strict form validation.
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	selected, ok := a.selectedMinecraft(w, r, session)
	if !ok {
		return
	}
	query := r.URL.Query()
	query.Del("server")
	r.URL.RawQuery = query.Encode()
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil || len(r.URL.Query()) > 0 {
		writeMinecraftWorkspaceError(w, http.StatusBadRequest, "Invalid server update request.")
		return
	}
	allowed := []string{"csrf", "server_type", "image_tag", "plan_fingerprint", "confirm_players", "policy", "version"}
	if r.FormValue("acknowledge") == "yes" {
		allowed = []string{"csrf", "acknowledge", "operation_id"}
	}
	for key, values := range r.PostForm {
		if !slices.Contains(allowed, key) || len(values) != 1 {
			writeMinecraftWorkspaceError(w, http.StatusBadRequest, "Invalid server update request.")
			return
		}
	}
	if choice := r.FormValue("confirm_players"); choice != "" && choice != "yes" && choice != "no" {
		writeMinecraftWorkspaceError(w, http.StatusBadRequest, "Invalid player confirmation.")
		return
	}
	if !a.validCSRF(r) {
		writeMinecraftWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
		return
	}
	client, ok := a.api.(versionUpdateAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, http.StatusServiceUnavailable, "Server updates are unavailable.")
		return
	}
	if r.FormValue("acknowledge") == "yes" {
		result, err := client.MinecraftInstanceAcknowledgeStackUpdate(r.Context(), session, selected.ID, r.FormValue("operation_id"))
		if err != nil {
			a.writeMinecraftWorkspaceAPIError(w, err, "Current Minecraft runtime could not be verified.")
			return
		}
		writeMinecraftWorkspaceJSON(w, http.StatusOK, result)
		return
	}
	result, err := client.MinecraftInstanceStackUpdate(r.Context(), session, selected.ID, api.MinecraftStackUpdateRequest{
		Policy: r.FormValue("policy"), Version: r.FormValue("version"), ServerType: r.FormValue("server_type"), ImageTag: r.FormValue("image_tag"), PlanFingerprint: r.FormValue("plan_fingerprint"), ConfirmPlayers: r.FormValue("confirm_players") == "yes",
	})
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Server update could not start.")
		return
	}
	if result.Operation == nil || result.Operation.InstanceID != selected.ID {
		writeMinecraftWorkspaceError(w, 503, "Selected server update identity could not be verified.")
		return
	}
	writeMinecraftWorkspaceJSON(w, http.StatusAccepted, result)
}

func (a *App) versionWorkspaceUpdateOperation(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	selected, ok := a.selectedMinecraft(w, r, session)
	if !ok {
		return
	}
	client, ok := a.api.(versionUpdateAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, http.StatusServiceUnavailable, "Server update status is unavailable.")
		return
	}
	result, err := client.MinecraftInstanceStackUpdateOperation(r.Context(), session, selected.ID, r.URL.Query().Get("id"))
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Server update status is unavailable.")
		return
	}
	if result.Operation != nil && (result.Operation.OperationType != "minecraft_update" || result.Operation.InstanceID != selected.ID) {
		writeMinecraftWorkspaceError(w, http.StatusNotFound, "Server update not found.")
		return
	}
	writeMinecraftWorkspaceJSON(w, http.StatusOK, result)
}

// Version planning delegates compatibility and fingerprinting to the same
// authoritative selected-instance planner used by Apply. It never writes config.
func (a *App) selectedVersionReview(w http.ResponseWriter, r *http.Request, session string, selected selectedServerContext, current api.InstanceRuntimeSettings, apply bool) {
	if apply {
		writeMinecraftWorkspaceError(w, 409, "Apply the reviewed Version operation.")
		return
	}
	client, ok := a.api.(versionStatusAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Version information is unavailable.")
		return
	}
	status, err := client.MinecraftInstanceVersionStatus(r.Context(), session, selected.ID, r.FormValue("version_policy"), r.FormValue("version"), "", r.FormValue("image_tag"))
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Version information is unavailable.")
		return
	}
	if current.VersionPolicy == "" {
		current.VersionPolicy = "pinned"
		if current.Version == "LATEST" {
			current.VersionPolicy = "latest"
		}
	}
	changes := []api.AdminConfigurationChange{}
	if current.Version != status.SelectedCandidate {
		changes = append(changes, api.AdminConfigurationChange{Field: "version", Label: "Minecraft", Before: current.Version, After: status.SelectedCandidate})
	}
	if current.ImageTag != status.ImageTag {
		changes = append(changes, api.AdminConfigurationChange{Field: "image_tag", Label: "Container channel", Before: current.ImageTag, After: status.ImageTag})
	}
	if current.VersionPolicy != status.Policy {
		changes = append(changes, api.AdminConfigurationChange{Field: "version_policy", Label: "Version policy", Before: current.VersionPolicy, After: status.Policy})
	}
	writeMinecraftWorkspaceJSON(w, 200, map[string]any{"ok": true, "changes": changes, "review": status.PlanFingerprint, "warnings": []string{}})
}
