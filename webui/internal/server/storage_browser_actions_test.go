package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeStorageActionAPI struct {
	fakeDiscoveryAPI
	plan             api.AdminStorageActionResponse
	apply            api.AdminStorageActionResponse
	planned          api.AdminStorageActionRequest
	applied          api.AdminStorageActionRequest
	planCalls        int
	applyCalls       int
	mountStatus      api.AdminStorageMountResponse
	mountPlan        api.AdminStorageMountResponse
	mountApply       api.AdminStorageMountResponse
	mountPlanned     api.AdminStorageMountRequest
	mountApplied     api.AdminStorageMountRequest
	mountStatusCalls int
	mountPlanCalls   int
	mountApplyCalls  int
}

func (f *fakeStorageActionAPI) AdminStorageActionPlan(_ context.Context, session string, request api.AdminStorageActionRequest) (api.AdminStorageActionResponse, error) {
	if session != "session-token" {
		return api.AdminStorageActionResponse{}, api.ErrUnauthorized
	}
	f.planCalls++
	f.planned = request
	return f.plan, nil
}

func (f *fakeStorageActionAPI) AdminStorageActionApply(_ context.Context, session string, request api.AdminStorageActionRequest) (api.AdminStorageActionResponse, error) {
	if session != "session-token" {
		return api.AdminStorageActionResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.applied = request
	return f.apply, nil
}

func (f *fakeStorageActionAPI) AdminStorageMountStatus(_ context.Context, session, device string) (api.AdminStorageMountResponse, error) {
	if session != "session-token" {
		return api.AdminStorageMountResponse{}, api.ErrUnauthorized
	}
	f.mountStatusCalls++
	if f.mountStatus.Proposed.Device == "" {
		f.mountStatus.Proposed.Device = device
	}
	return f.mountStatus, nil
}

func (f *fakeStorageActionAPI) AdminStorageMountPlan(_ context.Context, session string, request api.AdminStorageMountRequest) (api.AdminStorageMountResponse, error) {
	if session != "session-token" {
		return api.AdminStorageMountResponse{}, api.ErrUnauthorized
	}
	f.mountPlanCalls++
	f.mountPlanned = request
	return f.mountPlan, nil
}

func (f *fakeStorageActionAPI) AdminStorageMountApply(_ context.Context, session string, request api.AdminStorageMountRequest) (api.AdminStorageMountResponse, error) {
	if session != "session-token" {
		return api.AdminStorageMountResponse{}, api.ErrUnauthorized
	}
	f.mountApplyCalls++
	f.mountApplied = request
	return f.mountApply, nil
}

func TestStorageBrowserActionPlanUsesSelectedPartition(t *testing.T) {
	client := &fakeStorageActionAPI{}
	client.plan = api.AdminStorageActionResponse{
		OK: true,
		Proposed: api.AdminStorageActionPlan{
			Operation: "format", Device: "/dev/vdb1", Role: "Minecraft",
			Confirmation: "FORMAT /dev/vdb1", Fingerprint: "abc123", Destructive: true,
		},
		Warnings: []string{"Minecraft data is stored on this partition."},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&operation=format&device=%2Fdev%2Fvdb1"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/actions/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("storage action plan returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.planCalls != 1 || client.applyCalls != 0 {
		t.Fatalf("plan calls=%d apply calls=%d", client.planCalls, client.applyCalls)
	}
	if client.planned.Operation != "format" || client.planned.Device != "/dev/vdb1" {
		t.Fatalf("unexpected planned request: %#v", client.planned)
	}
	for _, want := range []string{"FORMAT /dev/vdb1", "Minecraft", "abc123"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("storage action plan response missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestStorageBrowserCreatePartitionPlanCarriesSizeAndFreeSegment(t *testing.T) {
	client := &fakeStorageActionAPI{}
	client.plan = api.AdminStorageActionResponse{
		OK: true,
		Proposed: api.AdminStorageActionPlan{
			Operation: "create_partition", Device: "/dev/vda", TargetFilesystem: "xfs",
			SizeGiB: "55", FreeStart: "102401MiB", FreeEnd: "204800MiB", PlannedEnd: "158721.00MiB",
			Confirmation: "CREATE PARTITION /dev/vda", Fingerprint: "layout123", Destructive: true,
		},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&operation=create_partition&device=%2Fdev%2Fvda&free_start=102401MiB&size_gib=55"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/actions/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("create partition plan returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.planCalls != 1 {
		t.Fatalf("plan calls=%d, want 1", client.planCalls)
	}
	if client.planned.Operation != "create_partition" || client.planned.Device != "/dev/vda" || client.planned.FreeStart != "102401MiB" || client.planned.SizeGiB != "55" {
		t.Fatalf("create partition request lost selected free-space geometry: %#v", client.planned)
	}
}

func TestStorageBrowserActionApplyCarriesReviewedEvidence(t *testing.T) {
	client := &fakeStorageActionAPI{}
	client.apply = api.AdminStorageActionResponse{
		OK: true, Applied: true,
		Proposed: api.AdminStorageActionPlan{Operation: "format", Device: "/dev/vdb1", Filesystem: "xfs"},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&operation=format&device=%2Fdev%2Fvdb1&confirmation=FORMAT+%2Fdev%2Fvdb1&fingerprint=abc123"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/actions/apply", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("storage action apply returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.applyCalls != 1 || client.planCalls != 0 {
		t.Fatalf("apply calls=%d plan calls=%d", client.applyCalls, client.planCalls)
	}
	if client.applied.Confirmation != "FORMAT /dev/vdb1" || client.applied.Fingerprint != "abc123" {
		t.Fatalf("review evidence lost at apply: %#v", client.applied)
	}
}

func TestStorageBrowserActionsRejectOperatorAndBadCSRF(t *testing.T) {
	client := &fakeStorageActionAPI{}
	client.role = "operator"
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&operation=unmount&device=%2Fdev%2Fvdb1"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/actions/plan", body))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator storage action status=%d, want 403", rr.Code)
	}
	if client.planCalls != 0 || client.applyCalls != 0 {
		t.Fatal("privileged storage action API ran for Operator")
	}

	client.role = "administrator"
	body = "csrf=wrong&operation=unmount&device=%2Fdev%2Fvdb1"
	rr = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/actions/plan", body))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("bad CSRF storage action status=%d, want 403", rr.Code)
	}
	if client.planCalls != 0 || client.applyCalls != 0 {
		t.Fatal("storage action API ran after CSRF rejection")
	}
}

func TestStorageBrowserMountStatusReportsPermanentState(t *testing.T) {
	client := &fakeStorageActionAPI{
		mountStatus: api.AdminStorageMountResponse{
			OK: true,
			Proposed: api.AdminStorageMountPlan{
				Device: "/dev/vdb1", Filesystem: "xfs", UUID: "uuid-1",
				MountPoint: "/var/mnt/vdb1", CurrentMountPoint: "/var/mnt/vdb1",
				Persistence: "justvoxel", Mounted: true,
			},
			Warnings: []string{},
		},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/api/new-storage/mounts/status?device=%2Fdev%2Fvdb1", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("mount status returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.mountStatusCalls != 1 {
		t.Fatalf("mount status calls=%d, want 1", client.mountStatusCalls)
	}
	for _, want := range []string{"justvoxel", "/var/mnt/vdb1", "uuid-1"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("mount status missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestStorageBrowserPermanentMountPlanAndApplyCarryReviewEvidence(t *testing.T) {
	client := &fakeStorageActionAPI{}
	client.mountPlan = api.AdminStorageMountResponse{
		OK: true,
		Proposed: api.AdminStorageMountPlan{
			Operation: "persist", Device: "/dev/vdb1", Filesystem: "ext4",
			MountPoint: "/var/mnt/vdb1", Fingerprint: "mount-fingerprint",
		},
		Warnings: []string{},
	}
	client.mountApply = api.AdminStorageMountResponse{
		OK: true, Applied: true,
		Proposed: api.AdminStorageMountPlan{
			Operation: "persist", Device: "/dev/vdb1", MountPoint: "/var/mnt/vdb1",
			Persistence: "justvoxel", Mounted: true,
		},
		Warnings: []string{},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	body := "csrf=csrf-token&operation=persist&device=%2Fdev%2Fvdb1&mount_point=%2Fvar%2Fmnt%2Fvdb1"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/mounts/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("permanent mount plan returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.mountPlanCalls != 1 || client.mountPlanned.MountPoint != "/var/mnt/vdb1" {
		t.Fatalf("unexpected permanent mount plan request: %#v", client.mountPlanned)
	}
	if !strings.Contains(rr.Body.String(), "mount-fingerprint") {
		t.Fatalf("permanent mount plan missing fingerprint: %s", rr.Body.String())
	}

	body = "csrf=csrf-token&operation=persist&device=%2Fdev%2Fvdb1&mount_point=%2Fvar%2Fmnt%2Fvdb1&fingerprint=mount-fingerprint"
	rr = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/mounts/apply", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("permanent mount apply returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.mountApplyCalls != 1 || client.mountApplied.Fingerprint != "mount-fingerprint" {
		t.Fatalf("permanent mount apply lost reviewed fingerprint: %#v", client.mountApplied)
	}
}

func TestStorageBrowserPermanentMountRejectsOperatorAndBadCSRF(t *testing.T) {
	client := &fakeStorageActionAPI{}
	client.role = "operator"
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/api/new-storage/mounts/status?device=%2Fdev%2Fvdb1", ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator mount status=%d, want 403", rr.Code)
	}
	if client.mountStatusCalls != 0 {
		t.Fatal("permanent mount status API ran for Operator")
	}

	client.role = "administrator"
	body := "csrf=wrong&operation=remove&device=%2Fdev%2Fvdb1&fingerprint=abc123"
	rr = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/mounts/apply", body))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("bad CSRF permanent mount status=%d, want 403", rr.Code)
	}
	if client.mountApplyCalls != 0 {
		t.Fatal("permanent mount apply API ran after CSRF rejection")
	}
}

func TestStorageBrowserHiddenStateOverridesComponentDisplayRules(t *testing.T) {
	css, err := assets.ReadFile("static/storage-browser.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".storage-browser-shell [hidden]{display:none!important}") {
		t.Fatal("storage browser hidden state can be overridden by component display rules")
	}
}

func TestStorageBrowserCardsHoverWithoutMovement(t *testing.T) {
	css, err := assets.ReadFile("static/storage-browser.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(css)
	for _, selector := range []string{
		".storage-disk-card:hover,.storage-disk-card:focus-visible",
		"button.storage-partition-card:hover,button.storage-partition-card:focus-visible",
	} {
		_, rule, found := strings.Cut(styles, selector+"{")
		if !found {
			t.Fatalf("storage card hover/focus rule missing: %s", selector)
		}
		rule, _, found = strings.Cut(rule, "}")
		if !found {
			t.Fatalf("storage card hover/focus rule is incomplete: %s", selector)
		}
		if strings.Contains(rule, "translateY(") {
			t.Errorf("storage card hover/focus moves vertically: %s", selector)
		}
		for _, feedback := range []string{"border-color:", "background:"} {
			if !strings.Contains(rule, feedback) {
				t.Errorf("storage card hover/focus lacks %s feedback: %s", feedback, selector)
			}
		}
	}
}

func TestStorageBrowserMountUXUsesHumanWording(t *testing.T) {
	js, err := assets.ReadFile("static/storage-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(js)
	for _, want := range []string{
		"Mount for now",
		"Mount permanently",
		"Make permanent",
		"Mount now",
		"Unmount for now",
		"Remove permanent mount",
		"reboot JustVoxel",
		"/api/new-storage/mounts/status",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("storage browser UX source missing %q", want)
		}
	}
	for _, unwanted := range []string{"current boot", "persistent UUID mount"} {
		if strings.Contains(source, unwanted) {
			t.Fatalf("storage browser UX source still exposes internal wording %q", unwanted)
		}
	}
}

func TestStorageBrowserWholeDiskFlowStaysInsideNewStorage(t *testing.T) {
	templateContent, err := assets.ReadFile("templates/storage_browser.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(templateContent)
	for _, want := range []string{
		`data-storage-whole-purpose="minecraft"`,
		`data-storage-whole-purpose="backups"`,
		`data-storage-whole-disk-dialog`,
		`data-storage-whole-review`,
		`data-storage-whole-apply`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("whole-disk New Storage flow missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`href="/settings/storage-provision"`,
		`href="/settings/data-migration#dedicated-disk"`,
		`name="operation" value="erase_disk"`,
	} {
		if strings.Contains(markup, forbidden) {
			t.Fatalf("New Storage still exposes redirect/destructive form %q", forbidden)
		}
	}
}

func TestStorageBrowserDestructiveConfirmationUsesSliderAndToggle(t *testing.T) {
	templateContent, err := assets.ReadFile("templates/storage_browser.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(templateContent)
	for _, want := range []string{
		`data-storage-confirm-slider`,
		`data-storage-confirm-toggle`,
		`data-storage-whole-confirm-slider`,
		`data-storage-whole-confirm-toggle`,
		`storage-confirm-toggle-callout`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("destructive confirmation markup missing %q", want)
		}
	}
	if strings.Contains(markup, "Type exactly") {
		t.Fatal("New Storage still asks users to type destructive confirmation phrases")
	}

	js, err := assets.ReadFile("static/storage-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(js)
	for _, want := range []string{
		"Slide fully to the right, then switch Confirm on before applying.",
		"body.set(\"confirmation\", reviewedWholeDisk.confirmation)",
		"const entered = expected && confirmationReady ? expected : \"\"",
		"confirmToggle?.addEventListener(\"change\", updateActionApplyState)",
		"wholeDiskConfirmToggle?.addEventListener(\"change\", updateWholeDiskApplyState)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("destructive confirmation behavior missing %q", want)
		}
	}

	css, err := assets.ReadFile("static/storage-browser.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(css)
	for _, want := range []string{
		".storage-confirm-slider",
		".storage-confirm-slider.is-armed",
		".storage-confirm-toggle-callout",
		"font-size:1.08rem",
		".storage-confirm-toggle-arrow",
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("destructive confirmation styling missing %q", want)
		}
	}
}

func TestStorageBrowserActionMenuClosesWithDialogAndOutsideClick(t *testing.T) {
	js, err := assets.ReadFile("static/storage-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(js)
	for _, want := range []string{
		"function closeActionMenu()",
		"detailDialog?.addEventListener(\"close\", closeActionMenu)",
		"detailDialog?.addEventListener(\"cancel\", closeActionMenu)",
		"root.addEventListener(\"pointerdown\"",
		"if (!actionMenu.contains(event.target)) closeActionMenu()",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("storage action-menu lifecycle missing %q", want)
		}
	}
	if strings.Contains(source, "detailDialog?.addEventListener(\"cancel\", (event) => event.preventDefault())") {
		t.Fatal("Escape is still prevented from closing the partition dialog")
	}
}

func TestStorageBrowserPortableFilesystemPolicy(t *testing.T) {
	template, err := assets.ReadFile("templates/storage_browser.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(template), "data-filesystem-display=") {
		t.Fatal("storage browser template does not carry the friendly filesystem display name")
	}

	js, err := assets.ReadFile("static/storage-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(js)
	for _, want := range []string{
		"[\"xfs\", \"ext4\", \"btrfs\", \"ntfs\", \"vfat\", \"exfat\"]",
		"FAT / FAT32",
		"exFAT",
		"NTFS",
		`showStorageAction("format", !wholeDevice && (!filesystem || mountable))`,
		`showStorageAction("delete_partition", !wholeDevice)`,
		`showStorageAction("initialize_disk", wholeDevice && data.canInitialize === "Yes" && !mountable)`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("storage browser portable-filesystem policy missing %q", want)
		}
	}
	if strings.Contains(source, "managedLinux") {
		t.Fatal("portable filesystem formatting is still restricted to Linux filesystems")
	}
}

func TestStorageBrowserCreatePartitionUXUsesNativeUnallocatedSpaceFlow(t *testing.T) {
	templateContent, err := assets.ReadFile("templates/storage_browser.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(templateContent)
	for _, want := range []string{
		"data-storage-free-space",
		"data-storage-free-action-menu",
		"data-storage-create-partition",
		"data-storage-create-all",
		"data-storage-create-size",
		"data-storage-create-confirm-slider",
		"data-storage-create-confirm-toggle",
		"Create and format partition",
		"Unavailable while unmounted",
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("create-partition Storage UX missing %q", want)
		}
	}

	js, err := assets.ReadFile("static/storage-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(js)
	for _, want := range []string{
		`body.set("operation", "create_partition")`,
		`body.set("free_start", selectedFreeSpace?.start || "")`,
		`body.set("size_gib", sizeGiB)`,
		"createUseAll.checked = false",
		"createSize.disabled = false",
		"Slide to confirm partition creation",
		"createConfirmToggle?.addEventListener",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("create-partition Storage behavior missing %q", want)
		}
	}
}

func TestStoragePartitionDialogsUseWholeDialogScrolling(t *testing.T) {
	css, err := assets.ReadFile("static/storage-browser.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(css)
	for _, want := range []string{
		".storage-detail-dialog{width:min(680px,calc(100% - 1.4rem))",
		".storage-detail-list{max-height:none;overflow:visible}",
		".storage-action-warnings{max-height:none;overflow:visible}",
		".storage-action-dialog{overflow-y:auto;overscroll-behavior:contain;max-height:calc(100dvh - 2rem)}",
	} {
		if !strings.Contains(styles, want) {
			t.Errorf("Storage dialog layout missing %q", want)
		}
	}
	for _, removed := range []string{
		".storage-detail-list{max-height:min(38dvh,340px);overflow-y:auto}",
		".storage-action-warnings{max-height:30dvh;overflow-y:auto",
	} {
		if strings.Contains(styles, removed) {
			t.Errorf("Storage retains clipped internal scrolling %q", removed)
		}
	}
}

type fakeStorageWholeDiskMigrationAPI struct {
	fakeDiscoveryAPI
	plan         api.AdminDataMigrationPlanResponse
	apply        api.AdminDataMigrationApplyResponse
	planCalls    int
	applyCalls   int
	planned      api.AdminDataMigrationPlanRequest
	applyRequest api.AdminDataMigrationApplyRequest
}

func (f *fakeStorageWholeDiskMigrationAPI) AdminDataMigrationPlan(_ context.Context, session string, request api.AdminDataMigrationPlanRequest) (api.AdminDataMigrationPlanResponse, error) {
	if session != "session-token" {
		return api.AdminDataMigrationPlanResponse{}, api.ErrUnauthorized
	}
	f.planCalls++
	f.planned = request
	return f.plan, nil
}

func (f *fakeStorageWholeDiskMigrationAPI) AdminDataMigrationApply(_ context.Context, session string, request api.AdminDataMigrationApplyRequest) (api.AdminDataMigrationApplyResponse, error) {
	if session != "session-token" {
		return api.AdminDataMigrationApplyResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.applyRequest = request
	return f.apply, nil
}

func wholeDiskStorageMigrationPlan() api.AdminDataMigrationPlanResponse {
	return api.AdminDataMigrationPlanResponse{
		OK: true, SchemaVersion: "v1", PlanFingerprint: "whole-disk-fingerprint",
		Normalized: &api.AdminDataMigrationPlanNormalized{
			Operation: "erase_disk", Device: "/dev/vdb", Model: "Data Disk", Transport: "virtio",
			Filesystem: "xfs", MountPoint: "/var/mnt/justvoxel-data", Path: "/var/mnt/justvoxel-data/minecraft",
			SizeGiB: "all", SizeBytes: 10737418240, DataBytes: 2147483648, TargetCapacityBytes: 10737418240,
			CurrentDataPath: "/var/lib/justvoxel/minecraft",
		},
		Warnings: []api.AdminDataMigrationWarning{{Code: "destructive", Message: "This disk will be erased."}},
		Requirements: &api.AdminDataMigrationRequirements{
			MigrationConfirmationRequired: true, DestructiveConfirmationRequired: true,
			ConfirmationPhrase: "ERASE /dev/vdb", Players: []string{},
			ExactSpaceValidationOnApply: true, ColdBackupRequired: true,
			CopyVerificationRequired: true, RuntimeValidationRequired: true,
		},
	}
}

func TestStorageBrowserWholeDiskMinecraftUsesSharedReviewedMigration(t *testing.T) {
	client := &fakeStorageWholeDiskMigrationAPI{plan: wholeDiskStorageMigrationPlan()}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	body := "csrf=csrf-token&purpose=minecraft&device=%2Fdev%2Fvdb"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/whole-disk/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("whole-disk Minecraft plan status=%d: %s", rr.Code, rr.Body.String())
	}
	if client.planCalls != 1 || client.applyCalls != 0 {
		t.Fatalf("whole-disk plan calls=%d apply calls=%d", client.planCalls, client.applyCalls)
	}
	for _, want := range []string{"whole-disk-fingerprint", "ERASE /dev/vdb", "This disk will be erased."} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("whole-disk Minecraft plan missing %q: %s", want, rr.Body.String())
		}
	}

	operation := &api.PersistentOperation{
		SchemaVersion: "v1", OperationID: storageMigrationOperationID, OperationType: "data_migration",
		PlanFingerprint: "whole-disk-fingerprint", State: "queued", Stage: "queued",
		Rollback: api.PersistentOperationRollback{State: "not_started"},
	}
	client.apply = api.AdminDataMigrationApplyResponse{OK: true, Created: true, Operation: operation}
	body = "csrf=csrf-token&purpose=minecraft&device=%2Fdev%2Fvdb&fingerprint=whole-disk-fingerprint&confirmation=ERASE+%2Fdev%2Fvdb"
	rr = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/whole-disk/apply", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("whole-disk Minecraft apply status=%d: %s", rr.Code, rr.Body.String())
	}
	if client.planCalls != 2 || client.applyCalls != 1 {
		t.Fatalf("whole-disk apply must re-plan once; plan=%d apply=%d", client.planCalls, client.applyCalls)
	}
	if client.applyRequest.PlanFingerprint != "whole-disk-fingerprint" || !client.applyRequest.MigrationConfirmed || client.applyRequest.Confirmation != "ERASE /dev/vdb" {
		t.Fatalf("whole-disk reviewed evidence lost: %#v", client.applyRequest)
	}
	if !strings.Contains(rr.Body.String(), storageMigrationOperationID) {
		t.Fatalf("whole-disk apply did not return persistent operation id: %s", rr.Body.String())
	}
}

func TestStorageBrowserActionDialogUsesViewportHeight(t *testing.T) {
	css, err := assets.ReadFile("static/storage-browser.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(css)
	if strings.Contains(styles, "760px") {
		t.Fatal("storage action dialog still has a fixed desktop height cap")
	}
	for _, want := range []string{
		"max-height:calc(100vh - 2rem)",
		"max-height:calc(100dvh - 2rem)",
		"overflow-y:auto;overscroll-behavior:contain",
		"body:has(.storage-action-dialog:modal){overflow:hidden}",
		".storage-action-warnings .storage-warning-panel{padding:.5rem .65rem;line-height:1.35}",
	} {
		if !strings.Contains(styles, want) {
			t.Fatalf("storage dialog viewport or compact warning styling missing %q", want)
		}
	}
}

func TestStorageBrowserLocalWarningsShareOnePanel(t *testing.T) {
	js, err := assets.ReadFile("static/storage-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(js)
	for _, flow := range []struct {
		function, container string
	}{
		{"renderWholeDiskWarnings", "wholeDiskWarnings"},
		{"renderCreateWarnings", "createWarnings"},
		{"renderWarnings", "warningBox"},
	} {
		want := "function " + flow.function + "(warnings) {\n    renderLocalStorageWarnings(" + flow.container + ", warnings);\n  }"
		if !strings.Contains(source, want) {
			t.Fatalf("local warning flow does not share the compact panel: %s", flow.function)
		}
	}
	_, renderer, found := strings.Cut(source, "function renderLocalStorageWarnings(container, warnings) {")
	if !found {
		t.Fatal("local warning renderer missing")
	}
	renderer, _, found = strings.Cut(renderer, "\n  }\n")
	if !found {
		t.Fatal("local warning renderer incomplete")
	}
	for _, want := range []string{
		`panel.className = "notice warning storage-warning-panel"`,
		`const list = document.createElement("ul")`,
		`const item = document.createElement("li")`,
		`item.textContent = message`,
		`list.appendChild(item)`,
		`panel.appendChild(list)`,
		`container.appendChild(panel)`,
	} {
		if !strings.Contains(renderer, want) {
			t.Fatalf("compact warning panel loses accessible warning text structure: %q", want)
		}
	}
	_, loop, found := strings.Cut(renderer, "warnings.forEach((message) => {")
	if !found {
		t.Fatal("warning renderer does not preserve every message")
	}
	loop, _, _ = strings.Cut(loop, "});")
	if strings.Contains(loop, `document.createElement("div")`) || strings.Contains(loop, "notice warning") {
		t.Fatal("warning renderer still creates a notice card for each warning")
	}
	template, err := assets.ReadFile("templates/storage_browser.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, control := range []string{
		"data-storage-action-review-button",
		"data-storage-action-apply-button",
		"data-storage-confirm-slider",
		"data-storage-confirm-toggle",
	} {
		if !strings.Contains(string(template), control) {
			t.Fatalf("destructive confirmation control missing: %s", control)
		}
	}
	for _, prefix := range []string{"data-storage-create", "data-storage-whole"} {
		for _, control := range []string{"-review", "-apply", "-confirm-slider", "-confirm-toggle"} {
			if !strings.Contains(string(template), prefix+control) {
				t.Fatalf("destructive confirmation control missing: %s", prefix+control)
			}
		}
	}
}

func TestStorageBrowserWholeDeviceUSBPermanentMountUsesExistingAPI(t *testing.T) {
	client := &fakeStorageActionAPI{}
	client.storage.Devices = []api.AdminStorageDevice{{Name: "sda", Path: "/dev/sda", Type: "disk", Transport: "usb", Filesystem: "vfat"}}
	client.mountPlan = api.AdminStorageMountResponse{
		OK: true,
		Proposed: api.AdminStorageMountPlan{
			Operation: "persist", Device: "/dev/sda", Filesystem: "vfat",
			MountPoint: "/var/mnt/sda", Fingerprint: "mount-fingerprint",
		},
		Warnings: []string{},
	}
	client.mountApply = api.AdminStorageMountResponse{
		OK: true, Applied: true,
		Proposed: api.AdminStorageMountPlan{
			Operation: "persist", Device: "/dev/sda", MountPoint: "/var/mnt/sda",
			Persistence: "justvoxel", Mounted: true,
		},
		Warnings: []string{},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	body := "csrf=csrf-token&operation=persist&device=%2Fdev%2Fsda&mount_point=%2Fvar%2Fmnt%2Fsda"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/mounts/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("permanent mount plan returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.mountPlanCalls != 1 || client.mountPlanned.MountPoint != "/var/mnt/sda" || client.mountPlanned.Device != "/dev/sda" || client.mountPlanned.Operation != "persist" {
		t.Fatalf("unexpected permanent mount plan request: %#v", client.mountPlanned)
	}
	if !strings.Contains(rr.Body.String(), "mount-fingerprint") {
		t.Fatalf("permanent mount plan missing fingerprint: %s", rr.Body.String())
	}

	body = "csrf=csrf-token&operation=persist&device=%2Fdev%2Fsda&mount_point=%2Fvar%2Fmnt%2Fsda&fingerprint=mount-fingerprint"
	rr = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/mounts/apply", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("permanent mount apply returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.mountApplyCalls != 1 || client.mountApplied.Fingerprint != "mount-fingerprint" || client.mountApplied.Device != "/dev/sda" || client.mountApplied.Operation != "persist" {
		t.Fatalf("permanent mount apply lost reviewed fingerprint: %#v", client.mountApplied)
	}
}

func TestStorageBrowserDiskTableAndTransportPolicy(t *testing.T) {
	cases := []struct {
		name       string
		table      string
		filesystem string
		transport  string
		system     bool
		readonly   bool
		initialize bool
		minecraft  bool
		backups    bool
	}{
		{name: "whole FAT32 USB", table: "none", filesystem: "vfat", transport: "usb", backups: true},
		{name: "parted loop FAT32 USB", table: "loop", filesystem: "vfat", transport: "usb", backups: true},
		{name: "empty GPT", table: "gpt"},
		{name: "empty MSDOS", table: "msdos"},
		{name: "empty MBR", table: "dos"},
		{name: "usable whole internal filesystem", filesystem: "ext4"},
		{name: "blank internal disk", table: "none", initialize: true, minecraft: true, backups: true},
		{name: "blank USB disk", transport: " USB ", initialize: true, backups: true},
		{name: "empty GPT USB", table: "gpt", transport: "usb"},
		{name: "system disk", system: true},
		{name: "readonly USB", transport: "usb", readonly: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeStorageBrowserMigrationAPI{}
			client.storage.Devices = []api.AdminStorageDevice{{
				Name: "sda", Path: "/dev/sda", Type: "disk", SizeBytes: 20 << 30,
				PartitionTable: tc.table, Filesystem: tc.filesystem, Transport: tc.transport,
				System: tc.system, ReadOnly: tc.readonly,
			}}
			// Deliberately report a candidate even for USB: browser policy must still exclude it.
			client.migration.WholeDisks = []api.AdminDataMigrationCandidate{{Path: "/dev/sda"}}
			client.migration.Partitions = []api.AdminDataMigrationCandidate{{Path: "/dev/sda"}}
			if tc.table == "gpt" || tc.table == "msdos" || tc.table == "dos" {
				client.storage.FreeSpaces = []api.AdminStorageFreeSpace{{Device: "/dev/sda", Start: "1MiB", End: "20479MiB", SizeBytes: 19 << 30}}
			}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			data, err := app.buildStorageBrowserPageData(context.Background(), "session-token", client, api.SessionInfo{Role: "administrator"}, "csrf")
			if err != nil {
				t.Fatal(err)
			}
			disk := data.Disks[0]
			if disk.CanInitialize != tc.initialize || disk.MinecraftWholeDisk != tc.minecraft || disk.BackupWholeDisk != tc.backups {
				t.Fatalf("unexpected disk actions: %+v", disk)
			}
			if len(client.storage.FreeSpaces) > 0 && len(disk.FreeSpaces) != 1 {
				t.Fatal("existing partition table lost its unallocated space")
			}
			if tc.filesystem != "" && len(disk.Partitions) != 1 {
				t.Fatal("whole-device filesystem was not exposed")
			}
			if strings.EqualFold(strings.TrimSpace(tc.transport), "usb") && len(disk.Partitions) > 0 && disk.Partitions[0].MinecraftCandidate {
				t.Fatal("USB filesystem exposed a Minecraft migration shortcut")
			}
			rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/workspace/storage", ""))
			if rr.Code != http.StatusOK {
				t.Fatalf("storage render returned %d: %s", rr.Code, rr.Body.String())
			}
			markup := rr.Body.String()
			if strings.Contains(markup, "data-storage-initialize data-path=") != tc.initialize || strings.Contains(markup, `data-storage-whole-purpose="minecraft"`) != tc.minecraft || strings.Contains(markup, `data-storage-whole-purpose="backups"`) != tc.backups {
				t.Fatalf("rendered shortcuts disagree with disk policy: %s", markup)
			}
			if len(disk.FreeSpaces) > 0 && (!strings.Contains(markup, "data-storage-free-space") || !strings.Contains(markup, "Create partition")) {
				t.Fatal("unallocated space must retain the create-partition workflow")
			}
		})
	}
}

func TestStorageBrowserWholeDiskMinecraftRejectsUSB(t *testing.T) {
	for _, phase := range []string{"plan", "apply"} {
		client := &fakeStorageWholeDiskMigrationAPI{plan: wholeDiskStorageMigrationPlan()}
		client.storage.Devices = []api.AdminStorageDevice{{Path: "/dev/sda", Type: "disk", Transport: "usb"}}
		app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/whole-disk/"+phase, "csrf=csrf-token&purpose=minecraft&device=%2Fdev%2Fsda"))
		if rr.Code != http.StatusBadRequest || client.planCalls != 0 || client.applyCalls != 0 {
			t.Fatalf("USB Minecraft %s reached migration: status=%d plan=%d apply=%d", phase, rr.Code, client.planCalls, client.applyCalls)
		}
	}
}

type fakeStorageBrowserBackupProvisionAPI struct {
	fakeDiscoveryAPI
	planned api.AdminStorageProvisionRequest
	applied api.AdminStorageProvisionRequest
}

func (f *fakeStorageBrowserBackupProvisionAPI) AdminStorageProvisionPlan(_ context.Context, _ string, request api.AdminStorageProvisionRequest) (api.AdminStorageProvisionResponse, error) {
	f.planned = request
	return api.AdminStorageProvisionResponse{OK: true, Proposed: api.AdminStorageProvisionPlan{
		Operation: request.Operation, Device: request.Device, MountPoint: request.MountPoint, Path: request.Path,
		Fingerprint: "usb-backup-review", Confirmation: "ERASE /dev/sda", Transport: "usb",
	}, Warnings: []string{"The entire disk and its FAT32 filesystem will be erased."}}, nil
}

func (f *fakeStorageBrowserBackupProvisionAPI) AdminStorageProvisionApply(_ context.Context, _ string, request api.AdminStorageProvisionRequest) (api.AdminStorageProvisionResponse, error) {
	f.applied = request
	return api.AdminStorageProvisionResponse{OK: true, Applied: true}, nil
}

func TestStorageBrowserUSBBackupsUseExistingWholeDiskProvisioner(t *testing.T) {
	client := &fakeStorageBrowserBackupProvisionAPI{}
	client.storage.Devices = []api.AdminStorageDevice{{Name: "sda", Path: "/dev/sda", Type: "disk", Transport: "usb", Filesystem: "vfat"}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&purpose=backups&device=%2Fdev%2Fsda"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/whole-disk/plan", body))
	if rr.Code != http.StatusOK || client.applied.Device != "" {
		t.Fatalf("Review must only plan backup preparation: %d %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"usb-backup-review", "ERASE /dev/sda", "FAT32"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("backup Review missing %q", want)
		}
	}
	request := client.planned
	if request.Operation != "erase_disk" || request.Device != "/dev/sda" || request.MountPoint != "/var/mnt/justvoxel-backup" || request.Path != "/var/mnt/justvoxel-backup/backups" || request.SizeGiB != "all" {
		t.Fatalf("backup preparation did not use existing whole-disk request: %+v", request)
	}
	body += "&fingerprint=usb-backup-review&confirmation=ERASE+%2Fdev%2Fsda"
	rr = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/whole-disk/apply", body))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"applied":true`) {
		t.Fatalf("backup Apply failed: %d %s", rr.Code, rr.Body.String())
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if _, exists := result["redirect"]; exists {
		t.Fatalf("backup Apply returned an obsolete redirect: %s", rr.Body.String())
	}
	if string(result["ok"]) != "true" || string(result["applied"]) != "true" {
		t.Fatalf("backup Apply lost successful result: %s", rr.Body.String())
	}
	request.Fingerprint = "usb-backup-review"
	request.Confirmation = "ERASE /dev/sda"
	if client.applied != request {
		t.Fatalf("backup Apply lost reviewed request: %+v", client.applied)
	}
}

type fakeStorageBackupPartitionAPI struct {
	fakeStorageActionAPI
	backupPlanned    api.AdminBackupStorageRequest
	backupApplied    api.AdminBackupStorageRequest
	backupPlanCalls  int
	backupApplyCalls int
}

func (f *fakeStorageBackupPartitionAPI) AdminBackupStoragePlan(_ context.Context, _ string, request api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error) {
	f.backupPlanCalls++
	f.backupPlanned = request
	return api.AdminBackupStorageResponse{OK: true, Proposed: api.AdminBackupStorageTarget{
		Type: request.Type, Device: request.Device, MountPoint: request.MountPoint, Path: request.Path,
		ExpectedUUID: "backup-uuid", Filesystem: "xfs", SamePhysicalDisk: true,
	}, Warnings: []string{"Backups and Minecraft data are on the same physical disk."}}, nil
}

func (f *fakeStorageBackupPartitionAPI) AdminBackupStorageApply(_ context.Context, _ string, request api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error) {
	f.backupApplyCalls++
	f.backupApplied = request
	return api.AdminBackupStorageResponse{OK: true, Applied: true}, nil
}

func storageBackupPartitionClient() *fakeStorageBackupPartitionAPI {
	client := &fakeStorageBackupPartitionAPI{}
	client.storage.Devices = []api.AdminStorageDevice{{Path: "/dev/vdb1", Type: "part", Filesystem: "xfs"}}
	client.mountStatus = api.AdminStorageMountResponse{OK: true, Proposed: api.AdminStorageMountPlan{
		Device: "/dev/vdb1", Mounted: true, Persistence: "justvoxel",
		MountPoint: "/var/mnt/vdb1", CurrentMountPoint: "/var/mnt/vdb1",
	}}
	return client
}

func TestStorageBrowserBackupPartitionReviewApply(t *testing.T) {
	for _, persistence := range []string{"justvoxel", "external"} {
		t.Run(persistence, func(t *testing.T) {
			client := storageBackupPartitionClient()
			client.mountStatus.Proposed.Persistence = persistence
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			values := url.Values{"csrf": {"csrf-token"}, "device": {"/dev/vdb1"}, "mount_point": {"/arbitrary"}}
			rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/backup-partition/plan", values.Encode()))
			if rr.Code != http.StatusOK {
				t.Fatalf("Review: %d %s", rr.Code, rr.Body.String())
			}
			want := api.AdminBackupStorageRequest{Type: "partition", Device: "/dev/vdb1", MountPoint: "/var/mnt/vdb1", Path: "/var/mnt/vdb1/backups"}
			if client.backupPlanned != want || client.backupApplyCalls != 0 {
				t.Fatalf("unexpected Review: %+v", client.backupPlanned)
			}
			if !strings.Contains(rr.Body.String(), "same physical disk") {
				t.Fatal("backend warning missing")
			}
			var review struct {
				Fingerprint string `json:"fingerprint"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &review); err != nil {
				t.Fatal(err)
			}
			values.Set("fingerprint", review.Fingerprint)
			values.Set("mount_point", want.MountPoint)
			values.Set("path", want.Path)
			rr = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/backup-partition/apply", values.Encode()))
			if rr.Code != http.StatusOK || client.backupApplyCalls != 1 || client.backupApplied != want {
				t.Fatalf("Apply: %d %s request=%+v", rr.Code, rr.Body.String(), client.backupApplied)
			}
			if client.planCalls != 0 || client.applyCalls != 0 || client.mountPlanCalls != 0 || client.mountApplyCalls != 0 {
				t.Fatal("adoption invoked storage mutation APIs")
			}
			client.mountStatus.Proposed.CurrentMountPoint = "/var/mnt/changed"
			client.mountStatus.Proposed.MountPoint = "/var/mnt/changed"
			rr = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/backup-partition/apply", values.Encode()))
			if rr.Code != http.StatusConflict || client.backupApplyCalls != 1 {
				t.Fatal("Apply accepted changed mount")
			}
		})
	}
}

func TestStorageBrowserBackupPartitionRejectsUnsafeRequests(t *testing.T) {
	for _, scenario := range []string{"missing", "whole USB FAT32", "system", "readonly", "temporary", "unmounted", "bad csrf", "operator", "unreviewed"} {
		t.Run(scenario, func(t *testing.T) {
			client := storageBackupPartitionClient()
			values := url.Values{"csrf": {"csrf-token"}, "device": {"/dev/vdb1"}}
			phase := "plan"
			switch scenario {
			case "missing":
				client.storage.Devices = nil
			case "whole USB FAT32":
				client.storage.Devices[0].Type = "disk"
				client.storage.Devices[0].Filesystem = "vfat"
				client.storage.Devices[0].Transport = "usb"
			case "system":
				client.storage.Devices[0].System = true
			case "readonly":
				client.storage.Devices[0].ReadOnly = true
			case "temporary":
				client.mountStatus.Proposed.Persistence = "none"
			case "unmounted":
				client.mountStatus.Proposed.Mounted = false
			case "bad csrf":
				values.Set("csrf", "wrong")
			case "operator":
				client.role = "operator"
			case "unreviewed":
				phase = "apply"
				values.Set("mount_point", "/var/mnt/vdb1")
				values.Set("path", "/var/mnt/vdb1/backups")
			}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/backup-partition/"+phase, values.Encode()))
			if rr.Code < 400 || client.backupApplyCalls != 0 {
				t.Fatalf("unsafe request accepted: %d", rr.Code)
			}
			if scenario != "unreviewed" && client.backupPlanCalls != 0 {
				t.Fatal("unsafe request reached backend")
			}
		})
	}
}

func TestStorageBrowserBackupPartitionUsesStorageReviewModal(t *testing.T) {
	script, err := assets.ReadFile("static/storage-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`return { family: "backup-partition", operation: "", mountPoint: "" }`,
		`if (selectedAction === "use-for-backups") reviewedPlan.fingerprint = payload.fingerprint`,
		`body.set("mount_point", reviewedPlan.mount_point)`,
		`body.set("path", reviewedPlan.path)`,
		`"Mount: " + plan.mount_point + " · Backup directory: " + plan.path`,
		`renderWarnings(payload.warnings)`,
		`const needsConfirmation = Boolean(reviewedPlan.confirmation)`,
		`applyButton?.classList.toggle("danger", action !== "use-for-backups")`,
	} {
		if !strings.Contains(string(script), want) {
			t.Errorf("backup partition Review/Apply missing %q", want)
		}
	}
	markup, err := assets.ReadFile("templates/storage_browser.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markup), `data-storage-action="use-for-backups" hidden>Use for backups</button>`) {
		t.Fatal("partition backup action missing")
	}
}

func TestStorageBrowserActionsRefreshWorkspaceInPlace(t *testing.T) {
	js, err := assets.ReadFile("static/storage-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(js)
	for _, forbidden := range []string{"payload.redirect", "window.location.reload()", "/settings/new-storage"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("Storage actions still contain %q", forbidden)
		}
	}
	for _, want := range []string{
		`function refreshStorageWorkspace()`,
		`root.closest("[data-storage-workspace-dialog]")`,
		`workspace?.querySelector("[data-storage-refresh]")?.click()`,
		"wholeDiskDialog?.close();\n        refreshStorageWorkspace();",
		"createDialog?.close();\n      refreshStorageWorkspace();",
		"actionDialog?.close();\n      refreshStorageWorkspace();",
		`migrationRefresh?.addEventListener("click", refreshStorageWorkspace)`,
		`if (payload.operation_id)`,
		`showMigrationProgress({`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("Storage workspace refresh contract missing %q", want)
		}
	}
	actions, err := os.ReadFile("storage_browser_actions.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`/settings/new-storage`, `json:"redirect`} {
		if strings.Contains(string(actions), forbidden) {
			t.Fatalf("Storage action response still exposes %q", forbidden)
		}
	}
}
