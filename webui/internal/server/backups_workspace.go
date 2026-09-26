package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type backupsWorkspaceFactoryResetRecoveryAPI interface {
	AdminCurrentFactoryResetOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error)
	AdminFactoryResetResolve(ctx context.Context, session, operationID string) (api.PersistentOperationResponse, error)
}

type backupsWorkspaceAPI interface {
	Session(ctx context.Context, session string) (api.SessionInfo, error)
	ManualBackup(ctx context.Context, session string) (api.ManualBackupResponse, error)
	AdminRestoreBackups(ctx context.Context, session string) (api.AdminRestoreBackupsResponse, error)
	AdminConfiguration(ctx context.Context, session string) (api.AdminConfigurationDiscovery, error)
	AdminConfigurationPlan(ctx context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error)
	AdminConfigurationApply(ctx context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error)
	AdminStorage(ctx context.Context, session string) (api.AdminStorageDiscovery, error)
	AdminBackupStorageStatus(ctx context.Context, session string) (api.AdminBackupStorageResponse, error)
	AdminBackupStoragePlan(ctx context.Context, session string, request api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error)
	AdminBackupStorageApply(ctx context.Context, session string, request api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error)
	AdminRestorePlan(ctx context.Context, session string, request api.AdminRestorePlanRequest) (api.AdminRestorePlanResponse, error)
	AdminRestoreApply(ctx context.Context, session string, request api.AdminRestoreApplyRequest) (api.AdminRestoreApplyResponse, error)
	AdminCurrentRestoreOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error)
	AdminOperation(ctx context.Context, session, id string) (api.PersistentOperationResponse, error)
}

type backupsWorkspaceAutomaticForm struct {
	Enabled   bool
	DailyTime string
	Keep      int
}

type backupsWorkspaceBackupView struct {
	ID             string
	CreatedLabel   string
	Size           string
	MetadataStatus string
	Version        string
	Bedrock        bool
	Variant        string
}

type backupsWorkspacePartitionView struct {
	Path       string
	Size       string
	Filesystem string
	Mountpoint string
	Model      string
}

type backupsWorkspaceData struct {
	CSRF                         string
	Identity                     api.SessionInfo
	Backups                      []backupsWorkspaceBackupView
	BackupCount                  int
	TotalSize                    string
	Message                      string
	Error                        string
	Automatic                    backupsWorkspaceAutomaticForm
	AutomaticPlan                *api.AdminConfigurationChangeResponse
	Timezone                     string
	Destination                  api.AdminBackupStorageResponse
	DestinationForm              api.AdminBackupStorageRequest
	DestinationPlan              *api.AdminBackupStorageResponse
	DestinationPartitions        []backupsWorkspacePartitionView
	DestinationAvailable         string
	DestinationFilesystemSize    string
	ProposedDestinationAvailable string
	ProposedDestinationSize      string
	RestoreRequest               api.AdminRestorePlanRequest
	RestorePlan                  *api.AdminRestorePlanResponse
	RestoreSize                  string
	RestoreModeTitle             string
	RestoreScope                 string
	RestoreError                 string
	RestoreOperation             *api.PersistentOperation
	RestoreStateLabel            string
	RestoreStageLabel            string
	RestoreBlockedResetID        string
}

func (a *App) registerBackupsWorkspaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /workspace/backups", a.backupsWorkspacePage)
	mux.HandleFunc("POST /workspace/backups/backup", a.backupsWorkspaceBackupNow)
	mux.HandleFunc("POST /workspace/backups/automatic/plan", a.backupsWorkspaceAutomaticPlan)
	mux.HandleFunc("POST /workspace/backups/automatic/apply", a.backupsWorkspaceAutomaticApply)
	mux.HandleFunc("POST /workspace/backups/destination/plan", a.backupsWorkspaceDestinationPlan)
	mux.HandleFunc("POST /workspace/backups/destination/apply", a.backupsWorkspaceDestinationApply)
	mux.HandleFunc("POST /workspace/backups/restore/plan", a.backupsWorkspaceRestorePlan)
	mux.HandleFunc("POST /workspace/backups/restore/apply", a.backupsWorkspaceRestoreApply)
	mux.HandleFunc("POST /workspace/backups/restore/resolve-reset", a.backupsWorkspaceRestoreResolveReset)
}

func (a *App) backupsWorkspaceRequest(w http.ResponseWriter, r *http.Request, requireCSRF bool) (string, backupsWorkspaceAPI, api.SessionInfo, bool) {
	if mustChange(r) {
		http.Error(w, "Password change required.", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	if requireCSRF && !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return "", nil, api.SessionInfo{}, false
	}
	client, ok := a.api.(backupsWorkspaceAPI)
	if !ok {
		http.Error(w, "Backup management is unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.handleBackupsWorkspaceError(w, r, err)
		return "", nil, api.SessionInfo{}, false
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	return session, client, identity, true
}

func (a *App) backupsWorkspacePage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.backupsWorkspaceRequest(w, r, false)
	if !ok {
		return
	}
	message := ""
	switch r.URL.Query().Get("result") {
	case "backup":
		message = "Backup started. It will appear in the library when the backup service finishes."
	case "deleted":
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		if count == 1 {
			message = "1 backup deleted."
		} else if count > 1 {
			message = strconv.Itoa(count) + " backups deleted."
		} else {
			message = "Selected backups deleted."
		}
	case "automatic":
		message = "Automatic backup settings saved."
	case "destination":
		message = "Backup destination updated. New manual and automatic backups will use it."
	case "restore":
		message = "Restore started. Progress is shown below."
	}
	a.renderBackupsWorkspace(w, r, session, client, identity, message, "", nil, nil, nil, nil, nil, nil, "")
}

func (a *App) backupsWorkspaceBackupNow(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.backupsWorkspaceRequest(w, r, true)
	if !ok {
		return
	}
	result, err := client.ManualBackup(r.Context(), session)
	if err != nil {
		message := apiMessage(err, "Manual backup could not be started.")
		if result.Reason == "backup_cooldown" && result.RetryAfterSeconds > 0 {
			message = "Backup available in " + formatRemaining(result.RetryAfterSeconds) + "."
		} else if result.AdministratorResetRequired || result.Reason == "backup_limit_reached" {
			message = "Backup unavailable. Administrator reset required."
		}
		w.WriteHeader(http.StatusBadRequest)
		a.renderBackupsWorkspace(w, r, session, client, identity, "", message, nil, nil, nil, nil, nil, nil, "")
		return
	}
	http.Redirect(w, r, "/workspace/backups?result=backup", http.StatusSeeOther)
}

func (a *App) backupsWorkspaceAutomaticPlan(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.backupsWorkspaceRequest(w, r, true)
	if !ok {
		return
	}
	form, err := parseBackupsWorkspaceAutomaticForm(r)
	if err != nil {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", err.Error(), &form, nil, nil, nil, nil, nil, "")
		return
	}
	configuration, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		a.handleBackupsWorkspaceError(w, r, err)
		return
	}
	request := backupsWorkspaceAutomaticConfigurationRequest(configuration, form)
	plan, err := client.AdminConfigurationPlan(r.Context(), session, request)
	if err != nil {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", apiMessage(err, "Could not validate automatic backup settings."), &form, nil, nil, nil, nil, nil, "")
		return
	}
	if !backupsWorkspacePlanIsBackupOnly(plan) {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "Configuration changed unexpectedly. Reload Backups and try again.", &form, nil, nil, nil, nil, nil, "")
		return
	}
	a.renderBackupsWorkspace(w, r, session, client, identity, "", "", &form, &plan, nil, nil, nil, nil, "")
}

func (a *App) backupsWorkspaceAutomaticApply(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.backupsWorkspaceRequest(w, r, true)
	if !ok {
		return
	}
	form, err := parseBackupsWorkspaceAutomaticForm(r)
	if err != nil {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", err.Error(), &form, nil, nil, nil, nil, nil, "")
		return
	}
	configuration, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		a.handleBackupsWorkspaceError(w, r, err)
		return
	}
	request := backupsWorkspaceAutomaticConfigurationRequest(configuration, form)
	plan, err := client.AdminConfigurationPlan(r.Context(), session, request)
	if err != nil {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", apiMessage(err, "Could not revalidate automatic backup settings."), &form, nil, nil, nil, nil, nil, "")
		return
	}
	if !backupsWorkspacePlanIsBackupOnly(plan) {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "Configuration changed unexpectedly. Nothing was applied. Reload Backups and try again.", &form, nil, nil, nil, nil, nil, "")
		return
	}
	result, err := client.AdminConfigurationApply(r.Context(), session, request)
	if err != nil {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", apiMessage(err, "Could not apply automatic backup settings."), &form, &plan, nil, nil, nil, nil, "")
		return
	}
	if !result.Applied {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "Automatic backup settings were not applied.", &form, &plan, nil, nil, nil, nil, "")
		return
	}
	http.Redirect(w, r, "/workspace/backups?result=automatic", http.StatusSeeOther)
}

func (a *App) backupsWorkspaceDestinationPlan(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.backupsWorkspaceRequest(w, r, true)
	if !ok {
		return
	}
	request, err := parseBackupsWorkspaceStorageForm(r, false)
	if err != nil {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", err.Error(), nil, nil, &request, nil, nil, nil, "")
		return
	}
	plan, err := client.AdminBackupStoragePlan(r.Context(), session, request)
	if err != nil {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", apiMessage(err, "Could not validate this backup destination."), nil, nil, &request, nil, nil, nil, "")
		return
	}
	a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, &request, &plan, nil, nil, "")
}

func (a *App) backupsWorkspaceDestinationApply(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.backupsWorkspaceRequest(w, r, true)
	if !ok {
		return
	}
	request, err := parseBackupsWorkspaceStorageForm(r, true)
	if err != nil {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", err.Error(), nil, nil, &request, nil, nil, nil, "")
		return
	}
	result, err := client.AdminBackupStorageApply(r.Context(), session, request)
	if err != nil {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", apiMessage(err, "Could not apply this backup destination."), nil, nil, &request, nil, nil, nil, "")
		return
	}
	if !result.Applied {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "Backup destination was not applied.", nil, nil, &request, &result, nil, nil, "")
		return
	}
	http.Redirect(w, r, "/workspace/backups?result=destination", http.StatusSeeOther)
}

func (a *App) backupsWorkspaceRestorePlan(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.backupsWorkspaceRequest(w, r, true)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, nil, nil, "Could not read the Restore request.")
		return
	}
	request := api.AdminRestorePlanRequest{
		BackupID: strings.TrimSpace(r.FormValue("backup_id")),
		Mode:     strings.TrimSpace(r.FormValue("mode")),
	}
	if request.BackupID == "" || (request.Mode != "world" && request.Mode != "full") {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, &request, nil, "Choose one backup and a Restore scope.")
		return
	}
	plan, err := client.AdminRestorePlan(r.Context(), session, request)
	if err != nil {
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusBadRequest && plan.SchemaVersion != "" {
			a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, &request, &plan, apiMessage(err, "Restore planning was rejected."))
			return
		}
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, &request, nil, apiMessage(err, "Restore planning is unavailable."))
		return
	}
	a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, &request, &plan, "")
}

func (a *App) backupsWorkspaceRestoreApply(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.backupsWorkspaceRequest(w, r, true)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, nil, nil, "Could not read the Restore request.")
		return
	}
	request := api.AdminRestorePlanRequest{
		BackupID: strings.TrimSpace(r.FormValue("backup_id")),
		Mode:     strings.TrimSpace(r.FormValue("mode")),
	}
	plan, err := client.AdminRestorePlan(r.Context(), session, request)
	if err != nil {
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusBadRequest && plan.SchemaVersion != "" {
			a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, &request, &plan, apiMessage(err, "Restore planning was rejected."))
			return
		}
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, &request, nil, apiMessage(err, "Restore planning is unavailable."))
		return
	}
	if strings.TrimSpace(r.FormValue("plan_fingerprint")) != plan.PlanFingerprint {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, &request, &plan, "This Restore changed since it was reviewed. Review the current plan before continuing.")
		return
	}
	if strings.TrimSpace(r.FormValue("confirmation")) != "RESTORE" {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, &request, &plan, "Restore confirmation is incomplete.")
		return
	}
	playersConfirmed := r.FormValue("players_confirmed") == "yes"
	if plan.Requirements != nil && plan.Requirements.PlayersConfirmationRequired && !playersConfirmed {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, &request, &plan, "Confirm that the online players may be interrupted before starting Restore.")
		return
	}
	result, err := client.AdminRestoreApply(r.Context(), session, api.AdminRestoreApplyRequest{
		PlanFingerprint: plan.PlanFingerprint,
		Request:         request, DestructiveConfirmed: true, PlayersConfirmed: playersConfirmed,
	})
	if err != nil {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, &request, &plan, apiMessage(err, "Could not start Restore."))
		return
	}
	if !result.OK || result.Operation == nil || result.Operation.OperationType != "restore" {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, &request, &plan, "Restore did not return a valid persistent operation.")
		return
	}
	http.Redirect(w, r, "/workspace/backups?result=restore&restore_operation="+result.Operation.OperationID, http.StatusSeeOther)
}

func (a *App) backupsWorkspaceRestoreResolveReset(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.backupsWorkspaceRequest(w, r, true)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, nil, nil, "Could not read the Restore recovery request.")
		return
	}
	operationID := strings.TrimSpace(r.FormValue("operation_id"))
	request := api.AdminRestorePlanRequest{
		BackupID: strings.TrimSpace(r.FormValue("backup_id")),
		Mode:     strings.TrimSpace(r.FormValue("mode")),
	}
	recovery, ok := any(client).(backupsWorkspaceFactoryResetRecoveryAPI)
	if !ok || operationID == "" {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, &request, nil, "Failed Factory Reset recovery is unavailable.")
		return
	}
	current, err := recovery.AdminCurrentFactoryResetOperation(r.Context(), session)
	if err != nil || current.Operation == nil || current.Operation.OperationID != operationID || current.Operation.OperationType != "factory_reset" || current.Operation.State != "needs_attention" {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, &request, nil, "The failed Factory Reset is no longer waiting for recovery. Review Restore again.")
		return
	}
	if _, err := recovery.AdminFactoryResetResolve(r.Context(), session, operationID); err != nil {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, &request, nil, apiMessage(err, "Failed Factory Reset could not be resolved."))
		return
	}
	plan, err := client.AdminRestorePlan(r.Context(), session, request)
	if err != nil {
		a.renderBackupsWorkspace(w, r, session, client, identity, "", "", nil, nil, nil, nil, &request, nil, apiMessage(err, "Restore planning is unavailable after resolving the failed reset."))
		return
	}
	a.renderBackupsWorkspace(w, r, session, client, identity, "Previous failed Factory Reset was resolved without deleting current data. Review Restore and continue.", "", nil, nil, nil, nil, &request, &plan, "")
}

func (a *App) renderBackupsWorkspace(
	w http.ResponseWriter,
	r *http.Request,
	session string,
	client backupsWorkspaceAPI,
	identity api.SessionInfo,
	message string,
	pageError string,
	automaticOverride *backupsWorkspaceAutomaticForm,
	automaticPlan *api.AdminConfigurationChangeResponse,
	destinationOverride *api.AdminBackupStorageRequest,
	destinationPlan *api.AdminBackupStorageResponse,
	restoreRequest *api.AdminRestorePlanRequest,
	restorePlan *api.AdminRestorePlanResponse,
	restoreError string,
) {
	data, err := a.buildBackupsWorkspaceData(r, session, client, identity, message, pageError, automaticOverride, automaticPlan, destinationOverride, destinationPlan, restoreRequest, restorePlan, restoreError)
	if err != nil {
		a.handleBackupsWorkspaceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, "backups_workspace.html", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (a *App) buildBackupsWorkspaceData(
	r *http.Request,
	session string,
	client backupsWorkspaceAPI,
	identity api.SessionInfo,
	message string,
	pageError string,
	automaticOverride *backupsWorkspaceAutomaticForm,
	automaticPlan *api.AdminConfigurationChangeResponse,
	destinationOverride *api.AdminBackupStorageRequest,
	destinationPlan *api.AdminBackupStorageResponse,
	restoreRequest *api.AdminRestorePlanRequest,
	restorePlan *api.AdminRestorePlanResponse,
	restoreError string,
) (backupsWorkspaceData, error) {
	backups, err := client.AdminRestoreBackups(r.Context(), session)
	if err != nil {
		return backupsWorkspaceData{}, err
	}
	configuration, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		return backupsWorkspaceData{}, err
	}

	automatic := backupsWorkspaceAutomaticForm{
		Enabled: configuration.Backup.TimerEnabled,
		Keep:    configuration.Backup.Keep,
	}
	if dailyTime, ok := backupsWorkspaceDailyTimeFromSchedule(configuration.Backup.Schedule); ok {
		automatic.DailyTime = dailyTime
	} else if pageError == "" {
		pageError = "The current automatic backup schedule is not a simple daily schedule. Change it from Minecraft Settings before editing it here."
	}
	if automaticOverride != nil {
		automatic = *automaticOverride
	}

	destination := api.AdminBackupStorageResponse{}
	destinationForm := api.AdminBackupStorageRequest{}
	partitions := []backupsWorkspacePartitionView{}
	if configuration.Configured {
		storage, err := client.AdminStorage(r.Context(), session)
		if err != nil {
			return backupsWorkspaceData{}, err
		}
		destination, err = client.AdminBackupStorageStatus(r.Context(), session)
		if err != nil {
			return backupsWorkspaceData{}, err
		}
		destinationForm = backupsWorkspaceStorageFormFromCurrent(destination.Current)
		partitions = backupsWorkspaceSafePartitions(storage)
	}
	if destinationOverride != nil {
		destinationForm = *destinationOverride
	}

	var restoreOperation *api.PersistentOperation
	requestedOperationID := strings.TrimSpace(r.URL.Query().Get("restore_operation"))
	if requestedOperationID != "" {
		operationResponse, err := client.AdminOperation(r.Context(), session, requestedOperationID)
		if err != nil {
			return backupsWorkspaceData{}, err
		}
		if operationResponse.Operation != nil && operationResponse.Operation.OperationType == "restore" {
			restoreOperation = operationResponse.Operation
		}
	} else {
		operationResponse, err := client.AdminCurrentRestoreOperation(r.Context(), session)
		if err != nil {
			return backupsWorkspaceData{}, err
		}
		if operationResponse.Operation != nil && operationResponse.Operation.OperationType == "restore" {
			restoreOperation = operationResponse.Operation
		}
	}

	views := make([]backupsWorkspaceBackupView, 0, len(backups.Backups))
	var totalBytes uint64
	for _, backup := range backups.Backups {
		totalBytes += backup.SizeBytes
		view := backupsWorkspaceBackupView{
			ID: backup.ID, CreatedLabel: backupsWorkspaceFormatTime(backup.CreatedAt),
			Size: humanBytes(backup.SizeBytes), MetadataStatus: backup.MetadataStatus,
		}
		if backup.Metadata != nil {
			view.Version = backup.Metadata.Minecraft.ServerReportedVersion
			if view.Version == "" {
				view.Version = backup.Metadata.Minecraft.ConfiguredVersion
			}
			view.Bedrock = backup.Metadata.Bedrock.Enabled
			view.Variant = backup.Metadata.JustVoxel.Variant
		}
		views = append(views, view)
	}

	data := backupsWorkspaceData{
		CSRF: csrfFromRequest(r), Identity: identity, Backups: views,
		BackupCount: len(views), TotalSize: humanBytes(totalBytes), Message: message, Error: pageError,
		Automatic: automatic, AutomaticPlan: automaticPlan, Timezone: configuration.Minecraft.Timezone,
		Destination: destination, DestinationForm: destinationForm, DestinationPlan: destinationPlan,
		DestinationPartitions:     partitions,
		DestinationAvailable:      backupsWorkspaceFormatOptionalBytes(destination.Current.AvailableBytes),
		DestinationFilesystemSize: backupsWorkspaceFormatOptionalBytes(destination.Current.FilesystemBytes),
		RestoreError:              restoreError, RestoreOperation: restoreOperation,
	}
	if restoreError != "" {
		if recovery, ok := any(client).(backupsWorkspaceFactoryResetRecoveryAPI); ok {
			if current, err := recovery.AdminCurrentFactoryResetOperation(r.Context(), session); err == nil && current.Operation != nil &&
				current.Operation.OperationType == "factory_reset" && current.Operation.State == "needs_attention" {
				data.RestoreBlockedResetID = current.Operation.OperationID
			}
		}
	}
	if destinationPlan != nil {
		data.ProposedDestinationAvailable = backupsWorkspaceFormatOptionalBytes(destinationPlan.Proposed.AvailableBytes)
		data.ProposedDestinationSize = backupsWorkspaceFormatOptionalBytes(destinationPlan.Proposed.FilesystemBytes)
	}
	if restoreRequest != nil {
		data.RestoreRequest = *restoreRequest
		data.RestoreModeTitle = backupsWorkspaceRestoreModeTitle(restoreRequest.Mode)
		data.RestoreScope = backupsWorkspaceRestoreModeScope(restoreRequest.Mode)
	}
	if restorePlan != nil {
		data.RestorePlan = restorePlan
		if restorePlan.Normalized != nil {
			data.RestoreSize = humanBytes(restorePlan.Normalized.SizeBytes)
		}
	}
	if restoreOperation != nil {
		data.RestoreStateLabel = backupsWorkspaceRestoreStateLabel(restoreOperation.State)
		data.RestoreStageLabel = backupsWorkspaceRestoreStageLabel(restoreOperation.Stage)
	}
	return data, nil
}

func parseBackupsWorkspaceAutomaticForm(r *http.Request) (backupsWorkspaceAutomaticForm, error) {
	var form backupsWorkspaceAutomaticForm
	if err := r.ParseForm(); err != nil {
		return form, errors.New("Could not read automatic backup settings.")
	}
	form.Enabled = r.FormValue("automatic_enabled") == "on"
	form.DailyTime = strings.TrimSpace(r.FormValue("daily_time"))
	form.Keep, _ = strconv.Atoi(strings.TrimSpace(r.FormValue("backup_keep")))
	if form.Keep <= 0 {
		return form, errors.New("Keep last backups must be a positive number.")
	}
	if _, err := time.Parse("15:04", form.DailyTime); err != nil {
		return form, errors.New("Daily backup time must be a valid 24-hour time.")
	}
	return form, nil
}

func backupsWorkspaceAutomaticConfigurationRequest(configuration api.AdminConfigurationDiscovery, form backupsWorkspaceAutomaticForm) api.AdminConfigurationChangeRequest {
	request := api.AdminConfigurationChangeRequest{
		JavaMemory: configuration.Minecraft.JavaMemory, ContainerMemory: configuration.Minecraft.ContainerMemory,
		JavaPort: configuration.Minecraft.JavaPort, BedrockEnabled: configuration.Minecraft.BedrockEnabled,
		BedrockPort: configuration.Minecraft.BedrockPort, Timezone: configuration.Minecraft.Timezone,
		MaxPlayers: configuration.Minecraft.MaxPlayers, MOTD: configuration.Minecraft.MOTD,
		ImageTag: configuration.Minecraft.ImageTag, VersionPolicy: configuration.Minecraft.VersionMode,
		Version: configuration.Minecraft.Version, BackupKeep: form.Keep,
		BackupSchedule: "*-*-* " + form.DailyTime + ":00", BackupTimerEnabled: form.Enabled,
	}
	return request
}

func backupsWorkspacePlanIsBackupOnly(plan api.AdminConfigurationChangeResponse) bool {
	for _, change := range plan.Changes {
		switch change.Field {
		case "backup_keep", "backup_schedule", "backup_timer_enabled":
		default:
			return false
		}
	}
	return !plan.RestartRequired && !plan.MemoryRestartRequired && !plan.ConfirmationRequired
}

func backupsWorkspaceDailyTimeFromSchedule(schedule string) (string, bool) {
	const prefix = "*-*-* "
	value := strings.TrimSpace(schedule)
	if !strings.HasPrefix(value, prefix) {
		return "", false
	}
	parsed, err := time.Parse("15:04:05", strings.TrimPrefix(value, prefix))
	if err != nil || parsed.Second() != 0 {
		return "", false
	}
	return parsed.Format("15:04"), true
}

func parseBackupsWorkspaceStorageForm(r *http.Request, includePassword bool) (api.AdminBackupStorageRequest, error) {
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

func backupsWorkspaceStorageFormFromCurrent(current api.AdminBackupStorageTarget) api.AdminBackupStorageRequest {
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

func backupsWorkspaceSafePartitions(storage api.AdminStorageDiscovery) []backupsWorkspacePartitionView {
	out := make([]backupsWorkspacePartitionView, 0)
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
		out = append(out, backupsWorkspacePartitionView{
			Path: device.Path, Size: humanBytes(device.SizeBytes), Filesystem: device.Filesystem,
			Mountpoint: mountpoint, Model: device.Model,
		})
	}
	return out
}

func backupsWorkspaceFormatOptionalBytes(value uint64) string {
	if value == 0 {
		return "Unknown"
	}
	return humanBytes(value)
}

func backupsWorkspaceFormatTime(raw string) string {
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	return parsed.UTC().Format("Jan 2, 2006 · 15:04 UTC")
}

func backupsWorkspaceRestoreModeTitle(mode string) string {
	if mode == "full" {
		return "Restore full Minecraft data"
	}
	return "Restore world"
}

func backupsWorkspaceRestoreModeScope(mode string) string {
	if mode == "full" {
		return "Replaces the complete Minecraft persistent data directory, including worlds, plugins, plugin data, server.properties, whitelist/ops, and other Minecraft-side persistent files. JustVoxel system configuration, container policy, Minecraft version policy, and the operating-system deployment remain unchanged."
	}
	return "Replaces the primary Minecraft world and its standard Nether/End world directories. World-based player data rolls back with the world. Plugins, plugin data outside the world directories, server.properties, whitelist/ops, JustVoxel configuration, container policy, Minecraft version policy, and the operating-system deployment remain unchanged."
}

func backupsWorkspaceRestoreStateLabel(state string) string {
	switch state {
	case "queued":
		return "Queued"
	case "validating":
		return "Validating"
	case "running":
		return "Restoring"
	case "verifying":
		return "Runtime validation"
	case "failed":
		return "Recovering"
	case "rolling_back":
		return "Rolling back"
	case "rolled_back":
		return "Rolled back"
	case "needs_attention":
		return "Needs attention"
	case "succeeded":
		return "Complete"
	default:
		return "Working"
	}
}

func backupsWorkspaceRestoreStageLabel(stage string) string {
	switch stage {
	case "queued":
		return "Waiting to start"
	case "restore_preflight", "backup_source":
		return "Checking backup storage"
	case "archive_integrity":
		return "Checking archive integrity"
	case "archive_safety":
		return "Checking archive safety"
	case "staging_space":
		return "Checking staging space"
	case "staging":
		return "Preparing Restore data"
	case "minecraft_stop":
		return "Stopping Minecraft safely"
	case "switching":
		return "Applying Restore data"
	case "minecraft_runtime":
		return "Validating restored Minecraft"
	case "restore_failed":
		return "Preparing recovery"
	case "rollback":
		return "Restoring previous Minecraft data"
	case "restore_rolled_back":
		return "Rolled back"
	case "restore_needs_attention", "interrupted":
		return "Administrator attention required"
	case "completed":
		return "Complete"
	default:
		return "Working"
	}
}

func (a *App) handleBackupsWorkspaceError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if errors.Is(err, api.ErrPasswordChangeRequired) {
		http.Error(w, "Password change required.", http.StatusForbidden)
		return
	}
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusForbidden {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return
	}
	http.Error(w, "backup management service unavailable", http.StatusBadGateway)
}
