package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type newBackupsDeleteAPI interface {
	AdminBackupDeletePlan(ctx context.Context, session string, request api.AdminBackupDeleteRequest) (api.AdminBackupDeleteResponse, error)
	AdminBackupDeleteApply(ctx context.Context, session string, request api.AdminBackupDeleteRequest) (api.AdminBackupDeleteResponse, error)
}

func (a *App) registerNewBackupsDeleteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/new-backups/delete/plan", a.newBackupsDeletePlan)
	mux.HandleFunc("POST /api/new-backups/delete/apply", a.newBackupsDeleteApply)
}

func (a *App) newBackupsDeletePlan(w http.ResponseWriter, r *http.Request) {
	a.newBackupsDeleteAction(w, r, false)
}

func (a *App) newBackupsDeleteApply(w http.ResponseWriter, r *http.Request) {
	a.newBackupsDeleteAction(w, r, true)
}

func (a *App) newBackupsDeleteAction(w http.ResponseWriter, r *http.Request, apply bool) {
	if !a.validCSRF(r) {
		writeNewBackupsDeleteJSON(w, http.StatusForbidden, api.AdminBackupDeleteResponse{OK: false, Error: "invalid CSRF token"})
		return
	}
	session, client, _, ok := a.newBackupsRequest(w, r, false)
	if !ok {
		return
	}
	deleter, ok := any(client).(newBackupsDeleteAPI)
	if !ok {
		writeNewBackupsDeleteJSON(w, http.StatusServiceUnavailable, api.AdminBackupDeleteResponse{OK: false, Error: "backup deletion is unavailable"})
		return
	}
	if err := r.ParseForm(); err != nil {
		writeNewBackupsDeleteJSON(w, http.StatusBadRequest, api.AdminBackupDeleteResponse{OK: false, Error: "could not read backup selection"})
		return
	}

	rawIDs := r.Form["backup_id"]
	ids := make([]string, 0, len(rawIDs))
	for _, raw := range rawIDs {
		id := strings.TrimSpace(raw)
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) < 1 || len(ids) > 100 {
		writeNewBackupsDeleteJSON(w, http.StatusBadRequest, api.AdminBackupDeleteResponse{OK: false, Error: "choose between 1 and 100 backups"})
		return
	}

	request := api.AdminBackupDeleteRequest{
		BackupIDs:    ids,
		Confirmation: r.FormValue("confirmation"),
		Fingerprint:  strings.TrimSpace(r.FormValue("fingerprint")),
	}

	var result api.AdminBackupDeleteResponse
	var err error
	if apply {
		result, err = deleter.AdminBackupDeleteApply(r.Context(), session, request)
	} else {
		result, err = deleter.AdminBackupDeletePlan(r.Context(), session, request)
	}
	if err != nil {
		status := http.StatusBadGateway
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode >= 400 && responseErr.StatusCode < 500 {
			status = responseErr.StatusCode
		}
		writeNewBackupsDeleteJSON(w, status, api.AdminBackupDeleteResponse{OK: false, Error: apiMessage(err, "backup deletion failed")})
		return
	}
	writeNewBackupsDeleteJSON(w, http.StatusOK, result)
}

func writeNewBackupsDeleteJSON(w http.ResponseWriter, status int, result api.AdminBackupDeleteResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}
