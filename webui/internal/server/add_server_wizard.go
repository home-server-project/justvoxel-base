package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const minecraftServersPath = "/minecraft/servers"
const addServerBasePath = minecraftServersPath + "/add"

var addServerDrafts = setupDraftStore{drafts: make(map[setupDraftKey]setupDraft)}
var addServerReviews = setupReviewStore{reviews: make(map[setupDraftKey]setupReviewState)}

// Serialize wizard requests, including double submissions, until create returns.
// Only the Add Server context is affected; legacy setup never takes this lock.
var addServerWizardMu sync.Mutex
var addServerIDPattern = regexp.MustCompile(`^jv-[a-z0-9]{6,12}$`)
var addServerBackupTimePattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

type addServerAPI interface {
	MinecraftInstances(context.Context, string) ([]api.RegisteredMinecraftInstance, error)
	MinecraftInstanceRuntimes(context.Context, string) ([]string, error)
	MinecraftInstanceConfiguration(context.Context, string, string) (api.InstanceRuntimeSettings, error)
	MinecraftInstanceBackupSettings(context.Context, string, string) (api.InstanceBackupSettings, error)
	CreateMinecraftInstance(context.Context, string, api.CreateMinecraftInstanceRequest) (api.CreateMinecraftInstanceResponse, error)
	SetMinecraftInstanceBackupSettings(context.Context, string, string, api.InstanceBackupSettings) error
}

func isAddServerRequest(r *http.Request) bool {
	return r.URL.Path == addServerBasePath || strings.HasPrefix(r.URL.Path, addServerBasePath+"/")
}
func setupBasePath(r *http.Request) string {
	if isAddServerRequest(r) {
		return addServerBasePath
	}
	return "/setup"
}
func setupDraftsFor(r *http.Request) *setupDraftStore {
	if isAddServerRequest(r) {
		return &addServerDrafts
	}
	return &firstRunSetupDrafts
}
func setupReviewsFor(r *http.Request) *setupReviewStore {
	if isAddServerRequest(r) {
		return &addServerReviews
	}
	return &firstRunSetupReviews
}

func (a *App) registerAddServerWizardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+minecraftServersPath, a.minecraftServersPage)
	mux.HandleFunc("GET /workspace/minecraft-servers", a.minecraftServersWorkspace)
	mux.HandleFunc("GET "+minecraftServersPath+"/view/{id}", a.minecraftSelectedServer)
	mux.HandleFunc("POST "+minecraftServersPath+"/view/{id}/{action}", a.minecraftSelectedServerAction)
	routes := map[string]http.HandlerFunc{
		"GET ":                 a.addServerWizardPage,
		"POST /start":          a.addServerWizardStart,
		"POST /recommended":    a.addServerWizardStart,
		"POST /server":         a.setupWizardSaveServer,
		"POST /connections":    a.setupWizardSaveConnections,
		"POST /resources":      a.setupWizardSaveResources,
		"GET /version-preview": a.setupWizardVersionPreview,
		"POST /storage":        a.addServerWizardFixedStep,
		"POST /backups":        a.addServerWizardFixedStep,
		"POST /navigate":       a.setupWizardNavigate,
		"POST /cancel":         a.setupWizardCancel,
		"GET /review":          a.addServerWizardReview,
		"POST /review/back":    a.addServerWizardReviewBack,
		"POST /review/cancel":  a.setupWizardCancel,
		"POST /review/apply":   a.addServerWizardCreate,
	}
	for route, handler := range routes {
		method, suffix, _ := strings.Cut(route, " ")
		mux.HandleFunc(method+" "+addServerBasePath+suffix, func(w http.ResponseWriter, r *http.Request) {
			addServerWizardMu.Lock()
			defer addServerWizardMu.Unlock()
			// After an uncertain/partial result the draft must never become another
			// creation request through Back, a stale tab, or a repeated Start POST.
			if session, ok := sessionFromRequest(r); ok {
				draft, exists := addServerDrafts.get(a, session)
				backupEdit := draft.CoreCreated && (suffix == "/review/back" || suffix == "/backups")
				if exists && draft.CreationAttempted && r.Method == http.MethodPost && !backupEdit && suffix != "/review/apply" && suffix != "/cancel" && suffix != "/review/cancel" {
					http.Error(w, "Creation has already been submitted. Return to Review to check the result.", http.StatusConflict)
					return
				}
			}
			handler(w, r)
		})
	}
}

type minecraftServersPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Message       string
	Error         string
	Configured    bool
	Servers       []minecraftServerSummary
	Selected      *minecraftServerSummary
}

func (a *App) minecraftServersPage(w http.ResponseWriter, r *http.Request) {
	if mustChange(r) {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	if _, _, _, ok := a.adminDiscoveryRequest(w, r); !ok {
		return
	}
	query := url.Values{"workspace": {"minecraft-servers"}}
	for _, key := range []string{"created", "deleted"} {
		if r.URL.Query().Get(key) == "yes" {
			query.Set(key, "yes")
		}
	}
	http.Redirect(w, r, "/?"+query.Encode(), http.StatusSeeOther)
}

func (a *App) minecraftServersWorkspace(w http.ResponseWriter, r *http.Request) {
	if mustChange(r) {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	session, client, identity, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	configuration, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	message := ""
	if r.URL.Query().Get("deleted") == "yes" {
		message = "Server deleted. Backup archives were preserved as historical archives. Current Restore remains tied to that server identity."
	}
	if r.URL.Query().Get("created") == "yes" {
		message = "Minecraft server created. It is stopped and ready for use."
	}
	data := minecraftServersPageData{Title: "Minecraft Servers", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI, CSRF: csrfFromRequest(r), Identity: identity, Message: message, Configured: configuration.Configured}
	data.Servers, data.Error = a.minecraftServerInventory(r.Context(), session)
	a.renderAdminDiscovery(w, "minecraft_servers.html", data)
}

func (a *App) addServerWizardPage(w http.ResponseWriter, r *http.Request) {
	session, _, identity, ok := a.setupWizardRequest(w, r, false)
	if !ok {
		return
	}
	draft, exists := addServerDrafts.get(a, session)
	if !exists {
		draft = setupDraft{AddServer: true}
	}
	if draft.CurrentStep == setupReviewStep {
		http.Redirect(w, r, addServerBasePath+"/review", http.StatusSeeOther)
		return
	}
	a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), "")
}

// Same Phase 1 friendly-name rules, with a required non-empty name for this UI.
// The registry remains authoritative for uniqueness at creation time.
func (a *App) validateAddServerName(ctx context.Context, session, name string, excludeID ...string) error {
	if !utf8.ValidString(name) {
		return errors.New("Enter a valid server name.")
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return errors.New("Use letters, numbers, spaces, or . _ - ' ( ) in the server name.")
		}
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || utf8.RuneCountInString(name) > 64 {
		return errors.New("Enter a server name of 1–64 characters.")
	}
	for _, r := range name {
		if !(unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == ' ' || strings.ContainsRune("._-'()", r)) {
			return errors.New("Use letters, numbers, spaces, or . _ - ' ( ) in the server name.")
		}
	}
	client, ok := a.api.(interface {
		MinecraftInstances(context.Context, string) ([]api.RegisteredMinecraftInstance, error)
	})
	if !ok {
		return errors.New("Independent server creation is unavailable.")
	}
	instances, err := client.MinecraftInstances(ctx, session)
	if err != nil {
		return errors.New("Server names could not be checked. Try again.")
	}
	for _, instance := range instances {
		if len(excludeID) > 0 && instance.ID == excludeID[0] {
			continue
		}
		if strings.EqualFold(instance.DisplayName, name) {
			return errors.New("Another server already uses this name.")
		}
	}
	return nil
}

func availableInstancePort(start int, used map[int]bool) (int, error) {
	for port := start; port <= 65535; port++ {
		if !used[port] {
			return port, nil
		}
	}
	return 0, errors.New("No unused Minecraft port could be selected.")
}
func separatedBackupTimes(a, b string) bool {
	minute := func(s string) int { h, _ := strconv.Atoi(s[:2]); m, _ := strconv.Atoi(s[3:]); return h*60 + m }
	d := minute(a) - minute(b)
	if d < 0 {
		d = -d
	}
	return d >= 30 && d <= 1410
}

func (a *App) newAddServerDraft(ctx context.Context, session string, client adminDiscoveryAPI) (setupDraft, error) {
	defaults, err := client.AdminSetupDefaults(ctx, session)
	if err != nil {
		return setupDraft{}, err
	}
	configuration, err := client.AdminConfiguration(ctx, session)
	if err != nil {
		return setupDraft{}, err
	}
	instances, ok := a.api.(addServerAPI)
	if !ok {
		return setupDraft{}, errors.New("Independent server creation is unavailable.")
	}
	ids, err := instances.MinecraftInstanceRuntimes(ctx, session)
	if err != nil {
		return setupDraft{}, err
	}
	defaults = normalizedSetupDefaults(defaults)
	defaults.ImageTag, defaults.VersionMode = "stable", "recommended"
	draft := draftFromSetupDefaults(defaults, api.AdminStorageDiscovery{})
	draft.AddServer, draft.StartOnBoot = true, true
	draft.Storage = setupStorageDraft{Complete: true}
	draft.BackupAvailable = configuration.Backup.Path != ""
	draft.Backups.Complete = true
	draft.Backups.Automatic = draft.BackupAvailable
	javaUsed := map[int]bool{configuration.Minecraft.JavaPort: true}
	bedrockUsed := map[int]bool{}
	if configuration.Minecraft.BedrockEnabled {
		bedrockUsed[configuration.Minecraft.BedrockPort] = true
	}
	draft.OtherMemoryMiB, _ = setupMemoryMiB(configuration.Minecraft.ContainerMemory)
	schedules := []string{}
	if configuration.Backup.TimerEnabled {
		// Discovery stores the ordinary daily OnCalendar expression.
		schedule := strings.TrimPrefix(configuration.Backup.Schedule, "*-*-* ")
		schedule = strings.TrimSuffix(schedule, ":00")
		if addServerBackupTimePattern.MatchString(schedule) {
			schedules = append(schedules, schedule)
		} else {
			draft.Backups.Automatic = false
		}
	}
	for _, id := range ids {
		runtime, err := instances.MinecraftInstanceConfiguration(ctx, session, id)
		if err != nil {
			return setupDraft{}, errors.New("Existing server settings could not be checked. Resolve the existing server's configuration before adding another.")
		}
		javaUsed[runtime.JavaPort] = true
		if runtime.BedrockEnabled {
			bedrockUsed[runtime.BedrockPort] = true
		}
		memory, _ := setupMemoryMiB(runtime.ContainerMemory)
		draft.OtherMemoryMiB += memory
		backup, err := instances.MinecraftInstanceBackupSettings(ctx, session, id)
		if err != nil {
			return setupDraft{}, err
		}
		if backup.Enabled {
			if !addServerBackupTimePattern.MatchString(backup.Time) {
				return setupDraft{}, errors.New("An existing backup schedule could not be checked.")
			}
			schedules = append(schedules, backup.Time)
		}
	}
	if defaults.SystemMemoryMiB > 0 {
		available := defaults.SystemMemoryMiB - draft.OtherMemoryMiB - defaults.SystemReserveRecommendedMiB
		current, _ := setupMemoryMiB(draft.Minecraft.ContainerMemory)
		if available >= 1024 && available < current {
			draft.Minecraft.ContainerMemory = strconv.Itoa(available) + "M"
			draft.Minecraft.JavaMemory = strconv.Itoa(available-512) + "M"
		}
	}
	javaPort, err := availableInstancePort(defaults.JavaPort, javaUsed)
	if err != nil {
		return setupDraft{}, err
	}
	bedrockPort, err := availableInstancePort(defaults.BedrockPort, bedrockUsed)
	if err != nil {
		return setupDraft{}, err
	}
	draft.Minecraft.JavaPort, draft.Minecraft.BedrockPort = strconv.Itoa(javaPort), strconv.Itoa(bedrockPort)
	if draft.Backups.Automatic {
		found := false
		startMinute := 270
		if addServerBackupTimePattern.MatchString(defaults.BackupDailyTime) {
			h, _ := strconv.Atoi(defaults.BackupDailyTime[:2])
			m, _ := strconv.Atoi(defaults.BackupDailyTime[3:])
			startMinute = h*60 + m
		}
		for offset := 0; offset < 1440; offset++ {
			minute := (startMinute + offset) % 1440
			time := fmt.Sprintf("%02d:%02d", minute/60, minute%60)
			safe := true
			for _, other := range schedules {
				if !separatedBackupTimes(time, other) {
					safe = false
					break
				}
			}
			if safe {
				draft.Backups.DailyTime = time
				found = true
				break
			}
		}
		if !found {
			draft.Backups.Automatic = false
		}
	}
	return draft, nil
}

func (a *App) addServerWizardStart(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	draft, err := a.newAddServerDraft(r.Context(), session, client)
	if err != nil {
		message := "Server defaults are unavailable. Check existing server configuration and appliance storage, then try again."
		if detail, ok := api.ErrorMessage(err); ok {
			message += " " + detail
		}
		http.Error(w, message, http.StatusServiceUnavailable)
		return
	}
	draft.Mode = "advanced"
	if strings.HasSuffix(r.URL.Path, "/recommended") {
		draft.Mode = "recommended"
		draft.DisplayName = r.FormValue("display_name")
		draft.Minecraft.ServerType = strings.ToLower(strings.TrimSpace(r.FormValue("server_type")))
		if !validSetupServerType(draft.Minecraft.ServerType) {
			http.Error(w, "Choose Paper, Purpur or Vanilla.", http.StatusBadRequest)
			return
		}
		if err := a.validateAddServerName(r.Context(), session, draft.DisplayName); err != nil {
			draft.Started = false
			w.WriteHeader(http.StatusBadRequest)
			a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), err.Error())
			return
		}
		draft.DisplayName = strings.TrimSpace(draft.DisplayName)
		draft.Server.BedrockEnabled = recommendedServerSupportsBedrock(draft.Minecraft.ServerType)
		draft.Server.Complete = true
		draft.Minecraft.ConnectionsComplete, draft.Minecraft.VersionComplete = true, true
		draft.Minecraft.ResourcesComplete, draft.Minecraft.Complete = true, true
		draft.CurrentStep = setupReviewStep
	}
	addServerReviews.delete(a, session)
	addServerDrafts.save(a, session, draft)
	http.Redirect(w, r, addServerBasePath, http.StatusSeeOther)
}

func (a *App) addServerWizardFixedStep(w http.ResponseWriter, r *http.Request) {
	session, _, identity, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	draft, exists := addServerDrafts.get(a, session)
	step := setupStorageStep
	if strings.HasSuffix(r.URL.Path, "/backups") {
		step = setupBackupsStep
	}
	if !exists || draft.CurrentStep != step {
		http.Redirect(w, r, addServerBasePath, http.StatusSeeOther)
		return
	}
	if step == setupBackupsStep {
		draft.Backups.Automatic = r.FormValue("backup_automatic") == "on"
		draft.Backups.DailyTime = r.FormValue("backup_daily_time")
		draft.Backups.Keep = r.FormValue("backup_keep")
	}
	if r.FormValue("direction") == "back" && !draft.CoreCreated {
		draft.CurrentStep--
	} else {
		if step == setupBackupsStep {
			if err := validateAddServerBackups(draft); err != nil {
				draft.Backups.Complete = false
				addServerDrafts.save(a, session, draft)
				w.WriteHeader(http.StatusBadRequest)
				a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), err.Error())
				return
			}
			draft.Backups.Complete = true
		}
		draft.CurrentStep++
	}
	addServerReviews.delete(a, session)
	addServerDrafts.save(a, session, draft)
	http.Redirect(w, r, addServerBasePath, http.StatusSeeOther)
}
func validateAddServerBackups(draft setupDraft) error {
	keep, err := strconv.Atoi(draft.Backups.Keep)
	if err != nil || keep < 1 || keep > 365 || !addServerBackupTimePattern.MatchString(draft.Backups.DailyTime) {
		return errors.New("Choose a daily backup time and keep between 1 and 365 backups.")
	}
	if draft.Backups.Automatic && !draft.BackupAvailable {
		return errors.New("Appliance backup storage is not configured. Turn automatic backups off to create this server.")
	}
	return nil
}

func (a *App) addServerReviewState(ctx context.Context, session string, draft setupDraft) (setupReviewState, error) {
	if cached, ok := addServerReviews.get(a, session); ok && cached.DraftUpdatedAt.Equal(draft.UpdatedAt) {
		return cached, nil
	}
	if !setupDraftReadyForReview(draft) {
		return setupReviewState{}, errors.New("Complete all six setup steps before creating the server.")
	}
	if err := a.validateAddServerName(ctx, session, draft.DisplayName, draft.CreatedID); err != nil {
		return setupReviewState{}, err
	}
	if err := validateSetupServer(draft.Server); err != nil {
		return setupReviewState{}, err
	}
	if err := validateSetupMinecraft(draft.Minecraft, draft.Defaults); err != nil {
		return setupReviewState{}, err
	}
	if err := validateAddServerBackups(draft); err != nil {
		return setupReviewState{}, err
	}
	preview, ok := a.api.(setupVersionPreviewAPI)
	if !ok {
		return setupReviewState{}, errors.New("Version information is unavailable.")
	}
	var version api.AdminVersionStatus
	var err error
	if draft.CoreCreated {
		version.SelectedCandidate = draft.CreatedRuntime.Version
		version.CrossplayCompatible = true
	} else {
		version, err = preview.AdminSetupVersionPreview(ctx, session, draft.Minecraft.VersionPolicy, draft.Minecraft.Version, draft.Server.BedrockEnabled, draft.Minecraft.ServerType)
	}
	if err != nil || version.SelectedCandidate == "" {
		return setupReviewState{}, errors.New("The selected Minecraft version could not be checked. Try again.")
	}
	if draft.Server.BedrockEnabled && !version.CrossplayCompatible {
		return setupReviewState{}, errors.New("The selected Minecraft version does not support Bedrock cross-play. Go back and choose a compatible version or disable Bedrock cross-play.")
	}
	javaPort, _ := strconv.Atoi(draft.Minecraft.JavaPort)
	bedrockPort, _ := strconv.Atoi(draft.Minecraft.BedrockPort)
	if !draft.Server.BedrockEnabled {
		bedrockPort = 0
	}
	maxPlayers, _ := strconv.Atoi(draft.Server.MaxPlayers)
	keep, _ := strconv.Atoi(draft.Backups.Keep)
	motd := strings.TrimSpace(draft.Server.MOTD)
	if draft.Server.MOTDAutomatic || motd == "" {
		motd = "JustVoxel " + setupServerTypeLabel(draft.Minecraft.ServerType) + " Minecraft " + version.SelectedCandidate + " Server"
	}
	runtime := api.InstanceRuntimeSettings{
		JavaPort: javaPort, BedrockEnabled: draft.Server.BedrockEnabled, BedrockPort: bedrockPort,
		JavaMemory: draft.Minecraft.JavaMemory, ContainerMemory: draft.Minecraft.ContainerMemory,
		Software: draft.Minecraft.ServerType, VersionPolicy: draft.Minecraft.VersionPolicy, Version: version.SelectedCandidate, PaperChannel: "default", ImageTag: draft.Minecraft.ImageTag,
		StartOnBoot: draft.StartOnBoot, EULAAccepted: true, Timezone: draft.Server.Timezone,
		GameMode: draft.Minecraft.GameMode, Difficulty: "normal", MaxPlayers: maxPlayers, MOTD: motd,
	}
	if draft.Minecraft.VersionPolicy == "latest" {
		runtime.Version = "LATEST"
	}
	if runtime.Software == "paper" && (draft.Minecraft.VersionPolicy == "latest" || (version.CandidateChannel != "" && version.CandidateChannel != "STABLE" && version.CandidateChannel != "RELEASE")) {
		runtime.PaperChannel = "experimental"
	}
	if draft.CoreCreated {
		runtime = draft.CreatedRuntime
	}
	state := setupReviewState{AddServer: true, DisplayName: draft.DisplayName, StartOnBoot: draft.StartOnBoot, BackupAvailable: draft.BackupAvailable,
		SetupMode: draft.Mode, ServerType: draft.Minecraft.ServerType, DraftUpdatedAt: draft.UpdatedAt,
		InstanceRuntime: runtime, InstanceBackup: api.InstanceBackupSettings{Enabled: draft.Backups.Automatic, Time: draft.Backups.DailyTime, Retention: keep},
	}
	// This is only the existing Review presentation model. It is never submitted
	// to legacy Setup Plan/Apply and does not represent an appliance transaction.
	state.Plan = api.AdminSetupPlanResponse{OK: true, Normalized: api.AdminSetupNormalizedPlan{
		Server: api.AdminSetupPlanServer{MOTD: motd, MaxPlayers: maxPlayers, BedrockEnabled: runtime.BedrockEnabled, Timezone: runtime.Timezone},
		Minecraft: api.AdminSetupPlanMinecraft{ServerType: runtime.Software, GameMode: runtime.GameMode, JavaMemory: runtime.JavaMemory, ContainerMemory: runtime.ContainerMemory,
			JavaPort: javaPort, BedrockPort: bedrockPort, ImageTag: runtime.ImageTag, RequestedVersionPolicy: draft.Minecraft.VersionPolicy, Version: version.SelectedCandidate},
		Backups: api.AdminSetupPlanBackups{Automatic: draft.Backups.Automatic, DailyTime: draft.Backups.DailyTime, Keep: keep},
	}}
	data, err := json.Marshal(struct {
		Name    string
		Runtime api.InstanceRuntimeSettings
		Backup  api.InstanceBackupSettings
	}{draft.DisplayName, runtime, state.InstanceBackup})
	if err != nil {
		return state, err
	}
	state.Plan.PlanFingerprint = fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	addServerReviews.save(a, session, state)
	return state, nil
}

func (a *App) addServerWizardReview(w http.ResponseWriter, r *http.Request) {
	session, _, identity, ok := a.setupWizardRequest(w, r, false)
	if !ok {
		return
	}
	draft, exists := addServerDrafts.get(a, session)
	if !exists {
		http.Redirect(w, r, addServerBasePath, http.StatusSeeOther)
		return
	}
	state, err := a.addServerReviewState(r.Context(), session, draft)
	message := draft.CreationError
	if err != nil {
		state = setupReviewState{AddServer: true, SetupMode: draft.Mode, DisplayName: draft.DisplayName}
		message = err.Error()
		w.WriteHeader(http.StatusBadRequest)
	}
	state.CreationBlocked = draft.CreationAttempted && !draft.CoreCreated
	a.renderSetupReview(w, identity, state, csrfFromRequest(r), message)
}

func (a *App) addServerWizardReviewBack(w http.ResponseWriter, r *http.Request) {
	session, _, _, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	draft, exists := addServerDrafts.get(a, session)
	if exists {
		if draft.Mode == "recommended" && !draft.CoreCreated {
			draft.Started = false
			draft.CurrentStep = setupServerStep
		} else {
			draft.CurrentStep = setupBackupsStep
		}
		addServerDrafts.save(a, session, draft)
		addServerReviews.delete(a, session)
	}
	http.Redirect(w, r, addServerBasePath, http.StatusSeeOther)
}

func (a *App) addServerWizardCreate(w http.ResponseWriter, r *http.Request) {
	session, _, identity, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	draft, exists := addServerDrafts.get(a, session)
	if !exists {
		http.Redirect(w, r, addServerBasePath, http.StatusSeeOther)
		return
	}
	state, reviewed := addServerReviews.get(a, session)
	if !reviewed || !state.DraftUpdatedAt.Equal(draft.UpdatedAt) || r.FormValue("plan_fingerprint") != state.Plan.PlanFingerprint {
		http.Error(w, "Setup changed since Review. Reload Review before creating the server.", http.StatusConflict)
		return
	}
	if r.FormValue("eula_accepted") != "on" {
		http.Error(w, "Accept the Minecraft EULA before creating the server.", http.StatusBadRequest)
		return
	}
	client, ok := a.api.(addServerAPI)
	if !ok {
		http.Error(w, "Independent server creation is unavailable.", http.StatusServiceUnavailable)
		return
	}
	fail := func(message string, blocked bool) {
		draft.CreationError = message
		addServerDrafts.save(a, session, draft)
		draft, _ = addServerDrafts.get(a, session)
		state.DraftUpdatedAt = draft.UpdatedAt
		state.CreationBlocked = blocked
		addServerReviews.save(a, session, state)
		w.WriteHeader(http.StatusConflict)
		a.renderSetupReview(w, identity, state, csrfFromRequest(r), message)
	}
	if draft.CreationAttempted && (draft.CreatedID == "" || state.CreationBlocked) {
		fail(draft.CreationError, true)
		return
	}
	if !draft.CreationAttempted {
		if err := a.validateAddServerName(r.Context(), session, draft.DisplayName); err != nil {
			fail(err.Error(), false)
			return
		}
		draft.CreationAttempted = true
		addServerDrafts.save(a, session, draft)
		result, err := client.CreateMinecraftInstance(r.Context(), session, api.CreateMinecraftInstanceRequest{DisplayName: draft.DisplayName, Runtime: state.InstanceRuntime})
		if addServerIDPattern.MatchString(result.ID) {
			draft.CreatedID = result.ID
		}
		if err != nil || !result.OK || draft.CreatedID == "" || result.State != "stopped" {
			message := "Creation outcome could not be verified. Check the registered servers before trying to create another server."
			if draft.CreatedID != "" {
				message = "Server registered, but configuration was not completed. The server and its data have been preserved."
			}
			if result.Error != "" {
				message += " " + result.Error
			}
			// A definite refusal before registration can safely be edited and retried.
			var responseErr *api.ResponseError
			if draft.CreatedID == "" && errors.As(err, &responseErr) && (responseErr.StatusCode == http.StatusBadRequest || responseErr.StatusCode == http.StatusConflict) {
				draft.CreationAttempted = false
				message = "Server creation was refused. " + responseErr.Message
			}
			fail(message, draft.CreationAttempted)
			return
		}
		draft.CoreCreated = true
		draft.CreatedRuntime = state.InstanceRuntime
	}
	if err := client.SetMinecraftInstanceBackupSettings(r.Context(), session, draft.CreatedID, state.InstanceBackup); err != nil {
		message := "Server created and left stopped. Backup settings were not confirmed. Use Back to adjust the backup schedule, select Create Server to retry only backup settings, or return to Minecraft Servers."
		if detail, ok := api.ErrorMessage(err); ok {
			message += " " + detail
		}
		fail(message, false)
		return
	}
	addServerReviews.delete(a, session)
	addServerDrafts.delete(a, session)
	http.Redirect(w, r, minecraftServersPath+"?created=yes", http.StatusSeeOther)
}
