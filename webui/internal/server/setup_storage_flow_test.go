package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func TestAdvancedWizardHasSevenSeparateSteps(t *testing.T) {
	names := []string{"Server", "Cross-play", "Memory", "Version", "Storage", "Backups", "Review"}
	if len(setupWizardSteps) != len(names) {
		t.Fatalf("steps = %d", len(setupWizardSteps))
	}
	for i, name := range names {
		if setupWizardSteps[i].Number != i+1 || setupWizardSteps[i].Name != name {
			t.Fatalf("step %d = %#v", i+1, setupWizardSteps[i])
		}
	}
	app, err := New(setupWizardClient(), Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	startSetup(t, app)
	_ = saveServerStep(t, app, validServerValues())
	_ = saveConnectionsStep(t, app, validConnectionValues())
	_ = saveResourcesStep(t, app, validResourceValues())
	values := validConnectionValues()
	values.Set("image_tag", "java21")
	values.Set("version_policy", "pinned")
	values.Set("version", "1.21.8")
	values.Set("direction", "back")
	if rr := saveVersionStep(t, app, values); rr.Code != http.StatusSeeOther {
		t.Fatal(rr.Code)
	}
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	if draft.CurrentStep != setupMemoryStep || draft.Minecraft.ImageTag != "java21" || draft.Minecraft.Version != "1.21.8" || draft.Minecraft.VersionComplete {
		t.Fatal("Version Back lost values or skipped Memory")
	}
	_ = saveResourcesStep(t, app, validResourceValues())
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	for _, want := range []string{`value="java21"`, `value="1.21.8"`, `<h2>Version</h2>`} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("revisited Version lost %q", want)
		}
	}
	values.Set("direction", "next")
	if rr := saveVersionStep(t, app, values); rr.Code != http.StatusSeeOther {
		t.Fatal(rr.Code, rr.Body.String())
	}
	draft, _ = firstRunSetupDrafts.get(app, "session-token")
	if draft.CurrentStep != setupStorageStep || !draft.Minecraft.Complete {
		t.Fatal("Version did not advance to Storage")
	}
}

func TestNormalSetupStorageSelectsBackendFilesystemIdentity(t *testing.T) {
	client := setupWizardStorageClient()
	for i := range client.storage.Devices {
		if client.storage.Devices[i].Path == "/dev/vdb1" {
			client.storage.Devices[i].Label = "Worlds"
			client.storage.Devices[i].FilesystemUsageKnown = true
			client.storage.Devices[i].FilesystemFreeBytes = 30 * 1024 * 1024 * 1024
		}
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer firstRunSetupDrafts.delete(app, "session-token")
	advanceToStorage(t, app)
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	body := page.Body.String()
	if page.Code != http.StatusOK {
		t.Fatal(page.Code)
	}
	for _, want := range []string{"Use JustVoxel system storage", "Use an existing local filesystem / partition", "Worlds", "30.0 GiB available"} {
		if !strings.Contains(body, want) {
			t.Fatalf("storage missing %q", want)
		}
	}
	start := strings.Index(body, `class="setup-existing-filesystems"`)
	end := strings.Index(body[start:], `<details class="setup-advanced-storage">`)
	if end < 0 {
		t.Fatal("Advanced Storage separation missing")
	}
	normal := body[start : start+end]
	for _, want := range []string{`data-device="/dev/vdb1"`, "UUID: data-uuid", "ext4", "Samsung SSD"} {
		if !strings.Contains(normal, want) {
			t.Fatalf("backend identity missing %q", want)
		}
	}
	for _, forbidden := range []string{"data-storage-action", "data-storage-free-space", "data-storage-create", "Format partition", "Erase"} {
		if strings.Contains(normal, forbidden) {
			t.Fatalf("normal storage introduces %q", forbidden)
		}
	}
}

type storageDestinationTestClient struct{ *fakeNewBackupsAPI }

func (f *storageDestinationTestClient) AdminSetupDefaults(context.Context, string) (api.AdminSetupDefaults, error) {
	return api.AdminSetupDefaults{}, nil
}

func TestStorageWorkspaceSharesBackupDestinationPlanAndApply(t *testing.T) {
	client := &storageDestinationTestClient{&fakeNewBackupsAPI{role: "administrator"}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/workspace/storage", ""))
	if page.Code != http.StatusOK {
		t.Fatalf("Storage: %d %s", page.Code, page.Body.String())
	}
	for _, want := range []string{"Minecraft data location", `action="/workspace/storage/destination/plan"`, "Existing local partition / filesystem", "NFS share", "SMB / CIFS share", "40.0 GiB"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("Storage missing %q", want)
		}
	}
	if strings.Contains(page.Body.String(), "/settings/") {
		t.Fatal("Storage leaves workspace architecture")
	}
	form := "csrf=csrf-token&type=system&path=%2Fvar%2Flib%2Fjustvoxel%2Fbackups"
	plan := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/workspace/storage/destination/plan", form))
	if plan.Code != http.StatusOK || client.destinationPlanCalls != 1 || !strings.Contains(plan.Body.String(), `action="/workspace/storage/destination/apply"`) {
		t.Fatalf("Storage plan: %d %s", plan.Code, plan.Body.String())
	}
	apply := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/workspace/storage/destination/apply", form))
	if apply.Code != http.StatusSeeOther || client.destinationApplyCalls != 1 || apply.Header().Get("Location") != "/workspace/storage?result=destination" {
		t.Fatalf("Storage apply: %d", apply.Code)
	}
	for _, body := range []string{"type=system&path=%2Fvar%2Flib%2Fjustvoxel%2Fbackups", "csrf=wrong&type=system&path=%2Fvar%2Flib%2Fjustvoxel%2Fbackups"} {
		denied := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/workspace/storage/destination/apply", body))
		if denied.Code != http.StatusForbidden || client.destinationApplyCalls != 1 {
			t.Fatal("Storage bypasses CSRF")
		}
	}
	client.role = "operator"
	denied := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/workspace/storage/destination/apply", form))
	if denied.Code != http.StatusForbidden || client.destinationApplyCalls != 1 {
		t.Fatal("Storage bypasses administrator role")
	}
}
