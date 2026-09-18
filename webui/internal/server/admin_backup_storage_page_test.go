package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeBackupStorageAPI struct {
	fakeDiscoveryAPI
	status      api.AdminBackupStorageResponse
	plan        api.AdminBackupStorageResponse
	apply       api.AdminBackupStorageResponse
	planned     api.AdminBackupStorageRequest
	applied     api.AdminBackupStorageRequest
	statusCalls int
	planCalls   int
	applyCalls  int
}

func (f *fakeBackupStorageAPI) AdminBackupStorageStatus(_ context.Context, session string) (api.AdminBackupStorageResponse, error) {
	if session != "session-token" {
		return api.AdminBackupStorageResponse{}, api.ErrUnauthorized
	}
	f.statusCalls++
	return f.status, nil
}

func (f *fakeBackupStorageAPI) AdminBackupStoragePlan(_ context.Context, session string, request api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error) {
	if session != "session-token" {
		return api.AdminBackupStorageResponse{}, api.ErrUnauthorized
	}
	f.planCalls++
	f.planned = request
	return f.plan, nil
}

func (f *fakeBackupStorageAPI) AdminBackupStorageApply(_ context.Context, session string, request api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error) {
	if session != "session-token" {
		return api.AdminBackupStorageResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.applied = request
	return f.apply, nil
}

func configuredBackupStorageFake() *fakeBackupStorageAPI {
	client := &fakeBackupStorageAPI{}
	client.configuration.Configured = true
	client.configuration.Minecraft.DataPath = "/var/lib/justvoxel/minecraft"
	client.configuration.Backup.Type = "system"
	client.configuration.Backup.Path = "/var/lib/justvoxel/backups"
	client.storage.SystemDisks = []string{"/dev/vda"}
	client.storage.Devices = []api.AdminStorageDevice{
		{Name: "vda1", Path: "/dev/vda1", Type: "part", SizeBytes: 20 * 1024 * 1024 * 1024, Filesystem: "xfs", Mountpoints: []string{"/"}, System: true},
		{Name: "vdb1", Path: "/dev/vdb1", Type: "part", SizeBytes: 100 * 1024 * 1024 * 1024, Filesystem: "xfs", Label: "BACKUP", UUID: "backup-uuid", Mountpoints: []string{"/var/mnt/backup"}, Model: "Virtual Disk"},
		{Name: "vdc1", Path: "/dev/vdc1", Type: "part", SizeBytes: 50 * 1024 * 1024 * 1024, Filesystem: "", Mountpoints: nil, Model: "Blank Disk"},
	}
	client.status = api.AdminBackupStorageResponse{OK: true, Current: api.AdminBackupStorageTarget{
		Status: "ready", StatusDetail: "Backup destination is ready.", Type: "system", Path: "/var/lib/justvoxel/backups",
		AvailableBytes: 40 * 1024 * 1024 * 1024, FilesystemBytes: 80 * 1024 * 1024 * 1024, SamePhysicalDisk: true,
	}}
	return client
}

func TestBackupStoragePageShowsSafeChoicesAndClearDestructiveGuidance(t *testing.T) {
	client := configuredBackupStorageFake()
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/settings/backup-storage", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("backup storage page returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Backup storage", "Backup destination is ready.", "40.0 GiB", "Same physical disk",
		"/dev/vdb1", "Virtual Disk", "NFS network share", "SMB / CIFS network share",
		"Advanced storage", "/settings/storage-provision", "ERASE /dev/sdb", "exact typed confirmation phrase",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("backup storage page missing %q", want)
		}
	}
	if strings.Contains(body, "/dev/vda1") || strings.Contains(body, "/dev/vdc1") {
		t.Fatalf("unsafe or unformatted partition offered for safe adoption: %s", body)
	}
}

func TestBackupStoragePlanDoesNotCarrySMBPassword(t *testing.T) {
	client := configuredBackupStorageFake()
	client.plan = api.AdminBackupStorageResponse{OK: true, Changed: true, Proposed: api.AdminBackupStorageTarget{
		Type: "smb", Path: "/var/mnt/justvoxel-backup/backups", MountPoint: "/var/mnt/justvoxel-backup",
		ExpectedSource: "//nas/backups", CredentialsNeeded: true,
	}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&type=smb&path=%2Fvar%2Fmnt%2Fjustvoxel-backup%2Fbackups&mount_point=%2Fvar%2Fmnt%2Fjustvoxel-backup&source=%2F%2Fnas%2Fbackups&username=backup-user&domain=HOME&password=should-not-pass-plan"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/backup-storage/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("backup storage plan returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.planCalls != 1 || client.applyCalls != 0 {
		t.Fatalf("plan calls=%d apply calls=%d", client.planCalls, client.applyCalls)
	}
	if client.planned.Password != "" {
		t.Fatalf("SMB password was carried into Review: %q", client.planned.Password)
	}
	for _, want := range []string{"Proposed backup destination", "//nas/backups", "SMB password", "Apply backup location"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("review missing %q: %s", want, rr.Body.String())
		}
	}
	if strings.Contains(rr.Body.String(), "should-not-pass-plan") {
		t.Fatal("SMB password leaked into the review page")
	}
}

func TestBackupStorageApplyPassesPasswordOnlyAtApply(t *testing.T) {
	client := configuredBackupStorageFake()
	client.apply = api.AdminBackupStorageResponse{OK: true, Changed: true, Applied: true, Proposed: api.AdminBackupStorageTarget{Type: "smb", Path: "/var/mnt/justvoxel-backup/backups"}}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&type=smb&path=%2Fvar%2Fmnt%2Fjustvoxel-backup%2Fbackups&mount_point=%2Fvar%2Fmnt%2Fjustvoxel-backup&source=%2F%2Fnas%2Fbackups&username=backup-user&domain=HOME&password=secret-at-apply"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/settings/backup-storage/apply", body))
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("backup storage apply returned %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Location") != "/settings/backup-storage?result=saved" {
		t.Fatalf("unexpected redirect %q", rr.Header().Get("Location"))
	}
	if client.applied.Password != "secret-at-apply" {
		t.Fatalf("apply password = %q", client.applied.Password)
	}
}

func TestBackupStorageOperatorCannotOpenOrPlan(t *testing.T) {
	client := configuredBackupStorageFake()
	client.role = "operator"
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/settings/backup-storage", ""},
		{http.MethodPost, "/settings/backup-storage/plan", "csrf=csrf-token&type=system&path=%2Fvar%2Flib%2Fjustvoxel%2Fbackups"},
	} {
		rr := httptestResponse(app, authenticatedAdminRequest(tc.method, "http://example"+tc.path, tc.body))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("operator %s status=%d, want 403", tc.path, rr.Code)
		}
	}
	if client.statusCalls != 0 || client.planCalls != 0 || client.applyCalls != 0 {
		t.Fatal("privileged backup storage API ran for Operator")
	}
}
