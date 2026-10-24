package server

import (
	"context"
	"errors"
	"github.com/home-server-project/justvoxel-webui/internal/api"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

type setupSoftwareChoice struct{ ID, Name, Description string }

func simpleSoftware(family string) []setupSoftwareChoice {
	switch family {
	case "plugins":
		return []setupSoftwareChoice{{"paper", "Paper", "Plugins · Cross-play"}, {"purpur", "Purpur", "Plugins · Cross-play"}, {"spigot", "Spigot", "Plugins · Cross-play"}}
	case "mods":
		return []setupSoftwareChoice{{"fabric", "Fabric", "Mod loader · Cross-play supported when compatible"}, {"neoforge", "NeoForge", "Mod loader · Cross-play supported when compatible"}, {"forge", "Forge", "Mod loader · Java only"}, {"quilt", "Quilt", "Mod loader · Java only"}}
	case "original":
		return []setupSoftwareChoice{{"vanilla", "Vanilla", "Original Minecraft · no plugins or mods"}, {"bedrock", "Bedrock", "Native Bedrock server · no plugins or mods"}}
	case "bedrock":
		return []setupSoftwareChoice{{"bedrock", "Bedrock", "Native Bedrock · no plugins or mods"}}
	case "vanilla":
		return []setupSoftwareChoice{{"vanilla", "Vanilla", "Java only · no plugins or mods"}}
	}
	return nil
}

func setupNeedsExactRuntime(software string) bool {
	return software != "paper" && software != "purpur" && software != "vanilla"
}

func setupRuntimeImage(software, version string) string {
	image := ""
	if regexp.MustCompile(`^1\.(20\.[56]|21(\.([0-9]|1[01]))?)$`).MatchString(version) {
		image = "java21"
	}
	if regexp.MustCompile(`^26\.[1-9][0-9]*(\.(0|[1-9][0-9]*))?$`).MatchString(version) {
		image = "java25"
	}
	if image != "" && software == "spigot" {
		image += "-jdk"
	}
	return image
}

// Keep the user's channel/version choice separate from the exact runtime sent
// to the authoritative planner. New software never gets a moving update policy.
func effectiveSetupMinecraft(mc setupMinecraftDraft) setupMinecraftDraft {
	if mc.ResolvedVersion != "" {
		mc.Version, mc.VersionPolicy = mc.ResolvedVersion, "pinned"
	}
	if mc.RuntimeImageTag != "" {
		mc.ImageTag = mc.RuntimeImageTag
	}
	return mc
}

func (a *App) resolveSetupRuntime(ctx context.Context, session string, draft *setupDraft, automaticCrossplay bool) error {
	if draft.Minecraft.ServerType == "bedrock" {
		client, ok := a.api.(interface {
			AdminSetupNativeBedrockPreview(context.Context, string, int) (api.AdminVersionStatus, error)
		})
		if !ok {
			return errors.New("Native Bedrock version information is unavailable")
		}
		players, err := strconv.Atoi(draft.Server.MaxPlayers)
		if err != nil || players < 1 || players > 1000 {
			return errors.New("Bedrock maximum players must be between 1 and 1000")
		}
		status, err := client.AdminSetupNativeBedrockPreview(ctx, session, players)
		if err != nil || !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(\.[0-9]+)?$`).MatchString(status.SelectedCandidate) || status.ImageTag != "stable" || status.BedrockTCPPort < 1 || status.BedrockUDPStart < 1 || status.BedrockUDPEnd > 65535 || status.BedrockUDPEnd-status.BedrockUDPStart+1 < players {
			return errors.New("An exact official Bedrock release and safe connection ports could not be verified. Try again.")
		}
		if draft.Minecraft.ResolvedVersion != "" && (draft.Minecraft.ResolvedVersion != status.SelectedCandidate || draft.Minecraft.BedrockTCPPort != status.BedrockTCPPort || draft.Minecraft.BedrockUDPStart != status.BedrockUDPStart || draft.Minecraft.BedrockUDPEnd != status.BedrockUDPEnd) {
			return errors.New("The recommended Bedrock release or connection ports changed. Return to Server and review setup again.")
		}
		draft.Server.BedrockEnabled = false
		draft.Minecraft.JavaPort, draft.Minecraft.JavaMemory, draft.Minecraft.BedrockPort = "0", "", "0"
		draft.Minecraft.VersionPolicy, draft.Minecraft.Version, draft.Minecraft.ResolvedVersion = "pinned", status.SelectedCandidate, status.SelectedCandidate
		draft.Minecraft.ImageTag, draft.Minecraft.RuntimeImageTag, draft.Minecraft.LoaderVersion = "stable", "stable", ""
		draft.Minecraft.BedrockTCPPort, draft.Minecraft.BedrockUDPStart, draft.Minecraft.BedrockUDPEnd = status.BedrockTCPPort, status.BedrockUDPStart, status.BedrockUDPEnd
		return nil
	}
	if automaticCrossplay {
		draft.Server.BedrockEnabled = false
	}
	draft.Minecraft.ResolvedVersion, draft.Minecraft.RuntimeImageTag, draft.Minecraft.LoaderVersion = "", "", ""
	if err := validateSetupVersion(draft.Minecraft); err != nil {
		return err
	}
	if err := validateSetupConnectionDraft(*draft); err != nil {
		return err
	}
	client, ok := a.api.(setupVersionPreviewAPI)
	if !ok {
		return errors.New("Version information is unavailable. Try again before continuing.")
	}
	bedrock := draft.Server.BedrockEnabled
	if automaticCrossplay {
		bedrock = false
	}
	status, err := client.AdminSetupVersionPreview(ctx, session, draft.Minecraft.VersionPolicy, draft.Minecraft.Version, bedrock, draft.Minecraft.ServerType)
	if err != nil || status.SelectedCandidate == "" {
		return errors.New("An exact compatible Minecraft runtime could not be resolved. Try again or choose a supported Specific version.")
	}
	if draft.Minecraft.VersionPolicy == "pinned" && status.SelectedCandidate != draft.Minecraft.Version {
		return errors.New("The resolved Minecraft release does not match the selected release.")
	}
	if automaticCrossplay && recommendedServerSupportsBedrock(draft.Minecraft.ServerType) {
		// Ask about this exact release; cross-play must never choose a different
		// game/loader pair or prevent the verified Java server from being created.
		crossplay, crossErr := client.AdminSetupVersionPreview(ctx, session, "pinned", status.SelectedCandidate, true, draft.Minecraft.ServerType)
		bedrock = crossErr == nil && crossplay.SelectedCandidate == status.SelectedCandidate && crossplay.LoaderVersion == status.LoaderVersion && crossplay.CrossplayCompatible && (draft.Minecraft.ServerType != "fabric" && draft.Minecraft.ServerType != "neoforge" || crossplay.ManagedCrossplayAvailable)
	}
	if bedrock && !automaticCrossplay && (!status.CrossplayCompatible || ((draft.Minecraft.ServerType == "fabric" || draft.Minecraft.ServerType == "neoforge") && !status.ManagedCrossplayAvailable)) {
		return errors.New("Managed cross-play compatibility could not be confirmed. Disable cross-play to continue with Java setup.")
	}
	if setupNeedsExactRuntime(draft.Minecraft.ServerType) {
		required := setupRuntimeImage(draft.Minecraft.ServerType, status.SelectedCandidate)
		if required == "" || status.ImageTag != required {
			return errors.New("The resolved runtime does not match its Minecraft release.")
		}
		if draft.Minecraft.ServerType != "spigot" && !regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}$`).MatchString(status.LoaderVersion) {
			return errors.New("An exact stable loader could not be resolved.")
		}
		draft.Minecraft.RuntimeImageTag, draft.Minecraft.LoaderVersion = required, status.LoaderVersion
	}
	draft.Server.BedrockEnabled = bedrock
	if automaticCrossplay || setupNeedsExactRuntime(draft.Minecraft.ServerType) {
		draft.Minecraft.ResolvedVersion = status.SelectedCandidate
	}
	return nil
}

func (a *App) setupWizardSimple(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	if a.redirectCurrentSetupOperation(w, r, session, client) {
		return
	}
	draft, exists := setupDrafts.get(a, session)
	if !exists || draft.Mode != "recommended" {
		draft = setupDraft{AddServer: isAddServerRequest(r), Mode: "recommended", SimpleState: 1}
	}
	fail := func(message string) {
		setupDrafts.save(a, session, draft)
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), message)
	}
	if r.FormValue("direction") == "back" {
		if draft.SimpleState > 1 {
			draft.SimpleState--
		}
		if draft.SimpleState == 1 {
			draft.Minecraft.ServerType = ""
		}
	} else {
		switch draft.SimpleState {
		case 1:
			family := r.FormValue("family")
			if len(simpleSoftware(family)) == 0 {
				fail("Choose a usable server family.")
				return
			}
			draft.SimpleFamily, draft.SimpleState = family, 2
			draft.Minecraft.ServerType = ""
		case 2:
			selected := r.FormValue("server_type")
			valid := false
			for _, choice := range simpleSoftware(draft.SimpleFamily) {
				if choice.ID == selected {
					valid = true
				}
			}
			if !valid {
				fail("Choose software from the selected family.")
				return
			}
			draft.Minecraft.ServerType, draft.SimpleState = selected, 3
		case 3:
			name, gameMode := strings.TrimSpace(r.FormValue("display_name")), r.FormValue("game_mode")
			draft.DisplayName, draft.Minecraft.GameMode = name, gameMode
			if err := a.validateAddServerName(r.Context(), session, name); err != nil {
				fail(err.Error())
				return
			}
			if !validSetupGameMode(gameMode) || draft.Minecraft.ServerType == "bedrock" && gameMode == "spectator" {
				fail("Choose a valid game mode.")
				return
			}
			resolved, err := a.newSetupDraft(r.Context(), session, client, draft.AddServer)
			if err != nil {
				fail("Server defaults are unavailable. Check existing server configuration and storage, then try again.")
				return
			}
			resolved.Mode, resolved.SimpleState, resolved.SimpleFamily = "recommended", 3, draft.SimpleFamily
			resolved.DisplayName, resolved.Minecraft.ServerType, resolved.Minecraft.GameMode = name, draft.Minecraft.ServerType, gameMode
			resolved.Minecraft.VersionPolicy = "recommended"
			if err = a.resolveSetupRuntime(r.Context(), session, &resolved, true); err != nil {
				fail(err.Error())
				return
			}
			if err = a.resolveRecommendedSetupStorage(r.Context(), session, &resolved); err != nil {
				fail(err.Error())
				return
			}
			if err = validateSetupMinecraft(resolved.Minecraft, resolved.Defaults); err != nil {
				fail(err.Error())
				return
			}
			resolved.Server.Complete, resolved.Storage.Complete, resolved.Backups.Complete = true, true, true
			resolved.Minecraft.ConnectionsComplete, resolved.Minecraft.VersionComplete, resolved.Minecraft.ResourcesComplete, resolved.Minecraft.Complete = true, true, true, true
			resolved.CurrentStep = setupReviewStep
			draft = resolved
			a.recordSetupDraftDiagnosticBestEffort(r.Context(), client, session, "simple WebUI setup defaults accepted", draft)
		default:
			fail("Restart Simple setup.")
			return
		}
	}
	setupReviews.delete(a, session)
	setupDrafts.save(a, session, draft)
	http.Redirect(w, r, setupBasePath(r), http.StatusSeeOther)
}

type setupSoftwareGroup struct {
	Name, Description string
	Software          []setupSoftwareChoice
}

func setupSoftwareGroups() []setupSoftwareGroup {
	return []setupSoftwareGroup{
		{Name: "Plugins + managed cross-play", Software: simpleSoftware("plugins")},
		{Name: "Mod loaders · Cross-play: Fabric · NeoForge", Software: simpleSoftware("mods")},
		{Name: "Vanilla Java · Bedrock", Software: simpleSoftware("original")},
	}
}
