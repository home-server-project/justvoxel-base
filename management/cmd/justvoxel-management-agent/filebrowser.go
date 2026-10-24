package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

const fileBrowserUnit = "justvoxel-filebrowser.service"
const fileBrowserURL = "http://127.0.0.1:61209"

type fileBrowserStatus struct {
	Availability string `json:"availability"`
	Ready        bool   `json:"ready"`
	State        string `json:"state"`
}

var fileBrowserCommand = instanceRuntimeCommand
var fileBrowserHealth = func(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileBrowserURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (s *webUIStore) readFileBrowserAvailability() (string, error) {
	var mode string
	err := s.db.QueryRow(`SELECT availability FROM filebrowser_settings WHERE id = 1`).Scan(&mode)
	if err == nil && mode != "always" && mode != "on-demand" {
		err = errors.New("invalid File Browser availability")
	}
	return mode, err
}

// Persist the desired mode first. A failed systemd action remains retryable by
// saving the same mode again; SQLite is the single authoritative preference.
func (s *server) setFileBrowserAvailability(ctx context.Context, mode string) error {
	if mode != "always" && mode != "on-demand" {
		return errors.New("invalid availability")
	}
	if _, err := s.store.db.Exec(`UPDATE filebrowser_settings SET availability = ? WHERE id = 1`, mode); err != nil {
		return err
	}
	action := "disable"
	if mode == "always" {
		action = "enable"
	}
	if _, err := fileBrowserCommand(ctx, "systemctl", action, fileBrowserUnit); err != nil {
		return err
	}
	if mode == "always" {
		return startFileBrowser(ctx)
	}
	// Do not stop an open workspace when switching to on-demand.
	return nil
}

func startFileBrowser(ctx context.Context) error {
	_, err := fileBrowserCommand(ctx, "systemctl", "--no-block", "start", fileBrowserUnit)
	return err
}

func (s *server) stopFileBrowser(ctx context.Context) error {
	mode, err := s.store.readFileBrowserAvailability()
	if err != nil || mode == "always" {
		return err
	}
	_, err = fileBrowserCommand(ctx, "systemctl", "stop", fileBrowserUnit)
	return err
}

func registerFileBrowserRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/filebrowser", s.fileBrowserGet)
	mux.HandleFunc("POST /v1/admin/filebrowser/{action}", s.fileBrowserChange)
}

func (s *server) fileBrowserGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	s.fileBrowserMu.Lock()
	defer s.fileBrowserMu.Unlock()
	mode, err := s.store.readFileBrowserAvailability()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "File Browser settings unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	state, err := fileBrowserCommand(ctx, "systemctl", "show", "--property=ActiveState", "--value", fileBrowserUnit)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "File Browser status unavailable")
		return
	}
	writeJSON(w, http.StatusOK, fileBrowserStatus{Availability: mode, Ready: fileBrowserHealth(ctx), State: strings.TrimSpace(string(state))})
}

func (s *server) fileBrowserChange(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	s.fileBrowserMu.Lock()
	defer s.fileBrowserMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	var err error
	switch r.PathValue("action") {
	case "availability":
		var request struct {
			Availability string `json:"availability"`
		}
		if !decodeJSON(w, r, &request) {
			return
		}
		if request.Availability != "always" && request.Availability != "on-demand" {
			writeError(w, http.StatusBadRequest, "Choose Always available or On demand")
			return
		}
		err = s.setFileBrowserAvailability(ctx, request.Availability)
	case "start":
		err = startFileBrowser(ctx)
	case "stop":
		err = s.stopFileBrowser(ctx)
	default:
		writeError(w, http.StatusNotFound, "Unknown File Browser action")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "File Browser operation could not be completed. Retry.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
