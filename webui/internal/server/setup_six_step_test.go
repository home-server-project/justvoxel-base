package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

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

func TestCombinedCrossplayValidationPreservesDraftOnRefresh(t *testing.T) {
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
			if draft.CurrentStep != expectedStep || draft.Minecraft.ConnectionsComplete != (tc.code == http.StatusSeeOther) || draft.Minecraft.ImageTag != "stable" || draft.Minecraft.VersionPolicy != "pinned" || draft.Minecraft.JavaPort != "25566" || draft.Minecraft.BedrockPort != "19133" || draft.Minecraft.Version != "1.21.8" || draft.Server.BedrockEnabled != tc.bedrock {
				t.Fatalf("draft = %#v", draft)
			}
			if client.bedrock != tc.bedrock || client.policy != "pinned" || client.version != "1.21.8" || client.software != "paper" {
				t.Fatal("validation used stale choices")
			}
			if tc.code == http.StatusBadRequest {
				assertSetupBedrockChecked(t, rr.Body.String(), tc.bedrock)
				body := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", "")).Body.String()
				assertSetupBedrockChecked(t, body, tc.bedrock)
				for _, want := range []string{"<h2>Cross-play</h2>", `value="1.21.8"`, `value="25566"`, `value="19133"`, `id="image-tag" name="image_tag" type="hidden" value="stable"`, `value="pinned" selected`, ">Back</button>", ">Continue</button>"} {
					if !strings.Contains(body, want) {
						t.Fatalf("refresh lost %q", want)
					}
				}
			}
		})
	}
}

func TestCrossplayPreviewUsesCurrentToggleWithoutSavingDraft(t *testing.T) {
	client := &setupPreviewClient{fakeDiscoveryAPI: setupWizardClient(), status: api.AdminVersionStatus{SelectedCandidate: "1.21.8", CrossplayCompatible: true}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/version-preview?policy=recommended&bedrock_enabled=true", ""))
	if rr.Code != http.StatusConflict {
		t.Fatal("preview allowed outside Cross-play")
	}
	_ = saveServerStep(t, app, validServerValues())
	for _, toggle := range []string{"true", "false"} {
		rr = httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup/version-preview?policy=pinned&version=1.21.8&bedrock_enabled="+toggle, ""))
		if rr.Code != http.StatusOK || client.bedrock != (toggle == "true") {
			t.Fatalf("preview %s: %d", toggle, rr.Code)
		}
		draft, _ := firstRunSetupDrafts.get(app, "session-token")
		if draft.Server.BedrockEnabled || draft.CurrentStep != setupCrossplayStep {
			t.Fatal("preview changed draft")
		}
	}
	script := correctionSource(t, "static/settings.js")
	for _, want := range []string{`bedrock_enabled: String(!!form?.querySelector("[data-bedrock-toggle]")?.checked)`, `addEventListener("change", refresh)`} {
		if !strings.Contains(script, want) {
			t.Fatalf("preview missing %q", want)
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
	css := correctionSource(t, "static/setup.css")
	for _, want := range []string{`.setup-wizard-panel .setup-storage-segment.storage-partition-card{min-height:82px;align-content:center}`, `max-height:none;overflow:visible`, `height:auto;min-height:100dvh;overflow-y:auto`} {
		if !strings.Contains(css, want) {
			t.Fatalf("compact accessible layout missing %q", want)
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
	if rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/setup/navigate", "csrf=csrf-token&direction=jump&step=6")); rr.Code != http.StatusSeeOther {
		t.Fatalf("Review jump after revalidation: %d", rr.Code)
	}
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
