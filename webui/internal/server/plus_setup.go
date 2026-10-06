package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
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

type plusDeploymentAPI interface {
	PlusSetupDeploy(context.Context, string) (api.PlusDeploymentResponse, error)
}

func (a *App) plusSetupDeploy(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if !a.validCSRF(r) {
		writeSystemWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
		return
	}
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(plusDeploymentAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Deployment is unavailable.")
		return
	}
	response, err := client.PlusSetupDeploy(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Deployment could not be started.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusAccepted, response)
}

type plusApplicationsAPI interface {
	PlusApplications(context.Context, string) (api.PlusApplications, error)
}

func (a *App) plusApplicationLaunch(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator", "operator", "viewer")
	if !ok {
		return
	}
	client, ok := a.api.(plusApplicationsAPI)
	if !ok {
		http.Error(w, "Applications are unavailable.", http.StatusServiceUnavailable)
		return
	}
	applications, err := client.PlusApplications(r.Context(), session)
	if err != nil {
		http.Error(w, "Applications are unavailable.", http.StatusServiceUnavailable)
		return
	}
	address := applications.PanelURL
	if r.URL.Path == "/applications/drydock" {
		address = applications.DrydockURL
	}
	if !applications.Configured || address == "" {
		http.Error(w, "This application is not deployed. Complete setup from the desktop.", http.StatusConflict)
		return
	}
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		http.Error(w, "The application address is invalid.", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, address, http.StatusSeeOther)
}
