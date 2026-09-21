package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminSystemActionsAPI interface {
	Session(ctx context.Context, session string) (api.SessionInfo, error)
	AdminSystemActionsStatus(ctx context.Context, session string) (api.AdminSystemActionsStatus, error)
	AdminSystemAction(ctx context.Context, session, action string, confirmPlayers bool) (api.AdminSystemActionResponse, error)
}

func (a *App) registerAdminSystemActionPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/system-actions", a.systemActionsStatus)
	mux.HandleFunc("POST /api/system-actions/{action}", a.systemAction)
}

func (a *App) systemActionsStatus(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.systemActionsRequest(w, r, false)
	if !ok {
		return
	}
	status, err := client.AdminSystemActionsStatus(r.Context(), session)
	if err != nil {
		a.handleSystemActionsAPIError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		http.Error(w, "could not encode system action status", http.StatusInternalServerError)
	}
}

func (a *App) systemAction(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.systemActionsRequest(w, r, true)
	if !ok {
		return
	}
	action := r.PathValue("action")
	switch action {
	case "reboot", "poweroff", "firmware-reboot":
	default:
		http.Error(w, "unsupported system action", http.StatusBadRequest)
		return
	}
	result, err := client.AdminSystemAction(r.Context(), session, action, r.FormValue("confirm_players") == "yes")
	if err != nil {
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
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(result)
			return
		}
		http.Error(w, "system action service unavailable", http.StatusBadGateway)
		return
	}
	status := http.StatusOK
	if result.Accepted {
		status = http.StatusAccepted
	} else if !result.ConfirmationRequired {
		http.Error(w, "system action was not accepted", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(result); err != nil {
		return
	}
}

func (a *App) systemActionsRequest(w http.ResponseWriter, r *http.Request, requireCSRF bool) (string, adminSystemActionsAPI, bool) {
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
	client, ok := a.api.(adminSystemActionsAPI)
	if !ok {
		http.Error(w, "system actions are unavailable", http.StatusServiceUnavailable)
		return "", nil, false
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.handleSystemActionsAPIError(w, err)
		return "", nil, false
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return "", nil, false
	}
	return session, client, true
}

func (a *App) handleSystemActionsAPIError(w http.ResponseWriter, err error) {
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
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusForbidden {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return
	}
	http.Error(w, "system action service unavailable", http.StatusBadGateway)
}
