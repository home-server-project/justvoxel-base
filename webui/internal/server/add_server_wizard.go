package server

import (
	"context"
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
		"POST /recommended":    a.addServerSimple,
		"POST /simple":         a.addServerSimple,
		"POST /server":         a.setupWizardSaveServer,
		"POST /connections":    a.setupWizardSaveConnections,
		"POST /resources":      a.setupWizardSaveResources,
		"GET /version-preview": a.setupWizardVersionPreview,
		"POST /storage":        a.setupWizardSaveStorage,
		"POST /backups":        a.setupWizardSaveBackups,
		"POST /navigate":       a.setupWizardNavigate,
		"POST /cancel":         a.setupWizardCancel,
		"GET /review":          a.addServerWizardReview,
		"POST /review/back":    a.addServerWizardReviewBack,
		"POST /review/cancel":  a.setupWizardCancel,
		"POST /review/apply":   a.setupWizardReviewApply,
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
		message = "Minecraft server created."
	}
	data := minecraftServersPageData{Title: "Minecraft Servers", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI, CSRF: csrfFromRequest(r), Identity: identity, Message: message, Configured: configuration.Configured}
	data.Servers, data.Error = a.minecraftServerInventory(r.Context(), session)
	a.renderAdminDiscovery(w, "minecraft_servers.html", data)
}

func (a *App) addServerWizardPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.setupWizardRequest(w, r, false)
	if !ok {
		return
	}
	if a.redirectCurrentSetupOperation(w, r, session, client) {
		return
	}
	draft, exists := addServerDrafts.get(a, session)
	if r.URL.Query().Get("new") == "1" {
		// A fresh launch must not discard an uncertain or partially created server.
		if exists && (draft.CreationAttempted || draft.CoreCreated) {
			http.Redirect(w, r, addServerBasePath+"/review", http.StatusSeeOther)
			return
		}
		addServerReviews.delete(a, session)
		addServerDrafts.delete(a, session)
		http.Redirect(w, r, addServerBasePath, http.StatusSeeOther)
		return
	}
	if !exists {
		draft = setupDraft{AddServer: true}
	}
	if draft.CurrentStep == setupReviewStep {
		http.Redirect(w, r, addServerBasePath+"/review", http.StatusSeeOther)
		return
	}
	if draft.Started && (draft.CurrentStep == setupStorageStep || draft.CurrentStep == setupBackupsStep) {
		inventory, err := client.AdminStorage(r.Context(), session)
		if err != nil {
			a.handleAdminDiscoveryError(w, r, err)
			return
		}
		draft.Inventory = inventory
		addServerDrafts.save(a, session, draft)
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
	inventory, err := client.AdminStorage(ctx, session)
	if err != nil {
		return setupDraft{}, err
	}
	draft := draftFromSetupDefaults(defaults, inventory)
	draft.AddServer, draft.StartOnBoot = true, true
	draft.BackupAvailable = true
	javaUsed := map[int]bool{configuration.Minecraft.JavaPort: true}
	bedrockUsed := map[int]bool{}
	if configuration.Minecraft.BedrockEnabled {
		bedrockUsed[configuration.Minecraft.BedrockPort] = true
	}
	if len(ids) == 0 && configuration.Configured {
		draft.OtherMemoryMiB, _ = setupMemoryMiB(configuration.Minecraft.ContainerMemory)
		// Legacy runtime evidence is not sufficient to promise a simultaneous start.
		draft.StartAfterUnavailable, draft.StartBootUnavailable = true, true
	}
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
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return setupDraft{}, errors.New("Existing server inventory is ambiguous.")
		}
		seen[id] = true
		runtime, err := instances.MinecraftInstanceConfiguration(ctx, session, id)
		if err != nil {
			return setupDraft{}, errors.New("Existing server settings could not be checked. Resolve the existing server's configuration before adding another.")
		}
		if runtime.Software == "bedrock" {
			javaUsed[runtime.BedrockTCPPort], bedrockUsed[runtime.BedrockTCPPort] = true, true
			for port := runtime.BedrockUDPStart; port <= runtime.BedrockUDPEnd; port++ {
				javaUsed[port], bedrockUsed[port] = true, true
			}
		}
		javaUsed[runtime.JavaPort] = true
		if runtime.BedrockEnabled {
			bedrockUsed[runtime.BedrockPort] = true
		}
		memory, valid := setupMemoryMiB(runtime.ContainerMemory)
		if !valid {
			return setupDraft{}, errors.New("Existing server memory could not be checked.")
		}
		draft.OtherMemoryMiB += memory
		if runtime.StartOnBoot {
			draft.BootMemoryMiB += memory
		}
		statusClient, ok := a.api.(interface {
			MinecraftInstanceStatus(context.Context, string, string) (api.MinecraftInstanceStatus, error)
		})
		if !ok {
			draft.StartAfterUnavailable = true
		} else {
			status, err := statusClient.MinecraftInstanceStatus(ctx, session, id)
			if err != nil || status.ID != id {
				draft.StartAfterUnavailable = true
			} else {
				switch status.State {
				case "running", "starting":
					draft.RunningMemoryMiB += memory
				case "stopped", "configured", "failed":
				default:
					draft.StartAfterUnavailable = true
				}
			}
		}
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
	draft.StartCapacityUnknown, draft.BootCapacityUnknown = draft.StartAfterUnavailable, draft.StartBootUnavailable
	candidate, valid := setupMemoryMiB(draft.Minecraft.ContainerMemory)
	draft.StartAfterUnavailable = draft.StartAfterUnavailable || !valid || defaults.SystemMemoryMiB <= 0 || draft.RunningMemoryMiB+candidate+1024 > defaults.SystemMemoryMiB
	draft.StartBootUnavailable = draft.StartBootUnavailable || !valid || defaults.SystemMemoryMiB <= 0 || draft.BootMemoryMiB+candidate+1024 > defaults.SystemMemoryMiB
	draft.StartAfterSetup, draft.StartOnBoot = !draft.StartAfterUnavailable, !draft.StartBootUnavailable

	// Geyser reserves TCP as well as UDP; defaults must respect both.
	for port := range bedrockUsed {
		javaUsed[port] = true
	}
	for port := range javaUsed {
		bedrockUsed[port] = true
	}
	javaPort, err := availableInstancePort(defaults.JavaPort, javaUsed)
	if err != nil {
		return setupDraft{}, err
	}
	bedrockUsed[javaPort] = true
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
	session, client, _, ok := a.setupWizardRequest(w, r, true)
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

	addServerReviews.delete(a, session)
	addServerDrafts.save(a, session, draft)
	http.Redirect(w, r, addServerBasePath, http.StatusSeeOther)
}

func (a *App) addServerReviewState(ctx context.Context, session string, draft setupDraft) (setupReviewState, error) {
	if !setupDraftReadyForReview(draft) {
		return setupReviewState{}, errors.New("Complete all six setup steps before creating the server.")
	}
	client, ok := a.api.(adminDiscoveryAPI)
	if !ok {
		return setupReviewState{}, errors.New("Setup planning is unavailable.")
	}
	state, err, _, ready := a.setupReviewState(ctx, session, client, draft)
	if !ready {
		return state, err
	}
	return state, nil
}

func (a *App) addServerWizardReview(w http.ResponseWriter, r *http.Request) {
	session, _, identity, ok := a.setupWizardRequest(w, r, false)
	if !ok {
		return
	}
	draft, exists := addServerDrafts.get(a, session)
	if !exists || !setupDraftReadyForReview(draft) {
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
			draft.SimpleState = 3
			draft.CurrentStep = setupServerStep
		} else {
			draft.CurrentStep = setupBackupsStep
		}
		addServerDrafts.save(a, session, draft)
		addServerReviews.delete(a, session)
	}
	http.Redirect(w, r, addServerBasePath, http.StatusSeeOther)
}
