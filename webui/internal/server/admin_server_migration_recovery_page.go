package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminServerMigrationRecoveryAPI interface {
	adminServerMigrationAPI
	AdminMigrationRecoveryPlan(ctx context.Context, session string, request api.AdminMigrationRecoveryPlanRequest) (api.AdminMigrationRecoveryPlanResponse, error)
	AdminMigrationRecoveryApply(ctx context.Context, session string, request api.AdminMigrationRecoveryApplyRequest) (api.AdminMigrationApplyResponse, error)
}

type serverMigrationRecoveryTransactionView struct {
	Summary    api.AdminMigrationRecoverySummary
	ModeLabel  string
	PhaseLabel string
}

type serverMigrationRecoveryPageData struct {
	Title                string
	Version              string
	ManagementAPI        string
	CSRF                 string
	Identity             api.SessionInfo
	Discovery            api.AdminMigrationRecoveryDiscoveryResponse
	Transactions         []serverMigrationRecoveryTransactionView
	ImportNeedsAttention bool
	Error                string
}

type serverMigrationRecoveryReviewPageData struct {
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

func (a *App) registerAdminServerRecoveryPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings/server-migration/recovery", a.serverMigrationRecoveryPage)
	mux.HandleFunc("POST /settings/server-migration/recovery/review", a.serverMigrationRecoveryReview)
	mux.HandleFunc("POST /settings/server-migration/recovery/apply", a.serverMigrationRecoveryApply)
}

func (a *App) serverMigrationRecoveryRequest(w http.ResponseWriter, r *http.Request, requireCSRF bool) (string, adminServerMigrationRecoveryAPI, api.SessionInfo, bool, bool) {
	if requireCSRF && !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false, false
	}
	session, _, identity, ok := a.serverMigrationRequest(w, r)
	if !ok {
		return "", nil, api.SessionInfo{}, false, false
	}
	client, ok := a.api.(adminServerMigrationRecoveryAPI)
	if !ok {
		http.Error(w, "Migration Recovery management is unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false, false
	}
	current, err := client.AdminCurrentMigrationOperation(r.Context(), session)
	if err != nil {
		a.handleServerMigrationRequestError(w, r, err, "Server Migration operation status is unavailable.")
		return "", nil, api.SessionInfo{}, false, false
	}
	if current.Operation != nil {
		if current.Operation.OperationType == "migration_import" && current.Operation.State == "needs_attention" {
			return session, client, identity, true, true
		}
		if !serverMigrationOperationType(current.Operation.OperationType) {
			http.Error(w, "Server Migration operation status is invalid", http.StatusBadGateway)
			return "", nil, api.SessionInfo{}, false, false
		}
		http.Redirect(w, r, "/settings/server-migration/progress/"+current.Operation.OperationID, http.StatusSeeOther)
		return "", nil, api.SessionInfo{}, false, false
	}
	return session, client, identity, false, true
}

func (a *App) serverMigrationRecoveryPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, importNeedsAttention, ok := a.serverMigrationRecoveryRequest(w, r, false)
	if !ok {
		return
	}
	discovery, err := client.AdminMigrationRecoveryDiscovery(r.Context(), session)
	if err != nil {
		a.handleServerMigrationRequestError(w, r, err, "Migration Recovery discovery is unavailable.")
		return
	}
	a.renderServerMigrationRecoveryPage(w, http.StatusOK, identity, discovery, importNeedsAttention, csrfFromRequest(r), "")
}

func (a *App) serverMigrationRecoveryReview(w http.ResponseWriter, r *http.Request) {
	session, client, identity, _, ok := a.serverMigrationRecoveryRequest(w, r, true)
	if !ok {
		return
	}
	discovery, err := client.AdminMigrationRecoveryDiscovery(r.Context(), session)
	if err != nil {
		a.handleServerMigrationRequestError(w, r, err, "Migration Recovery discovery is unavailable.")
		return
	}
	request, err := parseServerMigrationRecoveryForm(r, discovery)
	if err != nil {
		a.renderServerMigrationRecoveryPage(w, http.StatusBadRequest, identity, discovery, false, csrfFromRequest(r), err.Error())
		return
	}
	plan, err := client.AdminMigrationRecoveryPlan(r.Context(), session, request)
	if err != nil {
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusBadRequest && plan.SchemaVersion != "" {
			a.renderServerMigrationRecoveryReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), apiMessage(err, "Migration Recovery planning was rejected."))
			return
		}
		a.handleServerMigrationRequestError(w, r, err, "Migration Recovery planning is unavailable.")
		return
	}
	a.renderServerMigrationRecoveryReview(w, http.StatusOK, identity, request, plan, csrfFromRequest(r), "")
}

func (a *App) serverMigrationRecoveryApply(w http.ResponseWriter, r *http.Request) {
	session, client, identity, _, ok := a.serverMigrationRecoveryRequest(w, r, true)
	if !ok {
		return
	}
	discovery, err := client.AdminMigrationRecoveryDiscovery(r.Context(), session)
	if err != nil {
		a.handleServerMigrationRequestError(w, r, err, "Migration Recovery discovery is unavailable.")
		return
	}
	request, err := parseServerMigrationRecoveryForm(r, discovery)
	if err != nil {
		a.renderServerMigrationRecoveryPage(w, http.StatusBadRequest, identity, discovery, false, csrfFromRequest(r), err.Error())
		return
	}
	plan, err := client.AdminMigrationRecoveryPlan(r.Context(), session, request)
	if err != nil {
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusBadRequest && plan.SchemaVersion != "" {
			a.renderServerMigrationRecoveryReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), apiMessage(err, "Migration Recovery state changed and must be reviewed again."))
			return
		}
		a.handleServerMigrationRequestError(w, r, err, "Migration Recovery planning is unavailable.")
		return
	}
	if submitted := strings.TrimSpace(r.FormValue("plan_fingerprint")); submitted == "" || submitted != plan.PlanFingerprint {
		a.renderServerMigrationRecoveryReview(w, http.StatusConflict, identity, request, plan, csrfFromRequest(r), "This retained Migration Recovery state changed since it was reviewed. Review the current state again before continuing.")
		return
	}
	if strings.TrimSpace(r.FormValue("finalize_confirmation")) != "FINALIZE" {
		a.renderServerMigrationRecoveryReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), "Type FINALIZE exactly to confirm removal of this reviewed retained recovery transaction after Agent validation.")
		return
	}
	if plan.Requirements == nil || !plan.Requirements.FinalizeConfirmationRequired {
		a.renderServerMigrationRecoveryReview(w, http.StatusBadGateway, identity, request, plan, csrfFromRequest(r), "Migration Recovery did not return its required finalization confirmation contract.")
		return
	}
	result, err := client.AdminMigrationRecoveryApply(r.Context(), session, api.AdminMigrationRecoveryApplyRequest{
		PlanFingerprint:   plan.PlanFingerprint,
		Transaction:       request.Transaction,
		FinalizeConfirmed: true,
	})
	if err != nil {
		a.renderServerMigrationRecoveryReview(w, http.StatusBadRequest, identity, request, plan, csrfFromRequest(r), apiMessage(err, "Could not start Migration Recovery finalization."))
		return
	}
	if !result.OK || result.Operation == nil || result.Operation.OperationType != "migration_recovery" {
		a.renderServerMigrationRecoveryReview(w, http.StatusBadGateway, identity, request, plan, csrfFromRequest(r), "Migration Recovery did not return a valid persistent operation.")
		return
	}
	http.Redirect(w, r, "/settings/server-migration/progress/"+result.Operation.OperationID, http.StatusSeeOther)
}

func parseServerMigrationRecoveryForm(r *http.Request, discovery api.AdminMigrationRecoveryDiscoveryResponse) (api.AdminMigrationRecoveryPlanRequest, error) {
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

func (a *App) renderServerMigrationRecoveryPage(w http.ResponseWriter, status int, identity api.SessionInfo, discovery api.AdminMigrationRecoveryDiscoveryResponse, importNeedsAttention bool, csrf, errorMessage string) {
	transactions := make([]serverMigrationRecoveryTransactionView, 0, len(discovery.Transactions))
	for _, summary := range discovery.Transactions {
		transactions = append(transactions, serverMigrationRecoveryTransactionView{
			Summary: summary, ModeLabel: serverMigrationRecoveryModeLabel(summary.Mode), PhaseLabel: serverMigrationRecoveryPhaseLabel(summary.Phase),
		})
	}
	a.renderServerMigration(w, "server_migration_recovery.html", status, serverMigrationRecoveryPageData{
		Title:                "Migration Recovery",
		Version:              a.config.Version,
		ManagementAPI:        a.config.ManagementAPI,
		CSRF:                 csrf,
		Identity:             identity,
		Discovery:            discovery,
		Transactions:         transactions,
		ImportNeedsAttention: importNeedsAttention,
		Error:                errorMessage,
	})
}

func (a *App) renderServerMigrationRecoveryReview(w http.ResponseWriter, status int, identity api.SessionInfo, request api.AdminMigrationRecoveryPlanRequest, plan api.AdminMigrationRecoveryPlanResponse, csrf, errorMessage string) {
	modeLabel := "Retained recovery state"
	phaseLabel := "Unknown"
	if plan.Normalized != nil {
		modeLabel = serverMigrationRecoveryModeLabel(plan.Normalized.Mode)
		phaseLabel = serverMigrationRecoveryPhaseLabel(plan.Normalized.Phase)
	}
	a.renderServerMigration(w, "server_migration_recovery_review.html", status, serverMigrationRecoveryReviewPageData{
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

func serverMigrationRecoveryModeLabel(value string) string {
	switch value {
	case "configured":
		return "Configured server rollback"
	case "configured-attention":
		return "Configured rollback requiring attention"
	case "fresh-unconfigured":
		return "Fresh appliance rollback"
	default:
		return friendlyMigrationToken(value)
	}
}

func serverMigrationRecoveryPhaseLabel(value string) string {
	switch value {
	case "rolled-back":
		return "Rolled back"
	case "critical-rollback":
		return "Critical rollback retained"
	case "rolled-back-fresh":
		return "Fresh Import rolled back"
	default:
		return friendlyMigrationToken(value)
	}
}
