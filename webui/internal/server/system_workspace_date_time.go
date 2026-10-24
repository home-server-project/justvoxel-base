package server

import (
	"context"
	"io/fs"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type systemDateTimeAPI interface {
	AdminDateTime(context.Context, string) (api.AdminDateTimeState, error)
	AdminDateTimeApply(context.Context, string, api.AdminDateTimeChange) (api.AdminDateTimeState, error)
}

func (a *App) systemWorkspaceDateTime(w http.ResponseWriter, r *http.Request) {
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(systemDateTimeAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Date and time controls are unavailable.")
		return
	}
	state, err := client.AdminDateTime(r.Context(), session)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Date and time are unavailable.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, struct {
		api.AdminDateTimeState
		Timezones []string `json:"timezones"`
	}{state, systemTimezoneOptions(state.Timezone)})
}

func (a *App) systemWorkspaceDateTimeApply(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeSystemWorkspaceError(w, http.StatusForbidden, "Invalid CSRF token.")
		return
	}
	session, _, ok := a.systemWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	client, ok := a.api.(systemDateTimeAPI)
	if !ok {
		writeSystemWorkspaceError(w, http.StatusServiceUnavailable, "Date and time controls are unavailable.")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeSystemWorkspaceError(w, http.StatusBadRequest, "Could not read date and time settings.")
		return
	}
	request := api.AdminDateTimeChange{TimeFormat: r.FormValue("time_format"), DateFormat: r.FormValue("date_format"), Timezone: strings.TrimSpace(r.FormValue("timezone")), Automatic: r.FormValue("automatic") == "on"}
	if !request.Automatic {
		request.Date, request.Time = r.FormValue("date"), r.FormValue("time")
	}
	state, err := client.AdminDateTimeApply(r.Context(), session, request)
	if err != nil {
		a.writeSystemWorkspaceAPIError(w, err, "Date and time could not be changed.")
		return
	}
	writeSystemWorkspaceJSON(w, http.StatusOK, state)
}

var systemTimezonePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+/-]*$`)

func systemTimezoneOptions(current string) []string {
	zones := map[string]struct{}{"UTC": {}}
	root := "/usr/share/zoneinfo"
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative == "posix" || relative == "right" {
				return filepath.SkipDir
			}
			return nil
		}
		switch relative {
		case "iso3166.tab", "leap-seconds.list", "leapseconds", "localtime", "posixrules", "tzdata.zi", "zone.tab", "zone1970.tab":
			return nil
		}
		if systemTimezonePattern.MatchString(relative) && !strings.Contains(relative, "..") {
			if _, err := time.LoadLocation(relative); err == nil {
				zones[relative] = struct{}{}
			}
		}
		return nil
	})
	if current = strings.TrimSpace(current); current != "" && systemTimezonePattern.MatchString(current) && !strings.Contains(current, "..") {
		if _, err := time.LoadLocation(current); err == nil {
			zones[current] = struct{}{}
		}
	}
	out := make([]string, 0, len(zones))
	for zone := range zones {
		out = append(out, zone)
	}
	sort.Strings(out)
	return out
}
