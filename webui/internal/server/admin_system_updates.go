package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminSystemUpdatesAPI interface {
	Session(ctx context.Context, session string) (api.SessionInfo, error)
	AdminSystemUpdateStatus(ctx context.Context, session string) (api.AdminSystemUpdateStatus, error)
	AdminSystemUpdate(ctx context.Context, session string) (api.AdminSystemUpdateStatus, error)
	AdminSystemUpdateRebootStatus(ctx context.Context, session string) (api.AdminSystemUpdateRebootStatus, error)
	AdminSystemUpdateReboot(ctx context.Context, session string, options api.AdminSystemUpdateRebootOptions) (api.AdminSystemUpdateRebootStatus, error)
}

func (a *App) registerAdminSystemUpdatePages(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/system-updates", a.systemUpdateStatus)
	mux.HandleFunc("POST /api/system-updates", a.systemUpdateApply)
	mux.HandleFunc("GET /api/system-updates/reboot", a.systemUpdateRebootStatus)
	mux.HandleFunc("POST /api/system-updates/reboot", a.systemUpdateReboot)
}

func (a *App) systemUpdateStatus(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.systemUpdateRequest(w, r, false)
	if !ok {
		return
	}
	status, err := client.AdminSystemUpdateStatus(r.Context(), session)
	if err != nil {
		a.handleSystemUpdateAPIError(w, err)
		return
	}
	writeSystemUpdateJSON(w, http.StatusOK, status)
}

func (a *App) systemUpdateApply(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.systemUpdateRequest(w, r, true)
	if !ok {
		return
	}
	status, err := client.AdminSystemUpdate(r.Context(), session)
	if err != nil {
		a.handleSystemUpdateAPIError(w, err)
		return
	}
	writeSystemUpdateJSON(w, http.StatusOK, status)
}

func (a *App) systemUpdateRebootStatus(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.systemUpdateRequest(w, r, false)
	if !ok {
		return
	}
	status, err := client.AdminSystemUpdateRebootStatus(r.Context(), session)
	if err != nil {
		a.handleSystemUpdateAPIError(w, err)
		return
	}
	writeSystemUpdateJSON(w, http.StatusOK, status)
}

func (a *App) systemUpdateReboot(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.systemUpdateRequest(w, r, true)
	if !ok {
		return
	}
	warningSeconds := 60
	if r.FormValue("quick_reboot") == "yes" {
		warningSeconds = 10
	}
	result, err := client.AdminSystemUpdateReboot(r.Context(), session, api.AdminSystemUpdateRebootOptions{
		ConfirmPlayers:  r.FormValue("confirm_players") == "yes",
		BackupMinecraft: r.FormValue("backup_minecraft") == "yes",
		WarningSeconds:  warningSeconds,
	})
	if err != nil {
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) {
			status := responseErr.StatusCode
			if status < 400 || status > 599 {
				status = http.StatusBadGateway
			}
			writeSystemUpdateJSON(w, status, result)
			return
		}
		a.handleSystemUpdateAPIError(w, err)
		return
	}
	status := http.StatusOK
	if result.Accepted {
		status = http.StatusAccepted
	}
	writeSystemUpdateJSON(w, status, result)
}

func (a *App) systemUpdateRequest(w http.ResponseWriter, r *http.Request, requireCSRF bool) (string, adminSystemUpdatesAPI, bool) {
	if requireCSRF && !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return "", nil, false
	}
	if mustChange(r) {
		http.Error(w, "password change required", http.StatusForbidden)
		return "", nil, false
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return "", nil, false
	}
	client, ok := a.api.(adminSystemUpdatesAPI)
	if !ok {
		http.Error(w, "system updates are unavailable", http.StatusServiceUnavailable)
		return "", nil, false
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.handleSystemUpdateAPIError(w, err)
		return "", nil, false
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return "", nil, false
	}
	return session, client, true
}

func (a *App) handleSystemUpdateAPIError(w http.ResponseWriter, err error) {
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if errors.Is(err, api.ErrPasswordChangeRequired) {
		http.Error(w, "password change required", http.StatusForbidden)
		return
	}
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) {
		status := responseErr.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		message := responseErr.Message
		if message == "" {
			message = "system update request failed"
		}
		writeSystemUpdateJSON(w, status, map[string]string{"error": message})
		return
	}
	http.Error(w, "system update service unavailable", http.StatusBadGateway)
}

func writeSystemUpdateJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
