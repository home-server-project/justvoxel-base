package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type plusSetupAPI interface {
	PlusSetupState(context.Context, string) (api.PlusSetupState, error)
	PlusSetupPrepare(context.Context, string, api.PlusSetupRequest) (api.PlusSetupResponse, error)
}

type plusSetupPageData struct {
	pageData
	Username string
}

func (a *App) plusSetupPage(w http.ResponseWriter, r *http.Request) {
	_, identity, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := plusSetupPageData{pageData: pageData{Title: "JustVoxel Plus setup", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI, CSRF: csrfFromRequest(r)}, Username: identity.Username}
	if err := a.templates.ExecuteTemplate(w, "plus-setup.html", data); err != nil {
		http.Error(w, "Setup page is unavailable.", http.StatusInternalServerError)
	}
}

func (a *App) plusSetupState(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(plusSetupAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Setup is unavailable.")
		return
	}
	state, err := client.PlusSetupState(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Setup information is unavailable.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, state)
}

func (a *App) plusSetupPrepare(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	if !a.validCSRF(r) {
		writeSystemWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
		return
	}
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(plusSetupAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Setup is unavailable.")
		return
	}
	var request api.PlusSetupRequest
	decoder := json.NewDecoder(strings.NewReader(r.FormValue("setup")))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeSystemWorkspaceError(w, http.StatusBadRequest, "Invalid setup request.")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeSystemWorkspaceError(w, http.StatusBadRequest, "Invalid setup request.")
		return
	}
	response, err := client.PlusSetupPrepare(r.Context(), session, request)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Setup could not be prepared.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, response)
}
