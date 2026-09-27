package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type systemDateTimeAPI interface {
	AdminDateTime(context.Context, string) (api.AdminDateTimeState, error)
	AdminDateTimeApply(context.Context, string, api.AdminDateTimeChange) (api.AdminDateTimeState, error)
}

func (a *App) systemWorkspaceDateTime(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok { return }
	client, ok := a.api.(systemDateTimeAPI)
	if !ok { writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Date and time controls are unavailable."); return }
	state, err := client.AdminDateTime(r.Context(), session)
	if err != nil { a.writeSystemWorkspaceAPIError(w, err, "Date and time are unavailable."); return }
	writeSystemWorkspaceJSON(w, http.StatusOK, struct {
		api.AdminDateTimeState
		Timezones []string `json:"timezones"`
	}{state, setupTimezoneOptions(state.Timezone)})
}

func (a *App) systemWorkspaceDateTimeApply(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) { writeSystemWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token."); return }
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok { return }
	client, ok := a.api.(systemDateTimeAPI)
	if !ok { writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Date and time controls are unavailable."); return }
	if err := r.ParseForm(); err != nil { writeSystemWorkspaceError(w, http.StatusBadRequest, "Could not read date and time settings."); return }
	request := api.AdminDateTimeChange{Timezone: strings.TrimSpace(r.FormValue("timezone")), Automatic: r.FormValue("automatic") == "on"}
	if !request.Automatic { request.Date, request.Time = r.FormValue("date"), r.FormValue("time") }
	state, err := client.AdminDateTimeApply(r.Context(), session, request)
	if err != nil { a.writeSystemWorkspaceAPIError(w, err, "Date and time could not be changed."); return }
	writeSystemWorkspaceJSON(w, http.StatusOK, state)
}
