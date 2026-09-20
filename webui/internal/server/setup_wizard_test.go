package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func setupWizardClient() *fakeDiscoveryAPI {
	client := &fakeDiscoveryAPI{}
	client.defaults.MOTD = "JustVoxel Java and Bedrock Server"
	client.defaults.MaxPlayers = 10
	client.defaults.BedrockEnabled = false
	client.defaults.Timezone = "America/Toronto"
	client.defaults.JavaMemory = "4G"
	client.defaults.ContainerMemory = "6G"
	client.defaults.JavaPort = 25565
	client.defaults.BedrockPort = 19132
	client.defaults.ImageTag = "stable"
	client.defaults.VersionMode = "pinned"
	client.defaults.SystemMemoryMiB = 8192
	client.defaults.SystemReserveMinimumMiB = 1024
	client.defaults.SystemReserveRecommendedMiB = 2048
	return client
}

func startSetup(t *testing.T, app *App) {
	t.Helper()
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/start", "csrf=csrf-token"))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup" {
		t.Fatalf("start returned %d %q: %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
}

func saveServerStep(t *testing.T, app *App, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if values == nil {
		values = url.Values{}
	}
	values.Set("csrf", "csrf-token")
	return httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/server", values.Encode()))
}

func saveMinecraftStep(t *testing.T, app *App, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if values == nil {
		values = url.Values{}
	}
	values.Set("csrf", "csrf-token")
	return httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/minecraft", values.Encode()))
}

func validServerValues() url.Values {
	return url.Values{
		"motd":            {"Family Minecraft"},
		"max_players":     {"20"},
		"bedrock_enabled": {"on"},
		"timezone":        {"America/Toronto"},
	}
}

func validMinecraftValues() url.Values {
	return url.Values{
		"java_memory":      {"4G"},
		"container_memory": {"6G"},
		"java_port":        {"25565"},
		"bedrock_port":     {"19132"},
		"image_tag":        {"stable"},
		"version_policy":   {"recommended"},
		"version":          {""},
		"direction":        {"next"},
	}
}

func TestSetupWizardShowsWelcomeForUnconfiguredAdministrator(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("setup welcome returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Welcome to JustVoxel", "Start setup", "Safe to explore", "Server", "Minecraft", "Storage", "Backups", "Review", "mjust setup", "setup-terminal-note",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("setup welcome missing %q: %s", want, body)
		}
	}
	if client.configurationHit != 1 {
		t.Fatalf("configuration discovery calls = %d, want 1", client.configurationHit)
	}
}

func TestSetupWizardStartsWithFriendlyServerDefaults(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("server step returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Step 1 of 5", "Server name / welcome message", "Family", "Maximum players", "Bedrock cross-play", "America/Toronto", "Technical name: MOTD", `list="timezone-options"`, `id="timezone-options"`, `value="UTC"`} {
		if want == "Family" {
			continue
		}
		if !strings.Contains(body, want) {
			t.Fatalf("server step missing %q: %s", want, body)
		}
	}
	if !strings.Contains(body, "JustVoxel Java and Bedrock Server") {
		t.Fatal("server step did not use setup defaults")
	}
	if client.defaultsHit != 1 {
		t.Fatalf("setup defaults calls = %d, want 1", client.defaultsHit)
	}
}

func TestSetupWizardUsesCompactAlignedActions(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)

	step1 := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	body := step1.Body.String()
	for _, want := range []string{"setup-step-actions", "setup-step-navigation", "Cancel setup", ">Continue</button>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("server actions missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Save server choices and continue") {
		t.Fatal("server step still uses the long continue label")
	}

	if rr := saveServerStep(t, app, validServerValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("server save returned %d", rr.Code)
	}
	step2 := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	body = step2.Body.String()
	for _, want := range []string{"Cancel setup", ">Back</button>", ">Continue</button>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Minecraft actions missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Save Minecraft choices and continue") {
		t.Fatal("Minecraft step still uses the long continue label")
	}

	template, err := assets.ReadFile("templates/setup_wizard.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(template)
	for _, oldLabel := range []string{"Save server choices and continue", "Save Minecraft choices and continue", "Save storage choice and continue", "Save backup choices and continue"} {
		if strings.Contains(markup, oldLabel) {
			t.Fatalf("setup template still contains old action label %q", oldLabel)
		}
	}
	if strings.Count(markup, ">Continue</button>") != 4 {
		t.Fatalf("setup template Continue button count = %d, want 4", strings.Count(markup, ">Continue</button>"))
	}
}

func TestSetupWizardAdvancedStorageKeepsReturnContext(t *testing.T) {
	content, err := assets.ReadFile("templates/setup_wizard.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `href="/settings/storage-provision?from=setup">Advanced Storage</a>`) {
		t.Fatal("first-run Advanced Storage link does not preserve setup return context")
	}
}
func TestSetupWizardServerStepValidatesAndPersistsChoices(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)

	rr := saveServerStep(t, app, validServerValues())
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup" {
		t.Fatalf("server save returned %d %q: %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	body := page.Body.String()
	for _, want := range []string{"Step 2 of 5", "Minecraft game memory", "Technical name: Java heap", "Maximum Minecraft memory", "container memory limit", "8.0 GiB detected", "2.0 GiB", "1.0 GiB", "20-player limit", "Recommended", "High memory", "/static/settings.js", "Minecraft container release channel", "Stable (recommended)", "Latest", "Custom", `id="image-tag"`, `value="stable"`, "Recommended version", "Always newest version", "Specific version", `id="specific-version-field"`, "Bedrock compatibility is checked before setup."} {
		if !strings.Contains(body, want) {
			t.Fatalf("Minecraft step missing %q: %s", want, body)
		}
	}
}

func TestSetupWizardServerStepRejectsInvalidPlayerLimit(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)

	values := validServerValues()
	values.Set("max_players", "0")
	rr := saveServerStep(t, app, values)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid server step returned %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Maximum players must be a positive number") || !strings.Contains(rr.Body.String(), `value="0"`) {
		t.Fatalf("invalid server step did not explain and preserve value: %s", rr.Body.String())
	}
	draft, ok := firstRunSetupDrafts.get(app, "session-token")
	if !ok || draft.CurrentStep != 1 {
		t.Fatalf("invalid server form advanced draft: %#v", draft)
	}
}

func TestSetupWizardSpecificVersionRequiresExplicitValue(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)
	if rr := saveServerStep(t, app, validServerValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("server save returned %d", rr.Code)
	}

	values := validMinecraftValues()
	values.Set("version_policy", "pinned")
	values.Set("version", "")
	rr := saveMinecraftStep(t, app, values)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Specific Minecraft version is invalid") {
		t.Fatalf("missing specific version was not rejected: %d %s", rr.Code, rr.Body.String())
	}
}

func TestSetupWizardMinecraftStepAdvancesOnlyAfterValidation(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)
	if rr := saveServerStep(t, app, validServerValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("server save returned %d", rr.Code)
	}

	bad := validMinecraftValues()
	bad.Set("container_memory", "4G")
	rr := saveMinecraftStep(t, app, bad)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "must be larger than Minecraft game memory") {
		t.Fatalf("invalid memory was not rejected: %d %s", rr.Code, rr.Body.String())
	}
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	if draft.CurrentStep != 2 {
		t.Fatalf("invalid Minecraft form advanced to step %d", draft.CurrentStep)
	}

	good := validMinecraftValues()
	good.Set("java_memory", "5G")
	good.Set("container_memory", "7G")
	rr = saveMinecraftStep(t, app, good)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup" {
		t.Fatalf("valid Minecraft form returned %d %q: %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Step 3 of 5") || !strings.Contains(page.Body.String(), "Storage configuration") {
		t.Fatalf("Minecraft step did not advance to storage: %d %s", page.Code, page.Body.String())
	}
}

func TestSetupWizardMinecraftBackPreservesUnsavedValues(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)
	_ = saveServerStep(t, app, validServerValues())

	values := validMinecraftValues()
	values.Set("java_memory", "5G")
	values.Set("container_memory", "7G")
	values.Set("image_tag", "java21")
	values.Set("direction", "back")
	back := saveMinecraftStep(t, app, values)
	if back.Code != http.StatusSeeOther {
		t.Fatalf("Minecraft back returned %d: %s", back.Code, back.Body.String())
	}
	serverPage := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if !strings.Contains(serverPage.Body.String(), "Step 1 of 5") || !strings.Contains(serverPage.Body.String(), "Family Minecraft") {
		t.Fatalf("server choices were not preserved: %s", serverPage.Body.String())
	}
	_ = saveServerStep(t, app, validServerValues())
	minecraftPage := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	for _, want := range []string{`value="5G"`, `value="7G"`, `value="java21"`, `value="custom" selected`, "Custom container tag"} {
		if !strings.Contains(minecraftPage.Body.String(), want) {
			t.Fatalf("Minecraft draft lost %q: %s", want, minecraftPage.Body.String())
		}
	}
}

func TestSetupWizardGenericNavigationCannotSkipRealForms(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/navigate", "csrf=csrf-token&direction=next"))
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("generic next returned %d", rr.Code)
	}
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	if draft.CurrentStep != 1 {
		t.Fatalf("generic navigation skipped required server form to step %d", draft.CurrentStep)
	}
}

func TestSetupWizardCancelDiscardsDraft(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")

	startSetup(t, app)
	cancel := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/cancel", "csrf=csrf-token"))
	if cancel.Code != http.StatusSeeOther || cancel.Header().Get("Location") != "/setup" {
		t.Fatalf("cancel returned %d %q", cancel.Code, cancel.Header().Get("Location"))
	}

	welcome := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if welcome.Code != http.StatusOK || !strings.Contains(welcome.Body.String(), "Welcome to JustVoxel") {
		t.Fatalf("cancel did not discard draft: %d %s", welcome.Code, welcome.Body.String())
	}
	if strings.Contains(welcome.Body.String(), "Step 1 of 5") {
		t.Fatal("cancelled draft still rendered as active setup")
	}
}

func TestSetupWizardRejectsMutationWithoutCSRF(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://example/setup/start", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("setup start without CSRF status = %d, want 403", rr.Code)
	}
	if _, ok := firstRunSetupDrafts.get(app, "session-token"); ok {
		t.Fatal("setup draft was created without CSRF")
	}
}

func TestSetupWizardIsAdministratorOnly(t *testing.T) {
	client := setupWizardClient()
	client.role = "operator"
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator setup status = %d, want 403", rr.Code)
	}
	if client.configurationHit != 0 {
		t.Fatalf("privileged configuration discovery ran for operator %d times", client.configurationHit)
	}
}

func TestConfiguredApplianceCannotEnterFirstRunWizard(t *testing.T) {
	client := setupWizardClient()
	client.configuration.Configured = true
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
		t.Fatalf("configured setup returned %d %q", rr.Code, rr.Header().Get("Location"))
	}
}

func TestDashboardIncludesFirstRunRedirectForUnconfiguredAdministrator(t *testing.T) {
	dashboard, err := assets.ReadFile("templates/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dashboard), `/static/first-run.js`) {
		t.Fatal("dashboard does not load first-run detection")
	}

	script, err := assets.ReadFile("static/first-run.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(script)
	for _, want := range []string{`role !== "administrator"`, `configured !== false`, `window.location.replace("/setup")`} {
		if !strings.Contains(content, want) {
			t.Fatalf("first-run redirect missing %q", want)
		}
	}
}
