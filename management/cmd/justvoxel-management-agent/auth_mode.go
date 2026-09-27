package main

import (
	"errors"
	"net/http"

	"github.com/home-server-project/justvoxel/management/internal/systemauth"
)

func (s *server) authStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministratorCredentialPath(w, r); !ok {
		return
	}
	mode, err := readAuthMode()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "authentication mode unavailable")
		return
	}
	policy, err := administratorPasswordPolicy()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "password policy unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":                 mode,
		"username":             systemAdminUsername,
		"minimum_password_len": policy.MinLength,
	})
}

func (s *server) changeAuthMode(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}

	var request struct {
		Mode               authMode `json:"mode"`
		SystemPassword     string   `json:"system_password"`
		NewWebPassword     string   `json:"new_web_password"`
		ConfirmWebPassword string   `json:"confirm_web_password"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if !validAuthMode(request.Mode) {
		writeError(w, http.StatusBadRequest, "unsupported authentication mode")
		return
	}

	current, err := readAuthMode()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "authentication mode unavailable")
		return
	}
	if current == request.Mode {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "auth_mode": current, "reauthenticate": false})
		return
	}

	if _, err := systemAuthenticate(systemAdminUsername, request.SystemPassword); err != nil {
		if errors.Is(err, systemauth.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "system password is incorrect")
			return
		}
		writeError(w, http.StatusForbidden, "system account authentication failed")
		return
	}

	switch request.Mode {
	case authModeSeparate:
		if request.NewWebPassword == "" || request.NewWebPassword != request.ConfirmWebPassword {
			writeError(w, http.StatusBadRequest, "new WebUI passwords do not match")
			return
		}
		if err := systemValidatePass(systemAdminUsername, request.SystemPassword, request.NewWebPassword); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := writeSeparateAdmin(request.NewWebPassword); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create separate WebUI credential")
			return
		}
	case authModeSystem:
		// The system credential was already verified above. The local WebUI
		// credential may remain stored, but System mode never consults it.
	}

	if err := writeAuthMode(request.Mode); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update authentication mode")
		return
	}
	s.invalidateSessions()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"auth_mode":      request.Mode,
		"reauthenticate": true,
	})
}
