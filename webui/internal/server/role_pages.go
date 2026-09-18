package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type rolePagesAPI interface {
	Session(ctx context.Context, session string) (api.SessionInfo, error)
	ManualBackup(ctx context.Context, session string) (api.ManualBackupResponse, error)
	Whitelist(ctx context.Context, session string) (api.TextOutputResponse, error)
	WhitelistChange(ctx context.Context, session, platform, action, name string) (api.TextOutputResponse, error)
	MinecraftLogs(ctx context.Context, session string, limit int) (api.LogsResponse, error)
	Activity(ctx context.Context, session string, limit int) (api.PublicActivityResponse, error)
	AdminAudit(ctx context.Context, session string, limit int) (api.AuditResponse, error)
	AdminNotifications(ctx context.Context, session string, openOnly bool) (api.NotificationsResponse, error)
	AdminResolveNotification(ctx context.Context, session string, id int64) error
}

type operationsPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Message       string
	Error         string
	Whitelist     string
	Logs          []string
}

type activityPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Events        []api.PublicActivityEvent
}

type adminActivityPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Notifications []api.Notification
	Events        []api.AuditEvent
	Message       string
	Error         string
}

func (a *App) registerRolePages(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/session-info", a.sessionInfo)
	mux.HandleFunc("GET /operations", a.operationsPage)
	mux.HandleFunc("POST /operations/backup", a.operationsBackup)
	mux.HandleFunc("POST /operations/whitelist", a.operationsWhitelist)
	mux.HandleFunc("GET /activity", a.activityPage)
	mux.HandleFunc("GET /settings/activity", a.adminActivityPage)
	mux.HandleFunc("POST /settings/activity/notifications/{id}/resolve", a.adminResolveNotification)
}

func (a *App) sessionInfo(w http.ResponseWriter, r *http.Request) {
	_, _, identity, ok := a.rolePageRequest(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(identity); err != nil {
		http.Error(w, "could not encode session", http.StatusInternalServerError)
	}
}

func (a *App) operationsPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.rolePageRequest(w, r)
	if !ok {
		return
	}
	if identity.Role != "administrator" && identity.Role != "operator" {
		http.Error(w, "Operator or Administrator access required", http.StatusForbidden)
		return
	}
	a.renderOperations(w, a.operationsData(r.Context(), session, client, identity, csrfFromRequest(r), operationResultMessage(r.URL.Query().Get("result")), ""))
}

func (a *App) operationsBackup(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	session, client, identity, ok := a.rolePageRequest(w, r)
	if !ok {
		return
	}
	if identity.Role != "administrator" && identity.Role != "operator" {
		http.Error(w, "operation not permitted for this role", http.StatusForbidden)
		return
	}
	result, err := client.ManualBackup(r.Context(), session)
	if err != nil {
		message := apiMessage(err, "Manual backup could not be started.")
		if result.Reason == "backup_cooldown" && result.RetryAfterSeconds > 0 {
			message = "Backup available in " + formatRemaining(result.RetryAfterSeconds) + "."
		} else if result.AdministratorResetRequired || result.Reason == "backup_limit_reached" {
			message = "Backup unavailable. Administrator reset required."
		}
		w.WriteHeader(http.StatusBadRequest)
		a.renderOperations(w, a.operationsData(r.Context(), session, client, identity, csrfFromRequest(r), "", message))
		return
	}
	http.Redirect(w, r, "/operations?result=backup", http.StatusSeeOther)
}

func (a *App) operationsWhitelist(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	session, client, identity, ok := a.rolePageRequest(w, r)
	if !ok {
		return
	}
	if identity.Role != "administrator" && identity.Role != "operator" {
		http.Error(w, "operation not permitted for this role", http.StatusForbidden)
		return
	}
	platform := strings.ToLower(strings.TrimSpace(r.FormValue("platform")))
	action := strings.ToLower(strings.TrimSpace(r.FormValue("action")))
	name := strings.TrimSpace(r.FormValue("name"))
	if _, err := client.WhitelistChange(r.Context(), session, platform, action, name); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		a.renderOperations(w, a.operationsData(r.Context(), session, client, identity, csrfFromRequest(r), "", apiMessage(err, "Whitelist change was rejected.")))
		return
	}
	http.Redirect(w, r, "/operations?result=whitelist", http.StatusSeeOther)
}

func (a *App) activityPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.rolePageRequest(w, r)
	if !ok {
		return
	}
	activity, err := client.Activity(r.Context(), session, 50)
	if err != nil {
		a.handleRolePageError(w, r, err)
		return
	}
	a.renderActivity(w, activityPageData{
		Title: "Activity", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrfFromRequest(r), Identity: identity, Events: activity.Events,
	})
}

func (a *App) adminActivityPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.rolePageRequest(w, r)
	if !ok {
		return
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return
	}
	audit, err := client.AdminAudit(r.Context(), session, 100)
	if err != nil {
		a.handleRolePageError(w, r, err)
		return
	}
	notifications, err := client.AdminNotifications(r.Context(), session, true)
	if err != nil {
		a.handleRolePageError(w, r, err)
		return
	}
	message := ""
	if r.URL.Query().Get("result") == "resolved" {
		message = "Notification resolved."
	}
	a.renderAdminActivity(w, adminActivityPageData{
		Title: "Activity & notifications", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrfFromRequest(r), Identity: identity, Notifications: notifications.Notifications, Events: audit.Events, Message: message,
	})
}

func (a *App) adminResolveNotification(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	session, client, identity, ok := a.rolePageRequest(w, r)
	if !ok {
		return
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid notification id", http.StatusBadRequest)
		return
	}
	if err := client.AdminResolveNotification(r.Context(), session, id); err != nil {
		a.handleRolePageError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/activity?result=resolved", http.StatusSeeOther)
}

func (a *App) rolePageRequest(w http.ResponseWriter, r *http.Request) (string, rolePagesAPI, api.SessionInfo, bool) {
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return "", nil, api.SessionInfo{}, false
	}
	client, ok := a.api.(rolePagesAPI)
	if !ok {
		http.Error(w, "role-aware controls are unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.handleRolePageError(w, r, err)
		return "", nil, api.SessionInfo{}, false
	}
	return session, client, identity, true
}

func (a *App) operationsData(ctx context.Context, session string, client rolePagesAPI, identity api.SessionInfo, csrf, message, pageError string) operationsPageData {
	data := operationsPageData{
		Title: "Operations", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrf, Identity: identity, Message: message, Error: pageError,
	}
	if whitelist, err := client.Whitelist(ctx, session); err == nil {
		data.Whitelist = whitelist.Output
	} else if data.Error == "" {
		data.Error = apiMessage(err, "Whitelist status is unavailable.")
	}
	if logs, err := client.MinecraftLogs(ctx, session, 100); err == nil {
		data.Logs = logs.Lines
	} else if data.Error == "" {
		data.Error = apiMessage(err, "Minecraft logs are unavailable.")
	}
	return data
}

func (a *App) handleRolePageError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusForbidden {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
	http.Error(w, "management service unavailable", http.StatusBadGateway)
}

func operationResultMessage(result string) string {
	switch result {
	case "backup":
		return "Manual backup requested."
	case "whitelist":
		return "Whitelist updated."
	default:
		return ""
	}
}

func formatRemaining(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	d := time.Duration(seconds) * time.Second
	minutes := int(d / time.Minute)
	secondsPart := int((d % time.Minute) / time.Second)
	if minutes > 0 {
		return fmt.Sprintf("%dm %02ds", minutes, secondsPart)
	}
	return fmt.Sprintf("%ds", secondsPart)
}

func (a *App) renderOperations(w http.ResponseWriter, data operationsPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, "operations.html", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (a *App) renderActivity(w http.ResponseWriter, data activityPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, "activity.html", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (a *App) renderAdminActivity(w http.ResponseWriter, data adminActivityPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, "admin_activity.html", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
