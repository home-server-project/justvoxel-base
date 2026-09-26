package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type systemWorkspaceValidationAPI interface {
	AdminValidation(ctx context.Context, session string) (api.AdminValidationResponse, error)
}

type systemWorkspaceHistoryAPI interface {
	Activity(ctx context.Context, session string, limit int) (api.PublicActivityResponse, error)
	AdminAudit(ctx context.Context, session string, limit int) (api.AuditResponse, error)
	AdminNotifications(ctx context.Context, session string, openOnly bool) (api.NotificationsResponse, error)
	AdminResolveNotification(ctx context.Context, session string, id int64) error
}

type systemWorkspaceUsersAPI interface {
	AdminUsers(ctx context.Context, session string) (api.AdminUsersResponse, error)
	AdminCreateUser(ctx context.Context, session, username, role, password string) (api.AdminUser, error)
	AdminSetUserRole(ctx context.Context, session string, id int64, role string) (api.AdminUser, error)
	AdminSetUserEnabled(ctx context.Context, session string, id int64, enabled bool) (api.AdminUser, error)
	AdminSetUserPassword(ctx context.Context, session string, id int64, password string) error
	AdminDeleteUser(ctx context.Context, session string, id int64) error
	AdminResetRestartAllowance(ctx context.Context, session string, id int64) (api.AdminUser, error)
	AdminResetBackupAllowance(ctx context.Context, session string, id int64) (api.AdminUser, error)
}

type systemWorkspaceResetAPI interface {
	AdminMinecraftResetPlan(ctx context.Context, session string) (api.AdminResetPlanResponse, error)
	AdminFactoryResetPlan(ctx context.Context, session string) (api.AdminResetPlanResponse, error)
	AdminMinecraftResetApply(ctx context.Context, session string, request api.AdminMinecraftResetApplyRequest) (api.AdminResetApplyResponse, error)
	AdminFactoryResetApply(ctx context.Context, session string, request api.AdminFactoryResetApplyRequest) (api.AdminResetApplyResponse, error)
	AdminFactoryResetResolve(ctx context.Context, session, operationID string) (api.PersistentOperationResponse, error)
	AdminCurrentMinecraftResetOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error)
	AdminCurrentFactoryResetOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error)
	AdminOperation(ctx context.Context, session, id string) (api.PersistentOperationResponse, error)
}

type systemWorkspaceHealthResponse struct {
	OK     bool                        `json:"ok"`
	Result api.AdminValidationResponse `json:"result"`
}

type systemWorkspaceHistoryResponse struct {
	OK            bool                      `json:"ok"`
	Role          string                    `json:"role"`
	Username      string                    `json:"username"`
	Notifications []api.Notification        `json:"notifications,omitempty"`
	Audit         []api.AuditEvent          `json:"audit,omitempty"`
	Events        []api.PublicActivityEvent `json:"events,omitempty"`
}

type systemWorkspaceUsersResponse struct {
	OK                   bool            `json:"ok"`
	Username             string          `json:"username"`
	PrimaryAdministrator string          `json:"primary_administrator"`
	Users                []api.AdminUser `json:"users"`
	MinimumPasswordLen   int             `json:"minimum_password_len"`
	Message              string          `json:"message,omitempty"`
}

type systemWorkspaceSecurityResponse struct {
	OK                 bool   `json:"ok"`
	Mode               string `json:"mode"`
	Username           string `json:"username"`
	MinimumPasswordLen int    `json:"minimum_password_len"`
}

type systemWorkspaceMutationResponse struct {
	OK             bool   `json:"ok"`
	Message        string `json:"message,omitempty"`
	Reauthenticate bool   `json:"reauthenticate,omitempty"`
}

type systemWorkspaceAboutResponse struct {
	OK            bool   `json:"ok"`
	Username      string `json:"username"`
	Role          string `json:"role"`
	Variant       string `json:"variant"`
	JustVoxel     string `json:"justvoxel"`
	WebUI         string `json:"webui"`
	ManagementAPI string `json:"management_api"`
	Commit        string `json:"commit"`
}

type systemWorkspaceErrorResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func (a *App) registerSystemWorkspaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/system/workspace/health", a.systemWorkspaceHealth)
	mux.HandleFunc("GET /api/system/workspace/history", a.systemWorkspaceHistory)
	mux.HandleFunc("POST /api/system/workspace/history/notifications/{id}/resolve", a.systemWorkspaceResolveNotification)
	mux.HandleFunc("GET /api/system/workspace/users", a.systemWorkspaceUsers)
	mux.HandleFunc("POST /api/system/workspace/users/create", a.systemWorkspaceCreateUser)
	mux.HandleFunc("POST /api/system/workspace/users/{id}/role", a.systemWorkspaceSetUserRole)
	mux.HandleFunc("POST /api/system/workspace/users/{id}/enabled", a.systemWorkspaceSetUserEnabled)
	mux.HandleFunc("POST /api/system/workspace/users/{id}/password", a.systemWorkspaceSetUserPassword)
	mux.HandleFunc("POST /api/system/workspace/users/{id}/delete", a.systemWorkspaceDeleteUser)
	mux.HandleFunc("POST /api/system/workspace/users/{id}/restart-allowance/reset", a.systemWorkspaceResetRestartAllowance)
	mux.HandleFunc("POST /api/system/workspace/users/{id}/backup-allowance/reset", a.systemWorkspaceResetBackupAllowance)
	mux.HandleFunc("GET /api/system/workspace/security", a.systemWorkspaceSecurity)
	mux.HandleFunc("POST /api/system/workspace/security/authentication", a.systemWorkspaceAuthenticationChange)
	mux.HandleFunc("POST /api/system/workspace/security/password", a.systemWorkspacePasswordChange)
	mux.HandleFunc("POST /api/system/workspace/reset/minecraft/plan", a.systemWorkspaceMinecraftResetPlan)
	mux.HandleFunc("POST /api/system/workspace/reset/minecraft/apply", a.systemWorkspaceMinecraftResetApply)
	mux.HandleFunc("GET /api/system/workspace/reset/minecraft/current", a.systemWorkspaceMinecraftResetCurrent)
	mux.HandleFunc("POST /api/system/workspace/reset/factory/plan", a.systemWorkspaceFactoryResetPlan)
	mux.HandleFunc("POST /api/system/workspace/reset/factory/apply", a.systemWorkspaceFactoryResetApply)
	mux.HandleFunc("POST /api/system/workspace/reset/factory/resolve", a.systemWorkspaceFactoryResetResolve)
	mux.HandleFunc("GET /api/system/workspace/reset/factory/current", a.systemWorkspaceFactoryResetCurrent)
	mux.HandleFunc("GET /api/system/workspace/reset/operations/{id}", a.systemWorkspaceResetOperation)
	mux.HandleFunc("GET /api/system/workspace/about", a.systemWorkspaceAbout)
}

func (a *App) systemWorkspaceIdentity(w http.ResponseWriter, r *http.Request, allowed ...string) (string, api.SessionInfo, bool) {
	if mustChange(r) {
		writeSystemWorkspaceError(w, http.StatusForbidden, "Password change required.")
		return "", api.SessionInfo{}, false
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusUnauthorized, "Authentication required.")
		return "", api.SessionInfo{}, false
	}
	client, ok := a.api.(sessionIdentityAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Session information is unavailable.")
		return "", api.SessionInfo{}, false
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Session information is unavailable.")
		return "", api.SessionInfo{}, false
	}
	for _, role := range allowed {
		if identity.Role == role {
			return session, identity, true
		}
	}
	writeSystemWorkspaceError(w, http.StatusForbidden, "Operation not permitted for this role.")
	return "", api.SessionInfo{}, false
}

func (a *App) systemWorkspaceHealth(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(systemWorkspaceValidationAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "System health check is unavailable.")
		return
	}
	result, err := client.AdminValidation(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "System health check is unavailable.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, systemWorkspaceHealthResponse{OK: true, Result: result})
}

func (a *App) systemWorkspaceHistory(w http.ResponseWriter, r *http.Request) {
	session, identity, ok := a.systemWorkspaceIdentity(w, r, "administrator", "operator", "viewer")
	if !ok {
		return
	}
	client, ok := a.api.(systemWorkspaceHistoryAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "System history is unavailable.")
		return
	}
	response := systemWorkspaceHistoryResponse{OK: true, Role: identity.Role, Username: identity.Username}
	if identity.Role == "administrator" {
		audit, err := client.AdminAudit(r.Context(), session, 100)
		if err != nil {
			a.writeSystemWorkspaceAPIError(w, err, "Administrator history is unavailable.")
			return
		}
		notifications, err := client.AdminNotifications(r.Context(), session, true)
		if err != nil {
			a.writeSystemWorkspaceAPIError(w, err, "Administrator notifications are unavailable.")
			return
		}
		response.Audit = audit.Events
		response.Notifications = notifications.Notifications
	} else {
		activity, err := client.Activity(r.Context(), session, 100)
		if err != nil {
			a.writeSystemWorkspaceAPIError(w, err, "System history is unavailable.")
			return
		}
		response.Events = activity.Events
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, response)
}

func (a *App) systemWorkspaceResolveNotification(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeSystemWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
		return
	}
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(systemWorkspaceHistoryAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Administrator notifications are unavailable.")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeSystemWorkspaceError(w, http.StatusBadRequest, "Invalid notification id.")
		return
	}
	if err := client.AdminResolveNotification(r.Context(), session, id); err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Notification could not be resolved.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, systemWorkspaceMutationResponse{OK: true, Message: "Notification resolved."})
}

func (a *App) systemWorkspaceUsers(w http.ResponseWriter, r *http.Request) {
	session, identity, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(systemWorkspaceUsersAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "User management is unavailable.")
		return
	}
	payload, err := client.AdminUsers(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "User management is unavailable.")
		return
	}
	if payload.Users == nil {
		payload.Users = []api.AdminUser{}
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, systemWorkspaceUsersResponse{
		OK: true, Username: identity.Username, PrimaryAdministrator: payload.PrimaryAdministrator.Username,
		Users: payload.Users, MinimumPasswordLen: payload.MinimumPasswordLen,
	})
}

func (a *App) systemWorkspaceCreateUser(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.systemWorkspaceUsersMutation(w, r)
	if !ok {
		return
	}
	password := r.FormValue("password")
	if password == "" || password != r.FormValue("confirm_password") {
		writeSystemWorkspaceError(w, http.StatusBadRequest, "Passwords must match.")
		return
	}
	if _, err := client.AdminCreateUser(r.Context(), session, strings.TrimSpace(r.FormValue("username")), r.FormValue("role"), password); err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Could not create WebUI user.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusCreated, systemWorkspaceMutationResponse{OK: true, Message: "WebUI user created."})
}

func (a *App) systemWorkspaceSetUserRole(w http.ResponseWriter, r *http.Request) {
	session, client, id, ok := a.systemWorkspaceUserMutation(w, r)
	if !ok {
		return
	}
	if _, err := client.AdminSetUserRole(r.Context(), session, id, r.FormValue("role")); err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Could not change WebUI user role.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, systemWorkspaceMutationResponse{OK: true, Message: "WebUI user updated."})
}

func (a *App) systemWorkspaceSetUserEnabled(w http.ResponseWriter, r *http.Request) {
	session, client, id, ok := a.systemWorkspaceUserMutation(w, r)
	if !ok {
		return
	}
	enabled := r.FormValue("enabled") == "true"
	if _, err := client.AdminSetUserEnabled(r.Context(), session, id, enabled); err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Could not change WebUI user status.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, systemWorkspaceMutationResponse{OK: true, Message: "WebUI user updated."})
}

func (a *App) systemWorkspaceSetUserPassword(w http.ResponseWriter, r *http.Request) {
	session, client, id, ok := a.systemWorkspaceUserMutation(w, r)
	if !ok {
		return
	}
	password := r.FormValue("password")
	if password == "" || password != r.FormValue("confirm_password") {
		writeSystemWorkspaceError(w, http.StatusBadRequest, "Passwords must match.")
		return
	}
	if err := client.AdminSetUserPassword(r.Context(), session, id, password); err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Could not change WebUI user password.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, systemWorkspaceMutationResponse{OK: true, Message: "WebUI user password changed. Existing sessions were signed out."})
}

func (a *App) systemWorkspaceDeleteUser(w http.ResponseWriter, r *http.Request) {
	session, client, id, ok := a.systemWorkspaceUserMutation(w, r)
	if !ok {
		return
	}
	if err := client.AdminDeleteUser(r.Context(), session, id); err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Could not delete WebUI user.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, systemWorkspaceMutationResponse{OK: true, Message: "WebUI user deleted."})
}

func (a *App) systemWorkspaceResetRestartAllowance(w http.ResponseWriter, r *http.Request) {
	session, client, id, ok := a.systemWorkspaceUserMutation(w, r)
	if !ok {
		return
	}
	if _, err := client.AdminResetRestartAllowance(r.Context(), session, id); err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Could not reset restart allowance.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, systemWorkspaceMutationResponse{OK: true, Message: "Restart allowance reset."})
}

func (a *App) systemWorkspaceResetBackupAllowance(w http.ResponseWriter, r *http.Request) {
	session, client, id, ok := a.systemWorkspaceUserMutation(w, r)
	if !ok {
		return
	}
	if _, err := client.AdminResetBackupAllowance(r.Context(), session, id); err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Could not reset backup allowance.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, systemWorkspaceMutationResponse{OK: true, Message: "Backup allowance reset."})
}

func (a *App) systemWorkspaceUsersMutation(w http.ResponseWriter, r *http.Request) (string, systemWorkspaceUsersAPI, bool) {
	if !a.validCSRF(r) {
		writeSystemWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
		return "", nil, false
	}
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return "", nil, false
	}
	client, ok := a.api.(systemWorkspaceUsersAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "User management is unavailable.")
		return "", nil, false
	}
	return session, client, true
}

func (a *App) systemWorkspaceUserMutation(w http.ResponseWriter, r *http.Request) (string, systemWorkspaceUsersAPI, int64, bool) {
	session, client, ok := a.systemWorkspaceUsersMutation(w, r)
	if !ok {
		return "", nil, 0, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeSystemWorkspaceError(w, http.StatusBadRequest, "Invalid WebUI user id.")
		return "", nil, 0, false
	}
	return session, client, id, true
}

func (a *App) systemWorkspaceSecurity(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.authAPI()
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Authentication settings are unavailable.")
		return
	}
	status, err := client.AuthStatus(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Authentication settings are unavailable.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, systemWorkspaceSecurityResponse{
		OK: true, Mode: status.Mode, Username: status.Username, MinimumPasswordLen: status.MinimumPasswordLen,
	})
}

func (a *App) systemWorkspaceAuthenticationChange(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeSystemWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
		return
	}
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.authAPI()
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Authentication settings are unavailable.")
		return
	}
	current, err := client.AuthStatus(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Authentication settings are unavailable.")
		return
	}
	change := api.AuthModeChange{
		Mode:               r.FormValue("mode"),
		SystemPassword:     r.FormValue("system_password"),
		NewWebPassword:     r.FormValue("new_web_password"),
		ConfirmWebPassword: r.FormValue("confirm_web_password"),
	}
	if change.Mode == "separate" && (change.NewWebPassword != change.ConfirmWebPassword || len(change.NewWebPassword) < current.MinimumPasswordLen) {
		writeSystemWorkspaceError(w, http.StatusBadRequest, "New WebUI passwords must match and meet the host system password policy.")
		return
	}
	result, err := client.ChangeAuthMode(r.Context(), session, change)
	if errors.Is(err, api.ErrUnauthorized) {
		writeSystemWorkspaceError(w, http.StatusBadRequest, "System password is incorrect.")
		return
	}
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Authentication mode change was rejected.")
		return
	}
	if result.Reauthenticate {
		a.clearSessionCookies(w)
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, systemWorkspaceMutationResponse{OK: true, Message: "Authentication mode changed.", Reauthenticate: result.Reauthenticate})
}

func (a *App) systemWorkspacePasswordChange(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeSystemWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
		return
	}
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.authAPI()
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Authentication settings are unavailable.")
		return
	}
	status, err := client.AuthStatus(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Authentication settings are unavailable.")
		return
	}
	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")
	confirmPassword := r.FormValue("confirm_password")
	if newPassword != confirmPassword || len(newPassword) < status.MinimumPasswordLen {
		writeSystemWorkspaceError(w, http.StatusBadRequest, "New passwords must match and meet the host system password policy.")
		return
	}
	if err := a.api.ChangePassword(r.Context(), session, currentPassword, newPassword); err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Password change was rejected by the host system password policy.")
		return
	}
	a.clearSessionCookies(w)
	writeSystemWorkspaceJSON(w, http.StatusOK, systemWorkspaceMutationResponse{OK: true, Message: "Password changed.", Reauthenticate: true})
}

func (a *App) systemWorkspaceResetClient(w http.ResponseWriter, r *http.Request, mutation bool) (string, systemWorkspaceResetAPI, bool) {
	if mutation && !a.validCSRF(r) {
		writeSystemWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
		return "", nil, false
	}
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return "", nil, false
	}
	client, ok := a.api.(systemWorkspaceResetAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Reset operations are unavailable.")
		return "", nil, false
	}
	return session, client, true
}

func (a *App) systemWorkspaceMinecraftResetPlan(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.systemWorkspaceResetClient(w, r, true)
	if !ok {
		return
	}
	result, err := client.AdminMinecraftResetPlan(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Minecraft reset could not be planned.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, result)
}

func (a *App) systemWorkspaceFactoryResetPlan(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.systemWorkspaceResetClient(w, r, true)
	if !ok {
		return
	}
	result, err := client.AdminFactoryResetPlan(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Full factory reset could not be planned.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, result)
}

func (a *App) systemWorkspaceMinecraftResetApply(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.systemWorkspaceResetClient(w, r, true)
	if !ok {
		return
	}
	result, err := client.AdminMinecraftResetApply(r.Context(), session, api.AdminMinecraftResetApplyRequest{
		PlanFingerprint: r.FormValue("plan_fingerprint"),
		ConfirmPlayers:  r.FormValue("confirm_players") == "yes",
	})
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Minecraft reset could not start.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusAccepted, result)
}

func (a *App) systemWorkspaceFactoryResetApply(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.systemWorkspaceResetClient(w, r, true)
	if !ok {
		return
	}
	password := r.FormValue("system_password")
	result, err := client.AdminFactoryResetApply(r.Context(), session, api.AdminFactoryResetApplyRequest{
		PlanFingerprint: r.FormValue("plan_fingerprint"),
		ConfirmPlayers:  r.FormValue("confirm_players") == "yes",
		SystemPassword:  password,
	})
	password = ""
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Full factory reset could not start.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusAccepted, result)
}

func (a *App) systemWorkspaceFactoryResetResolve(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeSystemWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
		return
	}
	session, client, ok := a.systemWorkspaceResetClient(w, r, true)
	if !ok {
		return
	}
	operationID := strings.TrimSpace(r.FormValue("operation_id"))
	if operationID == "" || r.FormValue("keep_current_state") != "yes" {
		writeSystemWorkspaceError(w, http.StatusBadRequest, "Confirm that the current server state should be kept without retrying Factory Reset.")
		return
	}
	result, err := client.AdminFactoryResetResolve(r.Context(), session, operationID)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Failed Factory Reset could not be resolved.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, result)
}

func (a *App) systemWorkspaceMinecraftResetCurrent(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.systemWorkspaceResetClient(w, r, false)
	if !ok {
		return
	}
	result, err := client.AdminCurrentMinecraftResetOperation(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Minecraft reset status is unavailable.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, result)
}

func (a *App) systemWorkspaceFactoryResetCurrent(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.systemWorkspaceResetClient(w, r, false)
	if !ok {
		return
	}
	result, err := client.AdminCurrentFactoryResetOperation(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Factory reset status is unavailable.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, result)
}

func (a *App) systemWorkspaceResetOperation(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.systemWorkspaceResetClient(w, r, false)
	if !ok {
		return
	}
	result, err := client.AdminOperation(r.Context(), session, r.PathValue("id"))
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Reset operation status is unavailable.")
		return
	}
	if result.Operation != nil && result.Operation.OperationType != "minecraft_reset" && result.Operation.OperationType != "factory_reset" {
		writeSystemWorkspaceError(w, http.StatusNotFound, "Reset operation was not found.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, result)
}

func (a *App) systemWorkspaceAbout(w http.ResponseWriter, r *http.Request) {
	session, identity, ok := a.systemWorkspaceIdentity(w, r, "administrator", "operator", "viewer")
	if !ok {
		return
	}
	status, err := a.api.Status(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "About information is unavailable.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, systemWorkspaceAboutResponse{
		OK: true, Username: identity.Username, Role: identity.Role, Variant: status.System.Variant,
		JustVoxel: status.JustVoxel.Version, WebUI: a.config.Version, ManagementAPI: a.config.ManagementAPI, Commit: a.config.Commit,
	})
}

func (a *App) writeSystemWorkspaceAPIError(w http.ResponseWriter, err error, fallback string) {
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
	writeSystemWorkspaceError(w, status, message)
}

func writeSystemWorkspaceError(w http.ResponseWriter, status int, message string) {
	writeSystemWorkspaceJSON(w, status, systemWorkspaceErrorResponse{OK: false, Error: message})
}

func writeSystemWorkspaceJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
