package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const setupDraftLifetime = 24 * time.Hour

const (
	setupServerStep = iota + 1
	setupCrossplayStep
	setupMemoryStep
	setupStorageStep
	setupBackupsStep
	setupReviewStep
)

var setupMemoryPattern = regexp.MustCompile(`^([1-9][0-9]*)([mMgG])$`)
var setupImageTagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var setupVersionPattern = regexp.MustCompile(`^[0-9A-Za-z._-]+$`)
var setupTimezonePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+/-]*$`)

type setupDraftKey struct {
	app     *App
	session [32]byte
}

type setupServerDraft struct {
	MOTD           string
	MOTDAutomatic  bool
	MaxPlayers     string
	BedrockEnabled bool
	Timezone       string
	Complete       bool
}

type setupMinecraftDraft struct {
	GameMode            string
	ServerType          string
	JavaMemory          string
	ContainerMemory     string
	JavaPort            string
	BedrockPort         string
	ImageTag            string
	VersionPolicy       string
	Version             string
	ConnectionsComplete bool
	ResourcesComplete   bool
	Complete            bool
}

type setupDraft struct {
	Started             bool
	Mode                string
	CurrentStep         int
	HighestStep         int
	DiagnosticSessionID string
	Server              setupServerDraft
	Minecraft           setupMinecraftDraft
	Storage             setupStorageDraft
	Backups             setupBackupDraft
	Defaults            api.AdminSetupDefaults
	Inventory           api.AdminStorageDiscovery
	UpdatedAt           time.Time
}

type setupDraftStore struct {
	mu     sync.Mutex
	drafts map[setupDraftKey]setupDraft
}

var firstRunSetupDrafts = setupDraftStore{drafts: make(map[setupDraftKey]setupDraft)}

type setupWizardStepView struct {
	Number      int
	Name        string
	Description string
	Active      bool
	Complete    bool
	Visited     bool
}

type setupWizardPageData struct {
	Title                    string
	Version                  string
	ManagementAPI            string
	CSRF                     string
	Identity                 api.SessionInfo
	Started                  bool
	CurrentStep              int
	Current                  setupWizardStepView
	Steps                    []setupWizardStepView
	CanBack                  bool
	CanNext                  bool
	Error                    string
	Server                   setupServerDraft
	Minecraft                setupMinecraftDraft
	Storage                  setupStorageDraft
	Backups                  setupBackupDraft
	Filesystems              []setupFilesystemView
	StorageDisks             []setupStorageDiskView
	BackupDisks              []setupStorageDiskView
	ExternalStorageCount     int
	SameDiskWarning          string
	Defaults                 api.AdminSetupDefaults
	SystemMemory             string
	SystemReserveMinimum     string
	SystemReserveRecommended string
}

var setupWizardSteps = []setupWizardStepView{
	{Number: setupServerStep, Name: "Server", Description: "Choose server software, welcome message, game mode and player limit."},
	{Number: setupCrossplayStep, Name: "Cross-play", Description: "Choose Bedrock cross-play, connection ports, container updates and Minecraft version."},
	{Number: setupMemoryStep, Name: "Memory", Description: "Choose how much system memory Minecraft may use."},
	{Number: setupStorageStep, Name: "Storage", Description: "Choose where Minecraft worlds, configuration and server data will live."},
	{Number: setupBackupsStep, Name: "Backups", Description: "Choose backup location, retention and automatic backup schedule."},
	{Number: setupReviewStep, Name: "Review", Description: "Review the final setup plan and accept the Minecraft EULA before anything is applied."},
}

func (a *App) registerSetupWizardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /setup", a.setupWizardPage)
	mux.HandleFunc("POST /setup/start", a.setupWizardStart)
	mux.HandleFunc("POST /setup/recommended", a.setupWizardRecommended)
	mux.HandleFunc("POST /setup/server", a.setupWizardSaveServer)
	mux.HandleFunc("POST /setup/connections", a.setupWizardSaveConnections)
	mux.HandleFunc("POST /setup/resources", a.setupWizardSaveResources)
	mux.HandleFunc("GET /setup/version-preview", a.setupWizardVersionPreview)
	mux.HandleFunc("POST /setup/navigate", a.setupWizardNavigate)
	mux.HandleFunc("POST /setup/cancel", a.setupWizardCancel)
	a.registerSetupWizardStorageRoutes(mux)
}

type setupVersionPreviewAPI interface {
	AdminSetupVersionPreview(context.Context, string, string, string, bool, string) (api.AdminVersionStatus, error)
}

func (a *App) setupWizardVersionPreview(w http.ResponseWriter, r *http.Request) {
	session, _, _, ok := a.setupWizardRequest(w, r, false)
	if !ok {
		return
	}
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists || !draft.Started || draft.CurrentStep != setupCrossplayStep {
		http.Error(w, "Version setup is unavailable", http.StatusConflict)
		return
	}
	client, ok := a.api.(setupVersionPreviewAPI)
	if !ok {
		http.Error(w, "Version information is unavailable", http.StatusServiceUnavailable)
		return
	}
	status, err := client.AdminSetupVersionPreview(r.Context(), session, r.URL.Query().Get("policy"), r.URL.Query().Get("version"), r.URL.Query().Get("bedrock_enabled") == "true", draft.Minecraft.ServerType)
	if err != nil {
		http.Error(w, "Version information is unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(status)
}

func (a *App) setupWizardPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.setupWizardRequest(w, r, false)
	if !ok {
		return
	}
	if a.redirectCurrentSetupOperation(w, r, session, client) {
		return
	}
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists {
		draft = setupDraft{}
	} else if draft.Started && draft.CurrentStep == setupReviewStep {
		if setupDraftReadyForReview(draft) {
			http.Redirect(w, r, "/setup/review", http.StatusSeeOther)
			return
		}
		// A revisited form may have invalidated a previously visited Review.
		switch {
		case !draft.Server.Complete:
			draft.CurrentStep = setupServerStep
		case !draft.Minecraft.ConnectionsComplete:
			draft.CurrentStep = setupCrossplayStep
		case !draft.Minecraft.ResourcesComplete || !draft.Minecraft.Complete:
			draft.CurrentStep = setupMemoryStep
		case !draft.Storage.Complete:
			draft.CurrentStep = setupStorageStep
		default:
			draft.CurrentStep = setupBackupsStep
		}
		firstRunSetupDrafts.save(a, session, draft)
	} else if draft.Started && (draft.CurrentStep == setupStorageStep || draft.CurrentStep == setupBackupsStep) {
		storage, err := client.AdminStorage(r.Context(), session)
		if err != nil {
			a.handleAdminDiscoveryError(w, r, err)
			return
		}
		draft.Inventory = storage
		firstRunSetupDrafts.save(a, session, draft)
	}
	a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), "")
}

func (a *App) setupWizardStart(w http.ResponseWriter, r *http.Request) {
	session, client, _, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	if a.redirectCurrentSetupOperation(w, r, session, client) {
		return
	}
	diagnosticID := a.beginSetupDiagnosticBestEffort(r.Context(), client, session)
	defaults, err := client.AdminSetupDefaults(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	storage, err := client.AdminStorage(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	firstRunSetupReviews.delete(a, session)
	firstRunSetupDrafts.start(a, session, normalizedSetupDefaults(defaults), storage)
	if draft, exists := firstRunSetupDrafts.get(a, session); exists {
		draft.Mode = "advanced"
		draft.DiagnosticSessionID = diagnosticID
		firstRunSetupDrafts.save(a, session, draft)
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "initial WebUI setup defaults loaded", draft)
	}
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

func (a *App) setupWizardRecommended(w http.ResponseWriter, r *http.Request) {
	session, client, _, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	serverType := strings.ToLower(strings.TrimSpace(r.FormValue("server_type")))
	if !validSetupServerType(serverType) {
		http.Error(w, "selected Minecraft server type is not available yet", http.StatusBadRequest)
		return
	}

	diagnosticID := a.beginSetupDiagnosticBestEffort(r.Context(), client, session)
	defaults, err := client.AdminSetupDefaults(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	storage, err := client.AdminStorage(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}

	firstRunSetupReviews.delete(a, session)
	firstRunSetupDrafts.start(a, session, normalizedSetupDefaults(defaults), storage)
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists {
		http.Error(w, "could not create setup draft", http.StatusInternalServerError)
		return
	}

	draft.Mode = "recommended"
	draft.DiagnosticSessionID = diagnosticID
	draft.Minecraft.ServerType = serverType
	draft.Server.BedrockEnabled = recommendedServerSupportsBedrock(serverType)
	draft.Server.Complete = true
	draft.Minecraft.ConnectionsComplete = true
	draft.Minecraft.ResourcesComplete = true
	draft.Minecraft.Complete = true
	draft.Storage.Complete = true
	draft.Backups.Complete = true
	draft.CurrentStep = setupReviewStep

	if err := validateSetupServer(draft.Server); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateSetupMinecraft(draft.Minecraft, draft.Defaults); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateSetupStorage(draft.Storage, draft.Inventory, draft.Defaults); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateSetupBackups(draft.Backups, draft.Storage, draft.Inventory, draft.Defaults); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	firstRunSetupDrafts.save(a, session, draft)
	a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "recommended WebUI setup defaults accepted", draft)
	http.Redirect(w, r, "/setup/review", http.StatusSeeOther)
}

func validSetupServerType(value string) bool {
	return value == "paper" || value == "purpur" || value == "vanilla"
}

func recommendedServerSupportsBedrock(serverType string) bool {
	switch serverType {
	case "paper", "purpur":
		return true
	default:
		return false
	}
}

func (a *App) setupWizardSaveServer(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists || !draft.Started {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	serverType := strings.ToLower(strings.TrimSpace(r.FormValue("server_type")))
	if serverType == "" {
		serverType = draft.Minecraft.ServerType
	}
	if serverType == "" {
		serverType = "paper"
	}
	if !validSetupServerType(serverType) {
		draft.Server.Complete = false
		firstRunSetupDrafts.save(a, session, draft)
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), "Selected Minecraft server software is not available yet.")
		return
	}

	if draft.Minecraft.ServerType != serverType {
		draft.Minecraft.ConnectionsComplete = false
		draft.Minecraft.Complete = false
	}
	draft.Minecraft.ServerType = serverType
	if serverType == "vanilla" {
		draft.Server.BedrockEnabled = false
	}
	draft.Minecraft.GameMode = r.FormValue("game_mode")
	draft.Server.MOTDAutomatic = r.FormValue("motd_automatic") == "true"
	draft.Server.MOTD = r.FormValue("motd")
	draft.Server.MaxPlayers = strings.TrimSpace(r.FormValue("max_players"))
	if err := validateSetupServer(draft.Server); err != nil {
		draft.Server.Complete = false
		firstRunSetupDrafts.save(a, session, draft)
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "server settings rejected", draft)
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), err.Error())
		return
	}
	if !validSetupGameMode(draft.Minecraft.GameMode) {
		draft.Server.Complete = false
		firstRunSetupDrafts.save(a, session, draft)
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), "Choose a valid game mode.")
		return
	}
	draft.Server.Complete = true
	draft.CurrentStep = setupCrossplayStep
	firstRunSetupReviews.delete(a, session)
	firstRunSetupDrafts.save(a, session, draft)
	a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "server settings saved", draft)
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

func (a *App) setupWizardSaveConnections(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists || !draft.Started {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	draft.CurrentStep = setupCrossplayStep
	draft.Server.BedrockEnabled = r.FormValue("bedrock_enabled") == "on"
	draft.Minecraft.JavaPort = strings.TrimSpace(r.FormValue("java_port"))
	draft.Minecraft.BedrockPort = strings.TrimSpace(r.FormValue("bedrock_port"))
	draft.Minecraft.ImageTag = strings.TrimSpace(r.FormValue("image_tag"))
	draft.Minecraft.VersionPolicy = strings.TrimSpace(r.FormValue("version_policy"))
	draft.Minecraft.Version = strings.TrimSpace(r.FormValue("version"))
	draft.Minecraft.ConnectionsComplete = false
	draft.Minecraft.Complete = false
	firstRunSetupReviews.delete(a, session)

	if r.FormValue("direction") == "back" {
		draft.Minecraft.ConnectionsComplete = false
		draft.CurrentStep = setupServerStep
		firstRunSetupReviews.delete(a, session)
		firstRunSetupDrafts.save(a, session, draft)
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "connection settings changed; user returned to Server", draft)
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if err := a.validateSetupCrossplay(r.Context(), session, draft); err != nil {
		if !recommendedServerSupportsBedrock(draft.Minecraft.ServerType) {
			draft.Server.BedrockEnabled = false
		}
		draft.Minecraft.ConnectionsComplete = false
		firstRunSetupDrafts.save(a, session, draft)
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "connection settings rejected", draft)
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), err.Error())
		return
	}
	if draft.Minecraft.VersionPolicy != "pinned" {
		draft.Minecraft.Version = ""
	}
	draft.Minecraft.ConnectionsComplete = true
	draft.Minecraft.Complete = draft.Minecraft.ResourcesComplete
	draft.CurrentStep = setupMemoryStep
	firstRunSetupReviews.delete(a, session)
	firstRunSetupDrafts.save(a, session, draft)
	a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "connection settings saved", draft)
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

func (a *App) setupWizardSaveResources(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists || !draft.Started {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	draft.Minecraft.JavaMemory = strings.TrimSpace(r.FormValue("java_memory"))
	draft.Minecraft.ContainerMemory = strings.TrimSpace(r.FormValue("container_memory"))

	if r.FormValue("direction") == "back" {
		draft.Minecraft.ResourcesComplete = false
		draft.Minecraft.Complete = false
		draft.CurrentStep = setupCrossplayStep
		firstRunSetupReviews.delete(a, session)
		firstRunSetupDrafts.save(a, session, draft)
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "resource settings changed; user returned to Cross-play", draft)
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if err := validateSetupResources(draft.Minecraft, draft.Defaults); err != nil {
		draft.Minecraft.ResourcesComplete = false
		draft.Minecraft.Complete = false
		firstRunSetupDrafts.save(a, session, draft)
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "resource settings rejected", draft)
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), err.Error())
		return
	}
	draft.Minecraft.ResourcesComplete = true
	draft.Minecraft.Complete = draft.Minecraft.ConnectionsComplete
	draft.CurrentStep = setupStorageStep
	firstRunSetupReviews.delete(a, session)
	firstRunSetupDrafts.save(a, session, draft)
	a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "resource settings saved", draft)
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

func (a *App) setupWizardNavigate(w http.ResponseWriter, r *http.Request) {
	session, client, _, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	direction := r.FormValue("direction")
	if direction != "next" && direction != "back" && direction != "jump" {
		http.Error(w, "invalid setup navigation", http.StatusBadRequest)
		return
	}
	target, _ := strconv.Atoi(r.FormValue("step"))
	if !firstRunSetupDrafts.navigateTo(a, session, direction, target) {
		if direction != "jump" {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		http.Error(w, "setup step has not been visited", http.StatusBadRequest)
		return
	}
	firstRunSetupReviews.delete(a, session)
	if draft, exists := firstRunSetupDrafts.get(a, session); exists {
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "wizard navigation changed", draft)
		if draft.CurrentStep == setupReviewStep {
			http.Redirect(w, r, "/setup/review", http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

func (a *App) setupWizardCancel(w http.ResponseWriter, r *http.Request) {
	session, client, _, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	if draft, exists := firstRunSetupDrafts.get(a, session); exists {
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "WebUI setup wizard cancelled", draft)
	}
	firstRunSetupReviews.delete(a, session)
	firstRunSetupDrafts.delete(a, session)
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

func (a *App) setupWizardRequest(w http.ResponseWriter, r *http.Request, requireCSRF bool) (string, adminDiscoveryAPI, api.SessionInfo, bool) {
	if requireCSRF && !a.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	session, client, identity, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return "", nil, api.SessionInfo{}, false
	}
	configuration, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return "", nil, api.SessionInfo{}, false
	}
	if configuration.Configured {
		firstRunSetupReviews.delete(a, session)
		firstRunSetupDrafts.delete(a, session)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return "", nil, api.SessionInfo{}, false
	}
	return session, client, identity, true
}

func (a *App) renderSetupWizard(w http.ResponseWriter, identity api.SessionInfo, draft setupDraft, csrf, errorMessage string) {
	steps := make([]setupWizardStepView, len(setupWizardSteps))
	copy(steps, setupWizardSteps)
	current := setupWizardStepView{}
	if draft.Started {
		if draft.CurrentStep < 1 || draft.CurrentStep > len(steps) {
			draft.CurrentStep = setupServerStep
		}
		for i := range steps {
			steps[i].Active = steps[i].Number == draft.CurrentStep
			steps[i].Complete = steps[i].Number < draft.CurrentStep
			steps[i].Visited = steps[i].Number <= draft.HighestStep
			if steps[i].Active {
				current = steps[i]
			}
		}
	}
	storageDisks, externalStorageCount := setupStorageDiskViews(draft.Inventory)
	data := setupWizardPageData{
		Title: "First setup", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrf, Identity: identity, Started: draft.Started, CurrentStep: draft.CurrentStep,
		Current: current, Steps: steps, Error: errorMessage,
		Server: draft.Server, Minecraft: draft.Minecraft, Storage: draft.Storage, Backups: draft.Backups,
		Filesystems: setupFilesystemViews(draft.Inventory), StorageDisks: storageDisks, BackupDisks: setupBackupDiskViews(draft.Inventory),
		ExternalStorageCount: externalStorageCount, SameDiskWarning: setupSameDiskWarning(draft), Defaults: draft.Defaults,
		SystemMemory:             formatMemoryMiB(draft.Defaults.SystemMemoryMiB),
		SystemReserveMinimum:     formatMemoryMiB(draft.Defaults.SystemReserveMinimumMiB),
		SystemReserveRecommended: formatMemoryMiB(draft.Defaults.SystemReserveRecommendedMiB),
		CanBack:                  draft.Started && draft.CurrentStep > 1,
		CanNext:                  draft.Started && draft.CurrentStep < len(steps),
	}
	a.renderAdminDiscovery(w, "setup_wizard.html", data)
}

func setupTimezoneOptions(current string) []string {
	zones := map[string]struct{}{"UTC": {}}
	root := "/usr/share/zoneinfo"
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative == "posix" || relative == "right" {
				return filepath.SkipDir
			}
			return nil
		}
		switch relative {
		case "iso3166.tab", "leap-seconds.list", "leapseconds", "localtime", "posixrules", "tzdata.zi", "zone.tab", "zone1970.tab":
			return nil
		}
		if setupTimezonePattern.MatchString(relative) && !strings.Contains(relative, "..") {
			if _, err := time.LoadLocation(relative); err == nil {
				zones[relative] = struct{}{}
			}
		}
		return nil
	})
	if current = strings.TrimSpace(current); current != "" && setupTimezonePattern.MatchString(current) {
		zones[current] = struct{}{}
	}
	out := make([]string, 0, len(zones))
	for zone := range zones {
		out = append(out, zone)
	}
	sort.Strings(out)
	return out
}

func normalizedSetupDefaults(defaults api.AdminSetupDefaults) api.AdminSetupDefaults {
	if defaults.DataPath == "" {
		defaults.DataPath = "/var/lib/justvoxel/minecraft"
	}
	if defaults.BackupPath == "" {
		defaults.BackupPath = "/var/lib/justvoxel/backups"
	}
	if defaults.JavaMemory == "" {
		defaults.JavaMemory = "4G"
	}
	if defaults.ContainerMemory == "" {
		defaults.ContainerMemory = "6G"
	}
	if defaults.JavaPort == 0 {
		defaults.JavaPort = 25565
	}
	if defaults.BedrockPort == 0 {
		defaults.BedrockPort = 19132
	}
	if defaults.Timezone == "" {
		defaults.Timezone = "UTC"
	}
	if defaults.MaxPlayers == 0 {
		defaults.MaxPlayers = 10
	}
	if defaults.ImageTag == "" {
		defaults.ImageTag = "stable"
	}
	if defaults.BackupKeep == 0 {
		defaults.BackupKeep = 7
	}
	if defaults.BackupDailyTime == "" {
		defaults.BackupDailyTime = "04:30"
	}
	if defaults.SystemReserveMinimumMiB == 0 {
		defaults.SystemReserveMinimumMiB = 1024
	}
	if defaults.SystemReserveRecommendedMiB == 0 {
		defaults.SystemReserveRecommendedMiB = 2048
	}
	return defaults
}

func draftFromSetupDefaults(defaults api.AdminSetupDefaults, inventory api.AdminStorageDiscovery) setupDraft {
	defaults = normalizedSetupDefaults(defaults)
	policy := defaults.VersionMode
	if policy != "latest" {
		// First setup has no pinned version yet. Let the later validated setup
		// plan resolve the newest stable Paper-backed Minecraft release.
		policy = "recommended"
	}
	version := ""
	if policy == "latest" {
		version = "LATEST"
	}
	return setupDraft{
		Started: true, CurrentStep: setupServerStep, HighestStep: setupServerStep, Defaults: defaults, Inventory: inventory,
		Server: setupServerDraft{
			MOTDAutomatic: true, MaxPlayers: strconv.Itoa(defaults.MaxPlayers),
			BedrockEnabled: defaults.BedrockEnabled, Timezone: defaults.Timezone,
		},
		Minecraft: setupMinecraftDraft{
			ServerType: "paper",
			JavaMemory: defaults.JavaMemory, ContainerMemory: defaults.ContainerMemory,
			JavaPort: strconv.Itoa(defaults.JavaPort), BedrockPort: strconv.Itoa(defaults.BedrockPort),
			ImageTag: defaults.ImageTag, VersionPolicy: policy, Version: version, GameMode: "survival",
		},
		Storage: initialSetupStorageDraft(defaults),
		Backups: initialSetupBackupDraft(defaults),
	}
}

func validateSetupServer(server setupServerDraft) error {
	if strings.ContainsAny(server.MOTD, "\r\n") {
		return errors.New("Welcome message must be a single line.")
	}
	if _, err := parsePositiveFormInt(server.MaxPlayers, "Maximum players"); err != nil {
		return err
	}
	if !setupTimezonePattern.MatchString(server.Timezone) || strings.Contains(server.Timezone, "..") || strings.HasPrefix(server.Timezone, "/") {
		return errors.New("The appliance timezone is invalid. Check system Date & Time settings.")
	}
	if server.Timezone != "UTC" {
		info, err := os.Stat(filepath.Join("/usr/share/zoneinfo", server.Timezone))
		if err != nil || info.IsDir() {
			return errors.New("The appliance timezone is unavailable. Check system Date & Time settings.")
		}
	}
	return nil
}

func validateSetupResources(minecraft setupMinecraftDraft, defaults api.AdminSetupDefaults) error {
	javaMiB, ok := setupMemoryMiB(minecraft.JavaMemory)
	if !ok {
		return errors.New("Minecraft game memory must use a size such as 4G or 4096M.")
	}
	containerMiB, ok := setupMemoryMiB(minecraft.ContainerMemory)
	if !ok {
		return errors.New("Maximum Minecraft memory must use a size such as 6G or 6144M.")
	}
	if containerMiB <= javaMiB {
		return errors.New("Maximum Minecraft memory must be larger than Minecraft game memory.")
	}
	if defaults.SystemMemoryMiB > 0 && containerMiB >= defaults.SystemMemoryMiB {
		return errors.New("Maximum Minecraft memory must leave some memory available for JustVoxel and system services.")
	}
	return nil
}

func validateSetupConnections(minecraft setupMinecraftDraft) error {
	if _, err := parseSetupPort(minecraft.JavaPort, "Minecraft Java port"); err != nil {
		return err
	}
	if _, err := parseSetupPort(minecraft.BedrockPort, "Bedrock UDP port"); err != nil {
		return err
	}
	return nil
}

func validSetupGameMode(mode string) bool {
	switch mode {
	case "survival", "creative", "adventure", "spectator":
		return true
	}
	return false
}

func validateSetupMinecraft(minecraft setupMinecraftDraft, defaults api.AdminSetupDefaults) error {
	if !validSetupGameMode(minecraft.GameMode) {
		return errors.New("Choose a valid game mode.")
	}
	if err := validateSetupResources(minecraft, defaults); err != nil {
		return err
	}
	if err := validateSetupConnections(minecraft); err != nil {
		return err
	}
	return validateSetupVersion(minecraft)
}

func validateSetupVersion(minecraft setupMinecraftDraft) error {
	if !setupImageTagPattern.MatchString(minecraft.ImageTag) {
		return errors.New("Minecraft container channel or tag is invalid.")
	}
	switch minecraft.VersionPolicy {
	case "recommended":
		// A4.4 resolves and validates the newest stable Paper-backed version.
	case "latest":
		// The planner resolves an exact preview; runtime keeps this policy moving.
	case "pinned":
		if minecraft.Version == "" || minecraft.Version == "LATEST" || !setupVersionPattern.MatchString(minecraft.Version) {
			return errors.New("Specific Minecraft version is invalid.")
		}
	default:
		return errors.New("Choose a Minecraft version.")
	}
	return nil
}

func (a *App) validateSetupCrossplay(ctx context.Context, session string, draft setupDraft) error {
	if draft.Server.BedrockEnabled && !recommendedServerSupportsBedrock(draft.Minecraft.ServerType) {
		return errors.New("Managed Bedrock cross-play requires Paper or Purpur.")
	}
	if err := validateSetupConnections(draft.Minecraft); err != nil {
		return err
	}
	if err := validateSetupVersion(draft.Minecraft); err != nil {
		return err
	}
	client, ok := a.api.(setupVersionPreviewAPI)
	if !ok {
		return errors.New("Version information is unavailable. Try again before continuing.")
	}
	status, err := client.AdminSetupVersionPreview(ctx, session, draft.Minecraft.VersionPolicy, draft.Minecraft.Version, draft.Server.BedrockEnabled, draft.Minecraft.ServerType)
	if err != nil {
		return errors.New("Version information is unavailable. Try again before continuing.")
	}
	if status.SelectedCandidate == "" {
		return errors.New("The selected Minecraft version is unavailable. Choose an available version.")
	}
	if draft.Server.BedrockEnabled && !status.CrossplayCompatible {
		return errors.New("The selected Minecraft version does not support Bedrock cross-play. Choose a compatible version or disable Bedrock cross-play.")
	}
	return nil
}

func parseSetupPort(value, label string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("%s must be between 1 and 65535.", label)
	}
	return port, nil
}

func setupMemoryMiB(value string) (int, bool) {
	match := setupMemoryPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 3 {
		return 0, false
	}
	amount, err := strconv.Atoi(match[1])
	if err != nil || amount <= 0 {
		return 0, false
	}
	if strings.EqualFold(match[2], "G") {
		amount *= 1024
	}
	return amount, true
}

func setupKey(app *App, session string) setupDraftKey {
	return setupDraftKey{app: app, session: sha256.Sum256([]byte(session))}
}

func (s *setupDraftStore) pruneLocked(now time.Time) {
	for key, draft := range s.drafts {
		if now.Sub(draft.UpdatedAt) > setupDraftLifetime {
			delete(s.drafts, key)
		}
	}
}

func (s *setupDraftStore) get(app *App, session string) (setupDraft, bool) {
	now := time.Now()
	key := setupKey(app, session)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	draft, ok := s.drafts[key]
	return draft, ok
}

func (s *setupDraftStore) start(app *App, session string, defaults api.AdminSetupDefaults, inventory api.AdminStorageDiscovery) {
	now := time.Now()
	key := setupKey(app, session)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	draft := draftFromSetupDefaults(defaults, inventory)
	draft.UpdatedAt = now
	s.drafts[key] = draft
}

func (s *setupDraftStore) save(app *App, session string, draft setupDraft) {
	now := time.Now()
	key := setupKey(app, session)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	if previous, ok := s.drafts[key]; ok && previous.HighestStep > draft.HighestStep {
		draft.HighestStep = previous.HighestStep
	}
	if draft.CurrentStep > draft.HighestStep {
		draft.HighestStep = draft.CurrentStep
	}
	draft.UpdatedAt = now
	s.drafts[key] = draft
}

func (s *setupDraftStore) navigate(app *App, session, direction string) bool {
	return s.navigateTo(app, session, direction, 0)
}

func (s *setupDraftStore) navigateTo(app *App, session, direction string, target int) bool {
	now := time.Now()
	key := setupKey(app, session)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	draft, ok := s.drafts[key]
	if !ok || !draft.Started {
		return false
	}
	switch direction {
	case "next":
		// Steps before Review have real forms and cannot be skipped through the generic
		// navigation endpoint. The validated Review has its own route.
		if draft.CurrentStep < setupReviewStep {
			return false
		}
	case "back":
		if draft.CurrentStep > 1 {
			draft.CurrentStep--
		}
	case "jump":
		if target < 1 || target > draft.HighestStep || target > len(setupWizardSteps) {
			return false
		}
		previousStep := draft.CurrentStep
		draft.CurrentStep = target
		if target == setupReviewStep && !setupDraftReadyForReview(draft) {
			draft.CurrentStep = previousStep
			return false
		}
	default:
		return false
	}
	draft.UpdatedAt = now
	s.drafts[key] = draft
	return true
}

func (s *setupDraftStore) delete(app *App, session string) {
	key := setupKey(app, session)
	s.mu.Lock()
	delete(s.drafts, key)
	s.mu.Unlock()
}
