package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const setupReviewFingerprint = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type fakeSetupPlanningAPI struct {
	*fakeDiscoveryAPI
	plan        api.AdminSetupPlanResponse
	planErr     error
	planHit     int
	lastRequest api.AdminSetupPlanRequest
}

func (f *fakeSetupPlanningAPI) AdminSetupPlan(_ context.Context, session string, request api.AdminSetupPlanRequest) (api.AdminSetupPlanResponse, error) {
	if session != "session-token" {
		return api.AdminSetupPlanResponse{}, api.ErrUnauthorized
	}
	f.planHit++
	f.lastRequest = request
	return f.plan, f.planErr
}

func setupReviewClient() *fakeSetupPlanningAPI {
	base := setupWizardStorageClient()
	return &fakeSetupPlanningAPI{
		fakeDiscoveryAPI: base,
		plan: api.AdminSetupPlanResponse{
			OK:              true,
			SchemaVersion:   "v1",
			PlanFingerprint: setupReviewFingerprint,
			Normalized: api.AdminSetupNormalizedPlan{
				Server: api.AdminSetupPlanServer{
					MOTD: "Normalized Family Server", MaxPlayers: 20,
					BedrockEnabled: true, Timezone: "America/Toronto",
				},
				Minecraft: api.AdminSetupPlanMinecraft{
					GameMode:   "survival",
					JavaMemory: "4G", ContainerMemory: "6G", JavaPort: 25565, BedrockPort: 19132,
					ImageTag: "stable", RequestedVersionPolicy: "recommended", VersionPolicy: "recommended", Version: "1.21.8",
					SystemMemoryMiB: 8192, SystemReserveMiB: 2048,
				},
				Storage: api.AdminSetupPlanStorage{
					Type: "system", Path: "/var/lib/justvoxel/minecraft", Model: "JustVoxel system storage", SystemDisk: true,
				},
				Backups: api.AdminSetupPlanBackups{
					Type: "system", Path: "/var/lib/justvoxel/backups", Model: "JustVoxel system storage", SystemDisk: true,
					Automatic: true, DailyTime: "04:30", Schedule: "*-*-* 04:30:00", Keep: 7,
				},
			},
			Warnings: []api.AdminSetupPlanWarning{{
				Code: "same_physical_disk", Message: "Minecraft data and backups use JustVoxel system storage.",
			}},
		},
	}
}

func advanceSetupToReview(t *testing.T, app *App) {
	t.Helper()
	startSetup(t, app)
	if rr := saveServerStep(t, app, validServerValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("server save returned %d: %s", rr.Code, rr.Body.String())
	}
	if rr := saveConnectionsStep(t, app, validConnectionValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("connections save returned %d: %s", rr.Code, rr.Body.String())
	}
	if rr := saveResourcesStep(t, app, validResourceValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("resources save returned %d: %s", rr.Code, rr.Body.String())
	}
	if rr := saveMinecraftStep(t, app, validMinecraftValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("Minecraft save returned %d: %s", rr.Code, rr.Body.String())
	}
	storage := url.Values{
		"csrf": {"csrf-token"}, "storage_type": {"system"}, "storage_path": {"/var/lib/justvoxel/minecraft"}, "direction": {"next"},
	}
	if rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", storage.Encode())); rr.Code != http.StatusSeeOther {
		t.Fatalf("storage save returned %d: %s", rr.Code, rr.Body.String())
	}
	backups := url.Values{
		"csrf": {"csrf-token"}, "backup_automatic": {"on"}, "backup_daily_time": {"04:30"}, "backup_keep": {"7"},
		"backup_type": {"system"}, "backup_path": {"/var/lib/justvoxel/backups"}, "direction": {"next"},
	}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/backups", backups.Encode()))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup/review" {
		t.Fatalf("backup save returned %d %q: %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
}

func eulaForm(accepted bool, fingerprint string) string {
	values := url.Values{"csrf": {"csrf-token"}, "plan_fingerprint": {fingerprint}}
	if accepted {
		values.Set("eula_accepted", "on")
	}
	return values.Encode()
}

func TestSetupReviewUsesAuthoritativeNormalizedPlan(t *testing.T) {
	client := setupReviewClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("review returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"<h1>Set up JustVoxel</h1>", "Step 7 of 7", "<h2>Review</h2>", "Connections", "Version", "Normalized Family Server", "20", "1.21.8",
		"Recommended", "Game mode</dt><dd>Survival",
		"Backups are on the same disk", "If this disk fails, both Minecraft and its backups could be lost.", "Minecraft End User License Agreement", "https://www.minecraft.net/eula",
		"/static/setup-review.css", "/static/setup-operation.js",
		`name="plan_fingerprint" value="` + setupReviewFingerprint + `"`, "Download configuration", "data-setup-eula-dialog", "Accept and continue",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("review missing %q: %s", want, body)
		}
	}
	summaryStart := strings.Index(body, `class="setup-review-summary"`)
	warningsStart := strings.Index(body, `class="setup-review-warnings"`)
	if summaryStart < 0 || warningsStart <= summaryStart {
		t.Fatal("review summary and warnings are out of order")
	}
	summary := body[summaryStart:warningsStart]
	for _, want := range []string{"Server software</dt><dd>Paper", "Welcome message</dt><dd>Normalized Family Server", "Game mode</dt><dd>Survival", "Java</dt><dd>Enabled, port 25565", "Bedrock cross-play</dt><dd>Enabled, port 19132", "Minecraft version</dt><dd>1.21.8", "Version policy</dt><dd>Recommended", "Minecraft game memory</dt><dd>4G", "Automatic backups</dt><dd>Enabled", "Daily at 04:30", "JustVoxel system storage"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("human summary missing %q", want)
		}
	}
	for _, technical := range []string{"/var/lib/", "<code>", "UUID:", "Device:"} {
		if strings.Contains(summary, technical) {
			t.Fatalf("human summary exposed %q", technical)
		}
	}
	if strings.Contains(body, "Technical details") || strings.Contains(body, "Configuration validated.") || strings.Contains(body, "Review the configuration validated by JustVoxel.") {
		t.Fatal("review retains removed status or technical details")
	}
	if !strings.Contains(body, `<div class="setup-review-header"><h2>Review</h2><a class="button-link secondary" href="/setup/review/configuration">Download configuration</a></div>`) {
		t.Fatal("review heading and configuration download must share a row")
	}
	if client.planHit != 1 {
		t.Fatalf("setup planner calls = %d, want 1", client.planHit)
	}
	if client.lastRequest.Server.MOTD != "Family Minecraft" || client.lastRequest.Minecraft.VersionPolicy != "recommended" || client.lastRequest.Minecraft.GameMode != "survival" {
		t.Fatalf("review planner did not receive draft values: %#v", client.lastRequest)
	}
	if strings.Contains(body, `type="password"`) || strings.Contains(body, `name="backup_password"`) {
		t.Fatal("review rendered a password field")
	}
	if strings.Contains(body, "Ready to configure") || strings.Contains(body, "Your setup is validated and ready to apply.") {
		t.Fatal("review still renders the removed readiness message")
	}
	for _, forbidden := range []string{"setup-eula-card", "Validated plan: <code>", "Management Agent"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("review exposed %q", forbidden)
		}
	}
}

func TestSetupReviewLatestShowsResolvedVersion(t *testing.T) {
	client := setupReviewClient()
	client.plan.Normalized.Minecraft.RequestedVersionPolicy = "latest"
	client.plan.Normalized.Minecraft.VersionPolicy = "latest"
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("review returned %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "<dt>Minecraft version</dt><dd>1.21.8</dd>") ||
		!strings.Contains(rr.Body.String(), "<dt>Version policy</dt><dd>Latest</dd>") ||
		strings.Contains(rr.Body.String(), "Version: LATEST") {
		t.Fatalf("review did not show the exact Latest candidate: %s", rr.Body.String())
	}
}

func TestSetupReviewKeepsTechnicalValuesInDownloadAndUsesPlanWarnings(t *testing.T) {
	client := setupReviewClient()
	client.plan.Normalized.Server.BedrockEnabled = false
	client.plan.Normalized.Backups.Automatic = false
	client.plan.Normalized.Storage.Device = "/dev/vdb1"
	client.plan.Normalized.Storage.UUID = "data-uuid"
	client.plan.Normalized.Backups.Device = "/dev/vdc1"
	client.plan.Normalized.Backups.UUID = "backup-uuid"
	client.plan.Warnings = nil
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("review returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	summary := body
	for _, want := range []string{"Bedrock cross-play</dt><dd>Disabled", "Automatic backups</dt><dd>Disabled"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q", want)
		}
	}
	for _, technical := range []string{"/dev/vdb1", "/dev/vdc1", "data-uuid", "backup-uuid"} {
		if strings.Contains(summary, technical) {
			t.Fatalf("technical value %q leaked into human review", technical)
		}
	}
	download := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review/configuration", ""))
	if download.Code != http.StatusOK || !strings.Contains(download.Body.String(), "/dev/vdb1") || !strings.Contains(download.Body.String(), "/dev/vdc1") || !strings.Contains(download.Body.String(), "data-uuid") || !strings.Contains(download.Body.String(), "backup-uuid") {
		t.Fatal("configuration download lost technical storage values")
	}
	if strings.Contains(body, "Backups are on the same disk") {
		t.Fatal("review invented a same-disk warning absent from the plan")
	}
}

func TestFirstRunTemplatesUseUserFacingWording(t *testing.T) {
	for _, name := range []string{"setup_wizard.html", "setup_review.html", "setup_progress.html"} {
		t.Run(name, func(t *testing.T) {
			markup, err := assets.ReadFile("templates/" + name)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(markup), "Management Agent") {
				t.Fatal("first-run page exposes Management Agent terminology")
			}
		})
	}
}

func TestRecommendedAndAdvancedModesUseSameAuthoritativePlanRequest(t *testing.T) {
	client := setupWizardClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")

	values := url.Values{"csrf": {"csrf-token"}, "server_type": {"paper"}}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/recommended", values.Encode()))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup/review" {
		t.Fatalf("recommended setup returned %d %q: %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
	recommended, ok := firstRunSetupDrafts.get(app, "session-token")
	if !ok {
		t.Fatal("recommended setup draft missing")
	}
	advanced := recommended
	advanced.Mode = "advanced"

	recommendedRequest, err := setupPlanRequestFromDraft(recommended)
	if err != nil {
		t.Fatal(err)
	}
	advancedRequest, err := setupPlanRequestFromDraft(advanced)
	if err != nil {
		t.Fatal(err)
	}
	if recommendedRequest != advancedRequest {
		t.Fatalf("setup mode changed authoritative Agent plan request:\nrecommended=%#v\nadvanced=%#v", recommendedRequest, advancedRequest)
	}
	reviewSource, err := assets.ReadFile("templates/setup_review.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(reviewSource)
	if strings.Count(markup, `<div class="setup-review-header">`) != 1 || strings.Count(markup, `class="setup-review-summary"`) != 1 ||
		!strings.Contains(markup, `{{if ne .SetupMode "recommended"}}`) {
		t.Fatal("Recommended and Advanced must share one Review presentation")
	}
}

func TestSetupReviewConfigurationDownloadExcludesSecrets(t *testing.T) {
	client := setupReviewClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review/configuration", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("configuration download returned %d: %s", rr.Code, rr.Body.String())
	}
	if disposition := rr.Header().Get("Content-Disposition"); !strings.Contains(disposition, "justvoxel-setup.txt") {
		t.Fatalf("configuration download disposition = %q", disposition)
	}
	body := rr.Body.String()
	for _, want := range []string{"JustVoxel setup configuration", "Normalized Family Server", "Minecraft version: 1.21.8", "Game mode: Survival", "/var/lib/justvoxel/backups", "Passwords and other secrets are never included"} {
		if !strings.Contains(body, want) {
			t.Fatalf("configuration snapshot missing %q: %s", want, body)
		}
	}
	if strings.Contains(strings.ToLower(body), "password:") {
		t.Fatalf("configuration snapshot contains a password field: %s", body)
	}
}

func TestSetupReviewUsesCompactNavigationAndResponsiveLayout(t *testing.T) {
	client := setupReviewClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("review returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Cancel setup", ">Back</button>", "setup-review-panel", "setup-review-content", "setup-step-actions setup-actions-split", "setup-review-header", "<h3>Server</h3>", "<h3>Connections</h3>", "<h3>Minecraft</h3>", "<h3>Storage &amp; backups</h3>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("review layout missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Back to backups") {
		t.Fatal("review still uses the old Back to backups label")
	}
	if strings.Contains(body, "setup-execution-placeholder") || strings.Contains(body, "Apply this validated plan") {
		t.Fatal("review still renders the redundant Configure presentation")
	}
	if strings.Count(body, ">Configure JustVoxel</button>") != 1 || strings.Contains(body, "Ready to configure") {
		t.Fatal("review must show one Configure button without a readiness message")
	}

	css, err := assets.ReadFile("static/setup-review.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(css)
	for _, want := range []string{
		"setup-review-header",
		"white-space:nowrap",
		".setup-review-summary{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));",
		".setup-review-content{flex:1;min-height:0;overflow:visible}",
		".setup-review-panel>.setup-step-actions",
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("responsive review CSS missing %q", want)
		}
	}

	if strings.Contains(body, "Technical details") || strings.Contains(styles, "setup-review-technical") || strings.Contains(styles, "setup-review-table") {
		t.Fatal("review retains removed technical details")
	}
	if !strings.Contains(body, `class="setup-review-content"`) || !strings.Contains(body, `class="setup-step-actions setup-actions-split"`) {
		t.Fatal("review content and actions must remain separate")
	}

	setupCSS, err := assets.ReadFile("static/setup.css")
	if err != nil {
		t.Fatal(err)
	}
	setupStyles := string(setupCSS)
	for _, want := range []string{
		"max-width:1180px", ".setup-step-actions", "@media(max-width:850px)",
		".setup-body{display:flex;flex-direction:column;min-height:100dvh}",
		".setup-shell{display:flex;flex:1;flex-direction:column",
		".setup-shell>.project-footer",
		"@media(min-width:851px)", "height:100dvh", "overflow:hidden",
		".setup-body .setup-wizard-panel,.setup-body .setup-review-panel{flex:1;min-height:0;overflow:hidden}",
		".setup-body .setup-wizard-panel .setup-form>.setup-step-content",
		"overflow-y:auto", ".setup-body .setup-review-panel>.setup-review-content",
		".setup-body .setup-wizard-panel .setup-form>.setup-step-actions{position:static;flex:none",
		".setup-body .setup-review-panel>.setup-step-actions{flex:none",
		".setup-body .setup-wizard-panel .setup-form>.setup-step-content{display:contents}",
	} {
		if !strings.Contains(setupStyles, want) {
			t.Fatalf("stable desktop setup CSS missing %q", want)
		}
	}
	if strings.Contains(setupStyles, ".setup-body .setup-operation-panel{height:") {
		t.Fatal("operation and result pages must remain content-sized")
	}
	wizard, err := assets.ReadFile("templates/setup_wizard.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(wizard), `class="setup-step-content"`) != 6 || strings.Count(string(wizard), `class="setup-step-actions"`) != 6 {
		t.Fatal("each advanced wizard step needs separate content and action regions")
	}
	for _, want := range []string{`class="setup-shell"`, `class="title-row setup-title-row"`, `class="setup-progress"`, `class="panel setup-step-panel setup-wizard-panel"`, `class="setup-form"`} {
		if !strings.Contains(string(wizard), want) {
			t.Fatalf("wizard structure missing %q", want)
		}
	}
	progress, err := assets.ReadFile("templates/setup_progress.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(progress), "setup-operation-panel") || strings.Contains(string(progress), "setup-wizard-panel") || strings.Contains(string(progress), "setup-review-panel") {
		t.Fatal("operation and result pages must remain content-sized")
	}
}

func TestSetupReviewConfigureButtonDisabledForInvalidPlan(t *testing.T) {
	client := setupReviewClient()
	client.plan.OK = false
	client.plan.Error = "Setup needs attention."
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid review returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`class="setup-configure-disabled" role="group" aria-disabled="true" tabindex="0" aria-describedby="setup-configure-help"`, `<button type="button" disabled aria-describedby="setup-configure-help">Configure JustVoxel</button>`, `<span class="setup-configure-tooltip" id="setup-configure-help" role="tooltip">Setup is not ready. Resolve the items above first.</span>`} {
		if !strings.Contains(body, want) {
			t.Fatalf("invalid review missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `form="setup-apply-form" data-setup-configure`) || strings.Contains(body, `id="setup-apply-form"`) || strings.Count(body, ">Configure JustVoxel</button>") != 1 {
		t.Fatal("invalid review must not expose a working Configure action")
	}
	css, err := assets.ReadFile("static/setup-review.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".setup-configure-disabled button:disabled{background:#495158", ".setup-configure-disabled:hover .setup-configure-tooltip,.setup-configure-disabled:focus-visible .setup-configure-tooltip{visibility:visible"} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("disabled Configure styling missing %q", want)
		}
	}
}
func TestSetupReviewKeepsCompatibleRecommendedBedrockEnabled(t *testing.T) {
	client := setupReviewClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("review returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"<dt>Maximum players</dt><dd>20</dd>",
		"<dt>Bedrock cross-play</dt><dd>Enabled, port 19132</dd>",
		"<dt>Timezone</dt><dd>America/Toronto</dd>",
		"<dt>Minecraft version</dt><dd>1.21.8</dd>",
		"<dt>Version policy</dt><dd>Recommended</dd>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("compatible Bedrock review missing %q: %s", want, body)
		}
	}
}

func TestSetupReviewEULAMustBeExplicitAndIsBoundToReviewedPlan(t *testing.T) {
	client := setupReviewClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))

	missing := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/review/eula", eulaForm(false, setupReviewFingerprint)))
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "Accept the Minecraft End User License Agreement") {
		t.Fatalf("missing EULA acceptance was not rejected: %d %s", missing.Code, missing.Body.String())
	}

	accepted := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/review/eula", eulaForm(true, setupReviewFingerprint)))
	if accepted.Code != http.StatusSeeOther || accepted.Header().Get("Location") != "/setup/review" {
		t.Fatalf("EULA acceptance returned %d %q: %s", accepted.Code, accepted.Header().Get("Location"), accepted.Body.String())
	}
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if !strings.Contains(page.Body.String(), `data-eula-accepted="true"`) {
		t.Fatalf("accepted EULA state not bound to review: %s", page.Body.String())
	}
	if client.planHit != 1 {
		t.Fatalf("validated plan was needlessly recomputed %d times", client.planHit)
	}
}

func TestSetupReviewRejectsStalePlanFingerprint(t *testing.T) {
	client := setupReviewClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))

	stale := "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/review/eula", eulaForm(true, stale)))
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "Setup changed since it was reviewed") {
		t.Fatalf("stale reviewed plan was not rejected: %d %s", rr.Code, rr.Body.String())
	}
	state, ok := firstRunSetupReviews.get(app, "session-token")
	if !ok || state.EULAAccepted {
		t.Fatalf("stale fingerprint left EULA accepted: %#v", state)
	}
}

func TestSetupReviewRejectsMissingOrMalformedPlanIdentity(t *testing.T) {
	for _, fingerprint := range []string{"", "sha256:not-a-real-fingerprint"} {
		t.Run(fingerprint, func(t *testing.T) {
			client := setupReviewClient()
			client.plan.PlanFingerprint = fingerprint
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			defer firstRunSetupDrafts.delete(app, "session-token")
			defer firstRunSetupReviews.delete(app, "session-token")
			advanceSetupToReview(t, app)

			rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
			if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "Setup validation is temporarily unavailable") {
				t.Fatalf("bad plan identity returned %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestSetupReviewBackInvalidatesAcceptedReview(t *testing.T) {
	client := setupReviewClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/review/eula", eulaForm(true, setupReviewFingerprint)))

	back := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/review/back", "csrf=csrf-token"))
	if back.Code != http.StatusSeeOther || back.Header().Get("Location") != "/setup" {
		t.Fatalf("review back returned %d %q", back.Code, back.Header().Get("Location"))
	}
	if _, ok := firstRunSetupReviews.get(app, "session-token"); ok {
		t.Fatal("review/EULA state survived editing an earlier setup step")
	}
	draft, ok := firstRunSetupDrafts.get(app, "session-token")
	if !ok || draft.CurrentStep != 6 {
		t.Fatalf("review back did not return to backups: %#v", draft)
	}
}

func TestSetupReviewShowsAuthoritativeValidationError(t *testing.T) {
	client := setupReviewClient()
	client.plan.OK = false
	client.plan.PlanFingerprint = ""
	client.plan.Code = "invalid_storage_layout"
	client.plan.Error = "Minecraft data and backups cannot overlap."
	client.planErr = &api.ResponseError{StatusCode: http.StatusBadRequest, Message: client.plan.Error}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Minecraft data and backups cannot overlap.") {
		t.Fatalf("authoritative validation error was not rendered: %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "Configuration validated.") {
		t.Fatal("invalid plan was presented as validated")
	}
}

func TestSetupReviewPlannerUnavailableIsFriendly(t *testing.T) {
	base := setupWizardStorageClient()
	app, err := New(base, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "Setup validation is temporarily unavailable") {
		t.Fatalf("missing planning capability returned %d: %s", rr.Code, rr.Body.String())
	}
	if errors.Is(errors.New(rr.Body.String()), api.ErrUnauthorized) {
		t.Fatal("planner outage was misclassified as authentication failure")
	}
}
