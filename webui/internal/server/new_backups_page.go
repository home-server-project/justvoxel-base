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

type newBackupsAPI interface {
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
}

type newBackupAutomaticForm struct {
	Enabled   bool
	DailyTime string
	Keep      int
}

type newBackupView struct {
	ID             string
	CreatedAt      string
	CreatedLabel   string
	Size           string
	MetadataStatus string
	Version        string
	Bedrock        bool
	Variant        string
}

type newBackupsPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Backups       []newBackupView
	BackupCount   int
	TotalSize     string
	Message       string
	Error         string
	Automatic                    newBackupAutomaticForm
	AutomaticPlan                *api.AdminConfigurationChangeResponse
	Timezone                     string
	Destination                  api.AdminBackupStorageResponse
	DestinationForm              api.AdminBackupStorageRequest
	DestinationPlan              *api.AdminBackupStorageResponse
	DestinationPartitions        []backupPartitionView
	DestinationAvailable         string
	DestinationFilesystemSize    string
	ProposedDestinationAvailable string
	ProposedDestinationSize      string
}

func (a *App) registerNewBackupsPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings/new-backups", a.newBackupsPage)
	mux.HandleFunc("POST /settings/new-backups/backup", a.newBackupsNow)
	mux.HandleFunc("POST /settings/new-backups/automatic/plan", a.newBackupsAutomaticPlan)
	mux.HandleFunc("POST /settings/new-backups/automatic/apply", a.newBackupsAutomaticApply)
	mux.HandleFunc("POST /settings/new-backups/destination/plan", a.newBackupsDestinationPlan)
	mux.HandleFunc("POST /settings/new-backups/destination/apply", a.newBackupsDestinationApply)
	a.registerNewBackupsDeleteRoutes(mux)
}

func (a *App) newBackupsRequest(w http.ResponseWriter, r *http.Request, requireCSRF bool) (string, newBackupsAPI, api.SessionInfo, bool) {
	if requireCSRF && !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return "", nil, api.SessionInfo{}, false
	}
	client, ok := a.api.(newBackupsAPI)
	if !ok {
		http.Error(w, "Backup management is unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.handleNewBackupsError(w, r, err)
		return "", nil, api.SessionInfo{}, false
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	return session, client, identity, true
}

func (a *App) newBackupsPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.newBackupsRequest(w, r, false)
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
	}
	a.renderNewBackupsPage(w, r, session, client, identity, message, "")
}

func (a *App) newBackupsNow(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.newBackupsRequest(w, r, true)
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
		a.renderNewBackupsPage(w, r, session, client, identity, "", message)
		return
	}
	http.Redirect(w, r, "/settings/new-backups?result=backup", http.StatusSeeOther)
}

func (a *App) newBackupsAutomaticPlan(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.newBackupsRequest(w, r, true)
	if !ok {
		return
	}
	form, err := parseNewBackupsAutomaticForm(r)
	if err != nil {
		a.renderNewBackupsPageWithAutomatic(w, r, session, client, identity, "", err.Error(), &form, nil)
		return
	}
	configuration, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		a.handleNewBackupsError(w, r, err)
		return
	}
	request := newBackupsAutomaticConfigurationRequest(configuration, form)
	plan, err := client.AdminConfigurationPlan(r.Context(), session, request)
	if err != nil {
		a.renderNewBackupsPageWithAutomatic(w, r, session, client, identity, "", apiMessage(err, "Could not validate automatic backup settings."), &form, nil)
		return
	}
	if !newBackupsPlanIsBackupOnly(plan) {
		a.renderNewBackupsPageWithAutomatic(w, r, session, client, identity, "", "Configuration changed unexpectedly. Reload New Backups and try again.", &form, nil)
		return
	}
	a.renderNewBackupsPageWithAutomatic(w, r, session, client, identity, "", "", &form, &plan)
}

func (a *App) newBackupsAutomaticApply(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.newBackupsRequest(w, r, true)
	if !ok {
		return
	}
	form, err := parseNewBackupsAutomaticForm(r)
	if err != nil {
		a.renderNewBackupsPageWithAutomatic(w, r, session, client, identity, "", err.Error(), &form, nil)
		return
	}
	configuration, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		a.handleNewBackupsError(w, r, err)
		return
	}
	request := newBackupsAutomaticConfigurationRequest(configuration, form)
	plan, err := client.AdminConfigurationPlan(r.Context(), session, request)
	if err != nil {
		a.renderNewBackupsPageWithAutomatic(w, r, session, client, identity, "", apiMessage(err, "Could not revalidate automatic backup settings."), &form, nil)
		return
	}
	if !newBackupsPlanIsBackupOnly(plan) {
		a.renderNewBackupsPageWithAutomatic(w, r, session, client, identity, "", "Configuration changed unexpectedly. Nothing was applied. Reload New Backups and try again.", &form, nil)
		return
	}
	result, err := client.AdminConfigurationApply(r.Context(), session, request)
	if err != nil {
		a.renderNewBackupsPageWithAutomatic(w, r, session, client, identity, "", apiMessage(err, "Could not apply automatic backup settings."), &form, &plan)
		return
	}
	if !result.Applied {
		a.renderNewBackupsPageWithAutomatic(w, r, session, client, identity, "", "Automatic backup settings were not applied.", &form, &plan)
		return
	}
	http.Redirect(w, r, "/settings/new-backups?result=automatic", http.StatusSeeOther)
}

func (a *App) newBackupsDestinationPlan(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.newBackupsRequest(w, r, true)
	if !ok {
		return
	}
	request, err := parseBackupStorageForm(r, false)
	if err != nil {
		a.renderNewBackupsPageWithDestination(w, r, session, client, identity, "", err.Error(), &request, nil)
		return
	}
	plan, err := client.AdminBackupStoragePlan(r.Context(), session, request)
	if err != nil {
		a.renderNewBackupsPageWithDestination(w, r, session, client, identity, "", apiMessage(err, "Could not validate this backup destination."), &request, nil)
		return
	}
	a.renderNewBackupsPageWithDestination(w, r, session, client, identity, "", "", &request, &plan)
}

func (a *App) newBackupsDestinationApply(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.newBackupsRequest(w, r, true)
	if !ok {
		return
	}
	request, err := parseBackupStorageForm(r, true)
	if err != nil {
		a.renderNewBackupsPageWithDestination(w, r, session, client, identity, "", err.Error(), &request, nil)
		return
	}
	result, err := client.AdminBackupStorageApply(r.Context(), session, request)
	if err != nil {
		a.renderNewBackupsPageWithDestination(w, r, session, client, identity, "", apiMessage(err, "Could not apply this backup destination."), &request, nil)
		return
	}
	if !result.Applied {
		a.renderNewBackupsPageWithDestination(w, r, session, client, identity, "", "Backup destination was not applied.", &request, &result)
		return
	}
	http.Redirect(w, r, "/settings/new-backups?result=destination", http.StatusSeeOther)
}

func parseNewBackupsAutomaticForm(r *http.Request) (newBackupAutomaticForm, error) {
	var form newBackupAutomaticForm
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

func newBackupsAutomaticConfigurationRequest(configuration api.AdminConfigurationDiscovery, form newBackupAutomaticForm) api.AdminConfigurationChangeRequest {
	request := configurationRequestFromDiscovery(configuration)
	request.BackupKeep = form.Keep
	request.BackupSchedule = "*-*-* " + form.DailyTime + ":00"
	request.BackupTimerEnabled = form.Enabled
	return request
}

func backupDailyTimeFromSchedule(schedule string) (string, bool) {
	const prefix = "*-*-* "
	value := strings.TrimSpace(schedule)
	if !strings.HasPrefix(value, prefix) {
		return "", false
	}
	clock := strings.TrimPrefix(value, prefix)
	parsed, err := time.Parse("15:04:05", clock)
	if err != nil || parsed.Second() != 0 {
		return "", false
	}
	return parsed.Format("15:04"), true
}

func newBackupsPlanIsBackupOnly(plan api.AdminConfigurationChangeResponse) bool {
	for _, change := range plan.Changes {
		switch change.Field {
		case "backup_keep", "backup_schedule", "backup_timer_enabled":
		default:
			return false
		}
	}
	return !plan.RestartRequired && !plan.MemoryRestartRequired && !plan.ConfirmationRequired
}

func (a *App) renderNewBackupsPage(w http.ResponseWriter, r *http.Request, session string, client newBackupsAPI, identity api.SessionInfo, message, pageError string) {
	a.renderNewBackupsPageState(w, r, session, client, identity, message, pageError, nil, nil, nil, nil)
}

func (a *App) renderNewBackupsPageWithAutomatic(w http.ResponseWriter, r *http.Request, session string, client newBackupsAPI, identity api.SessionInfo, message, pageError string, automaticOverride *newBackupAutomaticForm, plan *api.AdminConfigurationChangeResponse) {
	a.renderNewBackupsPageState(w, r, session, client, identity, message, pageError, automaticOverride, plan, nil, nil)
}

func (a *App) renderNewBackupsPageWithDestination(w http.ResponseWriter, r *http.Request, session string, client newBackupsAPI, identity api.SessionInfo, message, pageError string, destinationOverride *api.AdminBackupStorageRequest, plan *api.AdminBackupStorageResponse) {
	a.renderNewBackupsPageState(w, r, session, client, identity, message, pageError, nil, nil, destinationOverride, plan)
}

func (a *App) renderNewBackupsPageState(w http.ResponseWriter, r *http.Request, session string, client newBackupsAPI, identity api.SessionInfo, message, pageError string, automaticOverride *newBackupAutomaticForm, automaticPlan *api.AdminConfigurationChangeResponse, destinationOverride *api.AdminBackupStorageRequest, destinationPlan *api.AdminBackupStorageResponse) {
	backups, err := client.AdminRestoreBackups(r.Context(), session)
	if err != nil {
		a.handleNewBackupsError(w, r, err)
		return
	}
	configuration, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		a.handleNewBackupsError(w, r, err)
		return
	}
	automatic := newBackupAutomaticForm{
		Enabled: configuration.Backup.TimerEnabled,
		Keep:    configuration.Backup.Keep,
	}
	if dailyTime, ok := backupDailyTimeFromSchedule(configuration.Backup.Schedule); ok {
		automatic.DailyTime = dailyTime
	} else if pageError == "" {
		pageError = "The current automatic backup schedule is not a simple daily schedule. Change it from Minecraft Settings before editing it here."
	}
	if automaticOverride != nil {
		automatic = *automaticOverride
	}

	destination := api.AdminBackupStorageResponse{}
	destinationForm := api.AdminBackupStorageRequest{}
	partitions := []backupPartitionView{}
	if configuration.Configured {
		storage, err := client.AdminStorage(r.Context(), session)
		if err != nil {
			a.handleNewBackupsError(w, r, err)
			return
		}
		destination, err = client.AdminBackupStorageStatus(r.Context(), session)
		if err != nil {
			a.handleNewBackupsError(w, r, err)
			return
		}
		destinationForm = backupStorageFormFromCurrent(destination.Current)
		partitions = safeBackupPartitions(storage)
	}
	if destinationOverride != nil {
		destinationForm = *destinationOverride
	}

	views := make([]newBackupView, 0, len(backups.Backups))
	var totalBytes uint64
	for _, backup := range backups.Backups {
		totalBytes += backup.SizeBytes
		view := newBackupView{
			ID:             backup.ID,
			CreatedAt:      backup.CreatedAt,
			CreatedLabel:   formatBackupLibraryTime(backup.CreatedAt),
			Size:           humanBytes(backup.SizeBytes),
			MetadataStatus: backup.MetadataStatus,
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
	data := newBackupsPageData{
		Title: "New Backups", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrfFromRequest(r), Identity: identity, Backups: views,
		BackupCount: len(views), TotalSize: humanBytes(totalBytes), Message: message, Error: pageError,
		Automatic: automatic, AutomaticPlan: automaticPlan, Timezone: configuration.Minecraft.Timezone,
		Destination: destination, DestinationForm: destinationForm, DestinationPlan: destinationPlan,
		DestinationPartitions: partitions,
		DestinationAvailable: formatOptionalBytes(destination.Current.AvailableBytes),
		DestinationFilesystemSize: formatOptionalBytes(destination.Current.FilesystemBytes),
	}
	if destinationPlan != nil {
		data.ProposedDestinationAvailable = formatOptionalBytes(destinationPlan.Proposed.AvailableBytes)
		data.ProposedDestinationSize = formatOptionalBytes(destinationPlan.Proposed.FilesystemBytes)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, "new_backups.html", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func formatBackupLibraryTime(raw string) string {
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	return parsed.UTC().Format("Jan 2, 2006 · 15:04 UTC")
}

func (a *App) handleNewBackupsError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusForbidden {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
	http.Error(w, "backup management service unavailable", http.StatusBadGateway)
}
