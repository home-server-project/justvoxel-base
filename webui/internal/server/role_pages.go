package server

import (
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
	if !a.validCSRF(r) {
		writeMinecraftWorkspaceError(w, 403, "invalid CSRF token")
		return
	}
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator", "operator")
	if !ok {
		return
	}
	selected, ok := a.selectedMinecraft(w, r, session)
	if !ok {
		return
	}
	client, ok := a.api.(selectedBackupsAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "Backup management is unavailable.")
		return
	}
	if _, err := client.MinecraftInstanceBackups(r.Context(), session, selected.ID, "now", struct{}{}); err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "Backup could not start.")
		return
	}
	writeMinecraftWorkspaceJSON(w, 200, map[string]any{"ok": true, "message": "Backup started. It will appear in the library when the backup service finishes."})
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
