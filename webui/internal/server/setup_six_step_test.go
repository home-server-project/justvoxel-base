package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

// These checks guard source layout constraints, not rendered browser geometry.
// FHD fitting and zoom accessibility still require browser/VM acceptance.
func TestWizardSharedDesktopFrame(t *testing.T) {
	css := correctionSource(t, "static/setup.css")
	for _, want := range []string{
		"@media(min-width:1100px) and (min-height:850px)",
		".setup-body.setup-advanced{height:100dvh;min-height:0;overflow:hidden}",
		".setup-advanced>.setup-shell{flex:1;min-height:0;margin:0 auto}",
		".setup-advanced .setup-advanced-frame{display:flex;flex:1;flex-direction:column;min-height:0",
		".setup-advanced .setup-advanced-frame .setup-form{flex:1;min-height:0}",
		".setup-advanced .setup-advanced-frame .setup-step-content{display:flex;flex:1",
		".setup-advanced .setup-advanced-frame .setup-step-actions{flex:none;min-height:55px;margin:.7rem 0 0;padding:.45rem .6rem}",
		".setup-advanced .setup-storage-browser>.storage-browser-content{display:flex;flex:1",
		".setup-advanced .storage-partition-grid{min-height:0;flex:0 1 auto}",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("shared desktop frame missing %q", want)
		}
	}
	for _, forbidden := range []string{"data-step", "overflow-y:scroll"} {
		if strings.Contains(css, forbidden) {
			t.Fatalf("per-step or page sizing: %q", forbidden)
		}
	}
	if strings.Count(css, ".setup-advanced .setup-advanced-frame{") != 1 {
		t.Fatal("Advanced must use one shared outer frame rule")
	}
	// Inspect outer-frame declarations only; bounded dialogs may use viewport
	// max-height without changing the shared wizard geometry.
	viewportHeight := regexp.MustCompile(`(?:^|;)\s*(?:min-|max-)?height\s*:\s*calc\(\s*100dvh`)
	for _, rule := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1) {
		for _, selector := range strings.Split(rule[1], ",") {
			selector = strings.TrimSpace(selector)
			for _, outer := range []string{".setup-body", ".setup-body.setup-advanced", ".setup-shell", ".setup-advanced-frame", ".setup-wizard-panel", ".setup-review-panel"} {
				if strings.HasSuffix(selector, outer) && viewportHeight.MatchString(rule[2]) {
					t.Fatalf("outer wizard frame has separate viewport sizing: %s", selector)
				}
			}
		}
	}
	markup := correctionSource(t, "templates/setup_wizard.html")
	if strings.Count(markup, `class="setup-step-content"`) != 5 || strings.Count(markup, `class="setup-step-actions"`) != 5 {
		t.Fatal("all five forms need shared content then navigation")
	}
	for _, form := range strings.Split(markup, `<form method="post" action="{{$wizard.BasePath}}/`)[1:] {
		if !strings.Contains(form, `class="setup-form"`) {
			continue
		}
		if strings.Index(form, `class="setup-step-content"`) > strings.Index(form, `class="setup-step-actions"`) {
			t.Fatal("navigation precedes content")
		}
	}
	if !strings.Contains(markup, "setup-wizard-panel setup-advanced-frame") {
		t.Fatal("wizard missing shared frame")
	}
	review := correctionSource(t, "templates/setup_review.html")
	if !strings.Contains(review, `setup-review-panel{{if ne .SetupMode "recommended"}} setup-advanced-frame{{end}}`) {
		t.Fatal("Advanced Review must share the frame without changing Recommended")
	}
	for _, forbidden := range []string{
		".setup-advanced .setup-review-header",
		".setup-advanced .setup-review-summary",
		".setup-advanced .setup-review-warnings",
	} {
		if strings.Contains(css, forbidden) {
			t.Fatalf("Advanced Review must retain normal spacing and typography without internal scrolling: %q", forbidden)
		}
	}
	if strings.Index(review, `class="setup-review-content"`) > strings.Index(review, `class="setup-step-actions setup-actions-split"`) {
		t.Fatal("Review navigation precedes content")
	}
}

type setupPreviewClient struct {
	*fakeDiscoveryAPI
	status                    api.AdminVersionStatus
	err                       error
	bedrock                   bool
	policy, version, software string
}

func (f *setupPreviewClient) AdminSetupVersionPreview(_ context.Context, _, policy, version string, bedrock bool, software string) (api.AdminVersionStatus, error) {
	f.policy, f.version, f.bedrock, f.software = policy, version, bedrock, software
	status := f.status
	status.CrossplayEnabled = bedrock
	return status, f.err
}

func TestVersionValidationPreservesDraftOnRefresh(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  api.AdminVersionStatus
		err     error
		bedrock bool
		code    int
	}{
		{"available", api.AdminVersionStatus{SelectedCandidate: "1.21.8", CrossplayCompatible: true}, nil, true, http.StatusSeeOther},
		{"unavailable", api.AdminVersionStatus{}, nil, false, http.StatusBadRequest},
		{"incompatible", api.AdminVersionStatus{SelectedCandidate: "1.21.8"}, nil, true, http.StatusBadRequest},
		{"Java only", api.AdminVersionStatus{SelectedCandidate: "1.21.8"}, nil, false, http.StatusSeeOther},
		{"lookup failed", api.AdminVersionStatus{}, errors.New("offline"), true, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &setupPreviewClient{fakeDiscoveryAPI: setupWizardClient(), status: tc.status, err: tc.err}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			defer firstRunSetupDrafts.delete(app, "session-token")
			startSetup(t, app)
			_ = saveServerStep(t, app, validServerValues())
			values := validConnectionValues()
			values.Set("image_tag", "stable")
			values.Set("version_policy", "pinned")
			values.Set("version", "1.21.8")
			values.Set("java_port", "25566")
			values.Set("bedrock_port", "19133")
			if !tc.bedrock {
				values.Del("bedrock_enabled")
			}
			rr := saveConnectionsStep(t, app, values)
			if rr.Code != tc.code {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			draft, _ := firstRunSetupDrafts.get(app, "session-token")
			expectedStep := setupCrossplayStep
			if tc.code == http.StatusSeeOther {
				expectedStep = setupMemoryStep
			}
			if draft.CurrentStep != expectedStep || draft.Minecraft.VersionComplete != (tc.code == http.StatusSeeOther) || draft.Minecraft.ImageTag != "stable" || draft.Minecraft.VersionPolicy != "pinned" || draft.Minecraft.JavaPort != "25566" || draft.Minecraft.BedrockPort != "19133" || draft.Minecraft.Version != "1.21.8" || draft.Server.BedrockEnabled != tc.bedrock {
				t.Fatalf("draft = %#v", draft)
			}
			if client.bedrock != tc.bedrock || client.policy != "pinned" || client.version != "1.21.8" || client.software != "paper" {
				t.Fatal("validation used stale choices")
			}
			if tc.code == http.StatusBadRequest {

				body := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", "")).Body.String()

				for _, want := range []string{"<h2>Cross-play</h2>", `value="1.21.8"`, `id="image-tag" name="image_tag" type="hidden" value="stable"`, `value="pinned" selected`, ">Back</button>", ">Continue</button>"} {
					if !strings.Contains(body, want) {
						t.Fatalf("refresh lost %q", want)
					}
				}
			}
		})
	}
}

func TestVersionPreviewUsesCurrentCrossplayWithoutChangingDraft(t *testing.T) {
	client := &setupPreviewClient{fakeDiscoveryAPI: setupWizardClient(), status: api.AdminVersionStatus{SelectedCandidate: "1.21.8", CrossplayCompatible: true}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/version-preview?policy=recommended", ""))
	if rr.Code != http.StatusConflict {
		t.Fatal("preview allowed before Cross-play")
	}
	_ = saveServerStep(t, app, validServerValues())
	before, _ := firstRunSetupDrafts.get(app, "session-token")
	for _, enabled := range []string{"on", "off"} {
		rr = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/version-preview?policy=pinned&version=1.21.8&bedrock_enabled="+enabled, ""))
		if rr.Code != http.StatusOK || client.bedrock != (enabled == "on") {
			t.Fatalf("preview: %d bedrock=%v", rr.Code, client.bedrock)
		}
		draft, _ := firstRunSetupDrafts.get(app, "session-token")
		if draft.Server != before.Server || draft.Minecraft != before.Minecraft || draft.CurrentStep != setupCrossplayStep {
			t.Fatal("preview changed draft")
		}
	}
}

func TestSetupCompactTilesAndElapsedContract(t *testing.T) {
	markup := correctionSource(t, "templates/setup_wizard.html")
	if strings.Contains(markup, "{{.Start}} → {{.End}}") || strings.Contains(markup, "Review partition creation and choose a size") {
		t.Fatal("Unallocated tiles still contain removed text")
	}
	for _, call := range []string{`{{template "storage_browser_content" .StorageBrowser}}`, `{{template "storage_browser_content" .BackupBrowser}}`} {
		if !strings.Contains(markup, call) {
			t.Fatalf("shared browser missing %q", call)
		}
	}
	for _, copied := range []string{"data-storage-partition", "data-storage-free-space", "storage-disk-card", "setup-existing-filesystems"} {
		if strings.Contains(markup, copied) {
			t.Fatalf("copied Setup presentation remains: %s", copied)
		}
	}
	shared := correctionSource(t, "templates/storage_browser.html")
	for _, want := range []string{`data-start="{{.Start}}"`, `data-end="{{.End}}"`, `data-size-bytes="{{.SizeBytes}}"`, "storage_partition_create_review", "storage_partition_action_review", "data-storage-detail-dialog", "data-storage-free-detail-dialog"} {
		if !strings.Contains(shared, want) {
			t.Fatalf("shared operations missing %q", want)
		}
	}
	css := correctionSource(t, "static/setup.css")
	if !strings.Contains(css, "grid-template-columns:repeat(6,minmax(0,1fr))") || strings.Contains(css, "setup-inventory-pages") {
		t.Fatal("six equal cards must reuse the shared bounded inventory layout")
	}
	sharedCSS := correctionSource(t, "static/storage-browser.css")
	if !strings.Contains(sharedCSS, "max-height:174px;overflow-y:auto") || !strings.Contains(sharedCSS, "max-height:290px;overflow-y:auto") {
		t.Fatal("shared large-inventory overflow missing")
	}
	script := correctionSource(t, "static/setup-operation.js")
	if !strings.Contains(script, "Time elapsed since setup started:") || strings.Contains(script, "Elapsed since setup started:") {
		t.Fatal("progress wording is incorrect")
	}
	if !strings.Contains(correctionSource(t, "templates/setup_progress.html"), `id="setup-operation-elapsed"`) {
		t.Fatal("shared progress display missing")
	}
}

func TestSetupInventoryRefreshRetainsActualMountedPartition(t *testing.T) {
	client := setupWizardStorageClient()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	advanceToStorage(t, app)
	for i := range client.storage.Devices {
		if client.storage.Devices[i].Path == "/dev/vdb1" {
			client.storage.Devices[i].Mountpoints = []string{"/var/mnt/vdb1"}
		}
	}
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `data-setup-existing-partition data-device="/dev/vdb1" data-mountpoint="/var/mnt/vdb1"`) {
		t.Fatal("mounted partition is not offered with its actual mount point")
	}
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	storage := setupStorageDraft{Type: "partition", Device: "/dev/vdb1", MountPoint: "/var/mnt/vdb1", Path: "/var/mnt/vdb1/minecraft"}
	if err := validateSetupStorage(storage, draft.Inventory, draft.Defaults); err != nil {
		t.Fatal(err)
	}
	storage.MountPoint = "/srv/stale"
	storage.Path = "/srv/stale/minecraft"
	if err := validateSetupStorage(storage, draft.Inventory, draft.Defaults); err == nil {
		t.Fatal("stale mount identity accepted")
	}
	for i := range draft.Inventory.Devices {
		if draft.Inventory.Devices[i].Path == "/dev/vdb1" {
			draft.Inventory.Devices[i].UUID = ""
		}
	}
	storage.MountPoint = "/var/mnt/vdb1"
	storage.Path = "/var/mnt/vdb1/minecraft"
	if err := validateSetupStorage(storage, draft.Inventory, draft.Defaults); err == nil {
		t.Fatal("missing UUID accepted")
	}
}

func TestIncompleteVisitedReviewCannotSkipFormsOrLoop(t *testing.T) {
	app, err := New(setupReviewClient(), Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	if draft.CurrentStep != setupReviewStep || draft.HighestStep != setupReviewStep || !setupDraftReadyForReview(draft) {
		t.Fatal("six-step flow did not reach validated Review")
	}
	draft.Minecraft.ConnectionsComplete = false
	draft.Minecraft.Complete = false
	draft.CurrentStep = setupCrossplayStep
	firstRunSetupDrafts.save(app, "session-token", draft)
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/navigate", "csrf=csrf-token&direction=jump&step=6"))
	if rr.Code != http.StatusBadRequest {
		t.Fatal("jump skipped incomplete Cross-play")
	}
	draft.CurrentStep = setupReviewStep
	firstRunSetupDrafts.save(app, "session-token", draft)
	rr = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "<h2>Cross-play</h2>") {
		t.Fatalf("incomplete Review looped: %d", rr.Code)
	}
}

func TestServerSoftwareChangeRequiresCrossplayRevalidationBeforeReview(t *testing.T) {
	client := &setupPreviewClient{
		fakeDiscoveryAPI: setupReviewClient().fakeDiscoveryAPI,
		status:           api.AdminVersionStatus{SelectedCandidate: "1.21.8", CrossplayCompatible: true},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	advanceSetupToReview(t, app)
	completed, _ := firstRunSetupDrafts.get(app, "session-token")
	if !setupDraftReadyForReview(completed) {
		t.Fatal("wizard did not reach validated Review")
	}

	if rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/navigate", "csrf=csrf-token&direction=jump&step=1")); rr.Code != http.StatusSeeOther {
		t.Fatalf("return to Server: %d", rr.Code)
	}
	values := validServerValues()
	values.Set("server_type", "purpur")
	if rr := saveServerStep(t, app, values); rr.Code != http.StatusSeeOther {
		t.Fatalf("software change: %d: %s", rr.Code, rr.Body.String())
	}
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	if draft.Minecraft.ServerType != "purpur" || draft.CurrentStep != setupCrossplayStep || draft.Minecraft.ConnectionsComplete || draft.Minecraft.Complete {
		t.Fatal("software change did not invalidate Cross-play and Minecraft completion")
	}
	assertPreserved := func(draft setupDraft) {
		t.Helper()
		if draft.Minecraft.JavaMemory != completed.Minecraft.JavaMemory || draft.Minecraft.ContainerMemory != completed.Minecraft.ContainerMemory || draft.Minecraft.ResourcesComplete != completed.Minecraft.ResourcesComplete || draft.Storage != completed.Storage || draft.Backups != completed.Backups {
			t.Fatal("software change or revalidation lost Memory, Storage or Backup settings")
		}
	}
	assertPreserved(draft)
	assertReviewBlocked := func() {
		t.Helper()
		rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/navigate", "csrf=csrf-token&direction=jump&step=6"))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("Review jump before revalidation: %d", rr.Code)
		}
		draft, _ := firstRunSetupDrafts.get(app, "session-token")
		if draft.CurrentStep != setupCrossplayStep || setupDraftReadyForReview(draft) {
			t.Fatal("blocked Review jump left Cross-play")
		}
	}
	assertReviewBlocked()

	client.status.CrossplayCompatible = false
	if rr := saveConnectionsStep(t, app, validConnectionValues()); rr.Code != http.StatusBadRequest {
		t.Fatalf("incompatible Cross-play revalidation: %d", rr.Code)
	}
	assertReviewBlocked()
	client.status.CrossplayCompatible = true
	if rr := saveConnectionsStep(t, app, validConnectionValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("Cross-play revalidation: %d: %s", rr.Code, rr.Body.String())
	}
	if client.software != "purpur" || client.policy != "recommended" || !client.bedrock {
		t.Fatal("Cross-play revalidation used stale choices")
	}
	draft, _ = firstRunSetupDrafts.get(app, "session-token")
	if draft.CurrentStep != setupMemoryStep || !draft.Minecraft.ConnectionsComplete || !draft.Minecraft.Complete {
		t.Fatal("successful Cross-play revalidation did not restore completion")
	}
	assertPreserved(draft)
	if rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/navigate", "csrf=csrf-token&direction=jump&step=6")); rr.Code != http.StatusBadRequest {
		t.Fatalf("forward Review jump after revalidation: %d", rr.Code)
	}
	_ = saveResourcesStep(t, app, validResourceValues())
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", "csrf=csrf-token&storage_type=system&direction=next"))
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/backups", "csrf=csrf-token&backup_type=system&backup_keep=7&backup_daily_time=04%3A30&backup_automatic=on&direction=next"))
	draft, _ = firstRunSetupDrafts.get(app, "session-token")
	if !setupDraftReadyForReview(draft) {
		t.Fatal("revalidated wizard did not reach Review")
	}
	assertPreserved(draft)
}

func TestSixStepBackRoutesPreserveMemoryStorageAndBackups(t *testing.T) {
	app, err := New(setupReviewClient(), Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	defer firstRunSetupReviews.delete(app, "session-token")
	startSetup(t, app)
	_ = saveServerStep(t, app, validServerValues())
	_ = saveConnectionsStep(t, app, validConnectionValues())
	memory := validResourceValues()
	memory.Set("java_memory", "5G")
	memory.Set("container_memory", "7G")
	memory.Set("direction", "back")
	if rr := saveResourcesStep(t, app, memory); rr.Code != http.StatusSeeOther {
		t.Fatal(rr.Code)
	}
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	if draft.CurrentStep != setupCrossplayStep || draft.Minecraft.JavaMemory != "5G" || draft.Minecraft.ContainerMemory != "7G" {
		t.Fatal("Memory Back lost values or skipped Cross-play")
	}
	_ = saveConnectionsStep(t, app, validConnectionValues())
	memory.Set("direction", "next")
	_ = saveResourcesStep(t, app, memory)
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", "csrf=csrf-token&storage_type=system&direction=back"))
	draft, _ = firstRunSetupDrafts.get(app, "session-token")
	if rr.Code != http.StatusSeeOther || draft.CurrentStep != setupMemoryStep || draft.Storage.Path != draft.Defaults.DataPath {
		t.Fatal("Storage Back lost destination or skipped Memory")
	}
	_ = saveResourcesStep(t, app, memory)
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", "csrf=csrf-token&storage_type=system&direction=next"))
	rr = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/backups", "csrf=csrf-token&backup_type=system&backup_keep=19&backup_daily_time=09%3A37&backup_automatic=on&direction=back"))
	draft, _ = firstRunSetupDrafts.get(app, "session-token")
	if rr.Code != http.StatusSeeOther || draft.CurrentStep != setupStorageStep || draft.Backups.Keep != "19" || draft.Backups.DailyTime != "09:37" || !draft.Backups.Automatic {
		t.Fatal("Backups Back lost settings or skipped Storage")
	}
}

// Both modes reach Review through their real forms before navigation is tested.
func sixStepProgressFixture(t *testing.T, addServer bool) (*App, string, *setupDraftStore, *setupDraftStore) {
	t.Helper()
	if !addServer {
		app, err := New(setupReviewClient(), Config{Version: "test", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			firstRunSetupDrafts.delete(app, "session-token")
			firstRunSetupReviews.delete(app, "session-token")
			addServerDrafts.delete(app, "session-token")
		})
		advanceSetupToReview(t, app)
		addServerDrafts.save(app, "session-token", setupDraft{AddServer: true, DisplayName: "Separate Add Server draft"})
		return app, "/setup", &firstRunSetupDrafts, &addServerDrafts
	}
	app, _ := newAddServerTestApp(t)
	firstRunSetupDrafts.save(app, "session-token", setupDraft{Started: true, CurrentStep: setupMemoryStep, Server: setupServerDraft{MOTD: "Separate first-run draft"}})
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/start", nil))
	server := validServerValues()
	server.Set("display_name", "Family Survival")
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/server", server))
	connections := validConnectionValues()
	connections.Set("java_port", "25567")
	connections.Set("bedrock_port", "19134")
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/connections", connections))
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/resources", url.Values{"java_memory": {"1G"}, "container_memory": {"2G"}, "direction": {"next"}}))
	draft, _ := addServerDrafts.get(app, "session-token")
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/storage", url.Values{"storage_type": {draft.Storage.Type}, "storage_path": {draft.Storage.Path}, "direction": {"next"}}))
	requireAddRedirect(t, addWizardRequest(app, http.MethodPost, "/backups", url.Values{"backup_type": {draft.Backups.Type}, "backup_path": {draft.Backups.Path}, "backup_daily_time": {"06:00"}, "backup_keep": {"9"}, "backup_automatic": {"on"}, "direction": {"next"}}))
	return app, "/minecraft/servers/add", &addServerDrafts, &firstRunSetupDrafts
}

func assertRenderedSixStepProgress(t *testing.T, body, basePath string, current int) {
	t.Helper()
	progress := regexp.MustCompile(`(?s)<ol class="setup-progress"[^>]*>(.*?)</ol>`).FindStringSubmatch(body)
	if len(progress) != 2 {
		t.Fatal("rendered six-step progress missing")
	}
	items := regexp.MustCompile(`(?s)<li\b[^>]*>.*?</li>`).FindAllString(progress[1], -1)
	labels := []string{"Server", "Cross-play", "Memory", "Storage", "Backups", "Review"}
	if len(items) != len(labels) {
		t.Fatalf("progress has %d steps, want six", len(items))
	}
	for i, label := range labels {
		number := i + 1
		item := items[i]
		if !strings.Contains(item, fmt.Sprintf(`<span class="setup-number">%d</span><span>%s</span>`, number, label)) {
			t.Fatalf("step %d missing label %q", number, label)
		}
		if strings.Contains(item, `aria-current="step"`) != (number == current) {
			t.Fatalf("step %d has incorrect active state at step %d", number, current)
		}
		forms := regexp.MustCompile(`(?s)<form\b[^>]*>.*?</form>`).FindAllString(item, -1)
		if number >= current {
			if len(forms) != 0 {
				t.Fatalf("step %d offers current/forward navigation at step %d", number, current)
			}
			continue
		}
		if len(forms) != 1 {
			t.Fatalf("completed step %d needs one navigation form", number)
		}
		for _, want := range []string{`method="post"`, `action="` + basePath + `/navigate"`, `name="csrf" value="csrf-token"`, `name="direction" value="jump"`, fmt.Sprintf(`name="step" value="%d"`, number), `<button type="submit">`} {
			if !strings.Contains(forms[0], want) {
				t.Fatalf("completed step %d navigation missing %q", number, want)
			}
		}
	}
}

// A visited future step never becomes a forward shortcut, even for a valid draft.
func TestSixStepProgressCardsOnlyNavigateBackward(t *testing.T) {
	for _, addServer := range []bool{false, true} {
		t.Run(fmt.Sprintf("AddServer=%t", addServer), func(t *testing.T) {
			app, basePath, drafts, otherDrafts := sixStepProgressFixture(t, addServer)
			complete, _ := drafts.get(app, "session-token")
			if !setupDraftReadyForReview(complete) || complete.CurrentStep != setupReviewStep {
				t.Fatal("fixture did not complete all six steps")
			}
			otherBefore, _ := otherDrafts.get(app, "session-token")
			for current := setupServerStep; current <= setupReviewStep; current++ {
				draft := complete
				draft.CurrentStep = current
				for target := setupServerStep; target <= setupReviewStep; target++ {
					drafts.save(app, "session-token", draft)
					rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example"+basePath+"/navigate", fmt.Sprintf("csrf=csrf-token&direction=jump&step=%d", target)))
					after, _ := drafts.get(app, "session-token")
					expected := draft
					if target >= current {
						if rr.Code != http.StatusBadRequest {
							t.Fatalf("step %d jumped forward/current to visited step %d: %d", current, target, rr.Code)
						}
					} else {
						expected.CurrentStep = target
						if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != basePath {
							t.Fatalf("backward jump %d -> %d: %d %q", current, target, rr.Code, rr.Header().Get("Location"))
						}
					}
					expected.UpdatedAt = after.UpdatedAt
					if !reflect.DeepEqual(expected, after) {
						t.Fatalf("jump %d -> %d changed draft values", current, target)
					}
					otherAfter, exists := otherDrafts.get(app, "session-token")
					if !exists || !reflect.DeepEqual(otherBefore, otherAfter) {
						t.Fatal("navigation changed or resumed the other wizard's draft")
					}
				}
			}
		})
	}
	source, err := os.ReadFile("setup_wizard.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "setupVersion"+"Step") {
		t.Fatal("Version navigation stage remains")
	}
}

func TestSixStepRecommendedFirstRunOmitsProgressAndKeepsDefaults(t *testing.T) {
	app, err := New(setupReviewClient(), Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		firstRunSetupDrafts.delete(app, "session-token")
		firstRunSetupReviews.delete(app, "session-token")
		addServerDrafts.delete(app, "session-token")
	})
	addServerDrafts.save(app, "session-token", setupDraft{AddServer: true, DisplayName: "Separate draft"})
	otherBefore, _ := addServerDrafts.get(app, "session-token")
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/recommended", "csrf=csrf-token&server_type=paper"))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/setup/review" {
		t.Fatalf("recommended: %d %q", rr.Code, rr.Header().Get("Location"))
	}
	draft, exists := firstRunSetupDrafts.get(app, "session-token")
	if !exists || draft.AddServer || draft.Mode != "recommended" || draft.CurrentStep != setupReviewStep || !setupDraftReadyForReview(draft) || draft.Minecraft.ServerType != "paper" || !draft.Server.BedrockEnabled || draft.Storage.Type != "system" || draft.Backups.Type != "system" || !draft.Backups.Automatic || draft.Minecraft.JavaMemory != draft.Defaults.JavaMemory || draft.Minecraft.ContainerMemory != draft.Defaults.ContainerMemory {
		t.Fatal("recommended first-run defaults or direct Review changed")
	}
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/review", ""))
	if page.Code != http.StatusOK || strings.Contains(page.Body.String(), `class="setup-progress"`) || strings.Contains(page.Body.String(), `action="/setup/navigate"`) || !strings.Contains(page.Body.String(), `class="setup-review-summary"`) {
		t.Fatalf("recommended Review presentation changed: %d", page.Code)
	}
	otherAfter, _ := addServerDrafts.get(app, "session-token")
	if !reflect.DeepEqual(otherBefore, otherAfter) {
		t.Fatal("recommended first-run changed the Add Server draft")
	}
}
