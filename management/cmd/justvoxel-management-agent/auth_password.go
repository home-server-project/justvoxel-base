package main

import (
	"errors"
	"net/http"

	"github.com/home-server-project/justvoxel/management/internal/systemauth"
)

func (s *server) providerChangePassword(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministratorCredentialPath(w, r); !ok {
		return
	}
	if s.operations != nil {
		current, err := s.operations.currentFactoryReset()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "factory reset state is unavailable")
			return
		}
		if current != nil && current.State == operationAwaitingPassword {
			writeError(w, http.StatusConflict, "finish the Factory Reset password step in System first")
			return
		}
	}

	var request struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}

	mode, err := currentAuthMode()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "authentication mode unavailable")
		return
	}

	if err := changeAdministratorPassword(mode, request.CurrentPassword, request.NewPassword); err != nil {
		switch {
		case errors.Is(err, systemauth.ErrInvalidCredentials):
			writeError(w, http.StatusUnauthorized, "current password is incorrect")
		case errors.Is(err, systemauth.ErrAccountUnavailable):
			writeError(w, http.StatusForbidden, "system account is unavailable")
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}

	s.invalidateSessions()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"reauthenticate":  true,
		"auth_mode":       mode,
		"system_password": mode == authModeSystem,
	})
}

func (s *server) invalidateSessions() {
	s.mu.Lock()
	s.sessions = make(map[string]session)
	s.mu.Unlock()
}
