package server

import "net/http"

const factoryResetCompleteCookie = "jv_factory_reset_complete"

func (a *App) factoryResetCompletePage(w http.ResponseWriter, r *http.Request) {
	marker, err := r.Cookie(factoryResetCompleteCookie)
	if err != nil || marker.Value != "1" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a.render(w, "factory_reset_complete.html", pageData{Title: "Full Factory Reset complete"})
}

func (a *App) clearFactoryResetCompleteCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: factoryResetCompleteCookie, Value: "", Path: "/factory-reset-complete",
		Secure: a.config.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}
