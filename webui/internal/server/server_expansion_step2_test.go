package server

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/home-server-project/justvoxel-webui/internal/api"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestAdvancedServerExpansionCards(t *testing.T) {
	app, _ := newAddServerTestApp(t)
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/start", nil))
	body := addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	previous := -1
	for _, group := range setupSoftwareGroups() {
		at := strings.Index(html.UnescapeString(body), "<legend>"+group.Name+"</legend>")
		if at <= previous {
			t.Fatalf("group missing or reordered: %s", group.Name)
		}
		previous = at
		for _, choice := range group.Software {
			input := regexp.MustCompile(`<input[^>]+name="server_type"[^>]+value="` + choice.ID + `"[^>]*>`).FindString(body)
			if input == "" || strings.Contains(input, "disabled") {
				t.Fatalf("wrong availability: %s", choice.ID)
			}
			if _, err := os.Stat("static/server-software/" + choice.ID + ".svg"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(setupWizardSteps) != 6 {
		t.Fatal("Advanced steps changed")
	}
}

func TestSpigotSetupUsesAutomaticRuntime(t *testing.T) {
	for _, policy := range []string{"recommended", "pinned"} {
		if err := validateSetupVersion(setupMinecraftDraft{ServerType: "spigot", VersionPolicy: policy, Version: "1.21.8", ImageTag: "stable"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ game, image string }{{"1.20.5", "java21-jdk"}, {"1.20.6", "java21-jdk"}, {"1.21", "java21-jdk"}, {"1.21.11", "java21-jdk"}, {"26.1", "java25-jdk"}, {"26.3", "java25-jdk"}} {
		if setupRuntimeImage("spigot", tc.game) != tc.image {
			t.Fatal(tc)
		}
	}
	if setupRuntimeImage("spigot", "1.20.4") != "" {
		t.Fatal("unsupported release")
	}
	if validateSetupVersion(setupMinecraftDraft{ServerType: "spigot", VersionPolicy: "recommended", ImageTag: "stable", ResolvedVersion: "26.3", RuntimeImageTag: "java21-jdk"}) == nil {
		t.Fatal("wrong derived JDK accepted")
	}
}

func TestCrossplayOverlayPreservesExactLoader(t *testing.T) {
	current := api.InstanceRuntimeSettings{Software: "fabric", LoaderVersion: "0.19.5", Version: "26.3", VersionPolicy: "pinned", ImageTag: "java25"}
	request := httptest.NewRequest("POST", "/", strings.NewReader(url.Values{"tab": {"crossplay"}, "java_port": {"25565"}, "bedrock_port": {"19132"}, "bedrock_enabled": {"on"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	next, err := selectedSettingsOverlay(request, current)
	if err != nil || next.LoaderVersion != current.LoaderVersion || next.Software != current.Software || next.Version != current.Version || !next.BedrockEnabled {
		t.Fatalf("loader or software changed: %+v %v", next, err)
	}
}

func TestUnavailableServerCardsRejectForgedJavaSetup(t *testing.T) {
	for _, software := range []string{"unknown-software"} {
		t.Run(software, func(t *testing.T) {
			app, err := New(setupWizardClient(), Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			defer firstRunSetupDrafts.delete(app, "session-token")
			startSetup(t, app)
			values := validServerValues()
			values.Set("server_type", software)
			response := saveServerStep(t, app, values)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("forged %s selection returned %d", software, response.Code)
			}
			draft, _ := firstRunSetupDrafts.get(app, "session-token")
			if draft.Minecraft.ServerType == software || draft.CurrentStep != setupServerStep {
				t.Fatal("unavailable software entered Java setup")
			}
		})
	}
}
