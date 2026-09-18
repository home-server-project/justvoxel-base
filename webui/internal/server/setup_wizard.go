package server

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"regexp"
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
	JavaMemory      string
	ContainerMemory string
	JavaPort        string
	BedrockPort     string
	ImageTag        string
	VersionPolicy   string
	Version         string
	Complete        bool
}

type setupDraft struct {
	Started     bool
	CurrentStep int
	Server      setupServerDraft
	Minecraft   setupMinecraftDraft
	Storage     setupStorageDraft
	Backups     setupBackupDraft
	Defaults    api.AdminSetupDefaults
	Inventory   api.AdminStorageDiscovery
	UpdatedAt   time.Time
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
}

var setupWizardSteps = []setupWizardStepView{
	{Number: 1, Name: "Server", Description: "Choose the server welcome message, player limit, Bedrock cross-play and timezone."},
	{Number: 2, Name: "Minecraft", Description: "Choose Minecraft memory, ports, container channel and version policy."},
	{Number: 3, Name: "Storage", Description: "Choose where Minecraft worlds, configuration and server data will live."},
	{Number: 4, Name: "Backups", Description: "Choose backup location, retention and automatic backup schedule."},
	{Number: 5, Name: "Review", Description: "The complete setup plan and Minecraft EULA acceptance will be reviewed here before any changes are applied."},
}

func (a *App) registerSetupWizardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /setup", a.setupWizardPage)
	mux.HandleFunc("POST /setup/start", a.setupWizardStart)
	mux.HandleFunc("POST /setup/server", a.setupWizardSaveServer)
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
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists {
		draft = setupDraft{}
	} else if draft.Started && draft.CurrentStep == 5 {
		http.Redirect(w, r, "/setup/review", http.StatusSeeOther)
		return
	} else if draft.Started && (draft.CurrentStep == 3 || draft.CurrentStep == 4) {
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
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

func (a *App) setupWizardSaveServer(w http.ResponseWriter, r *http.Request) {
	session, _, identity, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists || !draft.Started {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	draft.Server = setupServerDraft{
		MOTD:           strings.TrimSpace(r.FormValue("motd")),
		MaxPlayers:     strings.TrimSpace(r.FormValue("max_players")),
		BedrockEnabled: r.FormValue("bedrock_enabled") == "on",
		Timezone:       strings.TrimSpace(r.FormValue("timezone")),
	}
	if err := validateSetupServer(draft.Server); err != nil {
		draft.Server.Complete = false
		firstRunSetupDrafts.save(a, session, draft)
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), err.Error())
		return
	}
	draft.Server.Complete = true
	draft.CurrentStep = 2
	firstRunSetupReviews.delete(a, session)
	firstRunSetupDrafts.save(a, session, draft)
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

func (a *App) setupWizardSaveMinecraft(w http.ResponseWriter, r *http.Request) {
	session, _, identity, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists || !draft.Started {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	draft.Minecraft = setupMinecraftDraft{
		JavaMemory:      strings.TrimSpace(r.FormValue("java_memory")),
		ContainerMemory: strings.TrimSpace(r.FormValue("container_memory")),
		JavaPort:        strings.TrimSpace(r.FormValue("java_port")),
		BedrockPort:     strings.TrimSpace(r.FormValue("bedrock_port")),
		ImageTag:        strings.TrimSpace(r.FormValue("image_tag")),
		VersionPolicy:   strings.TrimSpace(r.FormValue("version_policy")),
		Version:         strings.TrimSpace(r.FormValue("version")),
	}

	if r.FormValue("direction") == "back" {
		draft.Minecraft.Complete = false
		draft.CurrentStep = 1
		firstRunSetupReviews.delete(a, session)
		firstRunSetupDrafts.save(a, session, draft)
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if err := validateSetupMinecraft(draft.Minecraft, draft.Defaults); err != nil {
		draft.Minecraft.Complete = false
		firstRunSetupDrafts.save(a, session, draft)
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), err.Error())
		return
	}
	draft.Minecraft.Complete = true
	if draft.Minecraft.VersionPolicy == "latest" {
		draft.Minecraft.Version = "LATEST"
	}
	if draft.Minecraft.VersionPolicy == "recommended" {
		draft.Minecraft.Version = ""
	}
	draft.CurrentStep = 3
	firstRunSetupReviews.delete(a, session)
	firstRunSetupDrafts.save(a, session, draft)
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

func (a *App) setupWizardNavigate(w http.ResponseWriter, r *http.Request) {
	session, _, _, ok := a.setupWizardRequest(w, r, true)
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
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

func (a *App) setupWizardCancel(w http.ResponseWriter, r *http.Request) {
	session, _, _, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
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
		CanBack:                  draft.Started && draft.CurrentStep > 1,
		CanNext:                  draft.Started && draft.CurrentStep < len(steps),
	}
	a.renderAdminDiscovery(w, "setup_wizard.html", data)
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
	return nil
}

func validateSetupMinecraft(minecraft setupMinecraftDraft, defaults api.AdminSetupDefaults) error {
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
	if _, err := parseSetupPort(minecraft.JavaPort, "Minecraft Java port"); err != nil {
		return err
	}
	if _, err := parseSetupPort(minecraft.BedrockPort, "Bedrock UDP port"); err != nil {
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
			return errors.New("Pinned Minecraft version is invalid.")
		}
	default:
		return errors.New("Choose a Minecraft version policy.")
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
		// Steps 1-4 have real forms and cannot be skipped through the generic
		// navigation endpoint. The validated Review has its own route.
		if draft.CurrentStep <= 4 {
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
