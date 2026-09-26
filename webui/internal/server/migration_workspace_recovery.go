package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminMigrationWorkspaceRecoveryAPI interface {
	adminMigrationWorkspaceAPI
	AdminMigrationRecoveryPlan(ctx context.Context, session string, request api.AdminMigrationRecoveryPlanRequest) (api.AdminMigrationRecoveryPlanResponse, error)
	AdminMigrationRecoveryApply(ctx context.Context, session string, request api.AdminMigrationRecoveryApplyRequest) (api.AdminMigrationApplyResponse, error)
	AdminMigrationImportResolve(ctx context.Context, session string, request api.AdminMigrationImportResolveRequest) (api.AdminMigrationImportResolveResponse, error)
}

type migrationWorkspaceRecoveryTransactionView struct {
	Summary    api.AdminMigrationRecoverySummary
	ModeLabel  string
	PhaseLabel string
}

type migrationWorkspaceRecoveryPageData struct {
	Title                string
	Version              string
	ManagementAPI        string
	CSRF                 string
	Identity             api.SessionInfo
	Discovery            api.AdminMigrationRecoveryDiscoveryResponse
	Transactions         []migrationWorkspaceRecoveryTransactionView
	ImportNeedsAttention bool
	ImportOperationID    string
	CanKeepCurrent       bool
	Error                string
}

type migrationWorkspaceRecoveryReviewPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Request       api.AdminMigrationRecoveryPlanRequest
	Plan          api.AdminMigrationRecoveryPlanResponse
	ModeLabel     string
	PhaseLabel    string
	Error         string
}

func (a *App) registerAdminMigrationWorkspaceRecoveryPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /workspace/migration/recovery", a.migrationWorkspaceRecoveryPage)
	mux.HandleFunc("POST /workspace/migration/recovery/review", a.migrationWorkspaceRecoveryReview)
	mux.HandleFunc("POST /workspace/migration/recovery/apply", a.migrationWorkspaceRecoveryApply)
	mux.HandleFunc("POST /workspace/migration/recovery/resolve", a.migrationWorkspaceRecoveryResolve)
}

func (a *App) migrationWorkspaceRecoveryRequest(w http.ResponseWriter, r *http.Request, requireCSRF bool) (string, adminMigrationWorkspaceRecoveryAPI, api.SessionInfo, bool, bool) {
	if requireCSRF && !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false, false
	}
	session, _, identity, ok := a.migrationWorkspaceRequest(w, r)
	if !ok {
		return "", nil, api.SessionInfo{}, false, false
	}
	client, ok := a.api.(adminMigrationWorkspaceRecoveryAPI)
	if !ok {
		http.Error(w, "Migration Recovery management is unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false, false
	}
	current, err := client.AdminCurrentMigrationOperation(r.Context(), session)
	if err != nil {
		a.handleMigrationWorkspaceRequestError(w, r, err, "Server Migration operation status is unavailable.")
		return "", nil, api.SessionInfo{}, false, false
	}
	if current.Operation != nil {
		if current.Operation.OperationType == "migration_import" && current.Operation.State == "needs_attention" {
			return session, client, identity, true, true
		}
		if !migrationWorkspaceOperationType(current.Operation.OperationType) {
			http.Error(w, "Server Migration operation status is invalid", http.StatusBadGateway)
			return "", nil, api.SessionInfo{}, false, false
		}
		http.Redirect(w, r, "/workspace/migration/progress/"+current.Operation.OperationID, http.StatusSeeOther)
		return "", nil, api.SessionInfo{}, false, false
	}
	return session, client, identity, false, true
}

func (a *App) migrationWorkspaceRecoveryPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, importNeedsAttention, ok := a.migrationWorkspaceRecoveryRequest(w, r, false)
	if !ok {
		return
	}
	discovery, err := client.AdminMigrationRecoveryDiscovery(r.Context(), session)
	if err != nil {
		a.handleMigrationWorkspaceRequestError(w, r, err, "Migration Recovery discovery is unavailable.")
		return
	}
	importOperationID := ""
	if importNeedsAttention && len(discovery.Transactions) == 0 {
		current, currentErr := client.AdminCurrentMigrationOperation(r.Context(), session)
		if currentErr != nil {
			a.handleMigrationWorkspaceRequestError(w, r, currentErr, "Server Migration operation status is unavailable.")
			return
		}
		if current.Operation != nil && current.Operation.OperationType == "migration_import" && current.Operation.State == "needs_attention" {
			importOperationID = current.Operation.OperationID
		}
	}
	a.renderMigrationWorkspaceRecoveryPage(w, http.StatusOK, identity, discovery, importNeedsAttention, importOperationID, csrfFromRequest(r), "")
}

func (a *App) migrationWorkspaceRecoveryReview(w http.ResponseWriter, r *http.Request) {
	session, client, identity, _, ok := a.migrationWorkspaceRecoveryRequest(w, r, true)
	if !ok {
		return
	}
	discovery, err := client.AdminMigrationRecoveryDiscovery(r.Context(), session)
	if err != nil {
		a.handleMigrationWorkspaceRequestError(w, r, err, "Migration Recovery discovery is unavailable.")
		return
	}
	request, err := parseMigrationWorkspaceRecoveryForm(r, discovery)
	if err != nil {
		a.renderMigrationWorkspaceRecoveryPage(w, http.StatusBadRequest, identity, discovery, false, "", csrfFromRequest(r), err.Error())
		return
	}
	plan, err := client.AdminMigrationRecoveryPlan(r.Context(), session, request)
	if err != nil {
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusBadRequest && plan.SchemaVersion != "" {
			a.renderMigrationWorkspaceRecoveryReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), apiMessage(err, "Migration Recovery planning was rejected."))
			return
		}
		a.handleMigrationWorkspaceRequestError(w, r, err, "Migration Recovery planning is unavailable.")
		return
	}
	a.renderMigrationWorkspaceRecoveryReview(w, http.StatusOK, identity, request, plan, csrfFromRequest(r), "")
}

func (a *App) migrationWorkspaceRecoveryApply(w http.ResponseWriter, r *http.Request) {
	session, client, identity, _, ok := a.migrationWorkspaceRecoveryRequest(w, r, true)
	if !ok {
		return
	}
	discovery, err := client.AdminMigrationRecoveryDiscovery(r.Context(), session)
	if err != nil {
		a.handleMigrationWorkspaceRequestError(w, r, err, "Migration Recovery discovery is unavailable.")
		return
	}
	request, err := parseMigrationWorkspaceRecoveryForm(r, discovery)
	if err != nil {
		a.renderMigrationWorkspaceRecoveryPage(w, http.StatusBadRequest, identity, discovery, false, "", csrfFromRequest(r), err.Error())
		return
	}
	plan, err := client.AdminMigrationRecoveryPlan(r.Context(), session, request)
	if err != nil {
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusBadRequest && plan.SchemaVersion != "" {
			a.renderMigrationWorkspaceRecoveryReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), apiMessage(err, "Migration Recovery state changed and must be reviewed again."))
			return
		}
		a.handleMigrationWorkspaceRequestError(w, r, err, "Migration Recovery planning is unavailable.")
		return
	}
	if submitted := strings.TrimSpace(r.FormValue("plan_fingerprint")); submitted == "" || submitted != plan.PlanFingerprint {
		a.renderMigrationWorkspaceRecoveryReview(w, http.StatusConflict, identity, request, plan, csrfFromRequest(r), "This retained Migration Recovery state changed since it was reviewed. Review the current state again before continuing.")
		return
	}
	if strings.TrimSpace(r.FormValue("finalize_confirmation")) != "FINALIZE" {
		a.renderMigrationWorkspaceRecoveryReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), "Type FINALIZE exactly to confirm removal of this reviewed retained recovery transaction after Agent validation.")
		return
	}
	if plan.Requirements == nil || !plan.Requirements.FinalizeConfirmationRequired {
		a.renderMigrationWorkspaceRecoveryReview(w, http.StatusBadGateway, identity, request, plan, csrfFromRequest(r), "Migration Recovery did not return its required finalization confirmation contract.")
		return
	}
	result, err := client.AdminMigrationRecoveryApply(r.Context(), session, api.AdminMigrationRecoveryApplyRequest{
		PlanFingerprint:   plan.PlanFingerprint,
		Transaction:       request.Transaction,
		FinalizeConfirmed: true,
	})
	if err != nil {
		a.renderMigrationWorkspaceRecoveryReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), apiMessage(err, "Could not start Migration Recovery finalization."))
		return
	}
	if !result.OK || result.Operation == nil || result.Operation.OperationType != "migration_recovery" {
		a.renderMigrationWorkspaceRecoveryReview(w, http.StatusBadGateway, identity, request, plan, csrfFromRequest(r), "Migration Recovery did not return a valid persistent operation.")
		return
	}
	http.Redirect(w, r, "/workspace/migration/progress/"+result.Operation.OperationID, http.StatusSeeOther)
}

func (a *App) migrationWorkspaceRecoveryResolve(w http.ResponseWriter, r *http.Request) {
	session, client, identity, importNeedsAttention, ok := a.migrationWorkspaceRecoveryRequest(w, r, true)
	if !ok {
		return
	}
	if !importNeedsAttention {
		http.Error(w, "Server Import does not require resolution", http.StatusConflict)
		return
	}
	discovery, err := client.AdminMigrationRecoveryDiscovery(r.Context(), session)
	if err != nil {
		a.handleMigrationWorkspaceRequestError(w, r, err, "Migration Recovery discovery is unavailable.")
		return
	}
	current, err := client.AdminCurrentMigrationOperation(r.Context(), session)
	if err != nil {
		a.handleMigrationWorkspaceRequestError(w, r, err, "Server Migration operation status is unavailable.")
		return
	}
	if current.Operation == nil || current.Operation.OperationType != "migration_import" || current.Operation.State != "needs_attention" {
		http.Error(w, "Server Import does not require resolution", http.StatusConflict)
		return
	}
	if len(discovery.Transactions) != 0 {
		a.renderMigrationWorkspaceRecoveryPage(w, http.StatusConflict, identity, discovery, true, "", csrfFromRequest(r), "Retained Import recovery state exists and must be reviewed before keeping the current server.")
		return
	}
	if strings.TrimSpace(r.FormValue("operation_id")) != current.Operation.OperationID || r.FormValue("keep_current_state") != "yes" {
		a.renderMigrationWorkspaceRecoveryPage(w, http.StatusBadRequest, identity, discovery, true, current.Operation.OperationID, csrfFromRequest(r), "Confirm keeping the validated current server before continuing.")
		return
	}
	_, err = client.AdminMigrationImportResolve(r.Context(), session, api.AdminMigrationImportResolveRequest{
		OperationID: current.Operation.OperationID, KeepCurrentState: true,
	})
	if err != nil {
		a.renderMigrationWorkspaceRecoveryPage(w, http.StatusConflict, identity, discovery, true, current.Operation.OperationID, csrfFromRequest(r), apiMessage(err, "Failed Server Import could not be resolved safely."))
		return
	}
	http.Redirect(w, r, "/workspace/migration", http.StatusSeeOther)
}

func parseMigrationWorkspaceRecoveryForm(r *http.Request, discovery api.AdminMigrationRecoveryDiscoveryResponse) (api.AdminMigrationRecoveryPlanRequest, error) {
	if err := r.ParseForm(); err != nil {
		return api.AdminMigrationRecoveryPlanRequest{}, errors.New("invalid Migration Recovery request")
	}
	transaction := strings.TrimSpace(r.FormValue("transaction"))
	for _, summary := range discovery.Transactions {
		if summary.Transaction != transaction {
			continue
		}
		if !summary.Finalizable {
			return api.AdminMigrationRecoveryPlanRequest{}, errors.New("this retained migration transaction is not safely finalizable in the current appliance state")
		}
		return api.AdminMigrationRecoveryPlanRequest{Transaction: transaction}, nil
	}
	return api.AdminMigrationRecoveryPlanRequest{}, errors.New("choose a retained recovery transaction currently reported by the Management Agent")
}

func (a *App) renderMigrationWorkspaceRecoveryPage(w http.ResponseWriter, status int, identity api.SessionInfo, discovery api.AdminMigrationRecoveryDiscoveryResponse, importNeedsAttention bool, importOperationID, csrf, errorMessage string) {
	transactions := make([]migrationWorkspaceRecoveryTransactionView, 0, len(discovery.Transactions))
	for _, summary := range discovery.Transactions {
		transactions = append(transactions, migrationWorkspaceRecoveryTransactionView{
			Summary: summary, ModeLabel: migrationWorkspaceRecoveryModeLabel(summary.Mode), PhaseLabel: migrationWorkspaceRecoveryPhaseLabel(summary.Phase),
		})
	}
	a.renderMigrationWorkspace(w, "migration_workspace_recovery.html", status, migrationWorkspaceRecoveryPageData{
		Title:                "Migration Recovery",
		Version:              a.config.Version,
		ManagementAPI:        a.config.ManagementAPI,
		CSRF:                 csrf,
		Identity:             identity,
		Discovery:            discovery,
		Transactions:         transactions,
		ImportNeedsAttention: importNeedsAttention,
		ImportOperationID:    importOperationID,
		CanKeepCurrent:       importNeedsAttention && importOperationID != "" && len(discovery.Transactions) == 0,
		Error:                errorMessage,
	})
}

func (a *App) renderMigrationWorkspaceRecoveryReview(w http.ResponseWriter, status int, identity api.SessionInfo, request api.AdminMigrationRecoveryPlanRequest, plan api.AdminMigrationRecoveryPlanResponse, csrf, errorMessage string) {
	modeLabel := "Retained recovery state"
	phaseLabel := "Unknown"
	if plan.Normalized != nil {
		modeLabel = migrationWorkspaceRecoveryModeLabel(plan.Normalized.Mode)
		phaseLabel = migrationWorkspaceRecoveryPhaseLabel(plan.Normalized.Phase)
	}
	a.renderMigrationWorkspace(w, "migration_workspace_recovery_review.html", status, migrationWorkspaceRecoveryReviewPageData{
		Title:         "Review Migration Recovery",
		Version:       a.config.Version,
		ManagementAPI: a.config.ManagementAPI,
		CSRF:          csrf,
		Identity:      identity,
		Request:       request,
		Plan:          plan,
		ModeLabel:     modeLabel,
		PhaseLabel:    phaseLabel,
		Error:         errorMessage,
	})
}

func migrationWorkspaceRecoveryModeLabel(value string) string {
	switch value {
	case "configured":
		return "Configured server rollback"
	case "configured-attention":
		return "Configured rollback requiring attention"
	case "fresh-unconfigured":
		return "Fresh appliance rollback"
	default:
		return migrationWorkspaceFriendlyToken(value)
	}
}

func migrationWorkspaceRecoveryPhaseLabel(value string) string {
	switch value {
	case "rolled-back":
		return "Rolled back"
	case "critical-rollback":
		return "Critical rollback retained"
	case "rolled-back-fresh":
		return "Fresh Import rolled back"
	default:
		return migrationWorkspaceFriendlyToken(value)
	}
}
