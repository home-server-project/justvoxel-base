package server

import (
	"crypto/sha256"
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
	MaxPlayers     string
	BedrockEnabled bool
	Timezone       string
	Complete       bool
}

type setupMinecraftDraft struct {
	ServerType        string
	JavaMemory        string
	ContainerMemory   string
	JavaPort          string
	BedrockPort       string
	ImageTag          string
	VersionPolicy     string
	Version             string
	ConnectionsComplete bool
	ResourcesComplete   bool
	Complete            bool
}

type setupDraft struct {
	Started             bool
	Mode                string
	CurrentStep         int
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
	SameDiskWarning          string
	Defaults                 api.AdminSetupDefaults
	SystemMemory             string
	SystemReserveMinimum     string
	SystemReserveRecommended string
	Timezones                []string
}

var setupWizardSteps = []setupWizardStepView{
	{Number: 1, Name: "Server", Description: "Choose server software, welcome message, player limit and timezone."},
	{Number: 2, Name: "Connections", Description: "Choose Java and Bedrock connectivity."},
	{Number: 3, Name: "Resources", Description: "Choose how much system memory Minecraft may use."},
	{Number: 4, Name: "Version", Description: "Choose the container channel and Minecraft version policy."},
	{Number: 5, Name: "Storage", Description: "Choose where Minecraft worlds, configuration and server data will live."},
	{Number: 6, Name: "Backups", Description: "Choose backup location, retention and automatic backup schedule."},
	{Number: 7, Name: "Review", Description: "Review the final setup plan and accept the Minecraft EULA before anything is applied."},
}

func (a *App) registerSetupWizardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /setup", a.setupWizardPage)
	mux.HandleFunc("POST /setup/start", a.setupWizardStart)
	mux.HandleFunc("POST /setup/recommended", a.setupWizardRecommended)
	mux.HandleFunc("POST /setup/server", a.setupWizardSaveServer)
	mux.HandleFunc("POST /setup/connections", a.setupWizardSaveConnections)
	mux.HandleFunc("POST /setup/resources", a.setupWizardSaveResources)
	mux.HandleFunc("POST /setup/minecraft", a.setupWizardSaveMinecraft)
	mux.HandleFunc("POST /setup/navigate", a.setupWizardNavigate)
	mux.HandleFunc("POST /setup/cancel", a.setupWizardCancel)
	a.registerSetupWizardStorageRoutes(mux)
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
	} else if draft.Started && draft.CurrentStep == 7 {
		http.Redirect(w, r, "/setup/review", http.StatusSeeOther)
		return
	} else if draft.Started && (draft.CurrentStep == 5 || draft.CurrentStep == 6) {
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
	if serverType != "paper" {
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
	draft.CurrentStep = 7

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

func recommendedServerSupportsBedrock(serverType string) bool {
	switch serverType {
	case "paper":
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
	if serverType != "paper" {
		draft.Server.Complete = false
		firstRunSetupDrafts.save(a, session, draft)
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), "Selected Minecraft server software is not available yet.")
		return
	}

	draft.Minecraft.ServerType = serverType
	draft.Server.MOTD = strings.TrimSpace(r.FormValue("motd"))
	draft.Server.MaxPlayers = strings.TrimSpace(r.FormValue("max_players"))
	draft.Server.Timezone = strings.TrimSpace(r.FormValue("timezone"))
	if err := validateSetupServer(draft.Server); err != nil {
		draft.Server.Complete = false
		firstRunSetupDrafts.save(a, session, draft)
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "server settings rejected", draft)
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), err.Error())
		return
	}
	draft.Server.Complete = true
	draft.CurrentStep = 2
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

	draft.Server.BedrockEnabled = r.FormValue("bedrock_enabled") == "on"
	draft.Minecraft.JavaPort = strings.TrimSpace(r.FormValue("java_port"))
	draft.Minecraft.BedrockPort = strings.TrimSpace(r.FormValue("bedrock_port"))

	if r.FormValue("direction") == "back" {
		draft.Minecraft.ConnectionsComplete = false
		draft.CurrentStep = 1
		firstRunSetupReviews.delete(a, session)
		firstRunSetupDrafts.save(a, session, draft)
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "connection settings changed; user returned to Server", draft)
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if err := validateSetupConnections(draft.Minecraft); err != nil {
		draft.Minecraft.ConnectionsComplete = false
		firstRunSetupDrafts.save(a, session, draft)
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "connection settings rejected", draft)
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), err.Error())
		return
	}
	draft.Minecraft.ConnectionsComplete = true
	draft.CurrentStep = 3
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
		draft.CurrentStep = 2
		firstRunSetupReviews.delete(a, session)
		firstRunSetupDrafts.save(a, session, draft)
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "resource settings changed; user returned to Connections", draft)
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
	draft.Minecraft.Complete = false
	draft.CurrentStep = 4
	firstRunSetupReviews.delete(a, session)
	firstRunSetupDrafts.save(a, session, draft)
	a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "resource settings saved", draft)
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

func (a *App) setupWizardSaveMinecraft(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists || !draft.Started {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	draft.Minecraft.ImageTag = strings.TrimSpace(r.FormValue("image_tag"))
	draft.Minecraft.VersionPolicy = strings.TrimSpace(r.FormValue("version_policy"))
	draft.Minecraft.Version = strings.TrimSpace(r.FormValue("version"))

	if r.FormValue("direction") == "back" {
		draft.Minecraft.Complete = false
		draft.CurrentStep = 3
		firstRunSetupReviews.delete(a, session)
		firstRunSetupDrafts.save(a, session, draft)
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "version settings changed; user returned to Resources", draft)
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if err := validateSetupMinecraft(draft.Minecraft, draft.Defaults); err != nil {
		draft.Minecraft.Complete = false
		firstRunSetupDrafts.save(a, session, draft)
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "Minecraft settings rejected", draft)
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), err.Error())
		return
	}
	draft.Minecraft.ResourcesComplete = true
	draft.Minecraft.Complete = true
	if draft.Minecraft.VersionPolicy == "latest" {
		draft.Minecraft.Version = "LATEST"
	}
	if draft.Minecraft.VersionPolicy == "recommended" {
		draft.Minecraft.Version = ""
	}
	draft.CurrentStep = 5
	firstRunSetupReviews.delete(a, session)
	firstRunSetupDrafts.save(a, session, draft)
	a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "version settings saved", draft)
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

func (a *App) setupWizardNavigate(w http.ResponseWriter, r *http.Request) {
	session, client, _, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	direction := r.FormValue("direction")
	if direction != "next" && direction != "back" {
		http.Error(w, "invalid setup navigation", http.StatusBadRequest)
		return
	}
	if !firstRunSetupDrafts.navigate(a, session, direction) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	firstRunSetupReviews.delete(a, session)
	if draft, exists := firstRunSetupDrafts.get(a, session); exists {
		a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "wizard navigation changed", draft)
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
			draft.CurrentStep = 1
		}
		for i := range steps {
			steps[i].Active = steps[i].Number == draft.CurrentStep
			steps[i].Complete = steps[i].Number < draft.CurrentStep
			if steps[i].Active {
				current = steps[i]
			}
		}
	}
	data := setupWizardPageData{
		Title: "First setup", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrf, Identity: identity, Started: draft.Started, CurrentStep: draft.CurrentStep,
		Current: current, Steps: steps, Error: errorMessage,
		Server: draft.Server, Minecraft: draft.Minecraft, Storage: draft.Storage, Backups: draft.Backups,
		Filesystems: setupFilesystemViews(draft.Inventory), SameDiskWarning: setupSameDiskWarning(draft), Defaults: draft.Defaults,
		SystemMemory:             formatMemoryMiB(draft.Defaults.SystemMemoryMiB),
		SystemReserveMinimum:     formatMemoryMiB(draft.Defaults.SystemReserveMinimumMiB),
		SystemReserveRecommended: formatMemoryMiB(draft.Defaults.SystemReserveRecommendedMiB),
		Timezones:                setupTimezoneOptions(draft.Server.Timezone),
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
			zones[relative] = struct{}{}
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
	if defaults.MOTD == "" {
		defaults.MOTD = "JustVoxel Java and Bedrock Server"
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
		Started: true, CurrentStep: 1, Defaults: defaults, Inventory: inventory,
		Server: setupServerDraft{
			MOTD: defaults.MOTD, MaxPlayers: strconv.Itoa(defaults.MaxPlayers),
			BedrockEnabled: defaults.BedrockEnabled, Timezone: defaults.Timezone,
		},
		Minecraft: setupMinecraftDraft{
			ServerType: "paper",
			JavaMemory: defaults.JavaMemory, ContainerMemory: defaults.ContainerMemory,
			JavaPort: strconv.Itoa(defaults.JavaPort), BedrockPort: strconv.Itoa(defaults.BedrockPort),
			ImageTag: defaults.ImageTag, VersionPolicy: policy, Version: version,
		},
		Storage: initialSetupStorageDraft(defaults),
		Backups: initialSetupBackupDraft(defaults),
	}
}

func validateSetupServer(server setupServerDraft) error {
	if strings.ContainsAny(server.MOTD, "\r\n") {
		return errors.New("Server name / welcome message must be a single line.")
	}
	if _, err := parsePositiveFormInt(server.MaxPlayers, "Maximum players"); err != nil {
		return err
	}
	if !setupTimezonePattern.MatchString(server.Timezone) || strings.Contains(server.Timezone, "..") || strings.HasPrefix(server.Timezone, "/") {
		return errors.New("Timezone must look like UTC or America/Toronto.")
	}
	if server.Timezone != "UTC" {
		info, err := os.Stat(filepath.Join("/usr/share/zoneinfo", server.Timezone))
		if err != nil || info.IsDir() {
			return errors.New("Choose a timezone from the JustVoxel timezone suggestions.")
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

func validateSetupMinecraft(minecraft setupMinecraftDraft, defaults api.AdminSetupDefaults) error {
	if err := validateSetupResources(minecraft, defaults); err != nil {
		return err
	}
	if err := validateSetupConnections(minecraft); err != nil {
		return err
	}
	if !setupImageTagPattern.MatchString(minecraft.ImageTag) {
		return errors.New("Minecraft container channel or tag is invalid.")
	}
	switch minecraft.VersionPolicy {
	case "recommended":
		// A4.4 resolves and validates the newest stable Paper-backed version.
	case "latest":
		// LATEST intentionally follows the container's moving Minecraft release.
	case "pinned":
		if minecraft.Version == "" || minecraft.Version == "LATEST" || !setupVersionPattern.MatchString(minecraft.Version) {
			return errors.New("Specific Minecraft version is invalid.")
		}
	default:
		return errors.New("Choose a Minecraft version.")
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
	draft.UpdatedAt = now
	s.drafts[key] = draft
}

func (s *setupDraftStore) navigate(app *App, session, direction string) bool {
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
		// Steps 1-6 have real forms and cannot be skipped through the generic
		// navigation endpoint. The validated Review has its own route.
		if draft.CurrentStep <= 6 {
			return false
		}
	case "back":
		if draft.CurrentStep > 1 {
			draft.CurrentStep--
		}
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
