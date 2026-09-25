package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminDataMigrationAPI interface {
	dataMigrationOrchestrationAPI
	Session(ctx context.Context, session string) (api.SessionInfo, error)
	AdminDataMigrationDiscovery(ctx context.Context, session string) (api.AdminDataMigrationDiscoveryResponse, error)
}

type dataMigrationCandidateView struct {
	Candidate api.AdminDataMigrationCandidate
	Size      string
}

type dataMigrationPageData struct {
	Title           string
	Version         string
	ManagementAPI   string
	CSRF            string
	Identity        api.SessionInfo
	WholeDisks      []dataMigrationCandidateView
	Partitions      []dataMigrationCandidateView
	BlankPartitions []dataMigrationCandidateView
	FreeSpaceDisks  []dataMigrationCandidateView
	Error           string
}

type dataMigrationReviewPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Request       api.AdminDataMigrationPlanRequest
	Plan          api.AdminDataMigrationPlanResponse
	DataSize      string
	TargetSize    string
	OperationName string
	Error         string
}

type dataMigrationProgressPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Operation     api.PersistentOperation
	StateLabel    string
	StageLabel    string
}

func (a *App) registerAdminDataMigrationPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings/data-migration", a.dataMigrationPage)
	mux.HandleFunc("POST /settings/data-migration/review", a.dataMigrationReview)
	mux.HandleFunc("POST /settings/data-migration/apply", a.dataMigrationApply)
	mux.HandleFunc("GET /settings/data-migration/progress/{id}", a.dataMigrationProgressPage)
	mux.HandleFunc("GET /api/data-migration/progress/{id}", a.dataMigrationProgressStatus)
}

func (a *App) dataMigrationRequest(w http.ResponseWriter, r *http.Request, requireCSRF bool) (string, adminDataMigrationAPI, api.SessionInfo, bool) {
	if requireCSRF && !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return "", nil, api.SessionInfo{}, false
	}
	client, ok := a.api.(adminDataMigrationAPI)
	if !ok {
		http.Error(w, "Minecraft data migration is unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.handleDataMigrationAuthError(w, r, err)
		return "", nil, api.SessionInfo{}, false
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	return session, client, identity, true
}

func (a *App) dataMigrationPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.dataMigrationRequest(w, r, false)
	if !ok {
		return
	}
	if a.redirectCurrentDataMigrationOperation(w, r, session, client) {
		return
	}
	discovery, err := client.AdminDataMigrationDiscovery(r.Context(), session)
	if err != nil {
		a.handleDataMigrationRequestError(w, r, err, "Minecraft data migration discovery is unavailable.")
		return
	}
	a.renderDataMigration(w, "data_migration.html", http.StatusOK, dataMigrationPageData{
		Title:           "Minecraft data storage",
		Version:         a.config.Version,
		ManagementAPI:   a.config.ManagementAPI,
		CSRF:            csrfFromRequest(r),
		Identity:        identity,
		WholeDisks:      migrationCandidateViews(discovery.WholeDisks),
		Partitions:      migrationCandidateViews(discovery.Partitions),
		BlankPartitions: migrationCandidateViews(discovery.BlankPartitions),
		FreeSpaceDisks:  migrationCandidateViews(discovery.FreeSpaceDisks),
	})
}

func (a *App) dataMigrationReview(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.dataMigrationRequest(w, r, true)
	if !ok {
		return
	}
	if a.redirectCurrentDataMigrationOperation(w, r, session, client) {
		return
	}
	request, err := parseDataMigrationForm(r)
	if err != nil {
		a.renderDataMigrationPageError(w, r, identity, client, session, err.Error())
		return
	}
	plan, err := dataMigrationPlanReviewed(r.Context(), client, session, request)
	if err != nil {
		a.renderDataMigrationReview(w, dataMigrationErrorStatus(err), identity, request, plan, csrfFromRequest(r), dataMigrationErrorMessage(err, "Minecraft data migration planning is unavailable."))
		return
	}
	a.renderDataMigrationReview(w, http.StatusOK, identity, request, plan, csrfFromRequest(r), "")
}

func (a *App) dataMigrationApply(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.dataMigrationRequest(w, r, true)
	if !ok {
		return
	}
	request, err := parseDataMigrationForm(r)
	if err != nil {
		http.Error(w, "invalid Minecraft data migration request", http.StatusBadRequest)
		return
	}

	plan, operation, err := dataMigrationApplyReviewed(r.Context(), client, session, request, dataMigrationApplyProof{
		PlanFingerprint:    strings.TrimSpace(r.FormValue("plan_fingerprint")),
		MigrationConfirmed: strings.TrimSpace(r.FormValue("migration_confirmation")) == "MIGRATE",
		Confirmation:       strings.TrimSpace(r.FormValue("destructive_confirmation")),
		PlayersConfirmed:   r.FormValue("players_confirmed") == "yes",
	})
	if err != nil {
		a.renderDataMigrationReview(w, dataMigrationErrorStatus(err), identity, request, plan, csrfFromRequest(r), dataMigrationErrorMessage(err, "Could not start Minecraft data migration."))
		return
	}
	http.Redirect(w, r, "/settings/data-migration/progress/"+operation.OperationID, http.StatusSeeOther)
}

func (a *App) dataMigrationProgressPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.dataMigrationRequest(w, r, false)
	if !ok {
		return
	}
	operation, err := dataMigrationOperation(r.Context(), client, session, r.PathValue("id"))
	if err != nil {
		a.handleDataMigrationProgressError(w, r, err)
		return
	}
	a.renderDataMigration(w, "data_migration_progress.html", http.StatusOK, dataMigrationProgressPageData{
		Title:         "Minecraft data migration progress",
		Version:       a.config.Version,
		ManagementAPI: a.config.ManagementAPI,
		CSRF:          csrfFromRequest(r),
		Identity:      identity,
		Operation:     *operation,
		StateLabel:    dataMigrationOperationStateLabel(operation.State),
		StageLabel:    dataMigrationOperationStageLabel(operation.Stage),
	})
}

func (a *App) dataMigrationProgressStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	client, ok := a.api.(adminDataMigrationAPI)
	if !ok {
		http.Error(w, "Minecraft data migration operation tracking is unavailable", http.StatusServiceUnavailable)
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
	operation, err := dataMigrationOperation(r.Context(), client, session, r.PathValue("id"))
	if err != nil {
		a.handleDataMigrationProgressAPIError(w, err)
		return
	}
	response := api.PersistentOperationResponse{Operation: operation}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "could not encode Minecraft data migration operation", http.StatusInternalServerError)
	}
}

func (a *App) redirectCurrentDataMigrationOperation(w http.ResponseWriter, r *http.Request, session string, client adminDataMigrationAPI) bool {
	operation, err := dataMigrationCurrentOperation(r.Context(), client, session)
	if err != nil {
		a.handleDataMigrationRequestError(w, r, err, "Minecraft data migration operation status is unavailable.")
		return true
	}
	if operation == nil {
		return false
	}
	http.Redirect(w, r, "/settings/data-migration/progress/"+operation.OperationID, http.StatusSeeOther)
	return true
}

func (a *App) renderDataMigrationPageError(w http.ResponseWriter, r *http.Request, identity api.SessionInfo, client adminDataMigrationAPI, session, errorMessage string) {
	discovery, err := client.AdminDataMigrationDiscovery(r.Context(), session)
	if err != nil {
		a.handleDataMigrationRequestError(w, r, err, "Minecraft data migration discovery is unavailable.")
		return
	}
	a.renderDataMigration(w, "data_migration.html", http.StatusBadRequest, dataMigrationPageData{
		Title:           "Minecraft data storage",
		Version:         a.config.Version,
		ManagementAPI:   a.config.ManagementAPI,
		CSRF:            csrfFromRequest(r),
		Identity:        identity,
		WholeDisks:      migrationCandidateViews(discovery.WholeDisks),
		Partitions:      migrationCandidateViews(discovery.Partitions),
		BlankPartitions: migrationCandidateViews(discovery.BlankPartitions),
		FreeSpaceDisks:  migrationCandidateViews(discovery.FreeSpaceDisks),
		Error:           errorMessage,
	})
}

func (a *App) renderDataMigrationReview(w http.ResponseWriter, status int, identity api.SessionInfo, request api.AdminDataMigrationPlanRequest, plan api.AdminDataMigrationPlanResponse, csrf, errorMessage string) {
	dataSize := ""
	targetSize := ""
	if plan.Normalized != nil {
		dataSize = humanBytes(plan.Normalized.DataBytes)
		targetSize = humanBytes(plan.Normalized.TargetCapacityBytes)
	}
	a.renderDataMigration(w, "data_migration_review.html", status, dataMigrationReviewPageData{
		Title:         "Review Minecraft data migration",
		Version:       a.config.Version,
		ManagementAPI: a.config.ManagementAPI,
		CSRF:          csrf,
		Identity:      identity,
		Request:       request,
		Plan:          plan,
		DataSize:      dataSize,
		TargetSize:    targetSize,
		OperationName: dataMigrationOperationName(request.Operation),
		Error:         errorMessage,
	})
}

func (a *App) renderDataMigration(w http.ResponseWriter, name string, status int, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := a.templates.ExecuteTemplate(w, name, data); err != nil && status == http.StatusOK {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func parseDataMigrationForm(r *http.Request) (api.AdminDataMigrationPlanRequest, error) {
	if err := r.ParseForm(); err != nil {
		return api.AdminDataMigrationPlanRequest{}, errors.New("invalid Minecraft data migration form")
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
	switch request.Operation {
	case "erase_disk", "use_partition", "format_partition", "create_partition":
	default:
		return request, errors.New("Choose a supported Minecraft data storage operation.")
	}
	if request.Device == "" || request.MountPoint == "" || request.Path == "" {
		return request, errors.New("Choose a storage device, mount point, and Minecraft data directory.")
	}
	return request, nil
}

func migrationCandidateViews(candidates []api.AdminDataMigrationCandidate) []dataMigrationCandidateView {
	views := make([]dataMigrationCandidateView, 0, len(candidates))
	for _, candidate := range candidates {
		views = append(views, dataMigrationCandidateView{Candidate: candidate, Size: humanBytes(candidate.SizeBytes)})
	}
	return views
}

func dataMigrationOperationName(operation string) string {
	switch operation {
	case "erase_disk":
		return "Provision dedicated whole disk"
	case "use_partition":
		return "Use existing filesystem"
	case "format_partition":
		return "Format blank partition"
	case "create_partition":
		return "Create partition in unallocated space"
	default:
		return "Minecraft data migration"
	}
}

func dataMigrationOperationStateLabel(state string) string {
	switch state {
	case "queued":
		return "Queued"
	case "validating":
		return "Validating"
	case "running":
		return "Migrating"
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

func dataMigrationOperationStageLabel(stage string) string {
	switch stage {
	case "queued":
		return "Waiting to start"
	case "migration_preflight", "target_recheck":
		return "Checking reviewed migration"
	case "target_prepare":
		return "Preparing target storage"
	case "staging_space":
		return "Checking free space"
	case "player_recheck":
		return "Rechecking players"
	case "minecraft_stop":
		return "Stopping Minecraft safely"
	case "cold_backup":
		return "Creating verified backup"
	case "copying":
		return "Copying Minecraft data"
	case "copy_verification":
		return "Verifying copied data"
	case "switching":
		return "Switching Minecraft data storage"
	case "minecraft_runtime":
		return "Validating migrated Minecraft"
	case "migrated_storage":
		return "Validating migrated storage"
	case "migration_failed":
		return "Preparing recovery"
	case "rollback":
		return "Restoring previous configuration"
	case "migration_rolled_back":
		return "Rolled back"
	case "migration_needs_attention", "migration_backend_interrupted", "migration_backend_incomplete", "invalid_execution_plan", "interrupted":
		return "Administrator attention required"
	case "completed":
		return "Complete"
	default:
		return "Working"
	}
}

func (a *App) handleDataMigrationAuthError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if errors.Is(err, api.ErrPasswordChangeRequired) {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	http.Error(w, "management service unavailable", http.StatusBadGateway)
}

func (a *App) handleDataMigrationRequestError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	if errors.Is(err, api.ErrUnauthorized) || errors.Is(err, api.ErrPasswordChangeRequired) {
		a.handleDataMigrationAuthError(w, r, err)
		return
	}
	http.Error(w, fallback, http.StatusServiceUnavailable)
}

func (a *App) handleDataMigrationProgressError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, api.ErrUnauthorized) || errors.Is(err, api.ErrPasswordChangeRequired) {
		a.handleDataMigrationAuthError(w, r, err)
		return
	}
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
		http.Error(w, "Minecraft data migration operation not found", http.StatusNotFound)
		return
	}
	http.Error(w, "Minecraft data migration operation status is unavailable", http.StatusBadGateway)
}

func (a *App) handleDataMigrationProgressAPIError(w http.ResponseWriter, err error) {
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if errors.Is(err, api.ErrPasswordChangeRequired) {
		http.Error(w, "password change required", http.StatusForbidden)
		return
	}
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
		http.Error(w, "Minecraft data migration operation not found", http.StatusNotFound)
		return
	}
	http.Error(w, "Minecraft data migration operation status is unavailable", http.StatusBadGateway)
}
