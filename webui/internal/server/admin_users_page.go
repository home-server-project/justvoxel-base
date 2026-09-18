package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminUsersAPI interface {
	Session(ctx context.Context, session string) (api.SessionInfo, error)
	AdminUsers(ctx context.Context, session string) (api.AdminUsersResponse, error)
	AdminCreateUser(ctx context.Context, session, username, role, password string) (api.AdminUser, error)
	AdminSetUserRole(ctx context.Context, session string, id int64, role string) (api.AdminUser, error)
	AdminSetUserEnabled(ctx context.Context, session string, id int64, enabled bool) (api.AdminUser, error)
	AdminSetUserPassword(ctx context.Context, session string, id int64, password string) error
	AdminDeleteUser(ctx context.Context, session string, id int64) error
	AdminResetRestartAllowance(ctx context.Context, session string, id int64) (api.AdminUser, error)
	AdminResetBackupAllowance(ctx context.Context, session string, id int64) (api.AdminUser, error)
}

type usersPageData struct {
	Title              string
	Version            string
	ManagementAPI      string
	CSRF               string
	Error              string
	Message            string
	Username           string
	PrimaryAdmin       string
	Users              []api.AdminUser
	MinimumPasswordLen int
}

func (a *App) registerAdminUsersRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings/users", a.adminUsersPage)
	mux.HandleFunc("POST /settings/users/create", a.adminCreateUser)
	mux.HandleFunc("POST /settings/users/{id}/role", a.adminSetUserRole)
	mux.HandleFunc("POST /settings/users/{id}/enabled", a.adminSetUserEnabled)
	mux.HandleFunc("POST /settings/users/{id}/password", a.adminSetUserPassword)
	mux.HandleFunc("POST /settings/users/{id}/delete", a.adminDeleteUser)
	mux.HandleFunc("POST /settings/users/{id}/restart-allowance/reset", a.adminResetRestartAllowance)
	mux.HandleFunc("POST /settings/users/{id}/backup-allowance/reset", a.adminResetBackupAllowance)
	a.registerRolePages(mux)
}

func (a *App) adminUsersPage(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.adminUsersRequest(w, r, false)
	if !ok {
		return
	}
	payload, err := client.AdminUsers(r.Context(), session)
	if err != nil {
		a.handleAdminUsersError(w, r, err)
		return
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.handleAdminUsersError(w, r, err)
		return
	}
	message := ""
	switch r.URL.Query().Get("result") {
	case "created":
		message = "WebUI user created."
	case "updated":
		message = "WebUI user updated."
	case "password":
		message = "WebUI user password changed. Existing sessions were signed out."
	case "deleted":
		message = "WebUI user deleted."
	case "restart-reset":
		message = "Restart allowance reset to 0 / 2."
	case "backup-reset":
		message = "Backup allowance reset to 0 / 2."
	}
	a.renderUsers(w, usersPageData{
		Title:              "Users",
		Version:            a.config.Version,
		ManagementAPI:      a.config.ManagementAPI,
		CSRF:               csrfFromRequest(r),
		Message:            message,
		Username:           identity.Username,
		PrimaryAdmin:       payload.PrimaryAdministrator.Username,
		Users:              payload.Users,
		MinimumPasswordLen: payload.MinimumPasswordLen,
	})
}

func (a *App) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.adminUsersRequest(w, r, true)
	if !ok {
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	role := r.FormValue("role")
	password := r.FormValue("password")
	confirm := r.FormValue("confirm_password")
	if password == "" || password != confirm {
		a.renderAdminUsersFormError(w, r, client, session, "Passwords must match.")
		return
	}
	if _, err := client.AdminCreateUser(r.Context(), session, username, role, password); err != nil {
		a.renderAdminUsersFormError(w, r, client, session, apiMessage(err, "Could not create WebUI user."))
		return
	}
	http.Redirect(w, r, "/settings/users?result=created", http.StatusSeeOther)
}

func (a *App) adminSetUserRole(w http.ResponseWriter, r *http.Request) {
	session, client, id, ok := a.adminUserMutationRequest(w, r)
	if !ok {
		return
	}
	if _, err := client.AdminSetUserRole(r.Context(), session, id, r.FormValue("role")); err != nil {
		a.renderAdminUsersFormError(w, r, client, session, apiMessage(err, "Could not change WebUI user role."))
		return
	}
	http.Redirect(w, r, "/settings/users?result=updated", http.StatusSeeOther)
}

func (a *App) adminSetUserEnabled(w http.ResponseWriter, r *http.Request) {
	session, client, id, ok := a.adminUserMutationRequest(w, r)
	if !ok {
		return
	}
	enabled := r.FormValue("enabled") == "true"
	if _, err := client.AdminSetUserEnabled(r.Context(), session, id, enabled); err != nil {
		a.renderAdminUsersFormError(w, r, client, session, apiMessage(err, "Could not change WebUI user status."))
		return
	}
	http.Redirect(w, r, "/settings/users?result=updated", http.StatusSeeOther)
}

func (a *App) adminSetUserPassword(w http.ResponseWriter, r *http.Request) {
	session, client, id, ok := a.adminUserMutationRequest(w, r)
	if !ok {
		return
	}
	password := r.FormValue("password")
	confirm := r.FormValue("confirm_password")
	if password == "" || password != confirm {
		a.renderAdminUsersFormError(w, r, client, session, "Passwords must match.")
		return
	}
	if err := client.AdminSetUserPassword(r.Context(), session, id, password); err != nil {
		a.renderAdminUsersFormError(w, r, client, session, apiMessage(err, "Could not change WebUI user password."))
		return
	}
	http.Redirect(w, r, "/settings/users?result=password", http.StatusSeeOther)
}

func (a *App) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	session, client, id, ok := a.adminUserMutationRequest(w, r)
	if !ok {
		return
	}
	if err := client.AdminDeleteUser(r.Context(), session, id); err != nil {
		a.renderAdminUsersFormError(w, r, client, session, apiMessage(err, "Could not delete WebUI user."))
		return
	}
	http.Redirect(w, r, "/settings/users?result=deleted", http.StatusSeeOther)
}

func (a *App) adminResetRestartAllowance(w http.ResponseWriter, r *http.Request) {
	session, client, id, ok := a.adminUserMutationRequest(w, r)
	if !ok {
		return
	}
	if _, err := client.AdminResetRestartAllowance(r.Context(), session, id); err != nil {
		a.renderAdminUsersFormError(w, r, client, session, apiMessage(err, "Could not reset restart allowance."))
		return
	}
	http.Redirect(w, r, "/settings/users?result=restart-reset", http.StatusSeeOther)
}

func (a *App) adminResetBackupAllowance(w http.ResponseWriter, r *http.Request) {
	session, client, id, ok := a.adminUserMutationRequest(w, r)
	if !ok {
		return
	}
	if _, err := client.AdminResetBackupAllowance(r.Context(), session, id); err != nil {
		a.renderAdminUsersFormError(w, r, client, session, apiMessage(err, "Could not reset backup allowance."))
		return
	}
	http.Redirect(w, r, "/settings/users?result=backup-reset", http.StatusSeeOther)
}

func (a *App) adminUsersRequest(w http.ResponseWriter, r *http.Request, requireCSRF bool) (string, adminUsersAPI, bool) {
	if requireCSRF && !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return "", nil, false
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return "", nil, false
	}
	client, ok := a.api.(adminUsersAPI)
	if !ok {
		http.Error(w, "user management is unavailable", http.StatusServiceUnavailable)
		return "", nil, false
	}
	return session, client, true
}

func (a *App) adminUserMutationRequest(w http.ResponseWriter, r *http.Request) (string, adminUsersAPI, int64, bool) {
	session, client, ok := a.adminUsersRequest(w, r, true)
	if !ok {
		return "", nil, 0, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid WebUI user id", http.StatusBadRequest)
		return "", nil, 0, false
	}
	return session, client, id, true
}

func (a *App) renderAdminUsersFormError(w http.ResponseWriter, r *http.Request, client adminUsersAPI, session, message string) {
	payload, err := client.AdminUsers(r.Context(), session)
	if err != nil {
		a.handleAdminUsersError(w, r, err)
		return
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.handleAdminUsersError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusBadRequest)
	a.renderUsers(w, usersPageData{
		Title:              "Users",
		Version:            a.config.Version,
		ManagementAPI:      a.config.ManagementAPI,
		CSRF:               csrfFromRequest(r),
		Error:              message,
		Username:           identity.Username,
		PrimaryAdmin:       payload.PrimaryAdministrator.Username,
		Users:              payload.Users,
		MinimumPasswordLen: payload.MinimumPasswordLen,
	})
}

func (a *App) handleAdminUsersError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusForbidden {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return
	}
	http.Error(w, "user management is unavailable", http.StatusBadGateway)
}

func (a *App) renderUsers(w http.ResponseWriter, data usersPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, "users.html", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func apiMessage(err error, fallback string) string {
	if message, ok := api.ErrorMessage(err); ok {
		return message
	}
	return fallback
}
