package server

import (
	"context"
	"net/http"
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
		"[\"xfs\", \"ext4\", \"btrfs\"]",
		"FAT / FAT32",
		"exFAT",
		"NTFS",
		"showStorageAction(\"format\", managedLinux)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("storage browser portable-filesystem policy missing %q", want)
		}
	}
	if strings.Contains(source, "showStorageAction(\"format\", true);\n      setOptional(detail.mountTypeRow") {
		t.Fatal("portable filesystems can still expose Format through the generic mounted-filesystem path")
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
		"Slide to confirm partition creation",
		"createConfirmToggle?.addEventListener",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("create-partition Storage behavior missing %q", want)
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
