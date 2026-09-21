package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminValidationAPI interface {
	Session(ctx context.Context, session string) (api.SessionInfo, error)
	AdminValidation(ctx context.Context, session string) (api.AdminValidationResponse, error)
}

type adminValidationPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Result        *api.AdminValidationResponse
	Unavailable   bool
}

func (a *App) registerAdminValidationPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings/validation", a.adminValidationPage)
}

func (a *App) adminValidationPage(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	client, ok := a.api.(adminValidationAPI)
	if !ok {
		http.Error(w, "validation service unavailable", http.StatusServiceUnavailable)
		return
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.handleAdminValidationAuthError(w, r, err)
		return
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return
	}

	data := adminValidationPageData{
		Title: "System validation", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrfFromRequest(r), Identity: identity,
	}
	result, err := client.AdminValidation(r.Context(), session)
	if err != nil {
		if errors.Is(err, api.ErrUnauthorized) || errors.Is(err, api.ErrPasswordChangeRequired) {
			a.handleAdminValidationAuthError(w, r, err)
			return
		}
		data.Unavailable = true
		a.renderAdminValidation(w, http.StatusServiceUnavailable, data)
		return
	}
	data.Result = &result
	a.renderAdminValidation(w, http.StatusOK, data)
}

func (a *App) handleAdminValidationAuthError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if errors.Is(err, api.ErrPasswordChangeRequired) {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	http.Error(w, "management service unavailable", http.StatusBadGateway)
}

func (a *App) renderAdminValidation(w http.ResponseWriter, status int, data adminValidationPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := a.templates.ExecuteTemplate(w, "validation.html", data); err != nil && status == http.StatusOK {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
