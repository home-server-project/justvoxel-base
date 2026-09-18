package server

import "net/http"

func (a *App) providerLoginPage(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if _, err := a.api.Status(r.Context(), c.Value); err == nil {
			if mustChange(r) {
				http.Redirect(w, r, "/password", http.StatusSeeOther)
			} else {
				http.Redirect(w, r, "/", http.StatusSeeOther)
			}
			return
		}
	}
	message := ""
	switch r.URL.Query().Get("message") {
	case "password-changed":
		message = "Password changed. Please sign in again."
	case "auth-mode-changed":
		message = "Authentication mode changed. Please sign in again."
	}
	a.render(w, "login.html", pageData{
		Title:         "Sign in",
		Version:       a.config.Version,
		ManagementAPI: a.config.ManagementAPI,
		Message:       message,
	})
}
