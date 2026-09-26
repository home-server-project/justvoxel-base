package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminMigrationWorkspaceImportAPI interface {
	adminMigrationWorkspaceAPI
	AdminMigrationImportPlan(ctx context.Context, session string, request api.AdminMigrationImportRequest) (api.AdminMigrationImportPlanResponse, error)
	AdminMigrationImportApply(ctx context.Context, session string, request api.AdminMigrationImportApplyRequest) (api.AdminMigrationApplyResponse, error)
	AdminStorage(ctx context.Context, session string) (api.AdminStorageDiscovery, error)
}

type migrationWorkspaceImportPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Discovery     api.AdminMigrationImportDiscoveryResponse
	Local         bool
	Backup        bool
	Device        bool
	NFS           bool
	SMB           bool
	SourceDevices []migrationWorkspaceExportDeviceView
	Filesystems   []setupFilesystemView
	Error         string
}

type migrationWorkspaceImportReviewPageData struct {
	Title           string
	Version         string
	ManagementAPI   string
	CSRF            string
	Identity        api.SessionInfo
	Request         api.AdminMigrationImportRequest
	Plan            api.AdminMigrationImportPlanResponse
	DataSize        string
	StorageSize     string
	BackupSize      string
	Prompt          string
	Error           string
	NeedsSourcePath bool
	NeedsRoot       bool
	NeedsVersion    bool
	SourceSMB       bool
	EULAURL         string
}

func (a *App) registerAdminMigrationWorkspaceImportPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /workspace/migration/import", a.migrationWorkspaceImportPage)
	mux.HandleFunc("POST /workspace/migration/import/review", a.migrationWorkspaceImportReview)
	mux.HandleFunc("POST /workspace/migration/import/apply", a.migrationWorkspaceImportApply)
}

func (a *App) migrationWorkspaceImportRequest(w http.ResponseWriter, r *http.Request, requireCSRF bool) (string, adminMigrationWorkspaceImportAPI, api.SessionInfo, bool) {
	if requireCSRF && !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	session, _, identity, ok := a.migrationWorkspaceRequest(w, r)
	if !ok {
		return "", nil, api.SessionInfo{}, false
	}
	client, ok := a.api.(adminMigrationWorkspaceImportAPI)
	if !ok {
		http.Error(w, "Server Import management is unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false
	}
	return session, client, identity, true
}

func (a *App) migrationWorkspaceImportPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.migrationWorkspaceImportRequest(w, r, false)
	if !ok {
		return
	}
	if a.redirectCurrentMigrationWorkspaceOperation(w, r, session, client) {
		return
	}
	discovery, devices, inventory, ok := a.migrationWorkspaceImportDependencies(w, r, session, client)
	if !ok {
		return
	}
	a.renderMigrationWorkspaceImportPage(w, http.StatusOK, identity, discovery, devices, inventory, csrfFromRequest(r), "")
}

func (a *App) migrationWorkspaceImportReview(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.migrationWorkspaceImportRequest(w, r, true)
	if !ok {
		return
	}
	if a.redirectCurrentMigrationWorkspaceOperation(w, r, session, client) {
		return
	}
	discovery, devices, inventory, ok := a.migrationWorkspaceImportDependencies(w, r, session, client)
	if !ok {
		return
	}
	request, err := parseMigrationWorkspaceImportForm(r, discovery, devices, inventory)
	if err != nil {
		a.renderMigrationWorkspaceImportPage(w, http.StatusBadRequest, identity, discovery, devices, inventory, csrfFromRequest(r), err.Error())
		return
	}
	plan, err := client.AdminMigrationImportPlan(r.Context(), session, request)
	if err != nil {
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusBadRequest && plan.SchemaVersion != "" {
			if migrationWorkspaceImportPlanNeedsInput(plan.Code) {
				a.renderMigrationWorkspaceImportReview(w, http.StatusOK, identity, scrubMigrationWorkspaceImportSecrets(request), plan, csrfFromRequest(r), migrationWorkspaceImportPrompt(plan), "")
				return
			}
			a.renderMigrationWorkspaceImportReview(w, http.StatusBadRequest, identity, scrubMigrationWorkspaceImportSecrets(request), plan, csrfFromRequest(r), "", apiMessage(err, "Server Import planning was rejected."))
			return
		}
		a.handleMigrationWorkspaceRequestError(w, r, err, "Server Import planning is unavailable.")
		return
	}
	a.renderMigrationWorkspaceImportReview(w, http.StatusOK, identity, scrubMigrationWorkspaceImportSecrets(request), plan, csrfFromRequest(r), "", "")
}

func (a *App) migrationWorkspaceImportApply(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.migrationWorkspaceImportRequest(w, r, true)
	if !ok {
		return
	}
	if a.redirectCurrentMigrationWorkspaceOperation(w, r, session, client) {
		return
	}
	discovery, devices, inventory, ok := a.migrationWorkspaceImportDependencies(w, r, session, client)
	if !ok {
		return
	}
	request, err := parseMigrationWorkspaceImportForm(r, discovery, devices, inventory)
	if err != nil {
		a.renderMigrationWorkspaceImportPage(w, http.StatusBadRequest, identity, discovery, devices, inventory, csrfFromRequest(r), err.Error())
		return
	}
	plan, err := client.AdminMigrationImportPlan(r.Context(), session, request)
	if err != nil {
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusBadRequest && plan.SchemaVersion != "" {
			a.renderMigrationWorkspaceImportReview(w, http.StatusBadRequest, identity, scrubMigrationWorkspaceImportSecrets(request), plan, csrfFromRequest(r), migrationWorkspaceImportPrompt(plan), apiMessage(err, "Server Import changed and must be reviewed again."))
			return
		}
		a.handleMigrationWorkspaceRequestError(w, r, err, "Server Import planning is unavailable.")
		return
	}

	publicRequest := scrubMigrationWorkspaceImportSecrets(request)
	if submitted := strings.TrimSpace(r.FormValue("plan_fingerprint")); submitted == "" || submitted != plan.PlanFingerprint {
		a.renderMigrationWorkspaceImportReview(w, http.StatusConflict, identity, publicRequest, plan, csrfFromRequest(r), "", "This Server Import changed since it was reviewed. Review the current source and destination again before continuing.")
		return
	}
	if strings.TrimSpace(r.FormValue("import_confirmation")) != "IMPORT" {
		a.renderMigrationWorkspaceImportReview(w, http.StatusBadRequest, identity, publicRequest, plan, csrfFromRequest(r), "", "Type IMPORT exactly to confirm this Server Import.")
		return
	}
	requirements := plan.Requirements
	if requirements == nil {
		a.renderMigrationWorkspaceImportReview(w, http.StatusBadGateway, identity, publicRequest, plan, csrfFromRequest(r), "", "Server Import did not return its required safety confirmations.")
		return
	}
	playersConfirmed := r.FormValue("players_confirmed") == "yes"
	eulaAccepted := r.FormValue("eula_accepted") == "yes"
	vanillaConfirmed := r.FormValue("vanilla_confirmed") == "yes"
	pluginsConfirmed := r.FormValue("plugins_confirmed") == "yes"
	onlineModeConfirmed := r.FormValue("online_mode_confirmed") == "yes"

	if requirements.PlayersConfirmationRequired && !playersConfirmed {
		a.renderMigrationWorkspaceImportReview(w, http.StatusBadRequest, identity, publicRequest, plan, csrfFromRequest(r), "", "Confirm that the online players may be interrupted before starting Server Import.")
		return
	}
	if requirements.EULAAcceptanceRequired && !eulaAccepted {
		a.renderMigrationWorkspaceImportReview(w, http.StatusBadRequest, identity, publicRequest, plan, csrfFromRequest(r), "", "Accept the Minecraft EULA for this fresh Server Import.")
		return
	}
	if requirements.VanillaConfirmationRequired && !vanillaConfirmed {
		a.renderMigrationWorkspaceImportReview(w, http.StatusBadRequest, identity, publicRequest, plan, csrfFromRequest(r), "", "Confirm the reviewed Vanilla-to-Paper conversion before starting Server Import.")
		return
	}
	if requirements.PluginsConfirmationRequired && !pluginsConfirmed {
		a.renderMigrationWorkspaceImportReview(w, http.StatusBadRequest, identity, publicRequest, plan, csrfFromRequest(r), "", "Confirm that the reviewed external plugin JARs may be preserved and executed.")
		return
	}
	if requirements.OnlineModeConfirmationRequired && !onlineModeConfirmed {
		a.renderMigrationWorkspaceImportReview(w, http.StatusBadRequest, identity, publicRequest, plan, csrfFromRequest(r), "", "Confirm that the source used online-mode=true before starting Server Import.")
		return
	}

	backupSMBPassword := ""
	if requirements.BackupSMBPasswordRequired {
		backupSMBPassword = r.FormValue("backup_smb_password")
		if backupSMBPassword == "" {
			a.renderMigrationWorkspaceImportReview(w, http.StatusBadRequest, identity, publicRequest, plan, csrfFromRequest(r), "", "Enter the SMB backup password to start this reviewed fresh Server Import.")
			return
		}
	}

	result, err := client.AdminMigrationImportApply(r.Context(), session, api.AdminMigrationImportApplyRequest{
		PlanFingerprint:     plan.PlanFingerprint,
		Request:             request,
		ImportConfirmed:     true,
		PlayersConfirmed:    playersConfirmed,
		EULAAccepted:        eulaAccepted,
		VanillaConfirmed:    vanillaConfirmed,
		PluginsConfirmed:    pluginsConfirmed,
		OnlineModeConfirmed: onlineModeConfirmed,
		BackupSMBPassword:   backupSMBPassword,
	})
	request.Source.SMBPassword = ""
	backupSMBPassword = ""
	if err != nil {
		a.renderMigrationWorkspaceImportReview(w, http.StatusBadRequest, identity, publicRequest, plan, csrfFromRequest(r), "", apiMessage(err, "Could not start Server Import."))
		return
	}
	if !result.OK || result.Operation == nil || result.Operation.OperationType != "migration_import" {
		a.renderMigrationWorkspaceImportReview(w, http.StatusBadGateway, identity, publicRequest, plan, csrfFromRequest(r), "", "Server Import did not return a valid persistent operation.")
		return
	}
	http.Redirect(w, r, "/workspace/migration/progress/"+result.Operation.OperationID, http.StatusSeeOther)
}

func (a *App) migrationWorkspaceImportDependencies(w http.ResponseWriter, r *http.Request, session string, client adminMigrationWorkspaceImportAPI) (api.AdminMigrationImportDiscoveryResponse, api.AdminMigrationExportDiscoveryResponse, api.AdminStorageDiscovery, bool) {
	discovery, err := client.AdminMigrationImportDiscovery(r.Context(), session)
	if err != nil {
		a.handleMigrationWorkspaceRequestError(w, r, err, "Server Import discovery is unavailable.")
		return api.AdminMigrationImportDiscoveryResponse{}, api.AdminMigrationExportDiscoveryResponse{}, api.AdminStorageDiscovery{}, false
	}
	devices, err := client.AdminMigrationExportDiscovery(r.Context(), session)
	if err != nil {
		a.handleMigrationWorkspaceRequestError(w, r, err, "Server Import media discovery is unavailable.")
		return api.AdminMigrationImportDiscoveryResponse{}, api.AdminMigrationExportDiscoveryResponse{}, api.AdminStorageDiscovery{}, false
	}
	inventory := api.AdminStorageDiscovery{}
	if !discovery.Configured {
		inventory, err = client.AdminStorage(r.Context(), session)
		if err != nil {
			a.handleMigrationWorkspaceRequestError(w, r, err, "Fresh Server Import storage discovery is unavailable.")
			return api.AdminMigrationImportDiscoveryResponse{}, api.AdminMigrationExportDiscoveryResponse{}, api.AdminStorageDiscovery{}, false
		}
	}
	return discovery, devices, inventory, true
}

func parseMigrationWorkspaceImportForm(r *http.Request, discovery api.AdminMigrationImportDiscoveryResponse, devices api.AdminMigrationExportDiscoveryResponse, inventory api.AdminStorageDiscovery) (api.AdminMigrationImportRequest, error) {
	if err := r.ParseForm(); err != nil {
		return api.AdminMigrationImportRequest{}, errors.New("invalid Server Import request")
	}
	kind := strings.TrimSpace(r.FormValue("source_kind"))
	if !migrationWorkspaceExportKindAvailable(discovery.SourceKinds, kind) {
		return api.AdminMigrationImportRequest{}, errors.New("choose an available Server Import source type")
	}

	source := api.AdminMigrationImportSourceRequest{
		Kind:          kind,
		SelectedRoot:  strings.TrimSpace(r.FormValue("selected_root")),
		SourceVersion: strings.TrimSpace(r.FormValue("source_version")),
	}
	manualPath := strings.TrimSpace(r.FormValue("manual_path"))
	relativePath := strings.TrimSpace(r.FormValue("source_path"))
	if manualPath != "" {
		relativePath = manualPath
	}

	switch kind {
	case "local":
		source.Path = strings.TrimSpace(r.FormValue("local_path"))
		if !strings.HasPrefix(source.Path, "/") {
			return api.AdminMigrationImportRequest{}, errors.New("enter an absolute local/server Import source path")
		}
	case "backup":
		source.Path = relativePath
	case "device":
		source.Path = relativePath
		selected := strings.TrimSpace(r.FormValue("source_device"))
		found := false
		for _, device := range devices.Devices {
			if device.Path == selected {
				source.Device = device.Path
				source.Removable = device.Removable
				found = true
				break
			}
		}
		if !found {
			return api.AdminMigrationImportRequest{}, errors.New("choose an attached disk, partition, or removable source reported by JustVoxel")
		}
	case "nfs":
		source.Path = relativePath
		source.Source = strings.TrimSpace(r.FormValue("source_remote"))
		if source.Source == "" || !strings.Contains(source.Source, ":") {
			return api.AdminMigrationImportRequest{}, errors.New("enter the temporary NFS source as server:/export/path")
		}
	case "smb":
		source.Path = relativePath
		source.Source = strings.TrimSpace(r.FormValue("source_remote"))
		source.Username = strings.TrimSpace(r.FormValue("source_username"))
		source.Domain = strings.TrimSpace(r.FormValue("source_domain"))
		source.SMBPassword = r.FormValue("source_smb_password")
		if !strings.HasPrefix(source.Source, "//") || source.Username == "" {
			return api.AdminMigrationImportRequest{}, errors.New("enter the SMB source as //server/share and provide the SMB username")
		}
		if source.SMBPassword == "" {
			return api.AdminMigrationImportRequest{}, errors.New("enter the SMB source password so JustVoxel can inspect this source")
		}
	default:
		return api.AdminMigrationImportRequest{}, errors.New("unsupported Server Import source type")
	}
	if strings.ContainsAny(source.Path+source.Device+source.Source+source.Username+source.Domain+source.SelectedRoot+source.SourceVersion, "\r\n") {
		return api.AdminMigrationImportRequest{}, errors.New("Server Import source contains invalid characters")
	}

	destination, err := parseMigrationWorkspaceImportDestination(r, discovery, inventory)
	if err != nil {
		return api.AdminMigrationImportRequest{}, err
	}
	return api.AdminMigrationImportRequest{Source: source, Destination: destination}, nil
}

func parseMigrationWorkspaceImportDestination(r *http.Request, discovery api.AdminMigrationImportDiscoveryResponse, inventory api.AdminStorageDiscovery) (api.AdminMigrationImportDestinationRequest, error) {
	defaults := discovery.Defaults
	if discovery.Configured {
		return api.AdminMigrationImportDestinationRequest{
			JavaPort:        defaults.JavaPort,
			BedrockPort:     defaults.BedrockPort,
			JavaMemory:      defaults.JavaMemory,
			ContainerMemory: defaults.ContainerMemory,
			Timezone:        defaults.Timezone,
			BackupKeep:      defaults.BackupKeep,
			BackupDailyTime: defaults.BackupDailyTime,
			BackupAutomatic: defaults.BackupAutomatic,
			Storage:         api.AdminSetupPlanStorageRequest{},
			Backups:         api.AdminSetupPlanBackupsRequest{},
		}, nil
	}

	javaPort, err := parseSetupPort(r.FormValue("java_port"), "Minecraft Java port")
	if err != nil {
		return api.AdminMigrationImportDestinationRequest{}, err
	}
	bedrockPort, err := parseSetupPort(r.FormValue("bedrock_port"), "Bedrock UDP port")
	if err != nil {
		return api.AdminMigrationImportDestinationRequest{}, err
	}
	javaMemory := strings.TrimSpace(r.FormValue("java_memory"))
	containerMemory := strings.TrimSpace(r.FormValue("container_memory"))
	if _, ok := setupMemoryMiB(javaMemory); !ok {
		return api.AdminMigrationImportDestinationRequest{}, errors.New("Minecraft game memory must use a size such as 4G or 4096M.")
	}
	if _, ok := setupMemoryMiB(containerMemory); !ok {
		return api.AdminMigrationImportDestinationRequest{}, errors.New("Maximum Minecraft memory must use a size such as 6G or 6144M.")
	}
	javaMiB, _ := setupMemoryMiB(javaMemory)
	containerMiB, _ := setupMemoryMiB(containerMemory)
	if containerMiB <= javaMiB {
		return api.AdminMigrationImportDestinationRequest{}, errors.New("Maximum Minecraft memory must be larger than Minecraft game memory.")
	}
	timezone := strings.TrimSpace(r.FormValue("timezone"))
	if !setupTimezonePattern.MatchString(timezone) || strings.Contains(timezone, "..") || strings.HasPrefix(timezone, "/") {
		return api.AdminMigrationImportDestinationRequest{}, errors.New("Timezone must look like UTC or America/Toronto.")
	}
	backupKeep, err := strconv.Atoi(strings.TrimSpace(r.FormValue("backup_keep")))
	if err != nil || backupKeep < 1 {
		return api.AdminMigrationImportDestinationRequest{}, errors.New("Backup retention must be a positive number.")
	}
	backupAutomatic := r.FormValue("backup_automatic") == "on"
	backupDailyTime := strings.TrimSpace(r.FormValue("backup_daily_time"))
	if backupAutomatic {
		backupDailyTime, err = normalizeSetupBackupTime(backupDailyTime)
		if err != nil {
			return api.AdminMigrationImportDestinationRequest{}, err
		}
	}
	if backupDailyTime == "" {
		backupDailyTime = defaults.BackupDailyTime
	}

	setupDefaults := api.AdminSetupDefaults{
		DataPath:        defaults.DataPath,
		BackupPath:      defaults.BackupPath,
		BackupKeep:      defaults.BackupKeep,
		BackupDailyTime: defaults.BackupDailyTime,
	}
	storageDraft := setupStorageDraft{
		Type:       strings.TrimSpace(r.FormValue("storage_type")),
		Path:       strings.TrimSpace(r.FormValue("data_path")),
		Device:     strings.TrimSpace(r.FormValue("data_device")),
		MountPoint: strings.TrimSpace(r.FormValue("data_mount_point")),
	}
	if storageDraft.Type == "system" {
		storageDraft.Path = defaults.DataPath
		storageDraft.Device = ""
		storageDraft.MountPoint = ""
	}
	if err := validateSetupStorage(storageDraft, inventory, setupDefaults); err != nil {
		return api.AdminMigrationImportDestinationRequest{}, err
	}

	backupDraft := setupBackupDraft{
		Automatic:  backupAutomatic,
		DailyTime:  backupDailyTime,
		Keep:       strconv.Itoa(backupKeep),
		Type:       strings.TrimSpace(r.FormValue("backup_type")),
		Path:       strings.TrimSpace(r.FormValue("backup_path")),
		Device:     strings.TrimSpace(r.FormValue("backup_device")),
		MountPoint: strings.TrimSpace(r.FormValue("backup_mount_point")),
		Source:     strings.TrimSpace(r.FormValue("backup_source")),
		Username:   strings.TrimSpace(r.FormValue("backup_username")),
		Domain:     strings.TrimSpace(r.FormValue("backup_domain")),
	}
	if backupDraft.Type == "system" {
		backupDraft.Path = defaults.BackupPath
		backupDraft.Device = ""
		backupDraft.MountPoint = ""
		backupDraft.Source = ""
		backupDraft.Username = ""
		backupDraft.Domain = ""
	}
	if err := validateSetupBackups(backupDraft, storageDraft, inventory, setupDefaults); err != nil {
		return api.AdminMigrationImportDestinationRequest{}, err
	}

	return api.AdminMigrationImportDestinationRequest{
		JavaPort:        javaPort,
		BedrockPort:     bedrockPort,
		JavaMemory:      javaMemory,
		ContainerMemory: containerMemory,
		Timezone:        timezone,
		BackupKeep:      backupKeep,
		BackupDailyTime: backupDailyTime,
		BackupAutomatic: backupAutomatic,
		Storage: api.AdminSetupPlanStorageRequest{
			Type: storageDraft.Type, Path: storageDraft.Path, Device: storageDraft.Device, MountPoint: storageDraft.MountPoint,
		},
		Backups: api.AdminSetupPlanBackupsRequest{
			Automatic: backupDraft.Automatic, DailyTime: backupDraft.DailyTime, Keep: backupKeep,
			Type: backupDraft.Type, Path: backupDraft.Path, Device: backupDraft.Device, MountPoint: backupDraft.MountPoint,
			Source: backupDraft.Source, Username: backupDraft.Username, Domain: backupDraft.Domain,
		},
	}, nil
}

func scrubMigrationWorkspaceImportSecrets(request api.AdminMigrationImportRequest) api.AdminMigrationImportRequest {
	request.Source.SMBPassword = ""
	return request
}

func migrationWorkspaceImportPlanNeedsInput(code string) bool {
	switch code {
	case "source_selection_required", "multiple_roots", "source_version_required", "stale_root":
		return true
	default:
		return false
	}
}

func migrationWorkspaceImportPrompt(plan api.AdminMigrationImportPlanResponse) string {
	switch plan.Code {
	case "source_selection_required":
		return "Choose an JustVoxel-discovered archive or server directory, or enter a safe relative path manually."
	case "multiple_roots", "stale_root":
		return "Choose exactly one Minecraft server root detected by JustVoxel."
	case "source_version_required":
		return "Enter the exact Minecraft version used by this source so JustVoxel can validate a matching stable Paper build."
	default:
		return ""
	}
}

func (a *App) renderMigrationWorkspaceImportPage(w http.ResponseWriter, status int, identity api.SessionInfo, discovery api.AdminMigrationImportDiscoveryResponse, devices api.AdminMigrationExportDiscoveryResponse, inventory api.AdminStorageDiscovery, csrf, errorMessage string) {
	deviceViews := make([]migrationWorkspaceExportDeviceView, 0, len(devices.Devices))
	for _, device := range devices.Devices {
		deviceViews = append(deviceViews, migrationWorkspaceExportDeviceView{Device: device, Size: humanBytes(device.SizeBytes)})
	}
	a.renderMigrationWorkspace(w, "migration_workspace_import.html", status, migrationWorkspaceImportPageData{
		Title:         "Server Import",
		Version:       a.config.Version,
		ManagementAPI: a.config.ManagementAPI,
		CSRF:          csrf,
		Identity:      identity,
		Discovery:     discovery,
		Local:         migrationWorkspaceExportKindAvailable(discovery.SourceKinds, "local"),
		Backup:        migrationWorkspaceExportKindAvailable(discovery.SourceKinds, "backup"),
		Device:        migrationWorkspaceExportKindAvailable(discovery.SourceKinds, "device"),
		NFS:           migrationWorkspaceExportKindAvailable(discovery.SourceKinds, "nfs"),
		SMB:           migrationWorkspaceExportKindAvailable(discovery.SourceKinds, "smb"),
		SourceDevices: deviceViews,
		Filesystems:   setupFilesystemViews(inventory),
		Error:         errorMessage,
	})
}

func (a *App) renderMigrationWorkspaceImportReview(w http.ResponseWriter, status int, identity api.SessionInfo, request api.AdminMigrationImportRequest, plan api.AdminMigrationImportPlanResponse, csrf, prompt, errorMessage string) {
	dataSize := ""
	storageSize := ""
	backupSize := ""
	if plan.Normalized != nil {
		dataSize = humanBytes(plan.Normalized.Source.ExpandedBytes)
		if plan.Normalized.Destination.Storage != nil && plan.Normalized.Destination.Storage.SizeBytes > 0 {
			storageSize = humanBytes(plan.Normalized.Destination.Storage.SizeBytes)
		}
		if plan.Normalized.Destination.Backups != nil && plan.Normalized.Destination.Backups.SizeBytes > 0 {
			backupSize = humanBytes(plan.Normalized.Destination.Backups.SizeBytes)
		}
	}
	a.renderMigrationWorkspace(w, "migration_workspace_import_review.html", status, migrationWorkspaceImportReviewPageData{
		Title:           "Review Server Import",
		Version:         a.config.Version,
		ManagementAPI:   a.config.ManagementAPI,
		CSRF:            csrf,
		Identity:        identity,
		Request:         request,
		Plan:            plan,
		DataSize:        dataSize,
		StorageSize:     storageSize,
		BackupSize:      backupSize,
		Prompt:          prompt,
		Error:           errorMessage,
		NeedsSourcePath: plan.Code == "source_selection_required",
		NeedsRoot:       plan.Code == "multiple_roots" || plan.Code == "stale_root",
		NeedsVersion:    plan.Code == "source_version_required",
		SourceSMB:       request.Source.Kind == "smb",
		EULAURL:         minecraftEULAURL,
	})
}
