package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func (a *App) registerRolePages(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/session-info", a.sessionInfo)
	mux.HandleFunc("POST /api/backups/manual", a.quickLookManualBackup)
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

func manualBackupErrorMessage(result api.ManualBackupResponse, err error) string {
	if result.Reason == "backup_cooldown" && result.RetryAfterSeconds > 0 {
		return "Backup available in " + formatRemaining(result.RetryAfterSeconds) + "."
	}
	if result.AdministratorResetRequired || result.Reason == "backup_limit_reached" {
		return "Backup unavailable. Administrator reset required."
	}
	return apiMessage(err, "Manual backup could not be started.")
}

func (a *App) quickLookManualBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	respond := func(status int, ok bool, message string) {
		payload := struct {
			OK      bool   `json:"ok"`
			Message string `json:"message,omitempty"`
			Error   string `json:"error,omitempty"`
		}{OK: ok}
		if ok {
			payload.Message = message
		} else {
			payload.Error = message
		}
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			http.Error(w, "could not encode manual backup response", http.StatusInternalServerError)
		}
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		respond(http.StatusUnauthorized, false, "Sign in required.")
		return
	}
	if mustChange(r) {
		respond(http.StatusForbidden, false, "Password change required.")
		return
	}
	if !a.validCSRF(r) {
		respond(http.StatusForbidden, false, "invalid CSRF token")
		return
	}
	client, ok := a.api.(interface {
		Session(context.Context, string) (api.SessionInfo, error)
		ManualBackup(context.Context, string) (api.ManualBackupResponse, error)
	})
	if !ok {
		respond(http.StatusServiceUnavailable, false, "Backup management is unavailable.")
		return
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, api.ErrUnauthorized) {
			status = http.StatusUnauthorized
		}
		respond(status, false, "Sign in could not be verified.")
		return
	}
	if identity.MustChange {
		respond(http.StatusForbidden, false, "Password change required.")
		return
	}
	if identity.Role != "administrator" && identity.Role != "operator" {
		respond(http.StatusForbidden, false, "operation not permitted for this role")
		return
	}
	result, err := client.ManualBackup(r.Context(), session)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, api.ErrUnauthorized) {
			status = http.StatusUnauthorized
		}
		respond(status, false, manualBackupErrorMessage(result, err))
		return
	}
	respond(http.StatusOK, true, "Backup started. It will appear in the library when the backup service finishes.")
}

func (a *App) rolePageRequest(w http.ResponseWriter, r *http.Request) (string, sessionIdentityAPI, api.SessionInfo, bool) {
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return "", nil, api.SessionInfo{}, false
	}
	client, ok := a.api.(sessionIdentityAPI)
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
