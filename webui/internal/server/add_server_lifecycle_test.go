package server

import (
	"html"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestAddServerSimpleFamilyLanes(t *testing.T) {
	app, _ := newAddServerTestApp(t)
	body := addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	labels := regexp.MustCompile(`<label[^>]*>.*?name="family".*?</label>`).FindAllString(body, -1)
	if len(labels) != 3 {
		t.Fatalf("got %d family lanes; want three", len(labels))
	}
	for i, family := range []struct {
		value, title string
		software     []string
	}{
		{"plugins", "Plugins", []string{"paper", "purpur", "spigot", "pufferfish", "leaf"}},
		{"mods", "Mods", []string{"fabric", "neoforge", "forge", "quilt"}},
		{"original", "Vanilla Java · Bedrock", []string{"vanilla", "bedrock"}},
	} {
		lane := labels[i]
		if !strings.Contains(lane, `value="`+family.value+`"`) || !strings.Contains(lane, "<strong>"+family.title+"</strong>") || strings.Count(lane, `type="radio"`) != 1 {
			t.Fatalf("wrong family lane: %s", lane)
		}
		for _, software := range family.software {
			if !strings.Contains(lane, "/static/server-software/"+software+".svg") {
				t.Fatalf("%s missing visual item %s", family.value, software)
			}
		}
	}
	for _, family := range []string{"vanilla", "bedrock"} {
		if len(simpleSoftware(family)) != 1 {
			t.Fatalf("stale family compatibility lost: %s", family)
		}
	}
}

func TestAddServerSimpleLaterStatesHaveOnlyBackAndNext(t *testing.T) {
	for _, software := range []string{"vanilla", "bedrock"} {
		t.Run(software, func(t *testing.T) {
			app, _ := newAddServerTestApp(t)
			requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/simple", url.Values{"family": {"original"}}))
			for state := 2; state <= 3; state++ {
				body := addWizardRequest(app, http.MethodGet, "", nil).Body.String()
				form := regexp.MustCompile(`(?s)<form[^>]*data-simple-setup>.*?</form>`).FindString(body)
				if strings.Count(form, ">Back</button>") != 1 || strings.Count(form, ">Next</button>") != 1 || strings.Contains(form, "Cancel") || strings.Contains(form, "/cancel") {
					t.Fatalf("State %d inner actions: %s", state, form)
				}
				if !strings.Contains(body, `<form method="post" action="/minecraft/servers/add/cancel"><input type="hidden" name="csrf" value="csrf-token">`) {
					t.Fatal("outer cancel must POST with CSRF")
				}
				if state == 2 {
					if strings.Count(form, `name="server_type"`) != 2 || !strings.Contains(form, `value="vanilla"`) || !strings.Contains(form, `value="bedrock"`) {
						t.Fatal("combined family must offer Vanilla and Bedrock")
					}
					requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/simple", url.Values{"server_type": {software}}))
				} else if strings.Contains(form, `value="spectator"`) != (software == "vanilla") {
					t.Fatal("Spectator must be offered for Java only")
				}
			}
		})
	}
}

func TestAddServerFreshLaunchAndRefresh(t *testing.T) {
	for _, state := range []string{"simple-2", "simple-3", "advanced-3"} {
		t.Run(state, func(t *testing.T) {
			app, _ := newAddServerTestApp(t)
			draft := setupDraft{AddServer: true, Mode: "recommended", SimpleState: 2, SimpleFamily: "original"}
			marker := "Choose software"
			if state == "simple-3" {
				draft.SimpleState, draft.Minecraft.ServerType = 3, "vanilla"
				marker = `name="display_name"`
			} else if state == "advanced-3" {
				requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/start", nil))
				draft, _ = setupDrafts.get(app, "session-token")
				draft.CurrentStep = setupMemoryStep
				marker = `name="java_memory"`
			}
			setupDrafts.save(app, "session-token", draft)
			before, _ := setupDrafts.get(app, "session-token")
			for refresh := 0; refresh < 2; refresh++ {
				response := addWizardRequest(app, http.MethodGet, "", nil)
				if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), marker) {
					t.Fatalf("refresh lost %s: %d %s", state, response.Code, response.Body.String())
				}
				if after, ok := setupDrafts.get(app, "session-token"); !ok || !reflect.DeepEqual(before, after) {
					t.Fatal("ordinary clean GET changed active draft")
				}
			}
			setupReviews.save(app, "session-token", setupReviewState{DraftUpdatedAt: before.UpdatedAt})
			response := addWizardRequest(app, http.MethodGet, "?new=1", nil)
			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != addServerBasePath {
				t.Fatalf("fresh launch must redirect to clean URL: %d %q", response.Code, response.Header().Get("Location"))
			}
			if _, ok := setupDrafts.get(app, "session-token"); ok {
				t.Fatal("fresh launch retained draft")
			}
			if _, ok := setupReviews.get(app, "session-token"); ok {
				t.Fatal("fresh launch retained review")
			}
			body := addWizardRequest(app, http.MethodGet, "", nil).Body.String()
			if !strings.Contains(body, "Choose family") || strings.Contains(body, `name="display_name"`) {
				t.Fatal("fresh launch did not return to State 1")
			}
		})
	}
}

func TestAddServerFreshLaunchPreservesCreationSafety(t *testing.T) {
	for _, coreCreated := range []bool{false, true} {
		app, _ := newAddServerTestApp(t)
		draft := setupDraft{AddServer: true, CreationAttempted: true, CoreCreated: coreCreated, CurrentStep: setupReviewStep}
		setupDrafts.save(app, "session-token", draft)
		before, _ := setupDrafts.get(app, "session-token")
		setupReviews.save(app, "session-token", setupReviewState{DraftUpdatedAt: before.UpdatedAt})
		response := addWizardRequest(app, http.MethodGet, "?new=1", nil)
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != addServerBasePath+"/review" {
			t.Fatal("fresh launch bypassed creation safety")
		}
		if after, ok := setupDrafts.get(app, "session-token"); !ok || !reflect.DeepEqual(before, after) {
			t.Fatal("fresh launch discarded creation evidence")
		}
		if _, ok := setupReviews.get(app, "session-token"); !ok {
			t.Fatal("fresh launch discarded creation review")
		}
		if response := addWizardRequest(app, http.MethodPost, "/start", nil); response.Code != http.StatusConflict {
			t.Fatal("creation guard no longer blocks repeated Start")
		}
	}
}

func TestAddServerAdvancedThreeLanesAndLaunchTarget(t *testing.T) {
	app, _ := newAddServerTestApp(t)
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/start", nil))
	body := addWizardRequest(app, http.MethodGet, "", nil).Body.String()
	lanes := regexp.MustCompile(`(?s)<fieldset class="setup-server-types setup-server-types-compact">.*?</fieldset>`).FindAllString(body, -1)
	if len(lanes) != 3 || !strings.Contains(body, "setup-software-lanes") {
		t.Fatal("Advanced must use three full-width lanes")
	}
	for i, expected := range []struct {
		heading  string
		software []string
	}{
		{"Plugins · Cross-play", []string{"paper", "purpur", "spigot", "pufferfish", "leaf"}},
		{"Mod loaders · Cross-play", []string{"fabric", "neoforge", "forge", "quilt"}},
		{"Vanilla Java · Bedrock", []string{"vanilla", "bedrock"}},
	} {
		renderedLane := html.UnescapeString(lanes[i])
		if !strings.Contains(renderedLane, "<legend>"+expected.heading+"</legend>") {
			t.Fatalf("lane %d heading", i)
		}
		inputs := regexp.MustCompile(`name="server_type" value="([^"]+)"`).FindAllStringSubmatch(lanes[i], -1)
		var software []string
		for _, input := range inputs {
			software = append(software, input[1])
		}
		if !reflect.DeepEqual(software, expected.software) {
			t.Fatalf("lane %d software: got %v; want %v", i, software, expected.software)
		}
	}
	if strings.Contains(body, "<legend>Mods</legend>") || setupSoftwareGroups()[1].Description != "" {
		t.Fatal("Advanced Mods must have one heading without a separate description")
	}
	if !strings.Contains(lanes[2], "Original Minecraft · no plugins or mods") || !strings.Contains(lanes[2], "Native Bedrock server · no plugins or mods") {
		t.Fatal("lane descriptions missing")
	}
	markup := correctionSource(t, "templates/minecraft_servers.html")
	if !strings.Contains(markup, `/minecraft/servers/add?new=1{{else}}/setup{{end}}"{{if .Configured}} target="justvoxel-add-server"{{end}}>Add Server</a>`) {
		t.Fatal("Add Server must reuse a named tab without noopener")
	}
	if !strings.Contains(markup, `<a href="/minecraft/how-to-connect?server={{.ContextID}}" target="_blank" rel="noopener">How to connect</a>`) {
		t.Fatal("How to connect changed")
	}
}

func TestAddServerFreshLaunchRequiresCurrentAuthorization(t *testing.T) {
	for _, role := range []string{"viewer", "operator"} {
		app, client := newAddServerTestApp(t)
		setupDrafts.save(app, "session-token", setupDraft{AddServer: true, SimpleState: 2})
		before, _ := setupDrafts.get(app, "session-token")
		client.role = role
		response := addWizardRequest(app, http.MethodGet, "?new=1", nil)
		if response.Code != http.StatusForbidden {
			t.Fatal("unauthorized fresh launch accepted")
		}
		if after, ok := setupDrafts.get(app, "session-token"); !ok || !reflect.DeepEqual(before, after) {
			t.Fatal("unauthorized launch cleared draft")
		}
	}
}
