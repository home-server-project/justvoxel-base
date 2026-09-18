package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminBackupStorageAPI interface {
	adminDiscoveryAPI
	AdminBackupStorageStatus(ctx context.Context, session string) (api.AdminBackupStorageResponse, error)
	AdminBackupStoragePlan(ctx context.Context, session string, request api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error)
	AdminBackupStorageApply(ctx context.Context, session string, request api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error)
}

type backupPartitionView struct {
	Path       string
	Name       string
	Size       string
	Filesystem string
	Label      string
	Mountpoint string
	Model      string
	SystemDisk bool
}

type backupStoragePageData struct {
	Title                  string
	Version                string
	ManagementAPI          string
	CSRF                   string
	Identity               api.SessionInfo
	Configuration          api.AdminConfigurationDiscovery
	Storage                api.AdminStorageDiscovery
	Status                 api.AdminBackupStorageResponse
	Form                   api.AdminBackupStorageRequest
	Plan                   *api.AdminBackupStorageResponse
	Partitions             []backupPartitionView
	Error                  string
	Message                string
	CurrentAvailable       string
	CurrentFilesystemSize  string
	ProposedAvailable      string
	ProposedFilesystemSize string
}

func (a *App) registerAdminBackupStoragePages(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings/backup-storage", a.backupStoragePage)
	mux.HandleFunc("POST /settings/backup-storage/plan", a.backupStoragePlan)
	mux.HandleFunc("POST /settings/backup-storage/apply", a.backupStorageApply)
}

func (a *App) backupStoragePage(w http.ResponseWriter, r *http.Request) {
	a.renderBackupStoragePage(w, r, api.AdminBackupStorageRequest{}, nil, "", storageResultMessage(r))
}

func (a *App) backupStoragePlan(w http.ResponseWriter, r *http.Request) {
	session, client, _, ok := a.backupStorageRequest(w, r, true)
	if !ok {
		return
	}
	request, err := parseBackupStorageForm(r, false)
	if err != nil {
		a.renderBackupStoragePage(w, r, request, nil, err.Error(), "")
		return
	}
	plan, err := client.AdminBackupStoragePlan(r.Context(), session, request)
	if err != nil {
		a.renderBackupStoragePage(w, r, request, nil, apiMessage(err, "Could not validate this backup destination."), "")
		return
	}
	a.renderBackupStoragePage(w, r, request, &plan, "", "")
}

func (a *App) backupStorageApply(w http.ResponseWriter, r *http.Request) {
	session, client, _, ok := a.backupStorageRequest(w, r, true)
	if !ok {
		return
	}
	request, err := parseBackupStorageForm(r, true)
	if err != nil {
		a.renderBackupStoragePage(w, r, request, nil, err.Error(), "")
		return
	}
	result, err := client.AdminBackupStorageApply(r.Context(), session, request)
	if err != nil {
		a.renderBackupStoragePage(w, r, request, nil, apiMessage(err, "Could not apply this backup destination."), "")
		return
	}
	if !result.Applied {
		a.renderBackupStoragePage(w, r, request, &result, "Backup destination was not applied.", "")
		return
	}
	http.Redirect(w, r, "/settings/backup-storage?result=saved", http.StatusSeeOther)
}

func (a *App) backupStorageRequest(w http.ResponseWriter, r *http.Request, requireCSRF bool) (string, adminBackupStorageAPI, api.SessionInfo, bool) {
	if requireCSRF && !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	session, _, identity, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return "", nil, api.SessionInfo{}, false
	}
	client, ok := a.api.(adminBackupStorageAPI)
	if !ok {
		http.Error(w, "backup storage management is unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false
	}
	return session, client, identity, true
}

func (a *App) renderBackupStoragePage(w http.ResponseWriter, r *http.Request, form api.AdminBackupStorageRequest, plan *api.AdminBackupStorageResponse, errorMessage, message string) {
	session, client, identity, ok := a.backupStorageRequest(w, r, false)
	if !ok {
		return
	}
	configuration, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	storage, err := client.AdminStorage(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}

	status := api.AdminBackupStorageResponse{}
	if configuration.Configured {
		status, err = client.AdminBackupStorageStatus(r.Context(), session)
		if err != nil {
			a.handleAdminDiscoveryError(w, r, err)
			return
		}
		if form.Type == "" {
			form = backupStorageFormFromCurrent(status.Current)
		}
	}

	data := backupStoragePageData{
		Title: "Backup storage", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrfFromRequest(r), Identity: identity, Configuration: configuration, Storage: storage,
		Status: status, Form: form, Plan: plan, Partitions: safeBackupPartitions(storage), Error: errorMessage, Message: message,
		CurrentAvailable:      formatOptionalBytes(status.Current.AvailableBytes),
		CurrentFilesystemSize: formatOptionalBytes(status.Current.FilesystemBytes),
	}
	if plan != nil {
		data.ProposedAvailable = formatOptionalBytes(plan.Proposed.AvailableBytes)
		data.ProposedFilesystemSize = formatOptionalBytes(plan.Proposed.FilesystemBytes)
	}
	a.renderAdminDiscovery(w, "backup_storage.html", data)
}

func backupStorageFormFromCurrent(current api.AdminBackupStorageTarget) api.AdminBackupStorageRequest {
	targetType := current.Type
	switch targetType {
	case "system", "partition", "nfs", "smb":
	default:
		targetType = "system"
	}
	request := api.AdminBackupStorageRequest{
		Type: targetType, Path: current.Path, MountPoint: current.MountPoint, Source: current.ExpectedSource,
	}
	if targetType == "partition" && strings.HasPrefix(current.ExpectedSource, "/dev/") {
		request.Device = current.ExpectedSource
	}
	return request
}

func parseBackupStorageForm(r *http.Request, includePassword bool) (api.AdminBackupStorageRequest, error) {
	var out api.AdminBackupStorageRequest
	if err := r.ParseForm(); err != nil {
		return out, errors.New("Could not read the backup storage form.")
	}
	out.Type = strings.TrimSpace(r.FormValue("type"))
	out.Path = strings.TrimSpace(r.FormValue("path"))
	out.Device = strings.TrimSpace(r.FormValue("device"))
	out.MountPoint = strings.TrimSpace(r.FormValue("mount_point"))
	out.Source = strings.TrimSpace(r.FormValue("source"))
	out.Username = strings.TrimSpace(r.FormValue("username"))
	out.Domain = strings.TrimSpace(r.FormValue("domain"))
	if includePassword {
		out.Password = r.FormValue("password")
	}
	if out.Type == "" || out.Path == "" {
		return out, errors.New("Choose a backup destination type and backup directory.")
	}
	return out, nil
}

func safeBackupPartitions(storage api.AdminStorageDiscovery) []backupPartitionView {
	out := make([]backupPartitionView, 0)
	for _, device := range storage.Devices {
		if device.Type != "part" || device.ReadOnly {
			continue
		}
		switch device.Filesystem {
		case "xfs", "ext4", "btrfs":
		default:
			continue
		}
		critical := false
		for _, mountpoint := range device.Mountpoints {
			switch mountpoint {
			case "/", "/boot", "/boot/efi", "/var":
				critical = true
			}
		}
		if critical {
			continue
		}
		mountpoint := ""
		if len(device.Mountpoints) > 0 {
			mountpoint = device.Mountpoints[0]
		}
		out = append(out, backupPartitionView{
			Path: device.Path, Name: device.Name, Size: humanBytes(device.SizeBytes), Filesystem: device.Filesystem,
			Label: device.Label, Mountpoint: mountpoint, Model: device.Model, SystemDisk: device.System,
		})
	}
	return out
}

func storageResultMessage(r *http.Request) string {
	if r.URL.Query().Get("result") == "saved" {
		return "Backup storage updated. New manual and automatic backups will use this destination. Minecraft was not restarted."
	}
	return ""
}

func formatOptionalBytes(value uint64) string {
	if value == 0 {
		return "Unknown"
	}
	return humanBytes(value)
}
