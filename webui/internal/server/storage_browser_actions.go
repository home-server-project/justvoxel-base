package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminStorageActionAPI interface {
	adminDiscoveryAPI
	AdminStorageActionPlan(ctx context.Context, session string, request api.AdminStorageActionRequest) (api.AdminStorageActionResponse, error)
	AdminStorageActionApply(ctx context.Context, session string, request api.AdminStorageActionRequest) (api.AdminStorageActionResponse, error)
}

type adminStorageMountAPI interface {
	AdminStorageMountStatus(ctx context.Context, session, device string) (api.AdminStorageMountResponse, error)
	AdminStorageMountPlan(ctx context.Context, session string, request api.AdminStorageMountRequest) (api.AdminStorageMountResponse, error)
	AdminStorageMountApply(ctx context.Context, session string, request api.AdminStorageMountRequest) (api.AdminStorageMountResponse, error)
}

type adminStorageWholeDiskProvisionAPI interface {
	AdminStorageProvisionPlan(ctx context.Context, session string, request api.AdminStorageProvisionRequest) (api.AdminStorageProvisionResponse, error)
	AdminStorageProvisionApply(ctx context.Context, session string, request api.AdminStorageProvisionRequest) (api.AdminStorageProvisionResponse, error)
}

type adminStorageWholeDiskMigrationAPI interface {
	dataMigrationPlanApplyAPI
}

type storageBrowserWholeDiskResponse struct {
	OK                          bool     `json:"ok"`
	Error                       string   `json:"error,omitempty"`
	Purpose                     string   `json:"purpose,omitempty"`
	Device                      string   `json:"device,omitempty"`
	Model                       string   `json:"model,omitempty"`
	Transport                   string   `json:"transport,omitempty"`
	SizeBytes                   uint64   `json:"size_bytes,omitempty"`
	MountPoint                  string   `json:"mount_point,omitempty"`
	Path                        string   `json:"path,omitempty"`
	Fingerprint                 string   `json:"fingerprint,omitempty"`
	Confirmation                string   `json:"confirmation,omitempty"`
	Warnings                    []string `json:"warnings"`
	PlayersConfirmationRequired bool     `json:"players_confirmation_required,omitempty"`
	Online                      int      `json:"online,omitempty"`
	Players                     []string `json:"players,omitempty"`
	Applied                     bool     `json:"applied,omitempty"`
	OperationID                 string   `json:"operation_id,omitempty"`
}

func (a *App) registerStorageBrowserActionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/new-storage/backup-partition/plan", a.storageBrowserBackupPartitionPlan)
	mux.HandleFunc("POST /api/new-storage/backup-partition/apply", a.storageBrowserBackupPartitionApply)
	mux.HandleFunc("POST /api/new-storage/actions/plan", a.storageBrowserActionPlan)
	mux.HandleFunc("POST /api/new-storage/actions/apply", a.storageBrowserActionApply)
	mux.HandleFunc("GET /api/new-storage/mounts/status", a.storageBrowserMountStatus)
	mux.HandleFunc("POST /api/new-storage/mounts/plan", a.storageBrowserMountPlan)
	mux.HandleFunc("POST /api/new-storage/mounts/apply", a.storageBrowserMountApply)
	mux.HandleFunc("POST /api/new-storage/whole-disk/plan", a.storageBrowserWholeDiskPlan)
	mux.HandleFunc("POST /api/new-storage/whole-disk/apply", a.storageBrowserWholeDiskApply)
	mux.HandleFunc("GET /api/new-storage/minecraft-data/current", a.storageMinecraftMigrationCurrent)
	mux.HandleFunc("POST /api/new-storage/minecraft-data/plan", a.storageMinecraftMigrationPlan)
	mux.HandleFunc("POST /api/new-storage/minecraft-data/apply", a.storageMinecraftMigrationApply)
	mux.HandleFunc("GET /api/new-storage/minecraft-data/progress/{id}", a.storageMinecraftMigrationProgress)
}

func (a *App) storageBrowserWholeDiskPlan(w http.ResponseWriter, r *http.Request) {
	a.storageBrowserWholeDiskChange(w, r, false)
}

func (a *App) storageBrowserWholeDiskApply(w http.ResponseWriter, r *http.Request) {
	a.storageBrowserWholeDiskChange(w, r, true)
}

func (a *App) storageBrowserWholeDiskChange(w http.ResponseWriter, r *http.Request, apply bool) {
	if !a.validCSRF(r) {
		writeStorageBrowserWholeDiskJSON(w, http.StatusForbidden, storageBrowserWholeDiskResponse{OK: false, Error: "invalid CSRF token"})
		return
	}
	session, discovery, _, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeStorageBrowserWholeDiskJSON(w, http.StatusBadRequest, storageBrowserWholeDiskResponse{OK: false, Error: "could not read disk preparation request"})
		return
	}
	purpose := strings.TrimSpace(r.FormValue("purpose"))
	device := strings.TrimSpace(r.FormValue("device"))
	if device == "" || (purpose != "minecraft" && purpose != "backups") {
		writeStorageBrowserWholeDiskJSON(w, http.StatusBadRequest, storageBrowserWholeDiskResponse{OK: false, Error: "choose a disk and storage purpose"})
		return
	}

	if purpose == "minecraft" {
		storage, err := discovery.AdminStorage(r.Context(), session)
		if err != nil {
			writeStorageBrowserWholeDiskJSON(w, storageBrowserErrorStatus(err), storageBrowserWholeDiskResponse{OK: false, Error: apiMessage(err, "storage discovery is unavailable")})
			return
		}
		for _, candidate := range storage.Devices {
			if candidate.Path == device && strings.EqualFold(strings.TrimSpace(candidate.Transport), "usb") {
				writeStorageBrowserWholeDiskJSON(w, http.StatusBadRequest, storageBrowserWholeDiskResponse{OK: false, Error: "USB disks cannot be used for Minecraft data"})
				return
			}
		}
	}

	if purpose == "backups" {
		client, ok := a.api.(adminStorageWholeDiskProvisionAPI)
		if !ok {
			writeStorageBrowserWholeDiskJSON(w, http.StatusServiceUnavailable, storageBrowserWholeDiskResponse{OK: false, Error: "backup disk preparation is unavailable"})
			return
		}
		request := api.AdminStorageProvisionRequest{
			Operation:  "erase_disk",
			Device:     device,
			MountPoint: "/var/mnt/justvoxel-backup",
			Path:       "/var/mnt/justvoxel-backup/backups",
			SizeGiB:    "all",
		}
		if apply {
			request.Fingerprint = strings.TrimSpace(r.FormValue("fingerprint"))
			request.Confirmation = r.FormValue("confirmation")
		}
		var result api.AdminStorageProvisionResponse
		var err error
		if apply {
			result, err = client.AdminStorageProvisionApply(r.Context(), session, request)
		} else {
			result, err = client.AdminStorageProvisionPlan(r.Context(), session, request)
		}
		if err != nil {
			writeStorageBrowserWholeDiskJSON(w, storageBrowserErrorStatus(err), storageBrowserWholeDiskResponse{OK: false, Error: apiMessage(err, "backup disk preparation failed")})
			return
		}
		if apply && !result.Applied {
			writeStorageBrowserWholeDiskJSON(w, http.StatusConflict, storageBrowserWholeDiskResponse{OK: false, Error: "backup disk preparation did not complete"})
			return
		}
		writeStorageBrowserWholeDiskJSON(w, http.StatusOK, storageBrowserWholeDiskResponse{
			OK: true, Purpose: purpose, Device: result.Proposed.Device, Model: result.Proposed.Model,
			Transport: result.Proposed.Transport, SizeBytes: result.Proposed.SizeBytes,
			MountPoint: result.Proposed.MountPoint, Path: result.Proposed.Path,
			Fingerprint: result.Proposed.Fingerprint, Confirmation: result.Proposed.Confirmation,
			Warnings: result.Warnings, Applied: result.Applied,
		})
		return
	}

	client, ok := a.api.(adminStorageWholeDiskMigrationAPI)
	if !ok {
		writeStorageBrowserWholeDiskJSON(w, http.StatusServiceUnavailable, storageBrowserWholeDiskResponse{OK: false, Error: "Minecraft disk preparation is unavailable"})
		return
	}
	request := api.AdminDataMigrationPlanRequest{
		Operation:  "erase_disk",
		Device:     device,
		MountPoint: "/var/mnt/justvoxel-data",
		Path:       "/var/mnt/justvoxel-data/minecraft",
		SizeGiB:    "all",
	}

	var plan api.AdminDataMigrationPlanResponse
	var operation *api.PersistentOperation
	var err error
	if apply {
		plan, operation, err = dataMigrationApplyReviewed(r.Context(), client, session, request, dataMigrationApplyProof{
			PlanFingerprint:    strings.TrimSpace(r.FormValue("fingerprint")),
			MigrationConfirmed: true,
			Confirmation:       r.FormValue("confirmation"),
			PlayersConfirmed:   r.FormValue("players_confirmed") == "yes",
		})
	} else {
		plan, err = dataMigrationPlanReviewed(r.Context(), client, session, request)
	}
	if err != nil {
		writeStorageBrowserWholeDiskJSON(w, dataMigrationErrorStatus(err), storageBrowserWholeDiskResponse{
			OK: false, Error: dataMigrationErrorMessage(err, "Minecraft disk preparation failed"),
		})
		return
	}

	warnings := make([]string, 0, len(plan.Warnings))
	for _, warning := range plan.Warnings {
		warnings = append(warnings, warning.Message)
	}
	response := storageBrowserWholeDiskResponse{
		OK: true, Purpose: purpose, Device: plan.Normalized.Device, Model: plan.Normalized.Model,
		Transport: plan.Normalized.Transport, SizeBytes: plan.Normalized.SizeBytes,
		MountPoint: plan.Normalized.MountPoint, Path: plan.Normalized.Path,
		Fingerprint: plan.PlanFingerprint, Warnings: warnings,
	}
	if plan.Requirements != nil {
		response.Confirmation = plan.Requirements.ConfirmationPhrase
		response.PlayersConfirmationRequired = plan.Requirements.PlayersConfirmationRequired
		response.Online = plan.Requirements.Online
		response.Players = plan.Requirements.Players
	}
	if apply {
		response.Applied = true
		response.OperationID = operation.OperationID
	}
	writeStorageBrowserWholeDiskJSON(w, http.StatusOK, response)
}

func storageBrowserErrorStatus(err error) int {
	status := http.StatusBadGateway
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode >= 400 && responseErr.StatusCode < 500 {
		status = responseErr.StatusCode
	}
	return status
}

func writeStorageBrowserWholeDiskJSON(w http.ResponseWriter, status int, result storageBrowserWholeDiskResponse) {
	if result.Warnings == nil {
		result.Warnings = []string{}
	}
	if result.Players == nil {
		result.Players = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

func (a *App) storageBrowserActionPlan(w http.ResponseWriter, r *http.Request) {
	a.storageBrowserAction(w, r, false)
}

func (a *App) storageBrowserActionApply(w http.ResponseWriter, r *http.Request) {
	a.storageBrowserAction(w, r, true)
}

func (a *App) storageBrowserAction(w http.ResponseWriter, r *http.Request, apply bool) {
	if !a.validCSRF(r) {
		writeStorageBrowserActionJSON(w, http.StatusForbidden, api.AdminStorageActionResponse{OK: false, Error: "invalid CSRF token"})
		return
	}
	session, _, _, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(adminStorageActionAPI)
	if !ok {
		writeStorageBrowserActionJSON(w, http.StatusServiceUnavailable, api.AdminStorageActionResponse{OK: false, Error: "storage actions are unavailable"})
		return
	}
	if err := r.ParseForm(); err != nil {
		writeStorageBrowserActionJSON(w, http.StatusBadRequest, api.AdminStorageActionResponse{OK: false, Error: "could not read storage action"})
		return
	}
	request := api.AdminStorageActionRequest{
		Operation:    strings.TrimSpace(r.FormValue("operation")),
		Device:       strings.TrimSpace(r.FormValue("device")),
		MountPoint:   strings.TrimSpace(r.FormValue("mount_point")),
		SizeGiB:      strings.TrimSpace(r.FormValue("size_gib")),
		FreeStart:    strings.TrimSpace(r.FormValue("free_start")),
		Confirmation: r.FormValue("confirmation"),
		Fingerprint:  strings.TrimSpace(r.FormValue("fingerprint")),
	}
	if request.Operation == "" || request.Device == "" {
		writeStorageBrowserActionJSON(w, http.StatusBadRequest, api.AdminStorageActionResponse{OK: false, Error: "choose a storage action and partition"})
		return
	}

	var result api.AdminStorageActionResponse
	var err error
	if apply {
		result, err = client.AdminStorageActionApply(r.Context(), session, request)
	} else {
		result, err = client.AdminStorageActionPlan(r.Context(), session, request)
	}
	if err != nil {
		status := http.StatusBadGateway
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode >= 400 && responseErr.StatusCode < 500 {
			status = responseErr.StatusCode
		}
		writeStorageBrowserActionJSON(w, status, api.AdminStorageActionResponse{OK: false, Error: apiMessage(err, "storage action failed")})
		return
	}
	writeStorageBrowserActionJSON(w, http.StatusOK, result)
}

func writeStorageBrowserActionJSON(w http.ResponseWriter, status int, result api.AdminStorageActionResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

func (a *App) storageBrowserMountStatus(w http.ResponseWriter, r *http.Request) {
	session, _, _, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(adminStorageMountAPI)
	if !ok {
		writeStorageBrowserMountJSON(w, http.StatusServiceUnavailable, api.AdminStorageMountResponse{OK: false, Error: "permanent mount actions are unavailable"})
		return
	}
	device := strings.TrimSpace(r.URL.Query().Get("device"))
	if device == "" {
		writeStorageBrowserMountJSON(w, http.StatusBadRequest, api.AdminStorageMountResponse{OK: false, Error: "choose a storage partition"})
		return
	}
	result, err := client.AdminStorageMountStatus(r.Context(), session, device)
	if err != nil {
		status := http.StatusBadGateway
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode >= 400 && responseErr.StatusCode < 500 {
			status = responseErr.StatusCode
		}
		writeStorageBrowserMountJSON(w, status, api.AdminStorageMountResponse{OK: false, Error: apiMessage(err, "permanent mount status is unavailable")})
		return
	}
	writeStorageBrowserMountJSON(w, http.StatusOK, result)
}

func (a *App) storageBrowserMountPlan(w http.ResponseWriter, r *http.Request) {
	a.storageBrowserMountChange(w, r, false)
}

func (a *App) storageBrowserMountApply(w http.ResponseWriter, r *http.Request) {
	a.storageBrowserMountChange(w, r, true)
}

func (a *App) storageBrowserMountChange(w http.ResponseWriter, r *http.Request, apply bool) {
	if !a.validCSRF(r) {
		writeStorageBrowserMountJSON(w, http.StatusForbidden, api.AdminStorageMountResponse{OK: false, Error: "invalid CSRF token"})
		return
	}
	session, _, _, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(adminStorageMountAPI)
	if !ok {
		writeStorageBrowserMountJSON(w, http.StatusServiceUnavailable, api.AdminStorageMountResponse{OK: false, Error: "permanent mount actions are unavailable"})
		return
	}
	if err := r.ParseForm(); err != nil {
		writeStorageBrowserMountJSON(w, http.StatusBadRequest, api.AdminStorageMountResponse{OK: false, Error: "could not read permanent mount action"})
		return
	}
	request := api.AdminStorageMountRequest{
		Operation:   strings.TrimSpace(r.FormValue("operation")),
		Device:      strings.TrimSpace(r.FormValue("device")),
		MountPoint:  strings.TrimSpace(r.FormValue("mount_point")),
		Fingerprint: strings.TrimSpace(r.FormValue("fingerprint")),
	}
	if request.Device == "" || (request.Operation != "persist" && request.Operation != "remove") {
		writeStorageBrowserMountJSON(w, http.StatusBadRequest, api.AdminStorageMountResponse{OK: false, Error: "choose a permanent mount action and partition"})
		return
	}

	var result api.AdminStorageMountResponse
	var err error
	if apply {
		result, err = client.AdminStorageMountApply(r.Context(), session, request)
	} else {
		result, err = client.AdminStorageMountPlan(r.Context(), session, request)
	}
	if err != nil {
		status := http.StatusBadGateway
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode >= 400 && responseErr.StatusCode < 500 {
			status = responseErr.StatusCode
		}
		writeStorageBrowserMountJSON(w, status, api.AdminStorageMountResponse{OK: false, Error: apiMessage(err, "permanent mount action failed")})
		return
	}
	writeStorageBrowserMountJSON(w, http.StatusOK, result)
}

func writeStorageBrowserMountJSON(w http.ResponseWriter, status int, result api.AdminStorageMountResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

// Partition adoption uses the existing backup destination backend, never provisioning.
type adminStorageBackupPartitionAPI interface {
	AdminStorageMountStatus(ctx context.Context, session, device string) (api.AdminStorageMountResponse, error)
	AdminBackupStoragePlan(ctx context.Context, session string, request api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error)
	AdminBackupStorageApply(ctx context.Context, session string, request api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error)
}

func (a *App) storageBrowserBackupPartitionPlan(w http.ResponseWriter, r *http.Request) {
	a.storageBrowserBackupPartitionChange(w, r, false)
}

func (a *App) storageBrowserBackupPartitionApply(w http.ResponseWriter, r *http.Request) {
	a.storageBrowserBackupPartitionChange(w, r, true)
}

func (a *App) storageBrowserBackupPartitionChange(w http.ResponseWriter, r *http.Request, apply bool) {
	fail := func(status int, message string) {
		writeStorageBrowserActionJSON(w, status, api.AdminStorageActionResponse{OK: false, Error: message})
	}
	if !a.validCSRF(r) {
		fail(http.StatusForbidden, "invalid CSRF token")
		return
	}
	session, discovery, _, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	client, ok := a.api.(adminStorageBackupPartitionAPI)
	if !ok {
		fail(http.StatusServiceUnavailable, "backup partition adoption is unavailable")
		return
	}
	if err := r.ParseForm(); err != nil {
		fail(http.StatusBadRequest, "could not read backup partition request")
		return
	}
	device := strings.TrimSpace(r.FormValue("device"))
	storage, err := discovery.AdminStorage(r.Context(), session)
	if err != nil {
		fail(storageBrowserErrorStatus(err), apiMessage(err, "storage discovery is unavailable"))
		return
	}
	eligible := false
	for _, candidate := range storage.Devices {
		if candidate.Path == device && candidate.Type == "part" && !candidate.System && !candidate.ReadOnly {
			eligible = true
			break
		}
	}
	if !eligible {
		fail(http.StatusBadRequest, "choose a writable non-system partition")
		return
	}
	status, err := client.AdminStorageMountStatus(r.Context(), session, device)
	if err != nil {
		fail(storageBrowserErrorStatus(err), apiMessage(err, "partition mount status is unavailable"))
		return
	}
	mount := status.Proposed
	if !status.OK || mount.Device != device || !mount.Mounted || mount.CurrentMountPoint == "" ||
		(mount.Persistence != "justvoxel" && mount.Persistence != "external") || mount.CurrentMountPoint != mount.MountPoint {
		fail(http.StatusBadRequest, "mount this partition permanently before using it for backups")
		return
	}
	request := api.AdminBackupStorageRequest{
		Type: "partition", Device: device, MountPoint: mount.CurrentMountPoint,
		Path: filepath.Join(mount.CurrentMountPoint, "backups"),
	}
	// Apply must use the reviewed mount, rather than adopting a new location silently.
	if apply && (r.FormValue("mount_point") != request.MountPoint || r.FormValue("path") != request.Path) {
		fail(http.StatusConflict, "partition mount changed; review the backup destination again")
		return
	}
	plan, err := client.AdminBackupStoragePlan(r.Context(), session, request)
	if err != nil {
		fail(storageBrowserErrorStatus(err), apiMessage(err, "backup destination review failed"))
		return
	}
	if !plan.OK {
		fail(http.StatusBadRequest, "backup destination review failed")
		return
	}
	// Bind review to the backend's validated destination identity and warnings.
	evidence, err := json.Marshal(struct {
		Request    api.AdminBackupStorageRequest
		UUID       string
		Source     string
		Filesystem string
		Warnings   []string
	}{request, plan.Proposed.ExpectedUUID, plan.Proposed.ExpectedSource, plan.Proposed.Filesystem, plan.Warnings})
	if err != nil {
		fail(http.StatusInternalServerError, "could not prepare backup destination review")
		return
	}
	fingerprint := fmt.Sprintf("sha256:%x", sha256.Sum256(evidence))
	if apply {
		if r.FormValue("fingerprint") != fingerprint {
			fail(http.StatusConflict, "backup destination changed; review it again")
			return
		}
		result, err := client.AdminBackupStorageApply(r.Context(), session, request)
		if err != nil {
			fail(storageBrowserErrorStatus(err), apiMessage(err, "backup destination apply failed"))
			return
		}
		if !result.OK || !result.Applied {
			fail(http.StatusConflict, "backup destination change did not complete")
			return
		}
	}
	warnings := plan.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		OK          bool                         `json:"ok"`
		Proposed    api.AdminBackupStorageTarget `json:"proposed"`
		Warnings    []string                     `json:"warnings"`
		Fingerprint string                       `json:"fingerprint"`
		Applied     bool                         `json:"applied"`
	}{true, plan.Proposed, warnings, fingerprint, apply})
}
