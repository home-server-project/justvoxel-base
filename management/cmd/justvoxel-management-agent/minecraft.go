package main

import (
	"encoding/json"
	"net/http"
)

type operatorRestartUsageView struct {
	RestartUsed     int `json:"restart_used"`
	RestartLimit    int `json:"restart_limit"`
	CooldownSeconds int `json:"cooldown_seconds"`
}

type minecraftActionResponse struct {
	ID                   string                    `json:"id,omitempty"`
	OK                   bool                      `json:"ok"`
	Action               string                    `json:"action,omitempty"`
	Message              string                    `json:"message,omitempty"`
	ConfirmationRequired bool                      `json:"confirmation_required,omitempty"`
	Reason               string                    `json:"reason,omitempty"`
	Players              []string                  `json:"players,omitempty"`
	Online               int                       `json:"online,omitempty"`
	OperatorUsage        *operatorRestartUsageView `json:"operator_usage,omitempty"`
}

func registerMinecraftRoutes(mux *http.ServeMux, s *server) {
	registerInstanceRuntimeRoutes(mux, s)
	registerMinecraftNetworkRoutes(mux, s)
	mux.HandleFunc("GET /v1/minecraft/instances", s.minecraftInstancesList)
	mux.HandleFunc("POST /v1/minecraft/instances/{id}/name", s.minecraftInstanceRename)
	mux.HandleFunc("GET /v1/minecraft/identity", s.minecraftInstanceRead)
	mux.HandleFunc("POST /v1/minecraft/identity/ensure", s.minecraftInstanceRegister)
	mux.HandleFunc("POST /v1/minecraft/identity/manual", s.minecraftInstanceRegister)
}

func writeHelperJSON(w http.ResponseWriter, status int, output []byte, invalidMessage string) {
	if !json.Valid(output) {
		writeError(w, http.StatusInternalServerError, invalidMessage)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(output)
}
