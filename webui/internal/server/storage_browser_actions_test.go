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
