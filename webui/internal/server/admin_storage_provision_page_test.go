package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeStorageProvisionAPI struct {
	fakeDiscoveryAPI
	discovery api.AdminStorageProvisionResponse
}

func (f *fakeStorageProvisionAPI) AdminStorageProvisionDiscover(_ context.Context, session string) (api.AdminStorageProvisionResponse, error) {
	if session != "session-token" {
		return api.AdminStorageProvisionResponse{}, api.ErrUnauthorized
	}
	return f.discovery, nil
}

func (f *fakeStorageProvisionAPI) AdminStorageProvisionPlan(_ context.Context, session string, request api.AdminStorageProvisionRequest) (api.AdminStorageProvisionResponse, error) {
	if session != "session-token" {
		return api.AdminStorageProvisionResponse{}, api.ErrUnauthorized
	}
	return api.AdminStorageProvisionResponse{OK: true, Proposed: api.AdminStorageProvisionPlan{
		Operation: request.Operation, Device: request.Device, MountPoint: request.MountPoint, Path: request.Path,
		Confirmation: "ERASE /dev/vdb", Fingerprint: "fingerprint",
	}}, nil
}

func (f *fakeStorageProvisionAPI) AdminStorageProvisionApply(_ context.Context, session string, request api.AdminStorageProvisionRequest) (api.AdminStorageProvisionResponse, error) {
	if session != "session-token" {
		return api.AdminStorageProvisionResponse{}, api.ErrUnauthorized
	}
	return api.AdminStorageProvisionResponse{OK: true, Applied: true}, nil
}

func TestAdvancedStorageShowsSafeCandidateGroups(t *testing.T) {
	client := &fakeStorageProvisionAPI{}
	client.configuration.Configured = true
	client.discovery.OK = true
	client.discovery.WholeDisks = []api.AdminStorageProvisionCandidate{{Path: "/dev/vdb", Model: "Backup Disk", SizeBytes: 20 * 1024 * 1024 * 1024}}
	client.discovery.BlankPartitions = []api.AdminStorageProvisionCandidate{{Path: "/dev/vdc1", Model: "Blank Disk", SizeBytes: 10 * 1024 * 1024 * 1024}}
	client.discovery.FreeSpaceDisks = []api.AdminStorageProvisionCandidate{{Path: "/dev/vdd", Model: "Mixed Disk", SizeBytes: 40 * 1024 * 1024 * 1024, FreeBytes: 8 * 1024 * 1024 * 1024}}

	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/settings/storage-provision", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("advanced storage returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Advanced storage", "Dedicated disk or USB drive", "Format a blank partition", "already-unallocated disk space",
		"Backup Disk", "/dev/vdb", "Blank Disk", "/dev/vdc1", "Mixed Disk", "/dev/vdd", "Review whole-disk erase",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("advanced storage page missing %q", want)
		}
	}
}

func TestAdvancedStorageFreshApplianceReturnsToSetup(t *testing.T) {
	client := &fakeStorageProvisionAPI{}
	client.configuration.Configured = false

	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/settings/storage-provision?from=setup", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("advanced storage returned %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`href="/setup"`, "Back to setup", "Minecraft is not configured yet"} {
		if !strings.Contains(body, want) {
			t.Fatalf("fresh Advanced Storage page missing %q: %s", want, body)
		}
	}
}
func TestAdvancedStorageTemplateMakesTypedConfirmationExplicit(t *testing.T) {
	content, err := assets.ReadFile("templates/storage_provision.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(content)
	for _, want := range []string{
		`id="storage-confirmation-phrase"`,
		`name="confirmation"`,
		`data-exact-confirmation="{{.Plan.Proposed.Confirmation}}"`,
		`name="fingerprint" value="{{.Plan.Proposed.Fingerprint}}"`,
		"including the action and exact <code>/dev/...</code> device",
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("destructive confirmation UX missing %q", want)
		}
	}
}
