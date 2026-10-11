package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

// These checks guard source layout constraints, not rendered browser geometry.
// FHD fitting and zoom accessibility still require browser/VM acceptance.
func TestWizardNaturalHeightLayout(t *testing.T) {
	css := correctionSource(t, "static/setup.css")
	for _, forbidden := range []string{"height:100dvh;min-height:0", "min-height:calc(100%", "position:sticky", "bottom:0;min-height:55px", "margin-top:auto;padding:.45rem", ".setup-review-panel{flex:1"} {
		if strings.Contains(css, forbidden) {
			t.Fatalf("wizard height regression: %q", forbidden)
		}
	}
	if !strings.Contains(css, ".setup-body .setup-wizard-panel .setup-form{flex:none;") {
		t.Fatal("wizard forms must use natural height")
	}
	markup := correctionSource(t, "templates/setup_wizard.html")
	if strings.Count(markup, `class="setup-step-content"`) != 6 || strings.Count(markup, `class="setup-step-actions"`) != 6 {
		t.Fatal("all six forms need shared content then navigation")
	}
	for _, form := range strings.Split(markup, `<form method="post" action="/setup/`)[1:] {
		if !strings.Contains(form, `class="setup-form"`) {
			continue
		}
		if strings.Index(form, `class="setup-step-content"`) > strings.Index(form, `class="setup-step-actions"`) {
			t.Fatal("navigation precedes content")
		}
	}
	review := correctionSource(t, "templates/setup_review.html")
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
			_ = saveConnectionsStep(t, app, values)
			_ = saveResourcesStep(t, app, validResourceValues())
			rr := saveVersionStep(t, app, values)
			if rr.Code != tc.code {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			draft, _ := firstRunSetupDrafts.get(app, "session-token")
			expectedStep := setupVersionStep
			if tc.code == http.StatusSeeOther {
				expectedStep = setupStorageStep
			}
			if draft.CurrentStep != expectedStep || draft.Minecraft.VersionComplete != (tc.code == http.StatusSeeOther) || draft.Minecraft.ImageTag != "stable" || draft.Minecraft.VersionPolicy != "pinned" || draft.Minecraft.JavaPort != "25566" || draft.Minecraft.BedrockPort != "19133" || draft.Minecraft.Version != "1.21.8" || draft.Server.BedrockEnabled != tc.bedrock {
				t.Fatalf("draft = %#v", draft)
			}
			if client.bedrock != tc.bedrock || client.policy != "pinned" || client.version != "1.21.8" || client.software != "paper" {
				t.Fatal("validation used stale choices")
			}
			if tc.code == http.StatusBadRequest {

				body := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", "")).Body.String()

				for _, want := range []string{"<h2>Version</h2>", `value="1.21.8"`, `id="image-tag" name="image_tag" type="hidden" value="stable"`, `value="pinned" selected`, ">Back</button>", ">Continue</button>"} {
					if !strings.Contains(body, want) {
						t.Fatalf("refresh lost %q", want)
					}
				}
			}
		})
	}
}

func TestVersionPreviewUsesSavedCrossplayWithoutChangingDraft(t *testing.T) {
	client := &setupPreviewClient{fakeDiscoveryAPI: setupWizardClient(), status: api.AdminVersionStatus{SelectedCandidate: "1.21.8", CrossplayCompatible: true}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)
	_ = saveServerStep(t, app, validServerValues())
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/version-preview?policy=recommended", ""))
	if rr.Code != http.StatusConflict {
		t.Fatal("preview allowed on Cross-play")
	}
	for _, enabled := range []bool{true, false} {
		values := validConnectionValues()
		if !enabled {
			values.Del("bedrock_enabled")
		}
		_ = saveConnectionsStep(t, app, values)
		_ = saveResourcesStep(t, app, validResourceValues())
		rr = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/version-preview?policy=pinned&version=1.21.8&bedrock_enabled=ignored", ""))
		if rr.Code != http.StatusOK || client.bedrock != enabled {
			t.Fatalf("preview: %d bedrock=%v", rr.Code, client.bedrock)
		}
		draft, _ := firstRunSetupDrafts.get(app, "session-token")
		if draft.Server.BedrockEnabled != enabled || draft.CurrentStep != setupVersionStep {
			t.Fatal("preview changed draft")
		}
	}
}

func TestSetupCompactTilesAndElapsedContract(t *testing.T) {
	markup := correctionSource(t, "templates/setup_wizard.html")
	if strings.Contains(markup, "{{.Start}} → {{.End}}") || strings.Contains(markup, "Review partition creation and choose a size") {
		t.Fatal("Unallocated tiles still contain removed text")
	}
	if strings.Count(markup, `data-start="{{.Start}}" data-end="{{.End}}" data-size-bytes="{{.SizeBytes}}"`) != 2 {
		t.Fatal("partition review geometry was removed")
	}
	if strings.Count(markup, `class="setup-storage-segment storage-partition-card"`) != 4 {
		t.Fatal("both steps must use the same tile classes for partitions and unallocated space")
	}
	if strings.Count(markup, `<dialog class="storage-detail-dialog setup-storage-operation"`) != 4 || strings.Contains(markup, `<section class="setup-storage-operation"`) {
		t.Fatal("partition and free-space details must not expand inline")
	}
	css := correctionSource(t, "static/setup.css")
	for _, want := range []string{`.setup-wizard-panel .setup-storage-segment.storage-partition-card{height:82px;min-height:82px;align-content:center;box-sizing:border-box}`, `max-height:none;overflow:visible`, `flex:none;min-height:0;margin:0;gap:.42rem;overflow:visible`} {
		if !strings.Contains(css, want) {
			t.Fatalf("compact accessible layout missing %q", want)
		}
	}
	for _, forbidden := range []string{"height:auto;min-height:100dvh;overflow-y:auto", ".setup-wizard-panel{overflow:hidden", ".setup-wizard-panel .storage-partition-grid{overflow:auto"} {
		if strings.Contains(css, forbidden) {
			t.Fatalf("wizard must fit by compact presentation rather than scrolling or clipping: %q", forbidden)
		}
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
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `data-setup-existing-partition data-mountpoint="/var/mnt/vdb1"`) {
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
		t.Fatal("seven-step flow did not reach validated Review")
	}
	draft.Minecraft.ConnectionsComplete = false
	draft.Minecraft.Complete = false
	draft.CurrentStep = setupCrossplayStep
	firstRunSetupDrafts.save(app, "session-token", draft)
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/navigate", "csrf=csrf-token&direction=jump&step=7"))
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
		rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/navigate", "csrf=csrf-token&direction=jump&step=7"))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("Review jump before revalidation: %d", rr.Code)
		}
		draft, _ := firstRunSetupDrafts.get(app, "session-token")
		if (draft.CurrentStep != setupCrossplayStep && draft.CurrentStep != setupVersionStep) || setupDraftReadyForReview(draft) {
			t.Fatal("blocked Review jump left Cross-play")
		}
	}
	assertReviewBlocked()

	client.status.CrossplayCompatible = false
	_ = saveConnectionsStep(t, app, validConnectionValues())
	_ = saveResourcesStep(t, app, validResourceValues())
	if rr := saveVersionStep(t, app, validConnectionValues()); rr.Code != http.StatusBadRequest {
		t.Fatalf("incompatible Cross-play revalidation: %d", rr.Code)
	}
	assertReviewBlocked()
	client.status.CrossplayCompatible = true
	if rr := saveVersionStep(t, app, validConnectionValues()); rr.Code != http.StatusSeeOther {
		t.Fatalf("Cross-play revalidation: %d: %s", rr.Code, rr.Body.String())
	}
	if client.software != "purpur" || client.policy != "recommended" || !client.bedrock {
		t.Fatal("Cross-play revalidation used stale choices")
	}
	draft, _ = firstRunSetupDrafts.get(app, "session-token")
	if draft.CurrentStep != setupStorageStep || !draft.Minecraft.ConnectionsComplete || !draft.Minecraft.Complete {
		t.Fatal("successful Cross-play revalidation did not restore completion")
	}
	assertPreserved(draft)
	if rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/navigate", "csrf=csrf-token&direction=jump&step=7")); rr.Code != http.StatusSeeOther {
		t.Fatalf("Review jump after revalidation: %d", rr.Code)
	}
	draft, _ = firstRunSetupDrafts.get(app, "session-token")
	if !setupDraftReadyForReview(draft) {
		t.Fatal("revalidated wizard did not reach Review")
	}
	assertPreserved(draft)
}

func TestSevenStepBackRoutesPreserveMemoryStorageAndBackups(t *testing.T) {
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
	_ = saveVersionStep(t, app, validConnectionValues())
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", "csrf=csrf-token&storage_type=system&direction=back"))
	draft, _ = firstRunSetupDrafts.get(app, "session-token")
	if rr.Code != http.StatusSeeOther || draft.CurrentStep != setupVersionStep || draft.Storage.Path != draft.Defaults.DataPath {
		t.Fatal("Storage Back lost destination or skipped Version")
	}
	_ = saveResourcesStep(t, app, memory)
	_ = saveVersionStep(t, app, validConnectionValues())
	_ = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/storage", "csrf=csrf-token&storage_type=system&direction=next"))
	rr = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/backups", "csrf=csrf-token&backup_type=system&backup_keep=19&backup_daily_time=09%3A37&backup_automatic=on&direction=back"))
	draft, _ = firstRunSetupDrafts.get(app, "session-token")
	if rr.Code != http.StatusSeeOther || draft.CurrentStep != setupStorageStep || draft.Backups.Keep != "19" || draft.Backups.DailyTime != "09:37" || !draft.Backups.Automatic {
		t.Fatal("Backups Back lost settings or skipped Storage")
	}
}
