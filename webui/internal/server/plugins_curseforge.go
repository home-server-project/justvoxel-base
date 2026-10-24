package server

import (
	"context"
	"io"
	"net/http"
	"net/url"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type curseForgeAPI interface {
	AdminCurseForgeAPI(context.Context, string, string, string) (api.CurseForgeAPIStatus, error)
}

func (a *App) pluginsCurseForgeAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	session, _, ok := a.minecraftWorkspaceIdentity(w, r, "administrator")
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeMinecraftWorkspaceError(w, 400, "Invalid CurseForge API request.")
		return
	}
	key := ""
	if r.Method != http.MethodGet {
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		var parseErr error
		if r.Method == http.MethodDelete {
			var data []byte
			data, parseErr = io.ReadAll(r.Body)
			if parseErr == nil {
				r.PostForm, parseErr = url.ParseQuery(string(data))
				r.Form = r.PostForm
			}
		} else {
			parseErr = r.ParseForm()
		}
		if parseErr != nil {
			writeMinecraftWorkspaceError(w, 400, "Invalid CurseForge API request.")
			return
		}
		if !a.validCSRF(r) {
			writeMinecraftWorkspaceError(w, 403, "Invalid CSRF token.")
			return
		}
		for name, values := range r.PostForm {
			if (name != "csrf" && (r.Method != http.MethodPut || name != "key")) || len(values) != 1 {
				writeMinecraftWorkspaceError(w, 400, "Invalid CurseForge API request.")
				return
			}
		}
		key = r.PostForm.Get("key")
	}
	client, ok := a.api.(curseForgeAPI)
	if !ok {
		writeMinecraftWorkspaceError(w, 503, "CurseForge API configuration is unavailable.")
		return
	}
	out, err := client.AdminCurseForgeAPI(r.Context(), session, r.Method, key)
	if err != nil {
		a.writeMinecraftWorkspaceAPIError(w, err, "CurseForge API configuration could not be updated.")
		return
	}
	writeMinecraftWorkspaceJSON(w, 200, out)
}
