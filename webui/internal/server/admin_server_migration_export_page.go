package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminServerMigrationExportAPI interface {
	adminServerMigrationAPI
	AdminMigrationExportPlan(ctx context.Context, session string, request api.AdminMigrationExportTargetRequest) (api.AdminMigrationExportPlanResponse, error)
	AdminMigrationExportApply(ctx context.Context, session string, request api.AdminMigrationExportApplyRequest) (api.AdminMigrationApplyResponse, error)
}

type serverMigrationExportDeviceView struct {
	Device api.AdminMigrationExportDevice
	Size   string
}

type serverMigrationExportPageData struct {
	Title            string
	Version          string
	ManagementAPI    string
	CSRF             string
	Identity         api.SessionInfo
	Discovery        api.AdminMigrationExportDiscoveryResponse
	LocalAvailable   bool
	BackupAvailable  bool
	DeviceAvailable  bool
	NFSAvailable     bool
	SMBAvailable     bool
	AttachedDevices  []serverMigrationExportDeviceView
	RemovableDevices []serverMigrationExportDeviceView
	Error            string
}

type serverMigrationExportReviewPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Request       api.AdminMigrationExportTargetRequest
	Plan          api.AdminMigrationExportPlanResponse
	DataSize      string
	AvailableSize string
	Error         string
}

func (a *App) registerAdminServerExportPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings/server-migration/export", a.serverMigrationExportPage)
	mux.HandleFunc("POST /settings/server-migration/export/review", a.serverMigrationExportReview)
	mux.HandleFunc("POST /settings/server-migration/export/apply", a.serverMigrationExportApply)
	mux.HandleFunc("GET /api/server-migration/progress/{id}", a.serverMigrationProgressStatus)
}

func (a *App) serverMigrationExportRequest(w http.ResponseWriter, r *http.Request, requireCSRF bool) (string, adminServerMigrationExportAPI, api.SessionInfo, bool) {
	if requireCSRF && !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	session, _, identity, ok := a.serverMigrationRequest(w, r)
	if !ok {
		return "", nil, api.SessionInfo{}, false
	}
	client, ok := a.api.(adminServerMigrationExportAPI)
	if !ok {
		http.Error(w, "Server Export management is unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false
	}
	return session, client, identity, true
}

func (a *App) serverMigrationExportPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.serverMigrationExportRequest(w, r, false)
	if !ok {
		return
	}
	if a.redirectCurrentServerMigrationOperation(w, r, session, client) {
		return
	}
	discovery, err := client.AdminMigrationExportDiscovery(r.Context(), session)
	if err != nil {
		a.handleServerMigrationRequestError(w, r, err, "Server Export discovery is unavailable.")
		return
	}
	a.renderServerMigrationExportPage(w, http.StatusOK, identity, discovery, csrfFromRequest(r), "")
}

func (a *App) serverMigrationExportReview(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.serverMigrationExportRequest(w, r, true)
	if !ok {
		return
	}
	if a.redirectCurrentServerMigrationOperation(w, r, session, client) {
		return
	}
	discovery, err := client.AdminMigrationExportDiscovery(r.Context(), session)
	if err != nil {
		a.handleServerMigrationRequestError(w, r, err, "Server Export discovery is unavailable.")
		return
	}
	request, err := parseServerMigrationExportForm(r, discovery)
	if err != nil {
		a.renderServerMigrationExportPage(w, http.StatusBadRequest, identity, discovery, csrfFromRequest(r), err.Error())
		return
	}
	plan, err := client.AdminMigrationExportPlan(r.Context(), session, request)
	if err != nil {
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusBadRequest && plan.SchemaVersion != "" {
			a.renderServerMigrationExportReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), apiMessage(err, "Server Export planning was rejected."))
			return
		}
		a.handleServerMigrationRequestError(w, r, err, "Server Export planning is unavailable.")
		return
	}
	a.renderServerMigrationExportReview(w, http.StatusOK, identity, request, plan, csrfFromRequest(r), "")
}

func (a *App) serverMigrationExportApply(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.serverMigrationExportRequest(w, r, true)
	if !ok {
		return
	}
	if a.redirectCurrentServerMigrationOperation(w, r, session, client) {
		return
	}
	discovery, err := client.AdminMigrationExportDiscovery(r.Context(), session)
	if err != nil {
		a.handleServerMigrationRequestError(w, r, err, "Server Export discovery is unavailable.")
		return
	}
	request, err := parseServerMigrationExportForm(r, discovery)
	if err != nil {
		a.renderServerMigrationExportPage(w, http.StatusBadRequest, identity, discovery, csrfFromRequest(r), err.Error())
		return
	}
	plan, err := client.AdminMigrationExportPlan(r.Context(), session, request)
	if err != nil {
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusBadRequest && plan.SchemaVersion != "" {
			a.renderServerMigrationExportReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), apiMessage(err, "Server Export planning was rejected."))
			return
		}
		a.handleServerMigrationRequestError(w, r, err, "Server Export planning is unavailable.")
		return
	}

	submittedFingerprint := strings.TrimSpace(r.FormValue("plan_fingerprint"))
	if submittedFingerprint == "" || submittedFingerprint != plan.PlanFingerprint {
		a.renderServerMigrationExportReview(w, http.StatusConflict, identity, request, plan, csrfFromRequest(r), "This Server Export changed since it was reviewed. Review the current destination again before continuing.")
		return
	}
	if strings.TrimSpace(r.FormValue("export_confirmation")) != "EXPORT" {
		a.renderServerMigrationExportReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), "Type EXPORT exactly to confirm this Server Export.")
		return
	}
	playersConfirmed := r.FormValue("players_confirmed") == "yes"
	if plan.Requirements != nil && plan.Requirements.PlayersConfirmationRequired && !playersConfirmed {
		a.renderServerMigrationExportReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), "Confirm that the online players may be interrupted before starting Server Export.")
		return
	}

	smbPassword := ""
	if plan.Requirements != nil && plan.Requirements.SMBPasswordRequired {
		smbPassword = r.FormValue("smb_password")
		if smbPassword == "" {
			a.renderServerMigrationExportReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), "Enter the SMB password to start this reviewed Server Export.")
			return
		}
	}

	result, err := client.AdminMigrationExportApply(r.Context(), session, api.AdminMigrationExportApplyRequest{
		PlanFingerprint:  plan.PlanFingerprint,
		Request:          request,
		ExportConfirmed:  true,
		PlayersConfirmed: playersConfirmed,
		SMBPassword:      smbPassword,
	})
	smbPassword = ""
	if err != nil {
		a.renderServerMigrationExportReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), apiMessage(err, "Could not start Server Export."))
		return
	}
	if !result.OK || result.Operation == nil || result.Operation.OperationType != "migration_export" {
		a.renderServerMigrationExportReview(w, http.StatusBadGateway, identity, request, plan, csrfFromRequest(r), "Server Export did not return a valid persistent operation.")
		return
	}
	http.Redirect(w, r, "/settings/server-migration/progress/"+result.Operation.OperationID, http.StatusSeeOther)
}

func parseServerMigrationExportForm(r *http.Request, discovery api.AdminMigrationExportDiscoveryResponse) (api.AdminMigrationExportTargetRequest, error) {
	if err := r.ParseForm(); err != nil {
		return api.AdminMigrationExportTargetRequest{}, errors.New("invalid Server Export request")
	}
	kind := strings.TrimSpace(r.FormValue("kind"))
	filename := strings.TrimSpace(r.FormValue("filename"))
	if !serverMigrationExportKindAvailable(discovery.TargetKinds, kind) {
		return api.AdminMigrationExportTargetRequest{}, errors.New("choose an available Server Export destination type")
	}
	if filename == "" || len(filename) > 160 || strings.ContainsAny(filename, "\r\n") {
		return api.AdminMigrationExportTargetRequest{}, errors.New("enter a valid migration bundle filename")
	}
	request := api.AdminMigrationExportTargetRequest{Kind: kind, Filename: filename}
	switch kind {
	case "local":
		request.Path = strings.TrimSpace(r.FormValue("path"))
		if !strings.HasPrefix(request.Path, "/") {
			return api.AdminMigrationExportTargetRequest{}, errors.New("enter an absolute local/server destination path")
		}
	case "backup":
		if !discovery.ConfiguredBackupAvailable {
			return api.AdminMigrationExportTargetRequest{}, errors.New("configured backup storage is not currently available for Server Export")
		}
	case "device":
		selected := strings.TrimSpace(r.FormValue("device"))
		found := false
		for _, device := range discovery.Devices {
			if device.Path == selected {
				request.Device = device.Path
				request.Removable = device.Removable
				found = true
				break
			}
		}
		if !found {
			return api.AdminMigrationExportTargetRequest{}, errors.New("choose an attached disk or removable-media destination reported by JustVoxel")
		}
	case "nfs":
		request.Source = strings.TrimSpace(r.FormValue("source"))
		if request.Source == "" || !strings.Contains(request.Source, ":") {
			return api.AdminMigrationExportTargetRequest{}, errors.New("enter the temporary NFS source as server:/export/path")
		}
	case "smb":
		request.Source = strings.TrimSpace(r.FormValue("source"))
		request.Username = strings.TrimSpace(r.FormValue("username"))
		request.Domain = strings.TrimSpace(r.FormValue("domain"))
		if !strings.HasPrefix(request.Source, "//") || request.Username == "" {
			return api.AdminMigrationExportTargetRequest{}, errors.New("enter the SMB share as //server/share and provide the SMB username")
		}
	default:
		return api.AdminMigrationExportTargetRequest{}, errors.New("unsupported Server Export destination type")
	}
	if strings.ContainsAny(request.Path+request.Device+request.Source+request.Username+request.Domain, "\r\n") {
		return api.AdminMigrationExportTargetRequest{}, errors.New("Server Export destination contains invalid characters")
	}
	return request, nil
}

func serverMigrationExportKindAvailable(kinds []string, wanted string) bool {
	for _, kind := range kinds {
		if kind == wanted {
			return true
		}
	}
	return false
}

func (a *App) renderServerMigrationExportPage(w http.ResponseWriter, status int, identity api.SessionInfo, discovery api.AdminMigrationExportDiscoveryResponse, csrf, errorMessage string) {
	attached := []serverMigrationExportDeviceView{}
	removable := []serverMigrationExportDeviceView{}
	for _, device := range discovery.Devices {
		view := serverMigrationExportDeviceView{Device: device, Size: humanBytes(device.SizeBytes)}
		if device.Removable {
			removable = append(removable, view)
		} else {
			attached = append(attached, view)
		}
	}
	a.renderServerMigration(w, "server_migration_export.html", status, serverMigrationExportPageData{
		Title:            "Server Export",
		Version:          a.config.Version,
		ManagementAPI:    a.config.ManagementAPI,
		CSRF:             csrf,
		Identity:         identity,
		Discovery:        discovery,
		LocalAvailable:   serverMigrationExportKindAvailable(discovery.TargetKinds, "local"),
		BackupAvailable:  serverMigrationExportKindAvailable(discovery.TargetKinds, "backup") && discovery.ConfiguredBackupAvailable,
		DeviceAvailable:  serverMigrationExportKindAvailable(discovery.TargetKinds, "device"),
		NFSAvailable:     serverMigrationExportKindAvailable(discovery.TargetKinds, "nfs"),
		SMBAvailable:     serverMigrationExportKindAvailable(discovery.TargetKinds, "smb"),
		AttachedDevices:  attached,
		RemovableDevices: removable,
		Error:            errorMessage,
	})
}

func (a *App) renderServerMigrationExportReview(w http.ResponseWriter, status int, identity api.SessionInfo, request api.AdminMigrationExportTargetRequest, plan api.AdminMigrationExportPlanResponse, csrf, errorMessage string) {
	dataSize := ""
	availableSize := ""
	if plan.Normalized != nil {
		dataSize = humanBytes(plan.Normalized.DataBytes)
		if plan.Normalized.TargetAvailableBytes > 0 {
			availableSize = humanBytes(plan.Normalized.TargetAvailableBytes)
		}
	}
	a.renderServerMigration(w, "server_migration_export_review.html", status, serverMigrationExportReviewPageData{
		Title:         "Review Server Export",
		Version:       a.config.Version,
		ManagementAPI: a.config.ManagementAPI,
		CSRF:          csrf,
		Identity:      identity,
		Request:       request,
		Plan:          plan,
		DataSize:      dataSize,
		AvailableSize: availableSize,
		Error:         errorMessage,
	})
}

func (a *App) redirectCurrentServerMigrationOperation(w http.ResponseWriter, r *http.Request, session string, client adminServerMigrationAPI) bool {
	response, err := client.AdminCurrentMigrationOperation(r.Context(), session)
	if err != nil {
		a.handleServerMigrationRequestError(w, r, err, "Server Migration operation status is unavailable.")
		return true
	}
	if response.Operation == nil {
		return false
	}
	if !serverMigrationOperationType(response.Operation.OperationType) {
		http.Error(w, "Server Migration operation status is invalid", http.StatusBadGateway)
		return true
	}
	http.Redirect(w, r, "/settings/server-migration/progress/"+response.Operation.OperationID, http.StatusSeeOther)
	return true
}

func (a *App) serverMigrationProgressStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	client, ok := a.api.(adminServerMigrationAPI)
	if !ok {
		http.Error(w, "Server Migration operation tracking is unavailable", http.StatusServiceUnavailable)
		return
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		if errors.Is(err, api.ErrUnauthorized) {
			a.clearSessionCookies(w)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if errors.Is(err, api.ErrPasswordChangeRequired) {
			http.Error(w, "password change required", http.StatusForbidden)
			return
		}
		http.Error(w, "management service unavailable", http.StatusBadGateway)
		return
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return
	}
	response, err := client.AdminOperation(r.Context(), session, r.PathValue("id"))
	if err != nil {
		var responseErr *api.ResponseError
		if errors.Is(err, api.ErrUnauthorized) {
			a.clearSessionCookies(w)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if errors.Is(err, api.ErrPasswordChangeRequired) {
			http.Error(w, "password change required", http.StatusForbidden)
			return
		}
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
			http.Error(w, "Server Migration operation not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Server Migration operation status is unavailable", http.StatusBadGateway)
		return
	}
	if response.Operation == nil || !serverMigrationOperationType(response.Operation.OperationType) {
		http.Error(w, "Server Migration operation not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "could not encode Server Migration operation", http.StatusInternalServerError)
	}
}

func serverMigrationStageLabel(operationType, value string) string {
	if value == "queued" {
		return "Waiting to start"
	}
	if value == "completed" {
		return "Complete"
	}
	if value == "player_recheck" {
		return "Rechecking online players"
	}
	if value == "minecraft_stop" {
		return "Stopping Minecraft safely"
	}
	if value == "invalid_execution_plan" || value == "interrupted" {
		return "Administrator attention required"
	}
	switch operationType {
	case "migration_export":
		switch value {
		case "export_preflight":
			return "Rechecking reviewed Export"
		case "archive_integrity", "integrity_verification":
			return "Verifying migration bundle"
		case "export_failed":
			return "Preparing safe Export recovery"
		case "rollback":
			return "Finalizing Export recovery state"
		case "export_rolled_back":
			return "Export rolled back"
		case "export_needs_attention", "export_backend_interrupted", "export_backend_incomplete":
			return "Administrator attention required"
		}
	case "migration_import":
		switch value {
		case "recovery_handoff":
			return "Recovery responsibility transferred"
		case "import_preflight":
			return "Rechecking reviewed Import"
		case "import_storage":
			return "Preparing fresh destination storage"
		case "import_execute":
			return "Importing Minecraft server data"
		case "import_verify":
			return "Validating imported Minecraft"
		case "import_failed":
			return "Preparing safe Import rollback"
		case "rollback":
			return "Finalizing Import rollback"
		case "import_rolled_back":
			return "Import rolled back"
		case "import_needs_attention", "import_backend_interrupted", "import_backend_incomplete":
			return "Administrator attention required"
		}
	case "migration_recovery":
		switch value {
		case "recovery_preflight":
			return "Rechecking retained recovery state"
		case "recovery_finalize":
			return "Finalizing retained recovery state"
		case "recovery_verify":
			return "Verifying recovery finalization"
		case "recovery_backend_failed", "recovery_backend_invalid", "recovery_backend_incomplete":
			return "Administrator attention required"
		}
	}
	return friendlyMigrationToken(value)
}
