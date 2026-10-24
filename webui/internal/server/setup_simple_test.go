package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func TestSimpleSetupStatesAndReview(t *testing.T) {
	for _, gameMode := range []string{"survival", "creative", "adventure", "spectator"} {
		t.Run(gameMode, func(t *testing.T) {
			app, _ := newAddServerTestApp(t)
			page := func() string {
				t.Helper()
				response := addWizardRequest(app, http.MethodGet, "", nil)
				if response.Code != 200 {
					t.Fatalf("entry: %d %s", response.Code, response.Body.String())
				}
				body := response.Body.String()
				if !strings.Contains(body, "Start advanced setup") {
					t.Fatal("Advanced card missing")
				}
				for _, field := range []string{"java_memory", "java_port", "image_tag", "loader_version", "motd", "max_players", "backup_keep"} {
					if strings.Contains(body, `name="`+field+`"`) {
						t.Fatalf("technical field exposed: %s", field)
					}
				}
				return body
			}
			initial := page()
			inputs := regexp.MustCompile(`<input[^>]+name="family"[^>]*>`).FindAllString(initial, -1)
			if len(inputs) != 4 || strings.Contains(inputs[3], "disabled") {
				t.Fatal("four enabled families missing")
			}
			if strings.Contains(initial, `name="display_name"`) {
				t.Fatal("name in State 1")
			}
			for _, family := range []string{"plugins", "mods", "vanilla"} {
				requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/simple", url.Values{"family": {family}}))
				softwarePage := page()
				for _, software := range []string{"paper", "purpur", "spigot", "fabric", "neoforge", "forge", "quilt", "vanilla"} {
					allowed := false
					for _, choice := range simpleSoftware(family) {
						if choice.ID == software {
							allowed = true
						}
					}
					if strings.Contains(softwarePage, `value="`+software+`"`) != allowed {
						t.Fatalf("wrong family: %s / %s", family, software)
					}
				}
				requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/simple", url.Values{"direction": {"back"}}))
				page()
			}
			requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/simple", url.Values{"family": {"plugins"}}))
			if rr := addWizardRequest(app, http.MethodPost, "/simple", url.Values{"server_type": {"fabric"}}); rr.Code != 400 {
				t.Fatal("cross-family selection accepted")
			}
			requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/simple", url.Values{"server_type": {"paper"}}))
			if body := page(); !strings.Contains(body, `name="display_name"`) || strings.Contains(body, `name="server_type"`) {
				t.Fatal("wrong State 3 fields")
			}
			requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/simple", url.Values{"direction": {"back"}}))
			if !strings.Contains(page(), `value="paper" required checked`) {
				t.Fatal("Back lost software")
			}
			requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/simple", url.Values{"server_type": {"paper"}}))
			requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/simple", url.Values{"display_name": {"Family"}, "game_mode": {gameMode}}))
			draft, _ := addServerDrafts.get(app, "session-token")
			if !setupDraftReadyForReview(draft) || draft.Minecraft.ResolvedVersion == "" || draft.Minecraft.GameMode != gameMode {
				t.Fatalf("not ready for existing Review: %+v", draft)
			}
			requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/review/back", nil))
			if !strings.Contains(page(), `name="display_name"`) {
				t.Fatal("Review Back did not return to State 3")
			}
			rr := addWizardRequest(app, http.MethodPost, "/cancel", nil)
			if rr.Header().Get("Location") != "/?workspace=minecraft-servers" {
				t.Fatal("cancel destination")
			}
			if _, exists := addServerDrafts.get(app, "session-token"); exists {
				t.Fatal("cancel retained draft")
			}
		})
	}
}

type exactSetupPreviewAPI struct {
	*addServerTestAPI
	status api.AdminVersionStatus
	err    error
}

func (f *exactSetupPreviewAPI) AdminSetupVersionPreview(context.Context, string, string, string, bool, string) (api.AdminVersionStatus, error) {
	return f.status, f.err
}

func TestSetupResolutionPinsAndOptionalCrossplay(t *testing.T) {
	for _, software := range []string{"paper", "purpur", "vanilla", "spigot", "fabric", "neoforge", "forge", "quilt"} {
		t.Run(software, func(t *testing.T) {
			_, base := newAddServerTestApp(t)
			loader := ""
			if setupNeedsExactRuntime(software) && software != "spigot" {
				loader = "0.16.10"
			}
			preview := &exactSetupPreviewAPI{addServerTestAPI: base, status: api.AdminVersionStatus{SelectedCandidate: "1.21.8", ImageTag: setupRuntimeImage(software, "1.21.8"), LoaderVersion: loader}}
			app, err := New(preview, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			for _, policy := range []string{"recommended", "pinned"} {
				draft := draftFromSetupDefaults(base.defaults, api.AdminStorageDiscovery{})
				draft.Minecraft.ServerType, draft.Minecraft.VersionPolicy, draft.Minecraft.Version = software, policy, "1.21.8"
				if err := app.resolveSetupRuntime(context.Background(), "session-token", &draft, true); err != nil {
					t.Fatal(err)
				}
				if draft.Server.BedrockEnabled {
					t.Fatal("unproven cross-play enabled")
				}
				mc := effectiveSetupMinecraft(draft.Minecraft)
				if mc.Version != "1.21.8" || mc.VersionPolicy != "pinned" || mc.LoaderVersion != loader {
					t.Fatalf("lost exact pins: %+v", mc)
				}
				request, err := setupPlanRequestFromDraft(draft)
				if err != nil {
					t.Fatal(err)
				}
				if request.Minecraft.LoaderVersion != loader || request.Minecraft.Version != "1.21.8" {
					t.Fatal("pins lost before planner")
				}
				if recommendedServerSupportsBedrock(software) {
					draft.Server.BedrockEnabled = true
					if err := app.resolveSetupRuntime(context.Background(), "session-token", &draft, false); err == nil {
						t.Fatal("unproven cross-play accepted")
					}
					preview.status.CrossplayCompatible, preview.status.ManagedCrossplayAvailable = true, true
					if err := app.resolveSetupRuntime(context.Background(), "session-token", &draft, false); err != nil {
						t.Fatal(err)
					}
					preview.status.CrossplayCompatible, preview.status.ManagedCrossplayAvailable = false, false
				}
			}
			if loader != "" {
				draft := draftFromSetupDefaults(base.defaults, api.AdminStorageDiscovery{})
				draft.Minecraft.ServerType = software
				draft.Server.BedrockEnabled = false
				preview.status.LoaderVersion = ""
				if app.resolveSetupRuntime(context.Background(), "session-token", &draft, false) == nil {
					t.Fatal("missing loader accepted")
				}
			}
			preview.err = errors.New("upstream unavailable")
			draft := draftFromSetupDefaults(base.defaults, api.AdminStorageDiscovery{})
			draft.Minecraft.ServerType = software
			draft.Server.BedrockEnabled = false
			if app.resolveSetupRuntime(context.Background(), "session-token", &draft, true) == nil {
				t.Fatal("resolution failure accepted")
			}
		})
	}
}

func TestAdvancedJavaSoftwareCanContinueWithoutCrossplay(t *testing.T) {
	for _, software := range []string{"paper", "purpur", "spigot", "vanilla", "fabric", "neoforge", "forge", "quilt"} {
		t.Run(software, func(t *testing.T) {
			_, base := newAddServerTestApp(t)
			loader := ""
			if setupNeedsExactRuntime(software) && software != "spigot" {
				loader = "0.18.0"
			}
			preview := &exactSetupPreviewAPI{addServerTestAPI: base, status: api.AdminVersionStatus{SelectedCandidate: "1.21.8", ImageTag: setupRuntimeImage(software, "1.21.8"), LoaderVersion: loader}}
			app, err := New(preview, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { addServerDrafts.delete(app, "session-token"); addServerReviews.delete(app, "session-token") })
			requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/start", nil))
			values := validServerValues()
			values.Set("server_type", software)
			values.Set("display_name", "Family")
			requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/server", values))
			requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/connections", url.Values{"java_port": {"25567"}, "bedrock_port": {"19134"}, "image_tag": {"stable"}, "version_policy": {"recommended"}, "direction": {"next"}}))
			draft, _ := addServerDrafts.get(app, "session-token")
			if draft.CurrentStep != setupMemoryStep || draft.Server.BedrockEnabled || draft.Minecraft.LoaderVersion != loader {
				t.Fatalf("Java setup blocked or pin lost: %+v", draft)
			}
		})
	}
}

func TestSimpleAllJavaSoftwareUsesExistingReview(t *testing.T) {
	for _, software := range []string{"paper", "purpur", "spigot", "vanilla", "fabric", "neoforge", "forge", "quilt"} {
		t.Run(software, func(t *testing.T) {
			_, base := newAddServerTestApp(t)
			loader := ""
			if setupNeedsExactRuntime(software) && software != "spigot" {
				loader = "0.18.0"
			}
			preview := &exactSetupPreviewAPI{addServerTestAPI: base, status: api.AdminVersionStatus{SelectedCandidate: "1.21.8", LoaderVersion: loader, ImageTag: setupRuntimeImage(software, "1.21.8")}}
			app, err := New(preview, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { addServerDrafts.delete(app, "session-token"); addServerReviews.delete(app, "session-token") })
			review := recommendedAddReview(t, app, software)
			if review.Request.Minecraft.LoaderVersion != loader || review.Plan.Normalized.Minecraft.LoaderVersion != loader || review.Plan.Normalized.Server.BedrockEnabled {
				t.Fatal("Review lost exact runtime or enabled unproven cross-play")
			}
			if review.Request.Minecraft.VersionPolicy != "pinned" {
				t.Fatal("Simple setup left a moving runtime")
			}
		})
	}
}
