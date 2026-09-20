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
					JavaMemory: "4G", ContainerMemory: "6G", JavaPort: 25565, BedrockPort: 19132,
					ImageTag: "stable", RequestedVersionPolicy: "recommended", VersionPolicy: "pinned", Version: "1.21.8",
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
		"Review your JustVoxel setup", "Configuration validated.", "Normalized Family Server", "20", "1.21.8",
		"Recommended version", "/var/lib/justvoxel/minecraft", "/var/lib/justvoxel/backups",
		"same_physical_disk", "Minecraft End User License Agreement", "https://www.minecraft.net/eula",
		"Setup execution will be enabled by the transactional setup engine", "/static/setup-review.css",
		`name="plan_fingerprint" value="` + setupReviewFingerprint + `"`, "Validated plan:", "01234567…",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("review missing %q: %s", want, body)
		}
	}
	if client.planHit != 1 {
		t.Fatalf("setup planner calls = %d, want 1", client.planHit)
	}
	if client.lastRequest.Server.MOTD != "Family Minecraft" || client.lastRequest.Minecraft.VersionPolicy != "recommended" {
		t.Fatalf("review planner did not receive draft values: %#v", client.lastRequest)
	}
	if strings.Contains(body, `type="password"`) || strings.Contains(body, `name="backup_password"`) {
		t.Fatal("review rendered a password field")
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
	for _, want := range []string{"Cancel setup", ">Back</button>", "setup-execution-copy"} {
		if !strings.Contains(body, want) {
			t.Fatalf("review layout missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Back to backups") {
		t.Fatal("review still uses the old Back to backups label")
	}

	css, err := assets.ReadFile("static/setup-review.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(css)
	for _, want := range []string{
		"repeat(auto-fit,minmax(min(100%,460px),1fr))",
		"white-space:nowrap",
		"overflow-x:auto",
		"grid-template-columns:minmax(0,1fr) auto",
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("responsive review CSS missing %q", want)
		}
	}

	setupCSS, err := assets.ReadFile("static/setup.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"max-width:1180px", ".setup-step-actions", "@media(max-width:850px)"} {
		if !strings.Contains(string(setupCSS), want) {
			t.Fatalf("responsive setup CSS missing %q", want)
		}
	}
}
func TestSetupReviewExplainsDisabledBedrockCompatibility(t *testing.T) {
	client := setupReviewClient()
	client.plan.Normalized.Server.BedrockEnabled = false
	client.plan.Normalized.Minecraft.Version = "26.3"
	client.plan.Warnings = append(client.plan.Warnings, api.AdminSetupPlanWarning{
		Code:    "bedrock_version_unsupported",
		Message: "Bedrock cross-play was turned off because Geyser/Floodgate currently supports Minecraft 26.2, while this setup uses 26.3. Geyser/Floodgate will not be installed, and Java server setup can continue. Come back later and check again after Geyser/Floodgate adds support for this Minecraft version.",
	})
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
	for _, want := range []string{"Bedrock cross-play</dt><dd>Disabled", "Bedrock cross-play unavailable", "currently supports Minecraft 26.2", "Come back later and check again"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Bedrock compatibility review missing %q: %s", want, body)
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
	if !strings.Contains(page.Body.String(), "EULA accepted") || !strings.Contains(page.Body.String(), "Ready for the next phase") {
		t.Fatalf("accepted EULA state not shown: %s", page.Body.String())
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
	if !ok || draft.CurrentStep != 4 {
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
