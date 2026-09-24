package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const systemMonitorGlancesURL = "http://127.0.0.1:61208/api/4/all"

func (a *App) registerSystemMonitorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/system-monitor", a.systemMonitor)
}

func (a *App) systemMonitor(w http.ResponseWriter, r *http.Request) {
	if mustChange(r) {
		http.Error(w, "password change required", http.StatusForbidden)
		return
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	client, ok := a.api.(sessionIdentityAPI)
	if !ok {
		http.Error(w, "session service unavailable", http.StatusServiceUnavailable)
		return
	}
	identity, err := client.Session(r.Context(), session)
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "session service unavailable", http.StatusBadGateway)
		return
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return
	}

	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, systemMonitorGlancesURL, nil)
	if err != nil {
		http.Error(w, "system monitor unavailable", http.StatusBadGateway)
		return
	}
	request.Header.Set("Accept", "application/json")
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		http.Error(w, "system monitor unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		http.Error(w, "system monitor unavailable", http.StatusBadGateway)
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil || !json.Valid(body) {
		http.Error(w, "invalid system monitor response", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
