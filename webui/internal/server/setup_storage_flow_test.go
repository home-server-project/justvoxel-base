package server

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

func TestAdvancedWizardHasSixCombinedSteps(t *testing.T) {
	names := []string{"Server", "Cross-play", "Memory", "Storage", "Backups", "Review"}
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
	server := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", "")).Body.String()
	if strings.Contains(server, "Timezone follows") {
		t.Fatal("removed timezone note remains")
	}
	_ = saveServerStep(t, app, validServerValues())
	values := validConnectionValues()
	values.Set("image_tag", "java21")
	values.Set("version_policy", "pinned")
	values.Set("version", "1.21.8")
	values.Set("direction", "back")
	if rr := saveConnectionsStep(t, app, values); rr.Code != http.StatusSeeOther {
		t.Fatal(rr.Code)
	}
	draft, _ := firstRunSetupDrafts.get(app, "session-token")
	if draft.CurrentStep != setupServerStep || draft.Minecraft.ImageTag != "java21" || draft.Minecraft.Version != "1.21.8" || draft.Minecraft.VersionComplete {
		t.Fatal("Cross-play Back lost version edits")
	}
	_ = saveServerStep(t, app, validServerValues())
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/setup", ""))
	for _, want := range []string{`value="java21"`, `value="1.21.8"`, `<h2>Cross-play</h2>`, "Minecraft Java TCP port", "Server update channel", `name="version_policy"`, "setup-version-preview"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("Cross-play missing %q", want)
		}
	}
	if strings.Contains(page.Body.String(), "Container updates") || strings.Contains(page.Body.String(), "><span>Version</span>") {
		t.Fatal("separate Version stage or container label remains")
	}
	values.Set("direction", "next")
	if rr := saveConnectionsStep(t, app, values); rr.Code != http.StatusSeeOther {
		t.Fatal(rr.Code, rr.Body.String())
	}
	draft, _ = firstRunSetupDrafts.get(app, "session-token")
	if draft.CurrentStep != setupMemoryStep || !draft.Minecraft.VersionComplete {
		t.Fatal("combined validation did not advance to Memory")
	}
	_ = saveResourcesStep(t, app, validResourceValues())
	draft, _ = firstRunSetupDrafts.get(app, "session-token")
	if draft.CurrentStep != setupStorageStep || !draft.Minecraft.Complete {
		t.Fatal("Memory did not advance to Storage")
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
	for _, want := range []string{"Use JustVoxel system storage", "Use another internal disk", `data-label="Worlds"`, `data-filesystem-free="30.0 GiB"`, `data-device="/dev/vdb1"`, `data-uuid="data-uuid"`, "ext4", "Samsung SSD", "data-storage-create-review", "data-storage-action-review"} {
		if !strings.Contains(body, want) {
			t.Fatalf("shared Storage missing %q", want)
		}
	}
	if strings.Contains(body, "setup-existing-filesystems") {
		t.Fatal("copied Setup filesystem browser remains")
	}

}

type storageDestinationTestClient struct{ *fakeNewBackupsAPI }

func (f *storageDestinationTestClient) AdminSetupDefaults(context.Context, string) (api.AdminSetupDefaults, error) {
	return api.AdminSetupDefaults{}, nil
}

func TestStorageWorkspaceRendersDiskAndPartitionBrowserOnly(t *testing.T) {
	for _, withUSB := range []bool{false, true} {
		name := "without USB"
		if withUSB {
			name = "with USB"
		}
		t.Run(name, func(t *testing.T) {
			inventory := setupWizardStorageClient().storage
			if !withUSB {
				devices := make([]api.AdminStorageDevice, 0, len(inventory.Devices))
				for _, device := range inventory.Devices {
					if device.Name != "vdd" && device.Parent != "vdd" {
						devices = append(devices, device)
					}
				}
				inventory.Devices = devices
			}
			client := &storageDestinationTestClient{&fakeNewBackupsAPI{role: "administrator", storage: inventory}}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/workspace/storage", ""))
			if page.Code != http.StatusOK {
				t.Fatalf("Storage: %d %s", page.Code, page.Body.String())
			}
			body := page.Body.String()
			for _, want := range []string{
				"data-storage-browser-root", "Internal drives", `aria-label="External USB drives"`, "<h3>USB drives</h3>",
				`data-storage-disk="vdb"`, "Samsung SSD", `data-storage-partitions="vda"`, `data-storage-partitions="vdb" hidden`,
				`data-path="/dev/vdb1"`, `data-mounted="Yes"`, "/srv/data",
				"storage-free-space-card", "data-storage-free-space", `data-device="/dev/vdb"`, `data-start="563201MiB"`, `data-end="716800MiB"`, "150.0 GiB", "Unallocated space",
				"data-storage-create-partition", "data-storage-create-review", "data-storage-create-apply",
				`data-storage-action="mount-for-now"`, `data-storage-action="format"`,
			} {
				if !strings.Contains(body, want) {
					t.Fatalf("Storage missing %q", want)
				}
			}
			selectedVDA := false
			for _, button := range regexp.MustCompile(`<button\b[^>]*>`).FindAllString(body, -1) {
				if strings.Contains(button, `data-storage-disk="vda"`) && strings.Contains(button, `aria-pressed="true"`) {
					selectedVDA = true
				}
			}
			if !selectedVDA {
				t.Fatal("vda storage-disk button must be selected")
			}
			externalStart := strings.Index(body, `aria-label="External USB drives"`)
			externalEnd := strings.Index(body[externalStart:], "</section>")
			if externalEnd < 0 {
				t.Fatal("External USB drive section is incomplete")
			}
			external := body[externalStart : externalStart+externalEnd]
			count := `<span class="badge">0</span>`
			if withUSB {
				count = `<span class="badge">1</span>`
				for _, want := range []string{"storage-disk-card-external", `data-storage-disk="vdd"`, "USB SSD", `data-path="/dev/vdd1"`} {
					if !strings.Contains(body, want) {
						t.Fatalf("Storage missing USB drive %q", want)
					}
				}
			} else if strings.Contains(body, "storage-disk-card-external") {
				t.Fatal("Storage renders a USB drive card without a USB drive")
			}
			if !strings.Contains(external, count) {
				t.Fatalf("External USB drive count missing %q", count)
			}
			for _, forbidden := range []string{
				"Minecraft data location", `class="storage-overview"`, "Minecraft storage assignment is unavailable in this WebUI build.",
				"Backup destination", "Current location", "Change destination", "backup-destination-section", "backup-destination-status", "backup-destination-summary", "/workspace/storage/destination/",
				"No USB storage detected.", "Advanced Storage — unallocated space", "/settings/",
			} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("Storage contains removed UI %q", forbidden)
				}
			}
		})
	}
}

func TestBackupsWorkspaceDestinationPlanAndApply(t *testing.T) {
	client := &storageDestinationTestClient{&fakeNewBackupsAPI{role: "administrator"}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	page := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/workspace/backups", ""))
	if page.Code != http.StatusOK {
		t.Fatalf("Backups: %d %s", page.Code, page.Body.String())
	}
	for _, want := range []string{"Backup destination", "Current location", "Change destination", `action="/workspace/backups/destination/plan"`, "Existing local partition / filesystem", "NFS share", "SMB / CIFS share", "40.0 GiB"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("Backups missing %q", want)
		}
	}
	if strings.Contains(page.Body.String(), "/settings/") {
		t.Fatal("Backups leaves workspace architecture")
	}
	form := "csrf=csrf-token&type=system&path=%2Fvar%2Flib%2Fjustvoxel%2Fbackups"
	plan := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/workspace/backups/destination/plan", form))
	if plan.Code != http.StatusOK || client.destinationPlanCalls != 1 || client.destinationApplyCalls != 0 || !strings.Contains(plan.Body.String(), `action="/workspace/backups/destination/apply"`) {
		t.Fatalf("Backups plan: %d %s", plan.Code, plan.Body.String())
	}
	apply := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/workspace/backups/destination/apply", form))
	if apply.Code != http.StatusSeeOther || client.destinationApplyCalls != 1 || apply.Header().Get("Location") != "/workspace/backups?result=destination" {
		t.Fatalf("Backups apply: %d", apply.Code)
	}
	for _, body := range []string{"type=system&path=%2Fvar%2Flib%2Fjustvoxel%2Fbackups", "csrf=wrong&type=system&path=%2Fvar%2Flib%2Fjustvoxel%2Fbackups"} {
		denied := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/workspace/backups/destination/apply", body))
		if denied.Code != http.StatusForbidden || client.destinationApplyCalls != 1 {
			t.Fatal("Backups bypasses CSRF")
		}
	}
	client.role = "operator"
	denied := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/workspace/backups/destination/apply", form))
	if denied.Code != http.StatusForbidden || client.destinationApplyCalls != 1 {
		t.Fatal("Backups bypasses administrator role")
	}
}
