package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"

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
	mux.HandleFunc("GET /api/plugins/workspace/icon", a.pluginsWorkspaceIcon)
	mux.HandleFunc("POST /api/plugins/workspace/install/plan", a.pluginsWorkspaceInstallPlan)
	mux.HandleFunc("POST /api/plugins/workspace/install", a.pluginsWorkspaceInstall)
	mux.HandleFunc("POST /api/plugins/workspace/install/acknowledge", a.pluginsWorkspaceInstallAcknowledge)
	mux.HandleFunc("GET /api/plugins/workspace/install-operation", a.pluginsWorkspaceInstallOperation)
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

type pluginsInstallAPI interface {
	AdminPluginInstallPlan(context.Context, string, api.PluginInstallRequest) (api.PluginInstallPlan, error)
	AdminPluginInstall(context.Context, string, api.PluginInstallApplyRequest) (api.PersistentOperationResponse, error)
	AdminCurrentPluginInstall(context.Context, string) (api.PersistentOperationResponse, error)
	AdminOperation(context.Context, string, string) (api.PersistentOperationResponse, error)
}

// The WebUI uses existing form CSRF; only bounded, explicit request fields are
// translated into the strict Management API JSON contract.
func (a *App) pluginInstallForm(w http.ResponseWriter, r *http.Request, apply bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil {
		writeMinecraftWorkspaceError(w, 400, "Invalid plugin request.")
		return false
	}
	if !a.validCSRF(r) {
		writeMinecraftWorkspaceError(w, 403, "Invalid CSRF token.")
		return false
	}
	allowed := []string{"csrf", "project_id", "version_id"}
	if apply {
		allowed = append(allowed, "plan_fingerprint", "confirm_players")
	}
	if len(r.URL.Query()) > 0 {
		writeMinecraftWorkspaceError(w, 400, "Invalid plugin request.")
		return false
	}
	for key, values := range r.PostForm {
		if !slices.Contains(allowed, key) || len(values) != 1 {
			writeMinecraftWorkspaceError(w, 400, "Invalid plugin request.")
			return false
		}
	}
	if apply && r.FormValue("confirm_players") != "true" && r.FormValue("confirm_players") != "false" {
		writeMinecraftWorkspaceError(w, 400, "Invalid player confirmation.")
		return false
	}
	return true
}
func (a *App) pluginsWorkspaceInstallPlan(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	if !a.pluginInstallForm(w, r, false) {
		return
	}
	client, ok := a.api.(pluginsInstallAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Plugin installation is unavailable.")
		return
	}
	out, err := client.AdminPluginInstallPlan(r.Context(), session, api.PluginInstallRequest{ProjectID: r.FormValue("project_id"), VersionID: r.FormValue("version_id")})
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Plugin plan could not be prepared.")
		return
	}
	writeMinecraftWorkspaceJSON(w, 200, out)
}
func (a *App) pluginsWorkspaceInstall(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	if !a.pluginInstallForm(w, r, true) {
		return
	}
	client, ok := a.api.(pluginsInstallAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Plugin installation is unavailable.")
		return
	}
	out, err := client.AdminPluginInstall(r.Context(), session, api.PluginInstallApplyRequest{ProjectID: r.FormValue("project_id"), VersionID: r.FormValue("version_id"), PlanFingerprint: r.FormValue("plan_fingerprint"), ConfirmPlayers: r.FormValue("confirm_players") == "true"})
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Plugin installation could not start; review again.")
		return
	}
	writeMinecraftWorkspaceJSON(w, http.StatusAccepted, out)
}
func (a *App) pluginsWorkspaceInstallOperation(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(pluginsInstallAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Plugin installation status is unavailable.")
		return
	}
	var out api.PersistentOperationResponse
	var err error
	if id := r.URL.Query().Get("id"); id != "" {
		out, err = client.AdminOperation(r.Context(), session, id)
	} else {
		out, err = client.AdminCurrentPluginInstall(r.Context(), session)
	}
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Plugin installation status is unavailable.")
		return
	}
	if out.Operation != nil && out.Operation.OperationType != "plugin_install" {
		writeMinecraftWorkspaceError(w, 404, "Plugin installation not found.")
		return
	}
	writeMinecraftWorkspaceJSON(w, 200, out)
}

func (a *App) pluginsWorkspaceInstallAcknowledge(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil {
		writeMinecraftWorkspaceError(w, 400, "Invalid plugin re-check.")
		return
	}
	if !a.validCSRF(r) {
		writeMinecraftWorkspaceError(w, 403, "Invalid CSRF token.")
		return
	}
	if len(r.URL.Query()) > 0 || r.FormValue("operation_id") == "" {
		writeMinecraftWorkspaceError(w, 400, "Invalid plugin re-check.")
		return
	}
	for key, values := range r.PostForm {
		if (key != "csrf" && key != "operation_id") || len(values) != 1 {
			writeMinecraftWorkspaceError(w, 400, "Invalid plugin re-check.")
			return
		}
	}
	client, ok := a.api.(interface {
		AdminPluginInstallAcknowledge(context.Context, string, api.PluginInstallAcknowledgeRequest) (api.PersistentOperationResponse, error)
	})
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Plugin re-check is unavailable.")
		return
	}
	out, err := client.AdminPluginInstallAcknowledge(r.Context(), session, api.PluginInstallAcknowledgeRequest{OperationID: r.FormValue("operation_id")})
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Plugin state could not be verified. Evidence was preserved.")
		return
	}
	writeMinecraftWorkspaceJSON(w, 200, out)
}

var pluginIconProjectID = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

func (a *App) pluginsWorkspaceIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	projectID := query.Get("project_id")
	if err != nil || len(query) != 1 || len(query["project_id"]) != 1 || !pluginIconProjectID.MatchString(projectID) {
		http.Error(w, "Invalid plugin icon request.", http.StatusBadRequest)
		return
	}
	client, ok := a.api.(interface {
		AdminPluginIcon(context.Context, string, string) (api.PluginIcon, error)
	})
	if !ok {
		http.Error(w, "Plugin icon unavailable.", http.StatusServiceUnavailable)
		return
	}
	icon, err := client.AdminPluginIcon(r.Context(), session, projectID)
	if err != nil {
		status := http.StatusBadGateway
		var response *api.ResponseError
		if errors.As(err, &response) && (response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusServiceUnavailable) {
			status = response.StatusCode
		}
		if errors.Is(err, api.ErrUnauthorized) {
			status = http.StatusUnauthorized
		}
		if errors.Is(err, api.ErrPasswordChangeRequired) {
			status = http.StatusForbidden
		}
		http.Error(w, "Plugin icon unavailable.", status)
		return
	}
	if len(icon.Data) > 512<<10 || (icon.ContentType != "image/png" && icon.ContentType != "image/jpeg" && icon.ContentType != "image/webp") || http.DetectContentType(icon.Data) != icon.ContentType {
		http.Error(w, "Plugin icon unavailable.", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", icon.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write(icon.Data)
}
