package server

import (
	"errors"
	"net/http"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type authenticationPageData struct {
	Title              string
	Version            string
	ManagementAPI      string
	CSRF               string
	Error              string
	Mode               string
	Username           string
	MinimumPasswordLen int
}

func (a *App) authenticationPage(w http.ResponseWriter, r *http.Request) {
	if mustChange(r) {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	client, ok := a.authAPI()
	if !ok {
		http.Error(w, "authentication settings unavailable", http.StatusServiceUnavailable)
		return
	}
	status, err := client.AuthStatus(r.Context(), session)
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err != nil {
		http.Error(w, "authentication settings unavailable", http.StatusBadGateway)
		return
	}
	a.renderAuthentication(w, authenticationPageData{
		Title:              "Authentication",
		Version:            a.config.Version,
		ManagementAPI:      a.config.ManagementAPI,
		CSRF:               csrfFromRequest(r),
		Mode:               status.Mode,
		Username:           status.Username,
		MinimumPasswordLen: status.MinimumPasswordLen,
	})
}

func (a *App) authenticationChange(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if mustChange(r) {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	client, ok := a.authAPI()
	if !ok {
		http.Error(w, "authentication settings unavailable", http.StatusServiceUnavailable)
		return
	}

	current, err := client.AuthStatus(r.Context(), session)
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err != nil {
		http.Error(w, "authentication settings unavailable", http.StatusBadGateway)
		return
	}

	change := api.AuthModeChange{
		Mode:               r.FormValue("mode"),
		SystemPassword:     r.FormValue("system_password"),
		NewWebPassword:     r.FormValue("new_web_password"),
		ConfirmWebPassword: r.FormValue("confirm_web_password"),
	}
	if change.Mode == "separate" {
		if change.NewWebPassword != change.ConfirmWebPassword || len(change.NewWebPassword) < current.MinimumPasswordLen {
			w.WriteHeader(http.StatusBadRequest)
			a.renderAuthentication(w, authenticationPageData{
				Title:              "Authentication",
				Version:            a.config.Version,
				ManagementAPI:      a.config.ManagementAPI,
				CSRF:               csrfFromRequest(r),
				Mode:               current.Mode,
				Username:           current.Username,
				MinimumPasswordLen: current.MinimumPasswordLen,
				Error:              "New WebUI passwords must match and meet the host system password policy.",
			})
			return
		}
	}

	result, err := client.ChangeAuthMode(r.Context(), session, change)
	if err != nil {
		if errors.Is(err, api.ErrUnauthorized) {
			w.WriteHeader(http.StatusUnauthorized)
			a.renderAuthentication(w, authenticationPageData{
				Title:              "Authentication",
				Version:            a.config.Version,
				ManagementAPI:      a.config.ManagementAPI,
				CSRF:               csrfFromRequest(r),
				Mode:               current.Mode,
				Username:           current.Username,
				MinimumPasswordLen: current.MinimumPasswordLen,
				Error:              "System password is incorrect.",
			})
			return
		}
		message := "Authentication mode change was rejected."
		if reason, ok := api.ErrorMessage(err); ok {
			message = reason
		}
		w.WriteHeader(http.StatusBadRequest)
		a.renderAuthentication(w, authenticationPageData{
			Title:              "Authentication",
			Version:            a.config.Version,
			ManagementAPI:      a.config.ManagementAPI,
			CSRF:               csrfFromRequest(r),
			Mode:               current.Mode,
			Username:           current.Username,
			MinimumPasswordLen: current.MinimumPasswordLen,
			Error:              message,
		})
		return
	}

	if result.Reauthenticate {
		a.clearSessionCookies(w)
		http.Redirect(w, r, "/login?message=auth-mode-changed", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings/authentication", http.StatusSeeOther)
}

func (a *App) renderAuthentication(w http.ResponseWriter, data authenticationPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, "authentication.html", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
