package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const minecraftEULAURL = "https://www.minecraft.net/eula"

var setupPlanFingerprintPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type setupPlanningAPI interface {
	AdminSetupPlan(ctx context.Context, session string, request api.AdminSetupPlanRequest) (api.AdminSetupPlanResponse, error)
}

type setupReviewState struct {
	Request        api.AdminSetupPlanRequest
	Plan           api.AdminSetupPlanResponse
	EULAAccepted   bool
	DraftUpdatedAt time.Time
	SetupMode      string
	ServerType     string
}

type setupReviewStore struct {
	mu      sync.Mutex
	reviews map[setupDraftKey]setupReviewState
}

var firstRunSetupReviews = setupReviewStore{reviews: make(map[setupDraftKey]setupReviewState)}

type setupReviewPageData struct {
	Title              string
	SetupMode          string
	ServerTypeLabel    string
	Version            string
	ManagementAPI      string
	CSRF               string
	Identity           api.SessionInfo
	Plan               api.AdminSetupPlanResponse
	Error              string
	EULAAccepted       bool
	EULAURL            string
	StorageLabel       string
	StorageSize        string
	BackupLabel        string
	BackupSize         string
	VersionLabel       string
	ShortPlanReference string
}

func (a *App) registerSetupWizardReviewRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /setup/review", a.setupWizardReviewPage)
	mux.HandleFunc("GET /setup/review/configuration", a.setupWizardReviewConfiguration)
	mux.HandleFunc("POST /setup/review/eula", a.setupWizardReviewEULA)
	mux.HandleFunc("POST /setup/review/back", a.setupWizardReviewBack)
	mux.HandleFunc("POST /setup/review/cancel", a.setupWizardReviewCancel)
	a.registerSetupWizardApplyRoutes(mux)
}

func (a *App) setupWizardReviewPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.setupWizardRequest(w, r, false)
	if !ok {
		return
	}
	if a.redirectCurrentSetupOperation(w, r, session, client) {
		return
	}
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists || !setupDraftReadyForReview(draft) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	state, message, status, ok := a.setupReviewState(r.Context(), session, client, draft)
	if !ok {
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			a.handleAdminDiscoveryError(w, r, message)
			return
		}
		w.WriteHeader(status)
		a.renderSetupReview(w, identity, state, csrfFromRequest(r), setupReviewErrorMessage(message))
		return
	}
	a.renderSetupReview(w, identity, state, csrfFromRequest(r), "")
}

func (a *App) setupWizardReviewConfiguration(w http.ResponseWriter, r *http.Request) {
	session, client, _, ok := a.setupWizardRequest(w, r, false)
	if !ok {
		return
	}
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists || !setupDraftReadyForReview(draft) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	state, err, status, ready := a.setupReviewState(r.Context(), session, client, draft)
	if !ready {
		http.Error(w, setupReviewErrorMessage(err), status)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"justvoxel-setup.txt\"")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(setupConfigurationSnapshot(state.Plan)))
}

func setupConfigurationSnapshot(plan api.AdminSetupPlanResponse) string {
	var b strings.Builder
	b.WriteString("JustVoxel setup configuration\n")
	b.WriteString("Validated plan: " + shortSetupPlanReference(plan.PlanFingerprint) + "\n\n")

	b.WriteString("Server\n")
	b.WriteString("Welcome message: " + plan.Normalized.Server.MOTD + "\n")
	b.WriteString("Maximum players: " + strconv.Itoa(plan.Normalized.Server.MaxPlayers) + "\n")
	if plan.Normalized.Server.BedrockEnabled {
		b.WriteString("Bedrock cross-play: Enabled\n")
	} else {
		b.WriteString("Bedrock cross-play: Disabled\n")
	}
	b.WriteString("Timezone: " + plan.Normalized.Server.Timezone + "\n\n")

	b.WriteString("Minecraft\n")
	if draft.Minecraft.ServerType != "" {
		b.WriteString("Server software: " + setupServerTypeLabel(draft.Minecraft.ServerType) + "\n")
	}
	b.WriteString("Game memory: " + plan.Normalized.Minecraft.JavaMemory + "\n")
	b.WriteString("Maximum memory: " + plan.Normalized.Minecraft.ContainerMemory + "\n")
	b.WriteString("Java port: " + strconv.Itoa(plan.Normalized.Minecraft.JavaPort) + "/TCP\n")
	if plan.Normalized.Server.BedrockEnabled {
		b.WriteString("Bedrock port: " + strconv.Itoa(plan.Normalized.Minecraft.BedrockPort) + "/UDP\n")
	}
	b.WriteString("Container tag: " + plan.Normalized.Minecraft.ImageTag + "\n")
	b.WriteString("Minecraft version: " + plan.Normalized.Minecraft.Version + "\n\n")

	b.WriteString("Storage\n")
	b.WriteString("Minecraft data: " + plan.Normalized.Storage.Path + "\n")
	if plan.Normalized.Storage.Device != "" {
		b.WriteString("Device: " + plan.Normalized.Storage.Device + "\n")
	}
	if plan.Normalized.Storage.MountPoint != "" {
		b.WriteString("Mount point: " + plan.Normalized.Storage.MountPoint + "\n")
	}
	b.WriteString("\nBackups\n")
	if plan.Normalized.Backups.Automatic {
		b.WriteString("Automatic backups: Enabled\n")
		b.WriteString("Daily time: " + plan.Normalized.Backups.DailyTime + "\n")
	} else {
		b.WriteString("Automatic backups: Disabled\n")
	}
	b.WriteString("Backups to keep: " + strconv.Itoa(plan.Normalized.Backups.Keep) + "\n")
	b.WriteString("Backup directory: " + plan.Normalized.Backups.Path + "\n")
	if plan.Normalized.Backups.Source != "" {
		b.WriteString("Network source: " + plan.Normalized.Backups.Source + "\n")
	}
	if plan.Normalized.Backups.MountPoint != "" {
		b.WriteString("Mount point: " + plan.Normalized.Backups.MountPoint + "\n")
	}
	if len(plan.Warnings) > 0 {
		b.WriteString("\nWarnings\n")
		for _, warning := range plan.Warnings {
			b.WriteString("- " + warning.Message + "\n")
		}
	}
	b.WriteString("\nThis snapshot contains configuration values only. Passwords and other secrets are never included.\n")
	return b.String()
}
func (a *App) setupWizardReviewEULA(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	if a.redirectCurrentSetupOperation(w, r, session, client) {
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
	if submitted := r.FormValue("plan_fingerprint"); submitted == "" || submitted != state.Plan.PlanFingerprint {
		state.EULAAccepted = false
		firstRunSetupReviews.save(a, session, state)
		w.WriteHeader(http.StatusConflict)
		a.renderSetupReview(w, identity, state, csrfFromRequest(r), "Setup changed since it was reviewed. Review the updated configuration before continuing.")
		return
	}
	if r.FormValue("eula_accepted") != "on" {
		state.EULAAccepted = false
		firstRunSetupReviews.save(a, session, state)
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupReview(w, identity, state, csrfFromRequest(r), "Accept the Minecraft End User License Agreement before this setup can be marked ready.")
		return
	}
	state.EULAAccepted = true
	firstRunSetupReviews.save(a, session, state)
	a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "Minecraft EULA acceptance recorded", draft)
	http.Redirect(w, r, "/setup/review", http.StatusSeeOther)
}

func (a *App) setupWizardReviewBack(w http.ResponseWriter, r *http.Request) {
	session, client, _, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	if a.redirectCurrentSetupOperation(w, r, session, client) {
		return
	}
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists || !draft.Started {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	firstRunSetupReviews.delete(a, session)
	if draft.Mode == "recommended" {
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "user returned from recommended Review to setup choice", draft)
		firstRunSetupDrafts.delete(a, session)
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	draft.CurrentStep = 5
	firstRunSetupDrafts.save(a, session, draft)
	a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "user returned from Review to Backups", draft)
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

func (a *App) setupWizardReviewCancel(w http.ResponseWriter, r *http.Request) {
	session, client, _, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	if a.redirectCurrentSetupOperation(w, r, session, client) {
		return
	}
	if draft, exists := firstRunSetupDrafts.get(a, session); exists {
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "WebUI setup cancelled from Review", draft)
	}
	firstRunSetupReviews.delete(a, session)
	firstRunSetupDrafts.delete(a, session)
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

func (a *App) setupReviewState(ctx context.Context, session string, client adminDiscoveryAPI, draft setupDraft) (setupReviewState, error, int, bool) {
	request, err := setupPlanRequestFromDraft(draft)
	if err != nil {
		return setupReviewState{}, err, http.StatusBadRequest, false
	}
	if cached, ok := firstRunSetupReviews.get(a, session); ok && cached.DraftUpdatedAt.Equal(draft.UpdatedAt) && cached.Request == request && validSetupPlanFingerprint(cached.Plan.PlanFingerprint) {
		return cached, nil, http.StatusOK, true
	}
	planner, ok := client.(setupPlanningAPI)
	if !ok {
		return setupReviewState{}, errors.New("setup planning is unavailable"), http.StatusServiceUnavailable, false
	}
	plan, err := planner.AdminSetupPlan(ctx, session, request)
	state := setupReviewState{
		Request: request, Plan: plan, DraftUpdatedAt: draft.UpdatedAt,
		SetupMode: draft.Mode, ServerType: draft.Minecraft.ServerType,
	}
	if err != nil {
		if errors.Is(err, api.ErrUnauthorized) {
			return state, err, http.StatusUnauthorized, false
		}
		if errors.Is(err, api.ErrPasswordChangeRequired) {
			return state, err, http.StatusForbidden, false
		}
		var responseErr *api.ResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusBadRequest {
			return state, err, http.StatusBadRequest, false
		}
		return state, err, http.StatusBadGateway, false
	}
	if !plan.OK {
		return state, errors.New("setup planning did not return a validated configuration"), http.StatusBadRequest, false
	}
	if !validSetupPlanFingerprint(plan.PlanFingerprint) {
		return state, errors.New("setup planning did not return a valid plan identity"), http.StatusBadGateway, false
	}
	firstRunSetupReviews.save(a, session, state)
	return state, nil, http.StatusOK, true
}

func validSetupPlanFingerprint(value string) bool {
	return setupPlanFingerprintPattern.MatchString(value)
}

func shortSetupPlanReference(value string) string {
	if !validSetupPlanFingerprint(value) {
		return ""
	}
	hexValue := strings.TrimPrefix(value, "sha256:")
	return hexValue[:8] + "…"
}

func setupDraftReadyForReview(draft setupDraft) bool {
	return draft.Started && draft.CurrentStep == 6 && draft.Server.Complete && draft.Minecraft.ResourcesComplete && draft.Minecraft.Complete && draft.Storage.Complete && draft.Backups.Complete
}

func setupPlanRequestFromDraft(draft setupDraft) (api.AdminSetupPlanRequest, error) {
	maxPlayers, err := strconv.Atoi(draft.Server.MaxPlayers)
	if err != nil || maxPlayers < 1 {
		return api.AdminSetupPlanRequest{}, errors.New("Maximum players must be a positive number.")
	}
	javaPort, err := strconv.Atoi(draft.Minecraft.JavaPort)
	if err != nil {
		return api.AdminSetupPlanRequest{}, errors.New("Minecraft Java port is invalid.")
	}
	bedrockPort, err := strconv.Atoi(draft.Minecraft.BedrockPort)
	if err != nil {
		return api.AdminSetupPlanRequest{}, errors.New("Bedrock UDP port is invalid.")
	}
	backupKeep, err := strconv.Atoi(draft.Backups.Keep)
	if err != nil || backupKeep < 1 {
		return api.AdminSetupPlanRequest{}, errors.New("Backup retention must be a positive number.")
	}
	return api.AdminSetupPlanRequest{
		DiagnosticSessionID: draft.DiagnosticSessionID,
		Server: api.AdminSetupPlanServerRequest{
			MOTD: draft.Server.MOTD, MaxPlayers: maxPlayers,
			BedrockEnabled: draft.Server.BedrockEnabled, Timezone: draft.Server.Timezone,
		},
		Minecraft: api.AdminSetupPlanMinecraftRequest{
			JavaMemory: draft.Minecraft.JavaMemory, ContainerMemory: draft.Minecraft.ContainerMemory,
			JavaPort: javaPort, BedrockPort: bedrockPort, ImageTag: draft.Minecraft.ImageTag,
			VersionPolicy: draft.Minecraft.VersionPolicy, Version: draft.Minecraft.Version,
		},
		Storage: api.AdminSetupPlanStorageRequest{
			Type: draft.Storage.Type, Path: draft.Storage.Path, Device: draft.Storage.Device, MountPoint: draft.Storage.MountPoint,
		},
		Backups: api.AdminSetupPlanBackupsRequest{
			Automatic: draft.Backups.Automatic, DailyTime: draft.Backups.DailyTime, Keep: backupKeep,
			Type: draft.Backups.Type, Path: draft.Backups.Path, Device: draft.Backups.Device,
			MountPoint: draft.Backups.MountPoint, Source: draft.Backups.Source,
			Username: draft.Backups.Username, Domain: draft.Backups.Domain,
		},
	}, nil
}

func setupReviewErrorMessage(err error) string {
	if err == nil {
		return "Setup validation is temporarily unavailable."
	}
	if message, ok := api.ErrorMessage(err); ok {
		return message
	}
	if err.Error() == "setup planning is unavailable" || err.Error() == "setup planning did not return a valid plan identity" {
		return "Setup validation is temporarily unavailable."
	}
	return err.Error()
}

func (a *App) renderSetupReview(w http.ResponseWriter, identity api.SessionInfo, state setupReviewState, csrf, errorMessage string) {
	plan := state.Plan
	data := setupReviewPageData{
		Title: "Review setup", SetupMode: state.SetupMode, ServerTypeLabel: setupServerTypeLabel(state.ServerType),
		Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrf, Identity: identity, Plan: plan, Error: errorMessage,
		EULAAccepted: state.EULAAccepted, EULAURL: minecraftEULAURL,
		StorageLabel:       setupStorageTypeLabel(plan.Normalized.Storage.Type),
		BackupLabel:        setupBackupTypeLabel(plan.Normalized.Backups.Type),
		VersionLabel:       setupVersionPolicyLabel(plan.Normalized.Minecraft.RequestedVersionPolicy),
		ShortPlanReference: shortSetupPlanReference(plan.PlanFingerprint),
	}
	if plan.Normalized.Storage.SizeBytes > 0 {
		data.StorageSize = humanBytes(plan.Normalized.Storage.SizeBytes)
	}
	if plan.Normalized.Backups.SizeBytes > 0 {
		data.BackupSize = humanBytes(plan.Normalized.Backups.SizeBytes)
	}
	a.renderAdminDiscovery(w, "setup_review.html", data)
}

func setupServerTypeLabel(value string) string {
	switch value {
	case "paper", "":
		return "Paper"
	case "purpur":
		return "Purpur"
	case "vanilla":
		return "Vanilla"
	default:
		return value
	}
}

func setupStorageTypeLabel(value string) string {
	switch value {
	case "system":
		return "JustVoxel system storage"
	case "partition":
		return "Existing local filesystem"
	default:
		return value
	}
}

func setupBackupTypeLabel(value string) string {
	switch value {
	case "system":
		return "JustVoxel system storage"
	case "partition":
		return "Existing local filesystem"
	case "nfs":
		return "NFS share"
	case "smb":
		return "SMB / CIFS share"
	default:
		return value
	}
}

func setupVersionPolicyLabel(value string) string {
	switch value {
	case "recommended":
		return "Recommended version"
	case "latest":
		return "Always newest version"
	case "pinned":
		return "Specific version"
	default:
		return value
	}
}

func (s *setupReviewStore) pruneLocked(now time.Time) {
	for key, state := range s.reviews {
		if now.Sub(state.DraftUpdatedAt) > setupDraftLifetime {
			delete(s.reviews, key)
		}
	}
}

func (s *setupReviewStore) get(app *App, session string) (setupReviewState, bool) {
	now := time.Now()
	key := setupKey(app, session)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	state, ok := s.reviews[key]
	return state, ok
}

func (s *setupReviewStore) save(app *App, session string, state setupReviewState) {
	now := time.Now()
	key := setupKey(app, session)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	s.reviews[key] = state
}

func (s *setupReviewStore) delete(app *App, session string) {
	key := setupKey(app, session)
	s.mu.Lock()
	delete(s.reviews, key)
	s.mu.Unlock()
}
