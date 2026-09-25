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
	Session(ctx context.Context, session string) (api.SessionInfo, error)
	AdminDataMigrationPlan(ctx context.Context, session string, request api.AdminDataMigrationPlanRequest) (api.AdminDataMigrationPlanResponse, error)
	AdminDataMigrationApply(ctx context.Context, session string, request api.AdminDataMigrationApplyRequest) (api.AdminDataMigrationApplyResponse, error)
	AdminCurrentDataMigrationOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error)
	AdminOperation(ctx context.Context, session, id string) (api.PersistentOperationResponse, error)
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
	result, err := client.AdminCurrentDataMigrationOperation(r.Context(), session)
	if err != nil {
		a.writeStorageMinecraftMigrationError(w, err, "Minecraft data migration operation status is unavailable.")
		return
	}
	if result.Operation != nil && result.Operation.OperationType != "data_migration" {
		writeStorageMinecraftMigrationJSON(w, http.StatusBadGateway, storageMinecraftMigrationResponse{OK: false, Error: "Minecraft data migration operation status is invalid."})
		return
	}
	writeStorageMinecraftMigrationJSON(w, http.StatusOK, storageMinecraftMigrationResponse{OK: true, Operation: result.Operation})
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
	if current, err := client.AdminCurrentDataMigrationOperation(r.Context(), session); err != nil {
		a.writeStorageMinecraftMigrationError(w, err, "Minecraft data migration operation status is unavailable.")
		return
	} else if current.Operation != nil {
		writeStorageMinecraftMigrationJSON(w, http.StatusConflict, storageMinecraftMigrationResponse{
			OK: false, Error: "A Minecraft data migration is already active.", Operation: current.Operation,
		})
		return
	}

	request, err := storageMinecraftMigrationRequest(r)
	if err != nil {
		writeStorageMinecraftMigrationJSON(w, http.StatusBadRequest, storageMinecraftMigrationResponse{OK: false, Error: err.Error()})
		return
	}
	plan, err := client.AdminDataMigrationPlan(r.Context(), session, request)
	if err != nil {
		status := storageBrowserErrorStatus(err)
		writeStorageMinecraftMigrationJSON(w, status, storageMinecraftMigrationResponse{
			OK: false, Error: apiMessage(err, "Minecraft data migration planning failed."), Plan: &plan,
		})
		return
	}
	if !plan.OK || plan.Normalized == nil || plan.Requirements == nil {
		writeStorageMinecraftMigrationJSON(w, http.StatusBadGateway, storageMinecraftMigrationResponse{
			OK: false, Error: "Minecraft data migration returned an incomplete Review plan.", Plan: &plan,
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

	plan, err := client.AdminDataMigrationPlan(r.Context(), session, request)
	if err != nil {
		writeStorageMinecraftMigrationJSON(w, storageBrowserErrorStatus(err), storageMinecraftMigrationResponse{
			OK: false, Error: apiMessage(err, "Minecraft data migration planning failed."), Plan: &plan,
		})
		return
	}

	fingerprint := strings.TrimSpace(r.FormValue("plan_fingerprint"))
	if fingerprint == "" || fingerprint != plan.PlanFingerprint {
		writeStorageMinecraftMigrationJSON(w, http.StatusConflict, storageMinecraftMigrationResponse{
			OK: false, Error: "This migration changed since Review. Review the current target again.", Plan: &plan,
		})
		return
	}
	if strings.TrimSpace(r.FormValue("migration_confirmation")) != "MIGRATE" {
		writeStorageMinecraftMigrationJSON(w, http.StatusBadRequest, storageMinecraftMigrationResponse{
			OK: false, Error: "Type MIGRATE exactly to start this reviewed Minecraft data migration.", Plan: &plan,
		})
		return
	}

	playersConfirmed := r.FormValue("players_confirmed") == "yes"
	destructiveConfirmation := strings.TrimSpace(r.FormValue("destructive_confirmation"))
	if plan.Requirements != nil {
		if plan.Requirements.PlayersConfirmationRequired && !playersConfirmed {
			writeStorageMinecraftMigrationJSON(w, http.StatusBadRequest, storageMinecraftMigrationResponse{
				OK: false, Error: "Confirm that online players may be interrupted before starting migration.", Plan: &plan,
			})
			return
		}
		if plan.Requirements.DestructiveConfirmationRequired && destructiveConfirmation != plan.Requirements.ConfirmationPhrase {
			writeStorageMinecraftMigrationJSON(w, http.StatusBadRequest, storageMinecraftMigrationResponse{
				OK: false, Error: "The destructive storage confirmation does not match the exact reviewed phrase.", Plan: &plan,
			})
			return
		}
	}

	result, err := client.AdminDataMigrationApply(r.Context(), session, api.AdminDataMigrationApplyRequest{
		PlanFingerprint:    plan.PlanFingerprint,
		Request:            request,
		MigrationConfirmed: true,
		Confirmation:       destructiveConfirmation,
		PlayersConfirmed:   playersConfirmed,
	})
	if err != nil {
		writeStorageMinecraftMigrationJSON(w, storageBrowserErrorStatus(err), storageMinecraftMigrationResponse{
			OK: false, Error: apiMessage(err, "Could not start Minecraft data migration."), Plan: &plan,
		})
		return
	}
	if !result.OK || result.Operation == nil || result.Operation.OperationType != "data_migration" {
		writeStorageMinecraftMigrationJSON(w, http.StatusBadGateway, storageMinecraftMigrationResponse{
			OK: false, Error: "Minecraft data migration did not return a valid persistent operation.",
		})
		return
	}
	writeStorageMinecraftMigrationJSON(w, http.StatusOK, storageMinecraftMigrationResponse{OK: true, Operation: result.Operation})
}

func (a *App) storageMinecraftMigrationProgress(w http.ResponseWriter, r *http.Request) {
	session, client, ok := a.storageMinecraftMigrationIdentity(w, r)
	if !ok {
		return
	}
	result, err := client.AdminOperation(r.Context(), session, r.PathValue("id"))
	if err != nil {
		a.writeStorageMinecraftMigrationError(w, err, "Minecraft data migration operation status is unavailable.")
		return
	}
	if result.Operation == nil || result.Operation.OperationType != "data_migration" {
		writeStorageMinecraftMigrationJSON(w, http.StatusNotFound, storageMinecraftMigrationResponse{OK: false, Error: "Minecraft data migration operation not found."})
		return
	}
	writeStorageMinecraftMigrationJSON(w, http.StatusOK, storageMinecraftMigrationResponse{OK: true, Operation: result.Operation})
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
