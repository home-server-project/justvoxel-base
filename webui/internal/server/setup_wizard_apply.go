package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

var setupOperationIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type setupExecutionAPI interface {
	AdminSetupApply(ctx context.Context, session string, request api.AdminSetupApplyRequest) (api.AdminSetupApplyResponse, error)
	AdminOperation(ctx context.Context, session, id string) (api.PersistentOperationResponse, error)
	AdminCurrentSetupOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error)
}

type setupProgressPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Operation     api.PersistentOperation
	StateLabel    string
	StageLabel    string
}

func (a *App) registerSetupWizardApplyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /setup/review/apply", a.setupWizardReviewApply)
	mux.HandleFunc("GET /setup/progress/{id}", a.setupWizardProgressPage)
	mux.HandleFunc("GET /api/setup/progress/{id}", a.setupWizardProgressStatus)
}

func (a *App) redirectCurrentSetupOperation(w http.ResponseWriter, r *http.Request, session string, client adminDiscoveryAPI) bool {
	executor, ok := client.(setupExecutionAPI)
	if !ok {
		return false
	}
	response, err := executor.AdminCurrentSetupOperation(r.Context(), session)
	if err != nil || response.Operation == nil {
		return false
	}
	http.Redirect(w, r, "/setup/progress/"+response.Operation.OperationID, http.StatusSeeOther)
	return true
}

func (a *App) setupWizardReviewApply(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists || !setupDraftReadyForReview(draft) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	state, planErr, status, ready := a.setupReviewState(r.Context(), session, client, draft)
	if !ready {
		if errors.Is(planErr, api.ErrUnauthorized) || errors.Is(planErr, api.ErrPasswordChangeRequired) {
			a.handleAdminDiscoveryError(w, r, planErr)
			return
		}
		w.WriteHeader(status)
		a.renderSetupReview(w, identity, state, csrfFromRequest(r), setupReviewErrorMessage(planErr))
		return
	}
	if !state.EULAAccepted {
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupReview(w, identity, state, csrfFromRequest(r), "Accept the Minecraft End User License Agreement before configuring JustVoxel.")
		return
	}
	if submitted := r.FormValue("plan_fingerprint"); submitted == "" || submitted != state.Plan.PlanFingerprint {
		state.EULAAccepted = false
		firstRunSetupReviews.save(a, session, state)
		w.WriteHeader(http.StatusConflict)
		a.renderSetupReview(w, identity, state, csrfFromRequest(r), "Setup changed since it was reviewed. Review the updated configuration before continuing.")
		return
	}

	smbPassword := r.FormValue("smb_password")
	if state.Plan.Requirements.SMBPasswordRequired {
		if smbPassword == "" {
			w.WriteHeader(http.StatusBadRequest)
			a.renderSetupReview(w, identity, state, csrfFromRequest(r), "Enter the SMB password before configuring JustVoxel.")
			return
		}
		if len(smbPassword) > 4096 || strings.ContainsAny(smbPassword, "\r\n") {
			w.WriteHeader(http.StatusBadRequest)
			a.renderSetupReview(w, identity, state, csrfFromRequest(r), "The SMB password is not valid.")
			return
		}
	} else if smbPassword != "" {
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupReview(w, identity, state, csrfFromRequest(r), "An SMB password is not required for this reviewed setup.")
		return
	}

	executor, ok := client.(setupExecutionAPI)
	if !ok {
		http.Error(w, "setup execution is unavailable", http.StatusServiceUnavailable)
		return
	}
	result, err := executor.AdminSetupApply(r.Context(), session, api.AdminSetupApplyRequest{
		PlanFingerprint: state.Plan.PlanFingerprint,
		Request:         state.Request,
		SMBPassword:     smbPassword,
		EULAAccepted:    true,
	})
	smbPassword = ""
	if err != nil {
		if errors.Is(err, api.ErrUnauthorized) || errors.Is(err, api.ErrPasswordChangeRequired) {
			a.handleAdminDiscoveryError(w, r, err)
			return
		}
		message := "Could not start JustVoxel setup."
		if apiMessage, ok := api.ErrorMessage(err); ok {
			message = apiMessage
		}
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupReview(w, identity, state, csrfFromRequest(r), message)
		return
	}
	if !result.OK || result.Operation == nil || !setupOperationIDPattern.MatchString(result.Operation.OperationID) {
		w.WriteHeader(http.StatusBadGateway)
		a.renderSetupReview(w, identity, state, csrfFromRequest(r), "Setup execution did not return a valid operation.")
		return
	}
	http.Redirect(w, r, "/setup/progress/"+result.Operation.OperationID, http.StatusSeeOther)
}

func (a *App) setupWizardProgressPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !setupOperationIDPattern.MatchString(id) {
		http.Error(w, "invalid setup operation", http.StatusBadRequest)
		return
	}
	executor, ok := client.(setupExecutionAPI)
	if !ok {
		http.Error(w, "setup operation tracking is unavailable", http.StatusServiceUnavailable)
		return
	}
	response, err := executor.AdminOperation(r.Context(), session, id)
	if err != nil {
		a.handleSetupProgressError(w, r, err)
		return
	}
	if response.Operation == nil {
		http.Error(w, "setup operation not found", http.StatusNotFound)
		return
	}
	switch response.Operation.State {
	case "succeeded":
		firstRunSetupReviews.delete(a, session)
		firstRunSetupDrafts.delete(a, session)
	case "rolled_back":
		firstRunSetupReviews.delete(a, session)
	}
	a.renderAdminDiscovery(w, "setup_progress.html", setupProgressPageData{
		Title: "Configuring JustVoxel", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrfFromRequest(r), Identity: identity, Operation: *response.Operation,
		StateLabel: setupOperationStateLabel(response.Operation.State),
		StageLabel: setupOperationStageLabel(response.Operation.Stage),
	})
}

func (a *App) setupWizardProgressStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	client, ok := a.api.(adminDiscoveryAPI)
	if !ok {
		http.Error(w, "setup operation tracking is unavailable", http.StatusServiceUnavailable)
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
	id := r.PathValue("id")
	if !setupOperationIDPattern.MatchString(id) {
		http.Error(w, "invalid setup operation", http.StatusBadRequest)
		return
	}
	executor, ok := client.(setupExecutionAPI)
	if !ok {
		http.Error(w, "setup operation tracking is unavailable", http.StatusServiceUnavailable)
		return
	}
	response, err := executor.AdminOperation(r.Context(), session, id)
	if err != nil {
		a.handleSetupProgressAPIError(w, err)
		return
	}
	if response.Operation == nil {
		http.Error(w, "setup operation not found", http.StatusNotFound)
		return
	}
	switch response.Operation.State {
	case "succeeded":
		firstRunSetupReviews.delete(a, session)
		firstRunSetupDrafts.delete(a, session)
	case "rolled_back":
		firstRunSetupReviews.delete(a, session)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "could not encode setup operation", http.StatusInternalServerError)
	}
}

func setupOperationStateLabel(state string) string {
	switch state {
	case "queued":
		return "Queued"
	case "validating":
		return "Validating"
	case "running":
		return "Configuring"
	case "verifying":
		return "Verifying"
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

func setupOperationStageLabel(stage string) string {
	switch stage {
	case "queued":
		return "Waiting to start"
	case "storage_preflight":
		return "Checking storage"
	case "storage_snapshot":
		return "Preparing storage changes"
	case "storage_verified":
		return "Storage ready"
	case "runtime_preflight":
		return "Checking Minecraft configuration"
	case "runtime_config":
		return "Writing Minecraft configuration"
	case "minecraft_verify":
		return "Starting and checking Minecraft"
	case "final_validation":
		return "Final validation"
	case "completed":
		return "Complete"
	case "setup_failed":
		return "Preparing recovery"
	case "runtime_rollback":
		return "Restoring Minecraft configuration"
	case "storage_rollback":
		return "Restoring storage changes"
	case "setup_rolled_back":
		return "Rolled back"
	case "interrupted":
		return "Interrupted"
	default:
		return "Working"
	}
}

func (a *App) handleSetupProgressError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, api.ErrUnauthorized) || errors.Is(err, api.ErrPasswordChangeRequired) {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
		http.Error(w, "setup operation not found", http.StatusNotFound)
		return
	}
	http.Error(w, "setup operation status is unavailable", http.StatusBadGateway)
}

func (a *App) handleSetupProgressAPIError(w http.ResponseWriter, err error) {
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
		http.Error(w, "setup operation not found", http.StatusNotFound)
		return
	}
	http.Error(w, "setup operation status is unavailable", http.StatusBadGateway)
}
