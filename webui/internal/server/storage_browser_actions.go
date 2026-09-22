package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminStorageActionAPI interface {
	adminDiscoveryAPI
	AdminStorageActionPlan(ctx context.Context, session string, request api.AdminStorageActionRequest) (api.AdminStorageActionResponse, error)
	AdminStorageActionApply(ctx context.Context, session string, request api.AdminStorageActionRequest) (api.AdminStorageActionResponse, error)
}

func (a *App) registerStorageBrowserActionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/new-storage/actions/plan", a.storageBrowserActionPlan)
	mux.HandleFunc("POST /api/new-storage/actions/apply", a.storageBrowserActionApply)
}

func (a *App) storageBrowserActionPlan(w http.ResponseWriter, r *http.Request) {
	a.storageBrowserAction(w, r, false)
}

func (a *App) storageBrowserActionApply(w http.ResponseWriter, r *http.Request) {
	a.storageBrowserAction(w, r, true)
}

func (a *App) storageBrowserAction(w http.ResponseWriter, r *http.Request, apply bool) {
	if !a.validCSRF(r) {
		writeStorageBrowserActionJSON(w, http.StatusForbidden, api.AdminStorageActionResponse{OK: false, Error: "invalid CSRF token"})
		return
	}
	session, _, _, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(adminStorageActionAPI)
	if !ok {
		writeStorageBrowserActionJSON(w, http.StatusServiceUnavailable, api.AdminStorageActionResponse{OK: false, Error: "storage actions are unavailable"})
		return
	}
	if err := r.ParseForm(); err != nil {
		writeStorageBrowserActionJSON(w, http.StatusBadRequest, api.AdminStorageActionResponse{OK: false, Error: "could not read storage action"})
		return
	}
	request := api.AdminStorageActionRequest{
		Operation:    strings.TrimSpace(r.FormValue("operation")),
		Device:       strings.TrimSpace(r.FormValue("device")),
		MountPoint:   strings.TrimSpace(r.FormValue("mount_point")),
		Confirmation: r.FormValue("confirmation"),
		Fingerprint:  strings.TrimSpace(r.FormValue("fingerprint")),
	}
	if request.Operation == "" || request.Device == "" {
		writeStorageBrowserActionJSON(w, http.StatusBadRequest, api.AdminStorageActionResponse{OK: false, Error: "choose a storage action and partition"})
		return
	}

	var result api.AdminStorageActionResponse
	var err error
	if apply {
		result, err = client.AdminStorageActionApply(r.Context(), session, request)
	} else {
		result, err = client.AdminStorageActionPlan(r.Context(), session, request)
	}
	if err != nil {
		status := http.StatusBadGateway
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode >= 400 && responseErr.StatusCode < 500 {
			status = responseErr.StatusCode
		}
		writeStorageBrowserActionJSON(w, status, api.AdminStorageActionResponse{OK: false, Error: apiMessage(err, "storage action failed")})
		return
	}
	writeStorageBrowserActionJSON(w, http.StatusOK, result)
}

func writeStorageBrowserActionJSON(w http.ResponseWriter, status int, result api.AdminStorageActionResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}
