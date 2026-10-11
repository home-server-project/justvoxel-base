package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type storageDestinationAPI interface {
	AdminStorage(context.Context, string) (api.AdminStorageDiscovery, error)
	AdminBackupStorageStatus(context.Context, string) (api.AdminBackupStorageResponse, error)
}

// Share the destination component and authoritative plan/apply handlers with Backups.
func (a *App) storageDestinationData(r *http.Request, session string, identity api.SessionInfo, csrf string, request *api.AdminBackupStorageRequest, plan *api.AdminBackupStorageResponse) (backupsWorkspaceData, error) {
	client, ok := a.api.(storageDestinationAPI)
	if !ok {
		return backupsWorkspaceData{}, errors.New("backup destination API is unavailable")
	}
	inventory, err := client.AdminStorage(r.Context(), session)
	if err != nil {
		return backupsWorkspaceData{}, err
	}
	destination, err := client.AdminBackupStorageStatus(r.Context(), session)
	if err != nil {
		return backupsWorkspaceData{}, err
	}
	form := backupsWorkspaceStorageFormFromCurrent(destination.Current)
	if request != nil {
		form = *request
	}
	data := backupsWorkspaceData{
		DestinationWorkspace: "/workspace/storage", CSRF: csrf, Identity: identity,
		Destination: destination, DestinationForm: form, DestinationPlan: plan,
		DestinationPartitions:     backupsWorkspaceSafePartitions(inventory),
		DestinationAvailable:      backupsWorkspaceFormatOptionalBytes(destination.Current.AvailableBytes),
		DestinationFilesystemSize: backupsWorkspaceFormatOptionalBytes(destination.Current.FilesystemBytes),
	}
	if plan != nil {
		data.ProposedDestinationAvailable = backupsWorkspaceFormatOptionalBytes(plan.Proposed.AvailableBytes)
		data.ProposedDestinationSize = backupsWorkspaceFormatOptionalBytes(plan.Proposed.FilesystemBytes)
	}
	return data, nil
}

func (a *App) renderStorageDestination(w http.ResponseWriter, r *http.Request, session string, identity api.SessionInfo, message, pageError string, request *api.AdminBackupStorageRequest, plan *api.AdminBackupStorageResponse) {
	client, ok := a.api.(adminDiscoveryAPI)
	if !ok {
		http.Error(w, "Storage discovery is unavailable", http.StatusServiceUnavailable)
		return
	}
	data, err := a.buildStorageBrowserPageData(r.Context(), session, client, identity, csrfFromRequest(r))
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	destination, err := a.storageDestinationData(r, session, identity, csrfFromRequest(r), request, plan)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	destination.Message, destination.Error = message, pageError
	data.BackupDestination = &destination
	a.renderAdminDiscovery(w, "storage_browser_content", data)
}
