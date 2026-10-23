package server

import (
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
	data, err := os.ReadFile("templates/setup_wizard.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	start := strings.Index(source, `<legend>Server software</legend>`)
	end := strings.Index(source[start:], `</fieldset>`)
	cards := regexp.MustCompile(`(?s)<label class="setup-server-type server-software-card [^"]+">.*?</label>`).FindAllString(source[start:start+end], -1)
	types := []string{"paper", "purpur", "spigot", "vanilla", "bedrock", "fabric", "forge", "neoforge", "quilt"}
	descriptions := []string{
		"Recommended · Plugins and managed Bedrock cross-play.", "Plugins and managed Bedrock cross-play.", "Plugins and managed Bedrock cross-play.",
		"Original Minecraft · Java only · no plugins or mods.", "Separate Bedrock only · no plugins or mods.", "Mod loader and managed Bedrock cross-play.",
		"Mod loader.", "Mod loader and managed Bedrock cross-play.", "Mod loader.",
	}
	if len(cards) != len(types) {
		t.Fatalf("got %d cards", len(cards))
	}
	for i, software := range types {
		for _, want := range []string{`value="` + software + `"`, descriptions[i], `/static/server-software/` + software + `.svg`} {
			if !strings.Contains(cards[i], want) {
				t.Fatalf("%s missing %q", software, want)
			}
		}
		if _, err := os.Stat("static/server-software/" + software + ".svg"); err != nil {
			t.Fatal(err)
		}
		unavailable := software == "bedrock" || software == "fabric" || software == "forge" || software == "neoforge" || software == "quilt"
		if strings.Contains(cards[i], " disabled") != unavailable {
			t.Fatalf("wrong availability: %s", software)
		}
		if validSetupServerType(software) == unavailable {
			t.Fatalf("wizard accepted unavailable software %s", software)
		}
	}
	// Vanilla and Bedrock are consecutive in the existing three-column grid.
	if !strings.Contains(cards[3], `value="vanilla"`) || !strings.Contains(cards[4], `value="bedrock"`) {
		t.Fatal("Java/Bedrock row changed")
	}
}

func TestSpigotSetupRequiresCompleteExplicitSelection(t *testing.T) {
	for _, tc := range []struct {
		policy, version, image string
		valid                  bool
	}{
		{"recommended", "", "stable", false}, {"pinned", "1.21.8", "stable", false},
		{"pinned", "1.21.8", "java21-jdk", true}, {"pinned", "26.3", "java25-jdk", true},
		{"pinned", "26.3", "java21-jdk", false}, {"pinned", "1.20.4", "java21-jdk", false},
	} {
		err := validateSetupVersion(setupMinecraftDraft{ServerType: "spigot", VersionPolicy: tc.policy, Version: tc.version, ImageTag: tc.image})
		if (err == nil) != tc.valid {
			t.Fatalf("%+v: %v", tc, err)
		}
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
	for _, software := range []string{"bedrock", "fabric", "forge", "neoforge", "quilt"} {
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
