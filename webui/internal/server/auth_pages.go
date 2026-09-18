package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type authenticationAPI interface {
	AuthStatus(ctx context.Context, session string) (api.AuthStatus, error)
	ChangeAuthMode(ctx context.Context, session string, change api.AuthModeChange) (api.AuthModeChangeResponse, error)
}

type passwordPageData struct {
	Title              string
	Version            string
	ManagementAPI      string
	Error              string
	Message            string
	CSRF               string
	MinimumPasswordLen int
	SystemMode         bool
}

func (a *App) authAPI() (authenticationAPI, bool) {
	client, ok := a.api.(authenticationAPI)
	return client, ok
}

func (a *App) providerPasswordPage(w http.ResponseWriter, r *http.Request) {
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
	a.renderPassword(w, passwordPageData{
		Title:              "Change administrator password",
		Version:            a.config.Version,
		ManagementAPI:      a.config.ManagementAPI,
		CSRF:               csrfFromRequest(r),
		MinimumPasswordLen: status.MinimumPasswordLen,
		SystemMode:         status.Mode == "system",
	})
}

func (a *App) providerPasswordChange(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
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

	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")
	confirmPassword := r.FormValue("confirm_password")
	if newPassword != confirmPassword || len(newPassword) < status.MinimumPasswordLen {
		w.WriteHeader(http.StatusBadRequest)
		a.renderPassword(w, passwordPageData{
			Title:              "Change administrator password",
			Version:            a.config.Version,
			ManagementAPI:      a.config.ManagementAPI,
			CSRF:               csrfFromRequest(r),
			MinimumPasswordLen: status.MinimumPasswordLen,
			SystemMode:         status.Mode == "system",
			Error:              "New passwords must match and meet the host system password policy.",
		})
		return
	}

	if err := a.api.ChangePassword(r.Context(), session, currentPassword, newPassword); err != nil {
		if errors.Is(err, api.ErrUnauthorized) {
			a.clearSessionCookies(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		message := "Password change was rejected by the host system password policy."
		if reason, ok := api.ErrorMessage(err); ok {
			message = reason
		}
		w.WriteHeader(http.StatusBadRequest)
		a.renderPassword(w, passwordPageData{
			Title:              "Change administrator password",
			Version:            a.config.Version,
			ManagementAPI:      a.config.ManagementAPI,
			CSRF:               csrfFromRequest(r),
			MinimumPasswordLen: status.MinimumPasswordLen,
			SystemMode:         status.Mode == "system",
			Error:              message,
		})
		return
	}

	a.clearSessionCookies(w)
	http.Redirect(w, r, "/login?message=password-changed", http.StatusSeeOther)
}

func (a *App) renderPassword(w http.ResponseWriter, data passwordPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, "password.html", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
