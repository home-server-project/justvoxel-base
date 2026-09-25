package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type storageMinecraftMigrationAPI interface {
	dataMigrationOrchestrationAPI
	Session(ctx context.Context, session string) (api.SessionInfo, error)
}

type storageMinecraftMigrationResponse struct {
	OK        bool                                `json:"ok"`
	Error     string                              `json:"error,omitempty"`
	Plan      *api.AdminDataMigrationPlanResponse `json:"plan,omitempty"`
	Operation *api.PersistentOperation            `json:"operation,omitempty"`
}

func (a *App) storageMinecraftMigrationIdentity(w http.ResponseWriter, r *http.Request) (string, storageMinecraftMigrationAPI, bool) {
	if mustChange(r) {
		writeStorageMinecraftMigrationJSON(w, http.StatusForbidden, storageMinecraftMigrationResponse{OK: false, Error: "Password change required."})
		return "", nil, false
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		writeStorageMinecraftMigrationJSON(w, http.StatusUnauthorized, storageMinecraftMigrationResponse{OK: false, Error: "Authentication required."})
		return "", nil, false
	}
	client, ok := a.api.(storageMinecraftMigrationAPI)
	if !ok {
		writeStorageMinecraftMigrationJSON(w, http.StatusServiceUnavailable, storageMinecraftMigrationResponse{OK: false, Error: "Minecraft data migration is unavailable."})
		return "", nil, false
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.writeStorageMinecraftMigrationError(w, err, "Minecraft data migration is unavailable.")
		return "", nil, false
	}
	if identity.Role != "administrator" {
		writeStorageMinecraftMigrationJSON(w, http.StatusForbidden, storageMinecraftMigrationResponse{OK: false, Error: "Administrator access required."})
		return "", nil, false
	}
	return session, client, true
}

func (a *App) storageMinecraftMigrationCurrent(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.storageMinecraftMigrationIdentity(w, r)
	if !ok {
		return
	}
	operation, err := dataMigrationCurrentOperation(r.Context(), client, session)
	if err != nil {
		a.writeStorageMinecraftMigrationError(w, err, "Minecraft data migration operation status is unavailable.")
		return
	}
	writeStorageMinecraftMigrationJSON(w, http.StatusOK, storageMinecraftMigrationResponse{OK: true, Operation: operation})
}

func (a *App) storageMinecraftMigrationPlan(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeStorageMinecraftMigrationJSON(w, http.StatusForbidden, storageMinecraftMigrationResponse{OK: false, Error: "Invalid CSRF token."})
		return
	}
	session, client, ok := a.storageMinecraftMigrationIdentity(w, r)
	if !ok {
		return
	}
	if current, err := dataMigrationCurrentOperation(r.Context(), client, session); err != nil {
		a.writeStorageMinecraftMigrationError(w, err, "Minecraft data migration operation status is unavailable.")
		return
	} else if current != nil {
		writeStorageMinecraftMigrationJSON(w, http.StatusConflict, storageMinecraftMigrationResponse{
			OK: false, Error: "A Minecraft data migration is already active.", Operation: current,
		})
		return
	}

	request, err := storageMinecraftMigrationRequest(r)
	if err != nil {
		writeStorageMinecraftMigrationJSON(w, http.StatusBadRequest, storageMinecraftMigrationResponse{OK: false, Error: err.Error()})
		return
	}
	plan, err := dataMigrationPlanReviewed(r.Context(), client, session, request)
	if err != nil {
		writeStorageMinecraftMigrationJSON(w, dataMigrationErrorStatus(err), storageMinecraftMigrationResponse{
			OK: false, Error: dataMigrationErrorMessage(err, "Minecraft data migration planning failed."), Plan: &plan,
		})
		return
	}
	writeStorageMinecraftMigrationJSON(w, http.StatusOK, storageMinecraftMigrationResponse{OK: true, Plan: &plan})
}

func (a *App) storageMinecraftMigrationApply(w http.ResponseWriter, r *http.Request) {
	if !a.validCSRF(r) {
		writeStorageMinecraftMigrationJSON(w, http.StatusForbidden, storageMinecraftMigrationResponse{OK: false, Error: "Invalid CSRF token."})
		return
	}
	session, client, ok := a.storageMinecraftMigrationIdentity(w, r)
	if !ok {
		return
	}
	request, err := storageMinecraftMigrationRequest(r)
	if err != nil {
		writeStorageMinecraftMigrationJSON(w, http.StatusBadRequest, storageMinecraftMigrationResponse{OK: false, Error: err.Error()})
		return
	}

	plan, operation, err := dataMigrationApplyReviewed(r.Context(), client, session, request, dataMigrationApplyProof{
		PlanFingerprint:    strings.TrimSpace(r.FormValue("plan_fingerprint")),
		MigrationConfirmed: strings.TrimSpace(r.FormValue("migration_confirmation")) == "MIGRATE",
		Confirmation:       strings.TrimSpace(r.FormValue("destructive_confirmation")),
		PlayersConfirmed:   r.FormValue("players_confirmed") == "yes",
	})
	if err != nil {
		writeStorageMinecraftMigrationJSON(w, dataMigrationErrorStatus(err), storageMinecraftMigrationResponse{
			OK: false, Error: dataMigrationErrorMessage(err, "Could not start Minecraft data migration."), Plan: &plan,
		})
		return
	}
	writeStorageMinecraftMigrationJSON(w, http.StatusOK, storageMinecraftMigrationResponse{OK: true, Operation: operation})
}

func (a *App) storageMinecraftMigrationProgress(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.storageMinecraftMigrationIdentity(w, r)
	if !ok {
		return
	}
	operation, err := dataMigrationOperation(r.Context(), client, session, r.PathValue("id"))
	if err != nil {
		a.writeStorageMinecraftMigrationError(w, err, "Minecraft data migration operation status is unavailable.")
		return
	}
	writeStorageMinecraftMigrationJSON(w, http.StatusOK, storageMinecraftMigrationResponse{OK: true, Operation: operation})
}

func storageMinecraftMigrationRequest(r *http.Request) (api.AdminDataMigrationPlanRequest, error) {
	if err := r.ParseForm(); err != nil {
		return api.AdminDataMigrationPlanRequest{}, errors.New("Could not read the Minecraft data migration request.")
	}
	request := api.AdminDataMigrationPlanRequest{
		Operation:  strings.TrimSpace(r.FormValue("operation")),
		Device:     strings.TrimSpace(r.FormValue("device")),
		MountPoint: strings.TrimSpace(r.FormValue("mount_point")),
		Path:       strings.TrimSpace(r.FormValue("path")),
		SizeGiB:    strings.TrimSpace(r.FormValue("size_gib")),
	}
	if request.SizeGiB == "" {
		request.SizeGiB = "all"
	}
	if request.Operation != "use_partition" {
		return request, errors.New("Storage may only migrate Minecraft data to the selected existing filesystem.")
	}
	if !strings.HasPrefix(request.Device, "/dev/") || !strings.HasPrefix(request.MountPoint, "/") || !strings.HasPrefix(request.Path, "/") {
		return request, errors.New("Choose a valid storage partition, mount point, and Minecraft data directory.")
	}
	return request, nil
}

func (a *App) writeStorageMinecraftMigrationError(w http.ResponseWriter, err error, fallback string) {
	status := storageBrowserErrorStatus(err)
	message := apiMessage(err, fallback)
	if errors.Is(err, api.ErrUnauthorized) {
		status = http.StatusUnauthorized
		message = "Authentication required."
	} else if errors.Is(err, api.ErrPasswordChangeRequired) {
		status = http.StatusForbidden
		message = "Password change required."
	}
	writeStorageMinecraftMigrationJSON(w, status, storageMinecraftMigrationResponse{OK: false, Error: message})
}

func writeStorageMinecraftMigrationJSON(w http.ResponseWriter, status int, result storageMinecraftMigrationResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}
