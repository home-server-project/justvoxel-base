package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminRestoreAPI interface {
	Session(ctx context.Context, session string) (api.SessionInfo, error)
	AdminOperation(ctx context.Context, session, id string) (api.PersistentOperationResponse, error)
}

func (a *App) registerAdminRestorePages(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/restore/progress/{id}", a.restoreProgressStatus)
}

func (a *App) restoreProgressStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	client, ok := a.api.(adminRestoreAPI)
	if !ok {
		http.Error(w, "Restore operation tracking is unavailable", http.StatusServiceUnavailable)
		return
	}
	identity, err := client.Session(r.Context(), session)
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
		http.Error(w, "management service unavailable", http.StatusBadGateway)
		return
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return
	}
	response, err := client.AdminOperation(r.Context(), session, r.PathValue("id"))
	if err != nil {
		a.handleRestoreProgressAPIError(w, err)
		return
	}
	if response.Operation == nil || response.Operation.OperationType != "restore" {
		http.Error(w, "Restore operation not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "could not encode Restore operation", http.StatusInternalServerError)
	}
}

func (a *App) handleRestoreProgressAPIError(w http.ResponseWriter, err error) {
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
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
		http.Error(w, "Restore operation not found", http.StatusNotFound)
		return
	}
	http.Error(w, "Restore operation status is unavailable", http.StatusBadGateway)
}
